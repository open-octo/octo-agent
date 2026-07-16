package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Request/Response types ─────────────────────────────────────────────────

type createChatRequest struct {
	Message string `json:"message"`
	Model   string `json:"model,omitempty"`
	Name    string `json:"name,omitempty"`
}

type createChatResponse struct {
	SessionID string `json:"session_id"`
	Reply     string `json:"reply"`
}

type turnRequest struct {
	Message string `json:"message"`
}

// sessionItem is the shape the Web UI expects for each session in listings
// and after creation. It is a superset of the raw agent.Session fields.
type sessionItem struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Title           string    `json:"title"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Model           string    `json:"model"`
	ModelID         string    `json:"model_id,omitempty"`
	Status          string    `json:"status"`
	Source          string    `json:"source"`
	AgentProfile    string    `json:"agent_profile"`
	Pinned          bool      `json:"pinned"`
	TotalTasks      int       `json:"total_tasks"`
	TurnCount       int       `json:"turn_count"`
	WorkingDir      string    `json:"working_dir,omitempty"`
	PermissionMode  string    `json:"permission_mode,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	ShowReasoning   *bool     `json:"show_reasoning,omitempty"`
	ContextUsage    int       `json:"context_usage,omitempty"`
	PendingQuestion bool      `json:"pending_question,omitempty"`
	BranchedFrom    string    `json:"branched_from,omitempty"`
}

type sessionDetail struct {
	ID        string          `json:"id"`
	CreatedAt time.Time       `json:"created_at"`
	Model     string          `json:"model"`
	Messages  []agent.Message `json:"messages"`
}

// sessionListResponse matches the Ruby frontend's expected list envelope.
type sessionListResponse struct {
	Sessions  []sessionItem `json:"sessions"`
	HasMore   bool          `json:"has_more"`
	CronCount int           `json:"cron_count"`
}

// sessionCreateRequest matches POST /api/sessions bodies from the Web UI.
type sessionCreateRequest struct {
	Name         string `json:"name"`
	AgentProfile string `json:"agent_profile"`
	Source       string `json:"source"`
	Model        string `json:"model,omitempty"`
}

// toSessionItem builds a frontend-friendly session descriptor.
func (srv *Server) toSessionItem(s *agent.Session, source, agentProfile string) sessionItem {
	updated := s.CreatedAt
	if p, err := s.SavePath(); err == nil {
		if st, err := os.Stat(p); err == nil {
			updated = st.ModTime()
		}
	}
	name := s.Title
	if name == "" {
		name = s.DisplayTitle()
	}
	_, pm, re, sr, ctxUsage := srv.sessionStatusFields(s)
	return sessionItem{
		ID:              s.ID,
		Name:            name,
		Title:           s.Title,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       updated,
		Model:           s.Model,
		Status:          srv.sessionStatus(s.ID),
		Source:          source,
		AgentProfile:    agentProfile,
		Pinned:          false,
		TotalTasks:      0,
		TurnCount:       s.TurnCount(),
		WorkingDir:      srv.sessionCwd(s),
		PermissionMode:  pm,
		ReasoningEffort: re,
		ShowReasoning:   sr,
		ContextUsage:    ctxUsage,
		PendingQuestion: srv.hasPendingQuestion(s.ID),
		BranchedFrom:    s.BranchedFrom,
	}
}

// entryForSession returns the model-config entry that actually backs sess's
// turns — the same resolution senderForSession uses (sess.ModelConfig when
// set and still configured, else the default entry) — or the default entry
// when sess is nil (no specific session in scope, e.g. the onboarding
// response). Before this existed, every reasoning_effort/show_reasoning
// status read and every "toggle it for this session" write resolved from
// cfg.DefaultEntry() unconditionally: a session actually running a
// non-default model (turns themselves were never affected — senderForSession
// already resolved the right entry for those) still saw the WRONG value in
// its own status bar, and toggling it via that session's Composer visibly
// flipped the icon — since the read and the write both used the same wrong
// entry — while never touching the entry that session's real turns read.
func entryForSession(cfg config.Config, sess *agent.Session) config.ModelEntry {
	if sess != nil && sess.ModelConfig != "" {
		if e, ok := cfg.EntryByModel(sess.ModelConfig); ok {
			return e
		}
	}
	return cfg.DefaultEntry()
}

// sessionStatusFields returns the server-level session metadata (permission
// mode, reasoning effort, show reasoning, current context usage) plus the
// DEFAULT working dir, resolved against sess's own model-config entry (see
// entryForSession) — pass nil when no specific session is in scope. Working
// dir is per-session regardless: callers with a session in hand override the
// returned value via sessionCwd / sessionCwdByID; the default here is the
// fallback for sessions with none.
func (srv *Server) sessionStatusFields(sess *agent.Session) (workingDir, permissionMode, reasoningEffort string, showReasoning *bool, contextUsage int) {
	workingDir = srv.cwd
	if sess != nil && sess.PermissionMode != "" {
		permissionMode = sess.PermissionMode
	} else {
		permissionMode = string(resolvePermissionMode())
	}
	if cfg, err := config.Load(); err == nil {
		entry := entryForSession(cfg, sess)
		reasoningEffort = entry.ReasoningEffort
		eff := cfg.EffectiveShowReasoning(entry.ShowReasoning)
		showReasoning = &eff
	}
	// Report the session's persisted context usage (its real last-turn token
	// count) so the list — and thus a page refresh — carries a correct value
	// instead of 0. The live WS path still refines the active session per turn.
	if sess != nil && sess.LastContextTokens > 0 {
		if window := agent.ContextWindow(sess.Model); window > 0 {
			contextUsage = sess.LastContextTokens * 100 / window
			if contextUsage > 100 {
				contextUsage = 100
			}
		}
	}
	return workingDir, permissionMode, reasoningEffort, showReasoning, contextUsage
}

type toolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Enabled     bool   `json:"enabled"`
}

// applyDefaultWorkspaceDir sets sess's WorkingDir to the server's configured
// default workspace dir (cfg.WorkspaceDir / tools.ResolveWorkspaceDir),
// unless the session already has one of its own — so it composes with the
// PATCH /api/sessions/{id}/working_dir override without special-casing. The
// directory is created lazily here, the first time a session actually needs
// it, rather than at server startup. A failure here is logged and otherwise
// a no-op: the session just falls back to the server's launch directory,
// exactly like before workspace_dir existed.
func (s *Server) applyDefaultWorkspaceDir(sess *agent.Session) {
	dir := s.curWorkspaceDir()
	if dir == "" || sess.WorkingDir != "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("workspace dir: mkdir failed, session keeps using the launch directory", "dir", dir, "err", err)
		return
	}
	if err := sess.SetWorkingDir(dir); err != nil {
		slog.Warn("workspace dir: could not set session working dir", "err", err)
	}
}

// ─── POST /api/chat ─────────────────────────────────────────────────────────

func (s *Server) handleCreateChat(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureSender(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	var req createChatRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	model := s.model
	if req.Model != "" {
		model = req.Model
	}
	sess := agent.NewSession(model, s.system)
	s.applyDefaultWorkspaceDir(sess)
	_ = sess.SetPermissionMode(string(resolvePermissionMode()))
	sess.Bind(agent.EntryWeb, false)
	if req.Name != "" {
		_ = sess.SetTitle(req.Name)
	}

	mu := s.sessionTurnLock(sess.ID)
	mu.Lock()
	defer func() {
		mu.Unlock()
		s.releaseSessionBinding(sess.ID, agent.EntryWeb)
	}()

	reply, err := s.runTurn(r.Context(), sess, req.Message)
	if errors.Is(err, errDraining) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := sess.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save session: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, createChatResponse{
		SessionID: sess.ID,
		Reply:     reply,
	})
}

// ─── POST /api/chat/:id/turn ────────────────────────────────────────────────

func (s *Server) handleTurn(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureSender(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	var req turnRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if ok, msg, berr := s.acquireSessionBinding(id, agent.EntryWeb, false); !ok {
		writeError(w, http.StatusConflict, berr.Error())
		return
	} else if msg != "" {
		// TODO: surface takeover notice to the client via an event.
		_ = msg
	}

	mu := s.sessionTurnLock(id)
	mu.Lock()
	defer func() {
		mu.Unlock()
		s.releaseSessionBinding(id, agent.EntryWeb)
	}()

	// A WS turn runs in a goroutine after releasing this mutex, guarded only by
	// turnRunning. Without this check a REST turn would acquire the (free) mutex
	// and run concurrently with that WS turn — both Save() the same session file,
	// clobbering history. Honour turnRunning like the WS path does. (deferred
	// unlock + binding release handle cleanup on this early return.)
	if s.turnRunning[id] {
		writeError(w, http.StatusConflict, "a turn is already running for this session")
		return
	}

	sess, err = agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	reply, err := s.runTurn(r.Context(), sess, req.Message)
	if errors.Is(err, errDraining) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := sess.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save session: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

// ─── GET /api/sessions ──────────────────────────────────────────────────────

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := agent.ListSessions(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]sessionItem, 0, len(sessions))
	cronCount := 0
	for _, sess := range sessions {
		// agent_profile is not persisted on the session; default to "general"
		// so the UI renders. Sessions from before source was persisted load
		// with an empty Source and fall back to "manual".
		source := sess.Source
		if source == "" {
			source = "manual"
		}
		item := s.toSessionItem(sess, source, "general")
		out = append(out, item)
		if item.Source == "cron" {
			cronCount++
		}
	}
	writeJSON(w, http.StatusOK, sessionListResponse{
		Sessions:  out,
		HasMore:   false,
		CronCount: cronCount,
	})
}

// ─── POST /api/sessions ─────────────────────────────────────────────────────

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req sessionCreateRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	model := s.model
	modelConfig := ""
	if req.Model != "" {
		model = req.Model
		// The web modal sends a config entry id; the session binds to that
		// entry so its turns run on the entry's sender. A non-matching value
		// stays a raw model string on the default sender.
		if cfg, err := config.Load(); err == nil {
			if e, ok := cfg.EntryByModel(req.Model); ok {
				modelConfig = e.Model
				if e.Model != "" {
					model = e.Model
				}
			}
		}
	}
	if model == "" {
		// Fall back to the user's configured default model.
		if cfg, err := config.Load(); err == nil && cfg.DefaultEntry().Model != "" {
			model = cfg.DefaultEntry().Model
		}
	}
	if model == "" {
		writeError(w, http.StatusBadRequest, "no default model configured")
		return
	}

	agentProfile := req.AgentProfile
	if agentProfile == "" {
		agentProfile = "general"
	}
	source := req.Source
	if source == "" {
		source = "manual"
	}

	sess := agent.NewSession(model, "")
	s.applyDefaultWorkspaceDir(sess)
	sess.Source = source
	sess.ModelConfig = modelConfig
	_ = sess.SetPermissionMode(string(resolvePermissionMode()))
	sess.Bind(agent.EntryWeb, false)
	if req.Name != "" {
		sess.Title = req.Name
	}
	if err := sess.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save session: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"session": s.toSessionItem(sess, source, agentProfile)})
}

// ─── GET /api/sessions/:id/messages ─────────────────────────────────────────

func (s *Server) handleGetSessionMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Resolve once, up front: the frontend needs this to decide whether a
	// reconstructed `thinking` event should be allowed to end a tool-call
	// group (see the "show_reasoning" field on the response below).
	_, _, _, showReasoning, _ := s.sessionStatusFields(sess)
	effShowReasoning := showReasoning == nil || *showReasoning

	// While a turn is in flight its progress is saved to disk incrementally,
	// and the WS replay buffer delivers those same rounds to (re)subscribing
	// tabs. Serve only the messages that predate the turn so the two sources
	// never overlap — without this cap a mid-turn refresh would render every
	// tool card twice.
	msgs := sess.Messages
	s.liveStateMu.RLock()
	if ls, ok := s.liveStates[id]; ok && ls.historyWatermark > 0 && ls.historyWatermark < len(msgs) {
		msgs = msgs[:ls.historyWatermark]
	}
	s.liveStateMu.RUnlock()

	// The Web UI expects an event stream that mirrors the live WS traffic.
	// We translate the persisted message list into user/assistant events and
	// reconstruct tool_call / tool_result pairs from tool_use / tool_result
	// blocks so the history replay is visually complete.
	events := make([]map[string]any, 0, len(msgs)*2)
	for i, m := range msgs {
		switch m.Role {
		case agent.RoleUser:
			// Emit tool_result events for any tool_result blocks before the
			// user message (they carry the actual output).
			hasToolResult := false
			for _, b := range m.Blocks {
				if b.Type == "tool_result" {
					hasToolResult = true
					// Persisted results may carry model-facing
					// <system-reminder> spans appended by the tool-result
					// hook (memory save-nudge) — strip them for display,
					// matching the live EventToolDone path.
					ev := map[string]any{
						"type":    "tool_result",
						"result":  agent.StripRemindersForDisplay(b.Result),
						"tool_id": b.ToolUseID,
					}
					if b.UI != nil {
						ev["ui_payload"] = b.UI
					}
					events = append(events, ev)
				}
			}
			// Use the message's own CreatedAt when available.  Older session
			// files don't have per-message timestamps, so fall back to the
			// array index as a unique cursor (not sess.CreatedAt — that
			// collides with the Web UI's dedup logic and drops everything
			// after the first user message).
			//
			// UnixMilli, not Unix: the live history_user_message broadcast in
			// doAgentTurn sends this message's CreatedAt in milliseconds, and
			// the Web UI dedups live-vs-history rounds by exact created_at
			// equality — mismatched units would render the same user message
			// twice when a history fetch races the live event.
			createdAt := m.CreatedAt.UnixMilli()
			if m.CreatedAt.IsZero() {
				createdAt = int64(i + 1)
			}
			// A multipart user message (image attachments) carries its text
			// in blocks rather than Content, and its images as blocks whose
			// persisted file maps to an /api/uploads/ thumbnail URL. Blocks
			// of a tool_result message are tool bookkeeping, not user input.
			text := m.Content
			var images []string
			if !hasToolResult {
				for _, b := range m.Blocks {
					switch b.Type {
					case "text":
						if text == "" {
							text = b.Text
						}
					case "image":
						if b.ImagePath != "" {
							images = append(images, "/api/uploads/"+filepath.Base(b.ImagePath))
						}
					}
				}
			}
			// <system-reminder> spans (background-process completion notes,
			// recalled memories) are model-facing context persisted inside
			// user turns — strip them so replay matches the TUI, which never
			// shows them.
			text = strings.TrimSpace(agent.StripSystemReminders(text))
			// Document attachments persist only as "[Attached file: <abspath>]"
			// notes in the text (no image block). Strip them and re-derive the
			// chip refs so a reloaded transcript shows the same chips the live
			// turn did (docChipRefs is the shared source for both).
			var docRefs []string
			text, docRefs = docChipRefs(text)
			images = append(images, docRefs...)
			// Only emit history_user_message if there is user-visible content
			// (tool_result-only messages are bookkeeping, not user-visible).
			if text != "" || len(images) > 0 {
				ev := map[string]any{
					"type":          "history_user_message",
					"content":       text,
					"created_at":    createdAt,
					"message_index": i, // position in the persisted Messages array — may differ from the rendered index (tool_result-only messages are skipped)
				}
				if len(images) > 0 {
					ev["images"] = images
				}
				events = append(events, ev)
			}
		case agent.RoleAssistant:
			// Reasoning trace: Anthropic returns a standalone "thinking" block;
			// OpenAI-protocol models stash it on the first tool_use block.
			thinking := ""
			hasToolUse := false
			for _, b := range m.Blocks {
				if b.Type == "tool_use" {
					hasToolUse = true
				}
				if thinking == "" {
					if b.Type == "thinking" && b.Thinking != "" {
						thinking = b.Thinking
					} else if b.Type == "tool_use" && b.Reasoning != "" {
						thinking = b.Reasoning
					}
				}
			}
			if hasToolUse {
				// Intermediate (tool) round — replay in block order so it mirrors
				// the live stream's think → act sequence: the reasoning (and any
				// answer text) come BEFORE the tool calls. Each non-tool segment
				// is a group boundary, so tools separated by thinking/text don't
				// collapse into one card. Emitted as a standalone "thinking"
				// segment rather than inline, since this round has no answer bubble.
				if thinking != "" {
					events = append(events, map[string]any{"type": "thinking", "text": thinking})
				}
				var txt strings.Builder
				for _, b := range m.Blocks {
					if b.Type == "text" {
						txt.WriteString(b.Text)
					}
				}
				if txt.Len() > 0 {
					events = append(events, map[string]any{
						"type":     "assistant_message",
						"content":  txt.String(),
						"thinking": "",
					})
				}
				for _, b := range m.Blocks {
					if b.Type == "tool_use" {
						events = append(events, map[string]any{
							"type":    "tool_call",
							"name":    b.Name,
							"args":    b.Input,
							"tool_id": b.ID,
						})
					}
				}
			} else {
				// Final answer turn: keep the reasoning inline on the answer
				// bubble, mirroring the live assistant_message.
				events = append(events, map[string]any{
					"type":     "assistant_message",
					"content":  m.Content,
					"thinking": thinking,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"has_more":       false,
		"events":         events,
		"show_reasoning": effShowReasoning,
	})
}

// ─── GET /api/sessions/:id ──────────────────────────────────────────────────

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, sessionDetail{
		ID:        sess.ID,
		CreatedAt: sess.CreatedAt,
		Model:     sess.Model,
		Messages:  sess.Messages,
	})
}

// ─── DELETE /api/sessions/:id ───────────────────────────────────────────────

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	// Cancel any in-flight turn first — including one parked in
	// ask_user_question, which (now that it has no timeout to fall back on)
	// would otherwise leak forever and, if answered via a stale modal,
	// resave the very file this handler is about to delete.
	s.interruptSession(id)
	if err := agent.DeleteSession(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.forgetTurnLock(id)
	tools.CloseSessionBackgroundManager(id) // reap the session's background daemons
	tools.CloseSessionSubAgentManager(id)   // and its sub-agents
	tools.CloseSessionWorkflowManager(id)   // and its background workflows
	tools.CloseSessionReadTracker(id)       // and its read-before-write tracker
	s.wsHub.broadcast("", wsEventSessionDeleted{Type: "session_deleted", SessionID: id})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": []string{id}})
}

// ─── POST /api/sessions/delete (batch) ──────────────────────────────────────

type deleteSessionsRequest struct {
	IDs []string `json:"ids"`
}

func (s *Server) handleDeleteSessions(w http.ResponseWriter, r *http.Request) {
	var req deleteSessionsRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}

	deleted := make([]string, 0, len(req.IDs))
	failed := map[string]string{}
	for _, id := range req.IDs {
		s.interruptSession(id)
		if err := agent.DeleteSession(id); err != nil {
			failed[id] = err.Error()
			continue
		}
		s.forgetTurnLock(id)
		tools.CloseSessionBackgroundManager(id) // reap the session's background daemons
		tools.CloseSessionSubAgentManager(id)   // and its sub-agents
		tools.CloseSessionWorkflowManager(id)   // and its background workflows
		tools.CloseSessionReadTracker(id)       // and its read-before-write tracker
		s.wsHub.broadcast("", wsEventSessionDeleted{Type: "session_deleted", SessionID: id})
		deleted = append(deleted, id)
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "failed": failed})
}

// ─── POST /api/sessions/{id}/branch ────────────────────────────────────────

type branchSessionRequest struct {
	MessageIndex   int    `json:"message_index"`
	PromptOverride string `json:"prompt_override,omitempty"`
}

// handleBranchSession creates a new session branched from the source session's
// history up to message_index (inclusive). The source session is untouched.
// If prompt_override is non-empty, the branched session's last message (the
// user message at message_index) is replaced with it before save — this lets
// the user vary the prompt and compare results. Returns the new session so the
// client can navigate to it.
func (s *Server) handleBranchSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	var req branchSessionRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	src, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if req.MessageIndex < 0 || req.MessageIndex >= len(src.Messages) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("message_index out of range: %d (have %d messages)", req.MessageIndex, len(src.Messages)))
		return
	}
	branch := agent.BranchFrom(src, req.MessageIndex+1) // +1: BranchFrom takes an exclusive count
	if req.PromptOverride != "" {
		branch.Messages[len(branch.Messages)-1].Content = req.PromptOverride
	}
	if err := branch.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.wsHub.broadcast("", wsEventSessionCreated{Type: "session_created", SessionID: branch.ID})
	writeJSON(w, http.StatusOK, map[string]any{"session": s.toSessionItem(branch, "web", "")})
}

// ─── GET /api/tools ─────────────────────────────────────────────────────────

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	defs := tools.DefaultTools()
	out := make([]toolInfo, 0, len(defs))
	for _, d := range defs {
		out = append(out, toolInfo{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── GET /api/skills ────────────────────────────────────────────────────────

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	// The registry is a startup-time snapshot. Skills added, edited, or
	// removed on disk (rather than through this API) would otherwise stay
	// invisible — or worse, deleted ones would linger in the panel — until
	// the server restarts. Re-scan before listing; Reload only reads each
	// skill dir's SKILL.md frontmatter, so it's cheap enough per request.
	s.skillReg.Reload()
	// Refresh the manifest for sessions built after this point, same as the
	// toggle/delete handlers do.
	s.setSkillsManifest(tools.SkillsManifest(s.skillReg))
	list := s.skillReg.All()
	out := make([]skillInfo, 0, len(list))
	for _, sk := range list {
		out = append(out, skillInfo{
			Name:        sk.Name,
			Description: sk.Description,
			Source:      sk.Source,
			Enabled:     s.skillReg.IsEnabled(sk.Name),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// ─── GET /api/workflows ───────────────────────────────────────────────────────

// handleListWorkflows lists every registered named workflow (embedded defaults +
// user + project) for the web discovery panel. Read-only; the registry is
// scanned fresh per request so newly-dropped files show up without a restart.
func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeJSON(w, http.StatusOK, map[string]any{"workflows": tools.ListNamedWorkflows()})
}

// ─── GET /api/health ────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─── Turn execution ─────────────────────────────────────────────────────────

// runTurn executes one user message against a session. It builds the agent,
// runs the tool loop if enabled, and returns the assistant's text reply.
func (s *Server) runTurn(ctx context.Context, sess *agent.Session, userInput string) (string, error) {
	if err := s.drain.begin(); err != nil {
		return "", err
	}
	defer s.drain.end()

	ctx = context.WithValue(ctx, ctxKeySessionID{}, sess.ID)
	a := s.buildAgent(sess)

	if !s.cfg.Tools {
		reply, err := a.Turn(ctx, userInput)
		if err != nil {
			return "", err
		}
		sess.SyncFrom(a.History)
		return reply.Content, nil
	}

	// Tool-enabled path: wire the per-turn tool environment (gate + ctx-scoped
	// sub-agent manager + task store) bound to this turn's agent.
	ctx, executor, _, cleanup, err := s.prepareToolTurn(ctx, a, sess)
	if err != nil {
		return "", err
	}
	defer cleanup()

	reply, err := a.Run(ctx, userInput, tools.DefaultToolsForCtx(ctx, a.Model), executor)
	if err != nil {
		return "", err
	}

	sess.SyncFrom(a.History)
	return reply.Content, nil
}

func permissionConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".octo", "permissions.yml")
}

// resolvePermissionMode reads the persisted config and returns the configured
// permission mode (or ModeInteractive as the default). This lets the server
// pick up mode changes written by the setup panel / onboard skill without
// restarting.
func resolvePermissionMode() permission.Mode {
	return permission.ResolveDefaultMode()
}

// ─── POST /api/file-action ────────────────────────────────────────────────

// fileActionRequest is sent when the user clicks a file:// link in chat.
type fileActionRequest struct {
	Path   string `json:"path"`
	Action string `json:"action"` // "open" or "download"
}

// handleFileAction handles file:// links from the chat UI.
// When action is "open" and the server is running on localhost,
// it attempts to open the file with the OS default handler.
// When action is "download", it streams the file contents back.
// The path is always resolved under the server's working directory (s.cwd);
// absolute paths or paths that escape cwd are rejected.
func (s *Server) handleFileAction(w http.ResponseWriter, r *http.Request) {
	var req fileActionRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	abs, ok := resolveUnderCWD(s.curCwd(), req.Path)
	if !ok {
		writeError(w, http.StatusForbidden, "path is outside the working directory")
		return
	}

	switch req.Action {
	case "open":
		// Only allow opening files on localhost for security.
		host := r.Host
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			writeError(w, http.StatusForbidden, "open action only allowed on localhost")
			return
		}
		var cmd string
		switch runtime.GOOS {
		case "darwin":
			cmd = "open"
		case "windows":
			cmd = "start"
		default:
			cmd = "xdg-open"
		}
		if err := exec.Command(cmd, abs).Start(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "opened"})

	case "download":
		f, err := os.Open(abs)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if info.IsDir() {
			writeError(w, http.StatusBadRequest, "cannot download a directory")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(abs)+"\"")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		http.ServeContent(w, r, filepath.Base(abs), info.ModTime(), f)

	default:
		writeError(w, http.StatusBadRequest, "unknown action")
	}
}

// ─── PATCH /api/sessions/{id} ───────────────────────────────────────────────

// updateSessionRequest carries the user-editable session fields. Only the
// title (the sidebar "name") is editable today.
type updateSessionRequest struct {
	Name string `json:"name"`
}

// handleUpdateSession renames a session — the sidebar's rename action.
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	var req updateSessionRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := sess.SetTitle(name); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("set title: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": s.toSessionItem(sess, "manual", "general")})
}

// ─── PATCH /api/sessions/{id}/model ─────────────────────────────────────────

type updateSessionModelRequest struct {
	ModelID string `json:"model_id"`
}

// handleUpdateSessionModel switches THIS session's model: model_id naming a
// config entry binds the session to it (provider, endpoint, key — the whole
// entry, applied from the next turn via the per-entry sender cache); other
// values are treated as a raw model string on the session, staying on the
// default sender. The global default model is not touched — that's
// POST /api/config/models/{id}/default.
func (s *Server) handleUpdateSessionModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	var req updateSessionModelRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.ModelID == "" {
		writeError(w, http.StatusBadRequest, "model_id is required")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if ok, _, berr := s.acquireSessionBinding(id, agent.EntryWeb, false); !ok {
		writeError(w, http.StatusConflict, berr.Error())
		return
	}
	defer s.releaseSessionBinding(id, agent.EntryWeb)

	// Reload after acquiring the binding in case another process saved.
	sess, err = agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	cfg, _ := config.Load()
	var model string
	if entry, ok := cfg.EntryByModel(req.ModelID); ok {
		model = entry.Model
		err = sess.SetModelConfig(entry.Model, entry.Model)
	} else if req.ModelID == "default" {
		// Legacy id for "the default entry". Unbind so the session follows
		// whatever the default is at turn time.
		model = cfg.DefaultEntry().Model
		if model == "" {
			writeError(w, http.StatusBadRequest, "no default model configured")
			return
		}
		err = sess.SetModelConfig("", model)
	} else {
		// Raw model string: keep the default sender, change only the model.
		model = req.ModelID
		err = sess.SetModelConfig("", model)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save session: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"model":    model,
		"model_id": req.ModelID,
	})
}

// ─── PATCH /api/sessions/{id}/reasoning_effort ──────────────────────────────

type updateSessionReasoningEffortRequest struct {
	ReasoningEffort string `json:"reasoning_effort"`
}

// handleUpdateSessionReasoningEffort updates the reasoning-effort tuning for
// the model THIS session actually runs on (see entryForSession) — not always
// the default entry, for the same reason and by the same fix as
// handleUpdateSessionShowReasoning's doc comment describes.
// Valid levels: "off", "low", "medium", "high", "xhigh", "max". Empty is normalised to "off".
func (s *Server) handleUpdateSessionReasoningEffort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	var req updateSessionReasoningEffortRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	level := strings.ToLower(req.ReasoningEffort)
	if level == "" {
		level = "off"
	}
	if level != "off" && level != "low" && level != "medium" && level != "high" && level != "xhigh" && level != "max" {
		writeError(w, http.StatusBadRequest, "reasoning_effort must be off, low, medium, high, xhigh, or max")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Resolve and mutate the entry THIS session actually runs on (see
	// entryForSession) — not always the default entry, so a session pinned
	// to a non-default model actually gets its own reasoning_effort changed
	// instead of silently editing an unrelated model's config.
	cfg, _ := config.Load()
	entry := entryForSession(cfg, sess)
	// "off" is only ever a wire/UI sentinel — the persisted and forwarded
	// representation of "no reasoning effort" is "" everywhere else in the
	// codebase (CLI, TUI, provider layer). Storing "off" verbatim used to
	// reach provider requests as a literal, invalid reasoning_effort value.
	if level == "off" {
		entry.ReasoningEffort = ""
	} else {
		entry.ReasoningEffort = level
	}
	// Reasoning off has nothing to show a trace of; keep show_reasoning from
	// staying on in a way that looks toggled on but does nothing.
	if level == "off" {
		off := false
		entry.ShowReasoning = &off
	}
	if !cfg.SetEntry(entry) {
		cfg.SetDefaultEntry(entry)
	}
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	// The default sender embeds reasoning_effort; rebuild it so existing
	// unbound sessions pick up the new effort on their next turn. Per-entry
	// senders are rebuilt lazily via invalidateSenderCache.
	s.invalidateSenderCache()
	if err := s.reloadDefaultSender(); err != nil {
		slog.Error("reload default sender after reasoning_effort change", "err", err)
	}

	// Push each session's own effective reasoning_effort — resolved against
	// ITS OWN model entry, not the one that just changed — so a toggle for
	// this session's model doesn't paint every other open tab's status bar
	// with this session's value.
	if s.wsHub != nil {
		sessions, _ := agent.ListSessions(50)
		for _, sess := range sessions {
			_, pm, re, sr, _ := s.sessionStatusFields(sess)
			s.wsHub.broadcast(sess.ID, map[string]any{
				"type":             "session_update",
				"session_id":       sess.ID,
				"working_dir":      s.sessionCwd(sess),
				"permission_mode":  pm,
				"reasoning_effort": re,
				"show_reasoning":   sr,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"reasoning_effort": level,
	})
}

// ─── PATCH /api/sessions/{id}/permission_mode ───────────────────────────────

type updateSessionPermissionModeRequest struct {
	PermissionMode string `json:"permission_mode"`
}

// handleUpdateSessionPermissionMode updates THIS session's own permission
// mode — the Web equivalent of the TUI's shift+tab cycle. Valid values:
// "interactive", "auto", "strict". Per-session: it never touches the global
// default (~/.octo/config.yml, edited instead via Settings → default model),
// so it only affects this session, not other sessions and not what a
// brand-new session inherits. The per-turn permission engine reads
// sess.PermissionMode (see prepareToolTurn/runChannelTurns), so the change
// takes effect on this session's next turn without a sender rebuild.
func (s *Server) handleUpdateSessionPermissionMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	var req updateSessionPermissionModeRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	mode := strings.ToLower(req.PermissionMode)
	switch mode {
	case string(permission.ModeInteractive), string(permission.ModeAutoApprove), string(permission.ModeStrict):
	default:
		writeError(w, http.StatusBadRequest, "permission_mode must be interactive, auto, or strict")
		return
	}

	if _, err := agent.LoadSession(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if ok, _, berr := s.acquireSessionBinding(id, agent.EntryWeb, false); !ok {
		writeError(w, http.StatusConflict, berr.Error())
		return
	}
	defer s.releaseSessionBinding(id, agent.EntryWeb)

	// Reload after acquiring the binding in case another process saved.
	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := sess.SetPermissionMode(mode); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save session: %v", err))
		return
	}

	// Push the new mode so this session's composer pill refreshes without
	// waiting for the next turn's session_update. Only this session — it's
	// the only one whose own mode changed.
	if s.wsHub != nil {
		s.wsHub.broadcast(id, map[string]any{
			"type":            "session_update",
			"session_id":      id,
			"permission_mode": mode,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"permission_mode": mode,
	})
}

// ─── PATCH /api/sessions/{id}/show_reasoning ────────────────────────────────

type updateSessionShowReasoningRequest struct {
	ShowReasoning bool `json:"show_reasoning"`
}

// handleUpdateSessionShowReasoning updates whether reasoning traces are shown
// for the model THIS session actually runs on (see entryForSession) — not
// always the default entry. Before this, the toggle always wrote to
// cfg.DefaultEntry(): for a session pinned to a non-default model, the
// Composer's eye icon still visibly flipped (the status read came from the
// same wrong entry the write used), but the session's real turns — gated by
// senderForSession's correct per-entry resolution — never actually changed,
// so reasoning kept showing (or hiding) no matter how many times it was
// toggled.
func (s *Server) handleUpdateSessionShowReasoning(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	var req updateSessionShowReasoningRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	cfg, _ := config.Load()
	entry := entryForSession(cfg, sess)
	entry.ShowReasoning = &req.ShowReasoning
	if !cfg.SetEntry(entry) {
		cfg.SetDefaultEntry(entry)
	}
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	// The default sender embeds show_reasoning; rebuild it so existing
	// unbound sessions pick up the new value on their next turn. Per-entry
	// senders are rebuilt lazily via invalidateSenderCache.
	s.invalidateSenderCache()
	if err := s.reloadDefaultSender(); err != nil {
		slog.Error("reload default sender after show_reasoning change", "err", err)
	}

	// Push each session's own effective show_reasoning — resolved against ITS
	// OWN model entry, not the one that just changed — so a toggle for this
	// session's model doesn't paint every other open tab's status bar with
	// this session's value.
	if s.wsHub != nil {
		sessions, _ := agent.ListSessions(50)
		for _, sess := range sessions {
			_, pm, re, sr, _ := s.sessionStatusFields(sess)
			s.wsHub.broadcast(sess.ID, map[string]any{
				"type":             "session_update",
				"session_id":       sess.ID,
				"working_dir":      s.sessionCwd(sess),
				"permission_mode":  pm,
				"reasoning_effort": re,
				"show_reasoning":   sr,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"show_reasoning": req.ShowReasoning,
	})
}

// ─── PATCH /api/sessions/{id}/working_dir ───────────────────────────────────

type updateSessionWorkingDirRequest struct {
	WorkingDir string `json:"working_dir"`
}

// handleUpdateSessionWorkingDir sets THIS session's working directory: the cwd
// its tools run in, the root its project hooks/skills resolve against, and the
// path shown in its env context, applied from the next turn. It is per-session
// — other sessions (and the server default for new ones) are untouched — so
// retargeting one session can't silently move another's tools. The dir must
// exist; a leading ~ expands to the home directory and relative paths resolve
// against the server's launch dir.
func (s *Server) handleUpdateSessionWorkingDir(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	var req updateSessionWorkingDirRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.WorkingDir == "" {
		writeError(w, http.StatusBadRequest, "working_dir is required")
		return
	}

	dir := expandDir(req.WorkingDir)
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("working_dir does not exist: %s (create it first)", dir))
		return
	case os.IsPermission(err):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("working_dir is not accessible: %s (permission denied)", dir))
		return
	case err != nil:
		// Neither IsNotExist nor IsPermission — e.g. a path component that
		// exists but isn't a directory (ENOTDIR). Report the reason without
		// the raw "stat <path>:" prefix, which just repeats dir.
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid working_dir: %s (%v)", dir, unwrapPathError(err)))
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "working_dir is not a directory")
		return
	}

	if _, err := agent.LoadSession(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if ok, _, berr := s.acquireSessionBinding(id, agent.EntryWeb, false); !ok {
		writeError(w, http.StatusConflict, berr.Error())
		return
	}
	defer s.releaseSessionBinding(id, agent.EntryWeb)

	// Reload after acquiring the binding in case another process saved.
	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := sess.SetWorkingDir(dir); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save session: %v", err))
		return
	}

	// Push the new dir so the composer's cwd chip refreshes without waiting for
	// the next turn's session_update.
	if s.wsHub != nil {
		s.wsHub.broadcast(id, map[string]any{
			"type":        "session_update",
			"session_id":  id,
			"working_dir": dir,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"working_dir": dir,
	})
}

// unwrapPathError strips the "stat <path>:" wrapper os.Stat adds so the
// error message doesn't repeat the path the caller already reports.
func unwrapPathError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// expandDir resolves a user-entered path to an absolute one: a leading ~ (or
// ~/…) becomes the home directory, and relative paths are taken against the
// server process's launch dir. On any failure it returns the input unchanged
// and lets the caller's os.Stat surface the error.
func expandDir(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
