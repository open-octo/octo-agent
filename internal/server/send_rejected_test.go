package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

// TestHandleWSUserMessage_BoundToOtherLeaseActiveEmitsSendRejected guards the
// web UI's optimistic-send rollback: when the session is bound to another entry
// and that entry still holds an active turn lease, the message is rejected
// outright because a force takeover would not be safe.
func TestHandleWSUserMessage_BoundToOtherLeaseActiveEmitsSendRejected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if _, _, err := sess.Bind(agent.EntryTUI, false); err != nil {
		t.Fatalf("bind to TUI: %v", err)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	// With an active turn lease the binding is not recoverable, so the server
	// must reject the message outright rather than offering a force takeover.
	if err := sess.WriteLease(agent.EntryTUI, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sess.ID)

	srv.handleWSUserMessage(conn, &wsMsgUserMessage{
		SessionID: sess.ID,
		Content:   json.RawMessage(`"hello from web"`),
	})

	var rejected bool
	deadline := time.After(2 * time.Second)
drain:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			if typ, _ := ev["type"].(string); typ == "send_rejected" {
				rejected = true
				if sid, _ := ev["session_id"].(string); sid != sess.ID {
					t.Errorf("send_rejected session_id = %q, want %q", sid, sess.ID)
				}
				break drain
			}
		case <-deadline:
			break drain
		}
	}
	if !rejected {
		t.Fatal("expected send_rejected event when session is bound to another entry with active lease")
	}
}

// TestHandleWSUserMessage_OtherEntryNoLeaseEmitsBindRequired: when the session
// is bound to another entry but no turn lease is active, the server asks the
// browser to confirm a force takeover instead of dropping the message.
func TestHandleWSUserMessage_OtherEntryNoLeaseEmitsBindRequired(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if _, _, err := sess.Bind(agent.EntryTUI, false); err != nil {
		t.Fatalf("bind to TUI: %v", err)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sess.ID)

	srv.handleWSUserMessage(conn, &wsMsgUserMessage{
		SessionID: sess.ID,
		Content:   json.RawMessage(`"hello from web"`),
	})

	var required bool
	deadline := time.After(2 * time.Second)
drain:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			typ, _ := ev["type"].(string)
			if typ == "bind_required" {
				required = true
				if sid, _ := ev["session_id"].(string); sid != sess.ID {
					t.Errorf("bind_required session_id = %q, want %q", sid, sess.ID)
				}
				break drain
			}
			if typ == "send_rejected" {
				t.Fatalf("expected bind_required, got send_rejected: %v", ev["message"])
			}
		case <-deadline:
			break drain
		}
	}
	if !required {
		t.Fatal("expected bind_required event when session is bound to another entry without active lease")
	}
}

// holdSender holds the turn open until the test closes the block channel.
// Used when we need to inspect mid-turn persisted state before release.
type holdSender struct {
	block   <-chan struct{}
	started chan<- struct{}
	once    sync.Once
}

func (s *holdSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	s.once.Do(func() { close(s.started) })
	<-s.block
	return agent.Reply{Content: "stub reply"}, nil
}

func (s *holdSender) StreamMessages(_ context.Context, _, _ string, _ []agent.Message, _ int, onChunk func(string), _ func(string)) (agent.Reply, error) {
	s.once.Do(func() { close(s.started) })
	<-s.block
	if onChunk != nil {
		onChunk("stub reply")
	}
	return agent.Reply{Content: "stub reply"}, nil
}

func (s *holdSender) SendMessagesWithTools(_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition) (agent.Reply, error) {
	s.once.Do(func() { close(s.started) })
	<-s.block
	return agent.Reply{Content: "stub reply"}, nil
}

func (s *holdSender) StreamMessagesWithTools(_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition, onChunk func(string), _ agent.ToolInputDeltaFunc, _ agent.ThinkingDeltaFunc) (agent.Reply, error) {
	s.once.Do(func() { close(s.started) })
	<-s.block
	if onChunk != nil {
		onChunk("stub reply")
	}
	return agent.Reply{Content: "stub reply"}, nil
}

// TestHandleWSUserMessage_ForceTakesOverStaleBinding: force=true lets the web
// UI take over a session bound to another entry when no turn lease is active.
func TestHandleWSUserMessage_ForceTakesOverStaleBinding(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if _, _, err := sess.Bind(agent.EntryTUI, false); err != nil {
		t.Fatalf("bind to TUI: %v", err)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sess.ID)

	// Hold the turn open so we can inspect the persisted binding before the
	// turn goroutine finishes and releases it.
	block := make(chan struct{})
	started := make(chan struct{})
	srv.sender = &holdSender{block: block, started: started}

	srv.handleWSUserMessage(conn, &wsMsgUserMessage{
		SessionID: sess.ID,
		Content:   json.RawMessage(`"hello from web"`),
		Force:     true,
	})

	// Wait until the turn goroutine has reached the sender; only then is it
	// guaranteed to still be holding the binding. Without this synchronization
	// the goroutine can finish and release the binding before we reload the
	// session, making the assertion flaky.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn never reached sender")
	}

	// The binding is acquired synchronously before the turn goroutine starts;
	// verify it persisted to disk before the turn completes and releases it.
	fresh, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if !fresh.BoundTo(agent.EntryWeb) {
		t.Fatalf("expected session to be bound to web after force takeover, got %q", fresh.BoundEntry)
	}

	// Now let the turn finish and wait for its completion broadcast.
	close(block)

	var sawComplete bool
	drain := time.After(3 * time.Second)
drainLoop:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			typ, _ := ev["type"].(string)
			if typ == "complete" {
				sawComplete = true
				break drainLoop
			}
			if typ == "send_rejected" {
				t.Fatalf("expected takeover success, got send_rejected: %v", ev["message"])
			}
			if typ == "bind_required" {
				t.Fatalf("expected takeover success, got bind_required: %v", ev["message"])
			}
		case <-drain:
			break drainLoop
		}
	}
	if !sawComplete {
		t.Fatal("expected turn to complete after force takeover")
	}
}

// TestHandleWSUserMessage_ForceRejectedWhenLeaseActive: force=true still cannot
// steal a session while another entry holds an active turn lease.
func TestHandleWSUserMessage_ForceRejectedWhenLeaseActive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if _, _, err := sess.Bind(agent.EntryTUI, false); err != nil {
		t.Fatalf("bind to TUI: %v", err)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := sess.WriteLease(agent.EntryTUI, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn
	srv.wsHub.subscribe(conn, sess.ID)

	srv.handleWSUserMessage(conn, &wsMsgUserMessage{
		SessionID: sess.ID,
		Content:   json.RawMessage(`"hello from web"`),
		Force:     true,
	})

	var rejected bool
	deadline := time.After(2 * time.Second)
drain:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			typ, _ := ev["type"].(string)
			if typ == "send_rejected" {
				rejected = true
				break drain
			}
			if typ == "bind_required" {
				t.Fatalf("expected send_rejected due to active lease, got bind_required")
			}
			if typ == "complete" {
				t.Fatalf("expected send_rejected due to active lease, got complete")
			}
		case <-deadline:
			break drain
		}
	}
	if !rejected {
		t.Fatal("expected send_rejected event when force takeover races an active lease")
	}
}
