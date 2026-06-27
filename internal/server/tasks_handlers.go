package server

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/channel"
	"github.com/Leihb/octo-agent/internal/scheduler"
	"github.com/Leihb/octo-agent/internal/tools"
)

// ─── Tasks REST API ─────────────────────────────────────────────────────────

type taskRequest struct {
	Name   string                  `json:"name"`
	Cron   string                  `json:"cron"`
	Prompt string                  `json:"prompt"`
	Model  string                  `json:"model,omitempty"`
	Agent  string                  `json:"agent,omitempty"`
	Notify scheduler.NotifyTargets `json:"notify,omitempty"`
}

type taskResponse struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Cron      string                  `json:"cron"`
	Prompt    string                  `json:"prompt"`
	Model     string                  `json:"model,omitempty"`
	Agent     string                  `json:"agent,omitempty"`
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
		// task.Directory is applied at run time: buildAgent recomposes the
		// system prompt from s.system every turn, so stashing it on sess.System
		// here would be silently dropped.
		if err := sess.Save(); err != nil {
			return sess.ID, fmt.Errorf("save session: %w", err)
		}
	}
	return sess.ID, nil
}

// RunTask implements scheduler.Runner. It executes a scheduled task by
// creating (or reusing) a session and running a single turn.
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

	a := s.buildAgent(sess)

	// Apply the task's working directory so the run actually happens there.
	if task.Directory != "" {
		var derr error
		if ctx, derr = applyTaskDirectory(ctx, a, task.Directory); derr != nil {
			return sessionID, derr
		}
	}

	var toolDefs []agent.ToolDefinition
	var executor agent.ToolExecutor
	if s.cfg.Tools {
		var perr error
		ctx, executor, _, perr = s.prepareToolTurn(ctx, a)
		if perr != nil {
			return sessionID, fmt.Errorf("prepare tools: %w", perr)
		}
		toolDefs = tools.DefaultToolsFor(a.Model)
	}

	reply, err := a.Run(ctx, task.Prompt, toolDefs, executor)
	if err != nil {
		sess.SyncFrom(a.History)
		_ = sess.Save()
		s.notifyTaskResult(task, fmt.Sprintf("⏰ %s failed: %v", task.Name, err))
		return sessionID, fmt.Errorf("run task: %w", err)
	}
	s.notifyTaskResult(task, fmt.Sprintf("⏰ %s\n\n%s", task.Name, reply.Content))

	sess.SyncFrom(a.History)
	if err := sess.Save(); err != nil {
		return sessionID, fmt.Errorf("save session: %w", err)
	}

	return sessionID, nil
}

// applyTaskDirectory roots a task run at dir: tools (terminal + file ops)
// resolve relative paths against it via WorkingDir(ctx), the planner uses it
// (a.CWD), and the model is told its cwd in both system-prompt variants (so a
// lean explore sub-agent sees it too) — buildAgent composed System/LeanSystem
// from the server cwd, so this note corrects it. Errors if dir isn't a usable
// directory rather than running the whole turn against a broken root.
func applyTaskDirectory(ctx context.Context, a *agent.Agent, dir string) (context.Context, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return ctx, fmt.Errorf("task directory %q: %w", dir, err)
	}
	if !fi.IsDir() {
		return ctx, fmt.Errorf("task directory %q is not a directory", dir)
	}
	a.CWD = dir
	note := "Working directory: " + dir
	a.System = note + "\n\n" + a.System
	if a.LeanSystem != "" {
		a.LeanSystem = note + "\n\n" + a.LeanSystem
	}
	return tools.WithWorkingDir(ctx, dir), nil
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
		Name:    req.Name,
		Cron:    req.Cron,
		Prompt:  req.Prompt,
		Model:   req.Model,
		Agent:   req.Agent,
		Notify:  req.Notify,
		Enabled: true,
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

// handleToggleTask pauses or resumes a scheduled task. Update reschedules or
// unschedules the live cron entry and persists, so the change survives restart.
func (s *Server) handleToggleTask(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	task, err := s.scheduler.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	task.Enabled = req.Enabled
	if err := s.scheduler.Update(*task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(*task))
}

func taskToResponse(t scheduler.Task) taskResponse {
	r := taskResponse{
		ID:      t.ID,
		Name:    t.Name,
		Cron:    t.Cron,
		Prompt:  t.Prompt,
		Model:   t.Model,
		Agent:   t.Agent,
		Notify:  t.Notify,
		Enabled: t.Enabled,
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
