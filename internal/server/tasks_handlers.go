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
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/scheduler"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Tasks REST API ─────────────────────────────────────────────────────────

type taskRequest struct {
	Name      string                  `json:"name"`
	Cron      string                  `json:"cron"`
	Prompt    string                  `json:"prompt"`
	Model     string                  `json:"model,omitempty"`
	Agent     string                  `json:"agent,omitempty"`
	Directory string                  `json:"directory,omitempty"`
	Notify    scheduler.NotifyTargets `json:"notify,omitempty"`
}

type taskResponse struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Cron      string                  `json:"cron"`
	Prompt    string                  `json:"prompt"`
	Model     string                  `json:"model,omitempty"`
	Agent     string                  `json:"agent,omitempty"`
	Directory string                  `json:"directory,omitempty"`
	Notify    scheduler.NotifyTargets `json:"notify,omitempty"`
	Enabled   bool                    `json:"enabled"`
	CreatedAt string                  `json:"created_at,omitempty"`
	LastRun   string                  `json:"last_run,omitempty"`
	SessionID string                  `json:"session_id,omitempty"`
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

// CreateSession implements scheduler.Runner. It creates or reuses the session
// for a task and persists it immediately so the web UI can open the session
// before the (potentially long) agent turn starts.
func (s *Server) CreateSession(task scheduler.Task) (string, error) {
	// Try to load an existing session for this task.
	sess, err := agent.LoadSession(task.SessionID)
	if err != nil {
		// Create a new session.
		model := task.Model
		if model == "" {
			model = s.model
		}
		sess = agent.NewSession(model, s.system)
		sess.Source = "cron"
		sess.Title = task.Name
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

// RunTask implements scheduler.Runner. It executes a scheduled task by
// creating (or reusing) a session and running a single streamed turn, so any
// subscribed web UI tab sees the same live progress, tool cards, and completion
// events as a normal chat turn.
func (s *Server) RunTask(ctx context.Context, task scheduler.Task) (string, error) {
	if err := s.drain.begin(); err != nil {
		return "", err
	}
	defer s.drain.end()

	sessionID, err := s.CreateSession(task)
	if err != nil {
		return sessionID, err
	}
	task.SessionID = sessionID

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
	// task is doing while it runs.
	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       "history_user_message",
		"session_id": sessionID,
		"content":    task.Prompt,
		"created_at": userMsg.CreatedAt.UnixMilli(),
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
		sw.error(err.Error())
		return sessionID, fmt.Errorf("sender: %w", err)
	}

	// Register the turn's interrupt so sessionStatus reports "running" and the
	// web UI offers the stop button.
	runCtx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKeySessionID{}, sessionID))
	s.registerInterrupt(sessionID, cancel)
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
			sw.error(perr.Error())
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

	reply, err := a.RunStream(runCtx, task.Prompt, toolDefs, executor, handler)

	sess.SyncFrom(a.History)
	_ = sess.Save()

	s.liveStateMu.Lock()
	delete(s.liveStates, sessionID)
	s.liveStateMu.Unlock()

	if err != nil {
		if !errors.Is(err, context.Canceled) {
			sw.error(err.Error())
		}
		s.notifyTaskResult(task, fmt.Sprintf("⏰ %s failed: %v", task.Name, err))
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

	s.wsHub.broadcast(sessionID, map[string]any{
		"type":       "complete",
		"session_id": sessionID,
		"iterations": a.TurnIterations(),
	})

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
	out := make([]taskResponse, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskToResponse(t))
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
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" || req.Cron == "" || req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "name, cron, and prompt are required")
		return
	}
	task := scheduler.Task{
		Name:      req.Name,
		Cron:      req.Cron,
		Prompt:    req.Prompt,
		Model:     req.Model,
		Agent:     req.Agent,
		Directory: req.Directory,
		Notify:    req.Notify,
		Enabled:   true,
	}
	if err := s.scheduler.Add(&task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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

// patchTaskRequest is the body for PATCH /api/tasks/{id}. Every field is a
// pointer so the handler only touches what the caller actually sent — a
// partial update. Enabling/disabling is just {"enabled": ...}.
type patchTaskRequest struct {
	Enabled   *bool                    `json:"enabled,omitempty"`
	Cron      *string                  `json:"cron,omitempty"`
	Prompt    *string                  `json:"prompt,omitempty"`
	Model     *string                  `json:"model,omitempty"`
	Agent     *string                  `json:"agent,omitempty"`
	Directory *string                  `json:"directory,omitempty"`
	Notify    *scheduler.NotifyTargets `json:"notify,omitempty"`
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
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	task, err := s.scheduler.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
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
	if req.Directory != nil {
		task.Directory = *req.Directory
	}
	if req.Notify != nil {
		task.Notify = *req.Notify
	}
	if err := s.scheduler.Update(*task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(*task))
}

func taskToResponse(t scheduler.Task) taskResponse {
	r := taskResponse{
		ID:        t.ID,
		Name:      t.Name,
		Cron:      t.Cron,
		Prompt:    t.Prompt,
		Model:     t.Model,
		Agent:     t.Agent,
		Directory: t.Directory,
		Notify:    t.Notify,
		Enabled:   t.Enabled,
	}
	if !t.CreatedAt.IsZero() {
		r.CreatedAt = t.CreatedAt.Format("2006-01-02T15:04:05Z")
	}
	if !t.LastRun.IsZero() {
		r.LastRun = t.LastRun.Format("2006-01-02T15:04:05Z")
	}
	r.SessionID = t.SessionID
	return r
}
