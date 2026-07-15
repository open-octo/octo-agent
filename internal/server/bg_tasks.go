package server

import (
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// Background-process visibility for the web UI. The frontend renders a
// "N background task(s)" badge with a popover from background_tasks_update,
// and an inline chat notice from background_task_notice; these helpers are
// the server-side emitters feeding the session's per-session
// tools.BackgroundManager state into the WebSocket stream.

// backgroundTasksUpdate builds the badge payload from a live process list.
func backgroundTasksUpdate(sessionID string, infos []tools.BgInfo, now time.Time) wsEventBackgroundTaskUpdate {
	list := make([]wsBackgroundTask, 0, len(infos))
	for _, in := range infos {
		list = append(list, wsBackgroundTask{
			HandleID: in.ID,
			Command:  in.Command,
			Elapsed:  int(now.Sub(in.Start).Seconds()),
		})
	}
	return wsEventBackgroundTaskUpdate{
		Type:      "background_tasks_update",
		SessionID: sessionID,
		Running:   len(list),
		Tasks:     list,
	}
}

// broadcastBackgroundTasks pushes the session's current background-process
// list to its subscribers. Called after every tool call — a tool call is the
// only way a turn starts or kills a process — and from the exit hook, so the
// badge tracks starts, kills, and natural completions.
func (s *Server) broadcastBackgroundTasks(sessionID string) {
	infos := tools.SessionBackgroundManager(sessionID).ListRunning()
	s.wsHub.broadcast(sessionID, backgroundTasksUpdate(sessionID, infos, time.Now()))
}

// wireBackgroundTaskNotices registers the session manager's exit hook so a
// finished background process surfaces as a chat notice, the badge count
// drops, and the model is notified. Re-registered (idempotently) at each turn
// start; the hook outlives the turn, so a process finishing between turns
// still notifies subscribed tabs — broadcasting to a session with no
// subscribers is a no-op.
func (s *Server) wireBackgroundTaskNotices(sessionID string) {
	tools.SessionBackgroundManager(sessionID).SetOnExit(func(e tools.BgExit) {
		s.wsHub.broadcast(sessionID, wsEventBackgroundTaskNotice{
			Type:      "background_task_notice",
			SessionID: sessionID,
			Command:   e.Command,
			HandleID:  e.ID,
			Status:    bgNoticeStatus(e.Status),
		})
		s.broadcastBackgroundTasks(sessionID)
		s.notifyAgentBgExit(sessionID, e)
	})
}

// notifyAgentBgExit pushes a background-completion note to the model — parity
// with the CLI/TUI's SetBackgroundOnExit → Inbox wiring. Includes a summary of
// other running background tasks so the model can track in-flight work without
// a dedicated process-list tool.
func (s *Server) notifyAgentBgExit(sessionID string, e tools.BgExit) {
	mgr := tools.SessionBackgroundManager(sessionID)
	s.deliverModelNote(sessionID, tools.FormatBgNoteWithSummary(mgr, e))
}

// notifySubAgentExit pushes an async sub-agent completion to the model —
// parity with the CLI/TUI's SubAgentManager.SetOnExit → Inbox wiring.
func (s *Server) notifySubAgentExit(sessionID string, ev tools.SubAgentNotification) {
	s.deliverModelNote(sessionID, tools.FormatSubAgentNote(ev))
}

// deliverModelNote routes a <system-reminder> completion note to the model.
// Mid-turn it goes straight into the running Agent's Inbox (drained between
// loop iterations); while idle it goes to the steer queue and a turn is
// kicked so the model reacts immediately — the same idle auto-turn the TUI
// does. The note is a <system-reminder> block, so the web transcript never
// renders it as user speech.
func (s *Server) deliverModelNote(sessionID, note string) {
	// Enqueue into the running Agent's Inbox while STILL holding sessionAgentsMu
	// so this can't interleave with the turn-teardown defer, which drains the
	// Inbox and deregisters the agent under the same lock. Otherwise a note
	// landing between the drain and the deregister reaches an Inbox nothing will
	// drain again — silently lost.
	s.sessionAgentsMu.Lock()
	if a := s.sessionAgents[sessionID]; a != nil {
		a.Inbox.Enqueue(note)
		s.sessionAgentsMu.Unlock()
		return
	}
	s.sessionAgentsMu.Unlock()
	s.enqueueSteer(sessionID, agent.InboxItem{Text: note})
	s.kickIdleSteerTurn(sessionID)
}

// kickIdleSteerTurn starts a turn for sessionID when none is running, so a
// completion note queued while idle reaches the model immediately. No-op when
// a turn is running (its chained loop drains the queue at turn end) or the
// queue is already empty by the time the lock is held. Returns true only when it
// actually launched a turn — callers that want to surface a "started" signal
// (e.g. the loop-tick notice) must gate on it, since the session may have been
// taken over by another entry or have nothing left to drain.
func (s *Server) kickIdleSteerTurn(sessionID string) bool {
	// Acquire the persistent binding before locking the turn: this keeps the
	// same lock order as the user-initiated web path and prevents idle
	// follow-up turns from interleaving with another entry (cli/tui/im) that
	// has taken over the session while we were idle.
	if ok, _, _ := s.acquireSessionBinding(sessionID, agent.EntryWeb, false); !ok {
		return false
	}

	mu := s.sessionTurnLock(sessionID)
	mu.Lock()
	if s.turnRunning[sessionID] {
		mu.Unlock()
		s.releaseSessionBinding(sessionID, agent.EntryWeb)
		return false
	}
	sess, err := agent.LoadSession(sessionID)
	if err != nil {
		// Session not loadable (deleted, or a transient read error) — leave
		// the queue untouched; the next turn will pick the note up.
		mu.Unlock()
		s.releaseSessionBinding(sessionID, agent.EntryWeb)
		return false
	}
	items := s.drainSteer(sessionID)
	if len(items) == 0 {
		mu.Unlock()
		s.releaseSessionBinding(sessionID, agent.EntryWeb)
		return false
	}
	s.turnRunning[sessionID] = true
	mu.Unlock()

	var texts []string
	var blocks []agent.ContentBlock
	for _, it := range items {
		if strings.TrimSpace(it.Text) != "" {
			texts = append(texts, it.Text)
		}
		blocks = append(blocks, it.Blocks...)
	}
	go func() {
		defer func() {
			mu.Lock()
			s.turnRunning[sessionID] = false
			mu.Unlock()
			s.releaseSessionBinding(sessionID, agent.EntryWeb)
		}()
		s.runAgentTurnLoop(sess, strings.Join(texts, "\n\n"), blocks, imageRefsFromBlocks(blocks))
	}()
	return true
}

// bgNoticeStatus maps a BackgroundManager exit status ("exited: 0",
// "exited: signal: killed", …) onto the frontend notice levels
// (success / cancelled / failed).
func bgNoticeStatus(status string) string {
	switch {
	case status == "exited: 0":
		return "success"
	case strings.Contains(status, "killed"):
		return "cancelled"
	default:
		return "failed"
	}
}

// subAgentNoticeStatus maps a SubAgentNotification onto the frontend notice
// levels (success / warning / failed). An empty StopReason means the sub-agent
// exited with an error; "max_turns" or "max_tokens" mean it returned partial
// work after hitting a budget; any other non-empty StopReason is a normal
// completion (end_turn, tool_use, etc.).
func subAgentNoticeStatus(ev tools.SubAgentNotification) string {
	switch ev.StopReason {
	case "":
		return "failed"
	case "killed":
		return "cancelled"
	case "max_turns", "max_tokens":
		return "warning"
	default:
		return "success"
	}
}
