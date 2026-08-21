package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/scheduler"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Tasks REST API ─────────────────────────────────────────────────────────

type taskRequest struct {
	Name      string                  `json:"name"`
	Cron      string                  `json:"cron"`
	Prompt    string                  `json:"prompt"`
	Model     string                  `json:"model,omitempty"`
	Agent     string                  `json:"agent,omitempty"` // deprecated
	AgentID   string                  `json:"agent_id,omitempty"`
	Directory string                  `json:"directory,omitempty"`
	Notify    scheduler.NotifyTargets `json:"notify,omitempty"`
}

type taskResponse struct {
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	Cron           string                  `json:"cron"`
	Prompt         string                  `json:"prompt"`
	Model          string                  `json:"model,omitempty"`
	Agent          string                  `json:"agent,omitempty"` // deprecated
	AgentID        string                  `json:"agent_id,omitempty"`
	Directory      string                  `json:"directory,omitempty"`
	Notify         scheduler.NotifyTargets `json:"notify,omitempty"`
	Enabled        bool                    `json:"enabled"`
	CreatedAt      string                  `json:"created_at,omitempty"`
	LastRun        string                  `json:"last_run,omitempty"`
	NextRun        string                  `json:"next_run,omitempty"`
	SessionID      string                  `json:"session_id,omitempty"`
	SessionGroupID string                  `json:"session_group_id,omitempty"`
}

// initScheduler creates the scheduler if not already initialized. It is
// called eagerly from ListenAndServe so scheduled tasks fire from server
// start; the calls in individual handlers remain as a safety net (and as the
// only path in tests that exercise the mux directly).
func (s *Server) initScheduler() {
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	if s.scheduler != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".octo", "tasks")
	sch, err := scheduler.New(dir, s)
	if err != nil {
		slog.Error("scheduler", "err", err)
		return
	}
	sch.Start()
	s.scheduler = sch
}

// createCronProject creates the project a task's runs cluster under. The
// workspace is generated like every other project's (workspaceDirForTask) —
// cron and regular projects share one shape — and an explicit task directory
// is mounted as the output folder rather than adopted as the directory itself:
// the runs' deliverables land there, their scratch stays in the workspace.
// An unusable explicit directory is dropped with a log, not fatal: a task
// must run regardless.
func (s *Server) createCronProject(task scheduler.Task) (sessionGroup, error) {
	var sourceDirs []string
	outputDir := ""
	if dir := strings.TrimSpace(task.Directory); dir != "" {
		mounted, verr := validateSourceDirs(s.curWorkspaceDir(), []string{dir})
		if verr != nil || len(mounted) == 0 {
			slog.Warn("cron: explicit directory unusable; dropping it", "task", task.Name, "dir", dir, "err", verr)
		} else {
			sourceDirs = mounted
			outputDir = mounted[0]
		}
	}
	groupMu.LockWrite()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		return sessionGroup{}, err
	}
	// Workspace generation inside the write lock, same standard as the HTTP
	// create handler: the claim check plus mkdir must be atomic or two
	// same-named tasks created concurrently share one directory. The snapshot
	// variant, because the lock is not reentrant and is already held here.
	workspace, werr := workspaceDirForTask(groups, s.curWorkspaceDir(), task.Name, task.ID)
	if werr != nil {
		return sessionGroup{}, werr
	}
	g := sessionGroup{ID: newGroupID(), Name: task.Name, SessionIDs: []string{}, WorkingDir: workspace, SourceDirs: sourceDirs, OutputDir: outputDir, TaskID: task.ID}
	groups = append(groups, g)
	if err := saveSessionGroups(groups); err != nil {
		return sessionGroup{}, err
	}
	return g, nil
}

// CreateSession implements scheduler.Runner. Every run creates a brand-new,
// empty session — each run starts from a clean transcript, and the previous
// run's session is left on disk. The session is filed under the task's Web-UI
// group (created lazily here for tasks that predate grouping), so all of a
// task's runs cluster together in the sidebar, named by date.
func (s *Server) CreateSession(task scheduler.Task) (string, error) {
	sessionID, err := s.newSession(task)
	if err != nil {
		return "", err
	}

	// File the session under the task's group. Grouping is a best-effort
	// convenience layer — a failure here must not fail the run, so errors are
	// logged, not returned.
	groupID := task.SessionGroupID
	if groupID == "" {
		// A task predating grouping (or whose group creation failed at create
		// time): create the group now and persist its ID back on the task so
		// later runs reuse it.
		g, gerr := s.createCronProject(task)
		if gerr != nil {
			slog.Warn("cron: create session group", "task", task.Name, "err", gerr)
		} else {
			groupID = g.ID
			if s.scheduler != nil {
				if serr := s.scheduler.SetSessionGroup(task.ID, groupID); serr != nil {
					slog.Warn("cron: persist session group", "task", task.Name, "err", serr)
				}
			}
		}
	}
	if groupID != "" {
		if gerr := addSessionToGroup(groupID, sessionID); gerr != nil {
			slog.Warn("cron: add session to group", "task", task.Name, "err", gerr)
		}
	}

	// Announce the new session globally. A scheduled fire has no subscribed
	// tab yet, and every per-session broadcast in RunTask is dropped for a
	// session nobody subscribed to — without this, an open sidebar can't learn
	// the session (or its group filing) exists until a manual reload. Emitted
	// after the group write above so a tab that refetches on receipt sees the
	// membership already on disk.
	s.wsHub.broadcast("", wsEventSessionCreated{Type: "session_created", SessionID: sessionID})
	return sessionID, nil
}

// newSession creates and persists a brand-new agent session for a task run,
// seeded with the task's model, working directory, and unattended permission
// mode. The title is the run's local date and time so a task's runs are
// distinguishable within its group (the group carries the task name).
func (s *Server) newSession(task scheduler.Task) (string, error) {
	model := task.Model
	if model == "" {
		model = s.model
	}
	sess := agent.NewSession(model, s.system)
	sess.Source = "cron"
	sess.Title = time.Now().Format("2006-01-02 15:04")
	sess.AgentID = task.AgentID
	// Cron ticks have no human to answer an ask prompt, unlike the web/IM
	// default this mirrors — see ResolveUnattendedDefaultMode's doc comment.
	_ = sess.SetPermissionMode(string(permission.ResolveUnattendedDefaultMode()))
	// task.Directory only seeds the session's WorkingDir here, once, at
	// creation. After that, sess.WorkingDir (editable any time via the
	// web Composer's directory chip, PATCH /api/sessions/{id}/working_dir)
	// is the single source of truth for where this session's tools run —
	// buildAgent derives both a.CWD and the system prompt's "Working
	// directory" note from it every turn, and prepareToolTurn wires
	// tools.WithWorkingDir from a.CWD, so nothing else needs to touch it.
	// Editing task.Directory later only affects the NEXT session created
	// for this task, never one that already exists.
	if task.Directory != "" {
		if err := seedSessionDirectory(sess, task.Directory); err != nil {
			// sess was never Saved — sess.ID names a session that exists
			// only in memory, not on disk. Returning it here would let
			// fire() (internal/scheduler/scheduler.go) persist it onto
			// task.SessionID unconditionally (it doesn't check err before
			// writing), permanently dangling the task on a session
			// agent.LoadSession can never load — every subsequent cron
			// tick would then hit this exact same error again with a
			// fresh throwaway ID, forever. Return "" instead so a bad
			// task.Directory can never leak into task.SessionID.
			return "", err
		}
	}
	if err := sess.Save(); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}
	return sess.ID, nil
}

// seedSessionDirectory validates dir and applies it as sess's working
// directory (see CreateSession's doc comment for why this only ever happens
// once, at session creation).
func seedSessionDirectory(sess *agent.Session, dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("task directory %q: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("task directory %q is not a directory", dir)
	}
	return sess.SetWorkingDir(dir)
}

// RunTask implements scheduler.Runner. It runs a single streamed turn in the
// task's session (created by the scheduler before this call, so each run gets a
// fresh session — see scheduler.fire/RunNow), so any subscribed web UI tab sees
// the same live progress, tool cards, and completion events as a normal chat
// turn.
func (s *Server) RunTask(ctx context.Context, task scheduler.Task) (sessionID string, err error) {
	// The scheduler fires this from a bare goroutine (go s.run), so a panic in
	// a scheduled turn would crash the whole serve process without this. Named
	// returns let the recover surface the panic as an error, so the scheduler
	// records the run as failed rather than silently as a success.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered panic in scheduled task", "task", task.Name, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("scheduled task %q panicked: %v", task.Name, r)
		}
	}()
	if derr := s.drain.begin(); derr != nil {
		return "", derr
	}
	defer s.drain.end()

	// The scheduler creates the fresh session before calling RunTask. A direct
	// caller (or a defensive path) that left it empty gets one created here.
	sessionID = task.SessionID
	if sessionID == "" {
		sessionID, err = s.CreateSession(task)
		if err != nil {
			return sessionID, err
		}
		task.SessionID = sessionID
	}

	if ok, _, berr := s.acquireSessionBinding(sessionID, agent.EntryCron, false); !ok {
		return sessionID, fmt.Errorf("acquire binding: %w", berr)
	}

	mu := s.sessionTurnLock(sessionID)
	mu.Lock()
	defer func() {
		mu.Unlock()
		s.releaseSessionBinding(sessionID, agent.EntryCron)
	}()

	// Reload the authoritative session after acquiring the binding.
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		return sessionID, fmt.Errorf("reload session: %w", err)
	}

	// Persist the task prompt as the turn's user message and set the history
	// watermark so mid-turn history fetches don't double-render live events.
	userMsg := agent.NewUserMessage(task.Prompt)
	sess.Messages = append(sess.Messages, userMsg)
	_ = sess.Save()
	historyWatermark := len(sess.Messages)
	sess.Messages = sess.Messages[:len(sess.Messages)-1]

	sw := s.newWSStreamWriter(sessionID)

	// Broadcast the user message immediately so the transcript shows what the
	// task is doing while it runs. message_index mirrors doAgentTurn: without
	// it an edit/branch on this bubble would send index 0 and clobber the
	// session's first message.
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":          "history_user_message",
		"session_id":    sessionID,
		"content":       task.Prompt,
		"created_at":    userMsg.CreatedAt.UnixMilli(),
		"message_index": len(sess.Messages),
	})

	// Seed the live state with a "thinking" progress indicator so late
	// subscribers and the initial tab see the turn as running.
	startedAt := time.Now().UnixMilli()
	s.liveStateMu.Lock()
	s.liveStates[sessionID] = &sessionLiveState{
		progress: &wsEventProgress{
			Type:         "progress",
			ProgressType: "thinking",
			Phase:        "active",
			StartedAt:    startedAt,
		},
		historyWatermark: historyWatermark,
	}
	s.liveStateMu.Unlock()
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":          "progress",
		"session_id":    sessionID,
		"progress_type": "thinking",
		"phase":         "active",
		"status":        "start",
		"started_at":    startedAt,
	})

	defer func() {
		s.liveStateMu.Lock()
		delete(s.liveStates, sessionID)
		s.liveStateMu.Unlock()
	}()

	if err := s.ensureSender(); err != nil {
		sw.userError(err)
		return sessionID, fmt.Errorf("sender: %w", err)
	}

	// Register the turn's interrupt so sessionStatus reports "running" and the
	// web UI offers the stop button.
	runCtx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKeySessionID{}, sessionID))
	runCtx = tools.WithSessionID(runCtx, sessionID)
	s.registerInterrupt(sessionID, cancel)
	// Global running-state pair: cron fires session_created before this turn
	// body runs, which makes every tab refresh its session list and seed
	// status "running" — without the paired turn_ended, an unsubscribed tab's
	// sidebar spinner would stick forever (session_update below only reaches
	// subscribers).
	defer s.broadcastTurnActivityStart(sessionID)()
	defer func() {
		cancel()
		s.interruptMu.Lock()
		delete(s.interrupts, sessionID)
		s.interruptMu.Unlock()
	}()

	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       "session_update",
		"session_id": sessionID,
		"status":     "running",
	})

	// buildAgent derives a.CWD (and the system prompt's "Working directory"
	// note) from sess.WorkingDir, which CreateSession seeded from
	// task.Directory when this session was first created — nothing task-
	// specific needs to happen here; prepareToolTurn below wires
	// tools.WithWorkingDir from a.CWD the same way it does for every other
	// session.
	a := s.buildAgent(sess)

	var toolDefs []agent.ToolDefinition
	var executor agent.ToolExecutor
	var cleanup func()
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	if s.cfg.Tools {
		var perr error
		runCtx, executor, _, cleanup, perr = s.prepareToolTurn(runCtx, a, sess)
		if perr != nil {
			sw.userError(perr)
			return sessionID, fmt.Errorf("prepare tools: %w", perr)
		}
		toolDefs = tools.DefaultToolsForCtx(runCtx, a.Model)
		s.wireBackgroundTaskNotices(sessionID)
	}

	lastSavedLen := -1
	persistTurnProgress := func() {
		if n := a.History.Len(); n != lastSavedLen || a.History.RewriteDirty() {
			sess.SyncFrom(a.History)
			if sess.Save() == nil {
				lastSavedLen = n
			}
		}
	}
	handler := func(ev agent.AgentEvent) {
		sw.handleEvent(ev)
		persistTurnProgress()
	}

	turnCallStart := time.Now()
	reply, err := a.RunStream(runCtx, task.Prompt, toolDefs, executor, handler)

	sess.SyncFrom(a.History)
	_ = sess.Save()
	// Record the real context-token count so this cron session shows accurate
	// usage when opened in the Web UI (parity with web/desktop turns).
	if perr := a.PersistContextUsage(sess); perr != nil {
		slog.Warn("scheduled task: persist context tokens", "task", task.Name, "err", perr)
	}

	s.liveStateMu.Lock()
	delete(s.liveStates, sessionID)
	s.liveStateMu.Unlock()

	if err != nil {
		if !errors.Is(err, context.Canceled) {
			sw.userError(err)
		}
		// A first-round failure rolls the task prompt back out of history
		// (an interrupt no longer does — finishInterrupted keeps the prompt)
		// and the SyncFrom+Save above erased the persisted copy — tell
		// watching tabs to re-fetch so their message indices realign with disk
		// (same contract as doAgentTurn).
		if len(sess.Messages) < historyWatermark {
			s.broadcastHistoryReload(sessionID)
		}
		s.notifyTaskResult(task, fmt.Sprintf("⏰ %s failed: %s", task.Name, agent.UserFacingError(err)))
	} else {
		rCopy := reply
		s.wsHub.broadcast(sessionID, map[string]any{
			"type":       "turn_done",
			"session_id": sessionID,
			"reply":      map[string]any{"content": rCopy.Content},
		})
		s.wsHub.broadcast(sessionID, map[string]any{
			"type":       "assistant_message",
			"session_id": sessionID,
			"content":    rCopy.Content,
			"thinking":   extractThinking(&rCopy),
		})
		s.notifyTaskResult(task, fmt.Sprintf("⏰ %s\n\n%s", task.Name, reply.Content))
	}

	completeEvent := map[string]any{
		"type":       "complete",
		"session_id": sessionID,
		"iterations": a.TurnIterations(),
	}
	// Same as doAgentTurn: hand the browser the reply's persisted index so a
	// tab watching this task's session can branch off the live bubble.
	if idx := lastBranchableIndex(sess); idx >= 0 {
		completeEvent["message_index"] = idx
	}
	if err == nil {
		// a is freshly built per turn (buildAgent), so its usage counters start
		// at zero — no before/after diff needed here.
		inTok, outTok := a.SessionTokens()
		completeEvent["duration_ms"] = time.Since(turnCallStart).Milliseconds()
		completeEvent["tokens"] = inTok + outTok
		// Cache utilization for the turn's prompt side; omitted (not 0) when
		// the backend reported no cache activity, so the UI hides the readout.
		cr, cw := a.SessionCacheTokens()
		if pct, ok := agent.CacheUtilizationPct(inTok, cr, cw); ok {
			completeEvent["cache_pct"] = pct
		}
	}
	s.wsHub.broadcast(sessionID, completeEvent)

	used, window := a.ContextUsage()
	ctxPct := 0
	if window > 0 {
		ctxPct = used * 100 / window
		if ctxPct > 100 {
			ctxPct = 100
		}
	}
	_, pm, re, _, _ := s.sessionStatusFields(sess)
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":             "session_update",
		"session_id":       sessionID,
		"status":           "idle",
		"context_usage":    ctxPct,
		"context_tokens":   used,
		"working_dir":      s.sessionCwdByID(sessionID),
		"permission_mode":  pm,
		"reasoning_effort": re,
	})

	if err != nil {
		return sessionID, fmt.Errorf("run task: %w", err)
	}
	return sessionID, nil
}

// notifyTaskResult pushes a task run's outcome to every configured IM notify
// target. Delivery failures are logged per target, never fatal — the run
// itself already happened and is recorded in the session, and one channel
// failing must not silence the others.
func (s *Server) notifyTaskResult(task scheduler.Task, text string) {
	for _, n := range task.Notify {
		if err := s.channelSend(n.Platform, n.ChatID, text); err != nil {
			log.Printf("[scheduler] task %q notify %s/%s: %v", task.Name, n.Platform, n.ChatID, err)
		}
	}
}

// channelSend delivers one message to an IM chat, preferring the live adapter
// started by this server (connected, with fresh per-chat state like weixin
// context tokens) and falling back to channel.SendOnce — a one-shot adapter
// built from config — when the platform isn't running here (--no-channel,
// disabled, or failed to start).
func (s *Server) channelSend(platform, chatID, text string) error {
	if v, ok := s.runningAdapters.Load(platform); ok {
		if res := v.(channel.Adapter).SendText(chatID, text, ""); !res.OK {
			return fmt.Errorf("send to %s chat %s: %s", platform, chatID, res.Error)
		}
		return nil
	}
	return channel.SendOnce(platform, chatID, text)
}

// channelSendFile is the file counterpart to channelSend: same live-adapter-then-
// SendFileOnce fallback, delivering a local file instead of text.
func (s *Server) channelSendFile(platform, chatID, path, name string) error {
	if v, ok := s.runningAdapters.Load(platform); ok {
		if res := v.(channel.Adapter).SendFile(chatID, path, name, ""); !res.OK {
			return fmt.Errorf("send file to %s chat %s: %s", platform, chatID, res.Error)
		}
		return nil
	}
	return channel.SendFileOnce(platform, chatID, path, name)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	s.initScheduler()
	if s.scheduler == nil {
		writeJSON(w, http.StatusOK, []taskResponse{})
		return
	}
	tasks := s.scheduler.List()
	filterID := r.URL.Query().Get("agent_id")
	out := make([]taskResponse, 0, len(tasks))
	for _, t := range tasks {
		if filterID != "" && t.AgentID != filterID {
			continue
		}
		out = append(out, s.taskToResponse(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	s.initScheduler()
	if s.scheduler == nil {
		writeError(w, http.StatusInternalServerError, "scheduler not available")
		return
	}
	var req taskRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	if req.Name == "" || req.Cron == "" || req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "name, cron, and prompt are required")
		return
	}
	if err := s.validateAgentID(req.AgentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task := scheduler.Task{
		Name:      req.Name,
		Cron:      req.Cron,
		Prompt:    req.Prompt,
		Model:     req.Model,
		Agent:     req.Agent,
		AgentID:   req.AgentID,
		Directory: req.Directory,
		Notify:    req.Notify,
		Enabled:   true,
	}
	if err := s.scheduler.Add(&task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Give the task its own session group so every run clusters under it.
	// Created after Add (which validates the cron and assigns the ID) so an
	// invalid task never leaves an orphan group; best-effort, since a run can
	// still create the group lazily if this fails.
	if g, gerr := s.createCronProject(task); gerr != nil {
		slog.Warn("cron: create session group", "task", task.Name, "err", gerr)
	} else if serr := s.scheduler.SetSessionGroup(task.ID, g.ID); serr != nil {
		slog.Warn("cron: persist session group", "task", task.Name, "err", serr)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": task.ID})
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	s.initScheduler()
	if s.scheduler == nil {
		writeError(w, http.StatusInternalServerError, "scheduler not available")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing task id")
		return
	}
	// Delete the task's session group too (its member sessions are kept — they
	// fall back to ungrouped). Look it up before Delete removes the task.
	if t, gerr := s.scheduler.Get(id); gerr == nil && t.SessionGroupID != "" {
		if derr := deleteSessionGroup(t.SessionGroupID); derr != nil {
			slog.Warn("cron: delete session group", "task", t.Name, "err", derr)
		}
	}
	if err := s.scheduler.Delete(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleRunTask(w http.ResponseWriter, r *http.Request) {
	s.initScheduler()
	if s.scheduler == nil {
		writeError(w, http.StatusInternalServerError, "scheduler not available")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing task id")
		return
	}
	sessionID, err := s.scheduler.RunNow(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started", "id": id, "session_id": sessionID})
}

// handleTransferTask serves PUT /api/tasks/:id/transfer — transfer a task's
// ownership to another agent. Only the default agent (or an admin) may
// transfer; the design scopes this to the default agent view.
func (s *Server) handleTransferTask(w http.ResponseWriter, r *http.Request) {
	s.initScheduler()
	if s.scheduler == nil {
		writeError(w, http.StatusInternalServerError, "scheduler not available")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing task id")
		return
	}
	var req agentTransferRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if err := s.validateAgentID(req.AgentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.scheduler.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	task.AgentID = req.AgentID
	if err := s.scheduler.Update(*task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.taskToResponse(*task))
}

// patchTaskRequest is the body for PATCH /api/tasks/{id}. Every field is a
// pointer so the handler only touches what the caller actually sent — a
// partial update. Enabling/disabling is just {"enabled": ...}.
type patchTaskRequest struct {
	Enabled   *bool                    `json:"enabled,omitempty"`
	Cron      *string                  `json:"cron,omitempty"`
	Prompt    *string                  `json:"prompt,omitempty"`
	Model     *string                  `json:"model,omitempty"`
	Agent     *string                  `json:"agent,omitempty"` // deprecated
	AgentID   *string                  `json:"agent_id,omitempty"`
	Directory *string                  `json:"directory,omitempty"`
	Notify    *scheduler.NotifyTargets `json:"notify,omitempty"`
	Name      *string                  `json:"name,omitempty"`
}

// handlePatchTask updates any subset of a scheduled task's fields and reschedules
// the live cron entry, persisting so the change survives restart and takes
// effect immediately. This is the single edit endpoint — it subsumes the former
// enable/disable toggle (send {"enabled": false}) and the retired
// /api/cron-tasks/{name} route.
func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	s.initScheduler()
	if s.scheduler == nil {
		writeError(w, http.StatusInternalServerError, "scheduler not available")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing task id")
		return
	}
	var req patchTaskRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	task, err := s.scheduler.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		*req.Name = name
		task.Name = name
	}
	if req.Enabled != nil {
		task.Enabled = *req.Enabled
	}
	if req.Cron != nil {
		task.Cron = *req.Cron
	}
	if req.Prompt != nil {
		task.Prompt = *req.Prompt
	}
	if req.Model != nil {
		task.Model = *req.Model
	}
	if req.Agent != nil {
		task.Agent = *req.Agent
	}
	if req.AgentID != nil {
		task.AgentID = *req.AgentID
	}
	if req.Directory != nil {
		task.Directory = *req.Directory
	}
	if req.Notify != nil {
		task.Notify = *req.Notify
	}
	if err := s.validateAgentID(task.AgentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.scheduler.Update(*task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Keep the task's session group name in sync with the task name so the
	// sidebar group label follows a rename. Best-effort; a missing group (the
	// task predates grouping, or the group was deleted in the UI) is a no-op.
	if req.Name != nil && task.SessionGroupID != "" {
		if gerr := renameSessionGroup(task.SessionGroupID, *req.Name); gerr != nil {
			slog.Warn("cron: rename session group", "task", task.Name, "err", gerr)
		}
	}
	writeJSON(w, http.StatusOK, s.taskToResponse(*task))
}

func (s *Server) taskToResponse(t scheduler.Task) taskResponse {
	r := taskResponse{
		ID:        t.ID,
		Name:      t.Name,
		Cron:      t.Cron,
		Prompt:    t.Prompt,
		Model:     t.Model,
		Agent:     t.Agent,
		AgentID:   t.AgentID,
		Directory: t.Directory,
		Notify:    t.Notify,
		Enabled:   t.Enabled,
	}
	// A bare trailing "Z" is a literal in a Go layout, not the UTC designator,
	// so formatting a local time with it used to claim local wall-clock as UTC
	// and every client shifted these stamps by the local offset. Convert first,
	// then let RFC3339 write the real "Z".
	if !t.CreatedAt.IsZero() {
		r.CreatedAt = t.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !t.LastRun.IsZero() {
		r.LastRun = t.LastRun.UTC().Format(time.RFC3339)
	}
	if s.scheduler != nil {
		if next := s.scheduler.NextRun(t.ID); !next.IsZero() {
			r.NextRun = next.UTC().Format(time.RFC3339)
		}
	}
	r.SessionID = t.SessionID
	r.SessionGroupID = t.SessionGroupID
	return r
}
