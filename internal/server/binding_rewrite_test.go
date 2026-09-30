package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// TestAcquireSessionBinding_AlreadyBound_AppendsOnly: re-acquiring a binding
// this entry already holds (the in-memory cache lapsed, as it does 30s into a
// long turn) must not rewrite the transcript. A running turn of ours may be
// appending to the file, and a rewrite from the snapshot acquire just loaded
// drops whatever that turn appended after the load.
func TestAcquireSessionBinding_AlreadyBound_AppendsOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := agent.NewSession("stub-model", "")
	sess.Messages = []agent.Message{agent.NewUserMessage("q"), {Role: agent.RoleAssistant, Content: "a"}}
	sess.Bind(agent.EntryWeb, false)
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	path := filepath.Join(tmp, ".octo", "sessions", sess.ID+".jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	if ok, _, err := srv.acquireSessionBinding(sess.ID, agent.EntryWeb, false); !ok {
		t.Fatalf("acquire: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Fatal("acquire rewrote the transcript of a session already bound to it; want an append-only lease renewal")
	}
}

// gatedSender blocks every provider call until release is closed, so a test
// can inspect the session while a turn is mid-flight.
type gatedSender struct {
	entered chan struct{}
	release chan struct{}
}

func (s *gatedSender) SendMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return agent.Reply{}, ctx.Err()
	}
	return agent.Reply{Content: "reply"}, nil
}

func (s *gatedSender) StreamMessages(ctx context.Context, _, _ string, _ []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	return s.SendMessages(ctx, "", "", nil, 0)
}

// TestHandleEditMessage_RerunHoldsBinding: the turn an edit reruns writes the
// transcript like any Web turn, so it must own the binding while it runs and
// give it back when done. Unbound, the next message sent mid-turn bound the
// session by rewriting the file from its own snapshot.
func TestHandleEditMessage_RerunHoldsBinding(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	sender := &gatedSender{entered: make(chan struct{}, 1), release: make(chan struct{})}
	srv.sender = sender
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	sess.Messages = []agent.Message{agent.NewUserMessage("one"), {Role: agent.RoleAssistant, Content: "two"}}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sess.ID+"/edit_message",
		strings.NewReader(`{"message_index":0,"new_content":"EDITED"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("edit: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	<-sender.entered

	mid, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load mid-turn: %v", err)
	}
	if mid.BoundEntry != agent.EntryWeb {
		t.Fatalf("mid-turn BoundEntry = %q, want %q", mid.BoundEntry, agent.EntryWeb)
	}

	close(sender.release)
	mu := srv.sessionTurnLock(sess.ID)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return !srv.turnRunning[sess.ID]
	})
	done, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load after turn: %v", err)
	}
	if done.BoundEntry != "" {
		t.Fatalf("BoundEntry after the rerun = %q, want released", done.BoundEntry)
	}
}

// TestKickIdleTurn_RunningTurnKeepsBinding: an idle kick that finds a turn
// already running must leave that turn's binding alone. Releasing it unbound
// the session mid-turn and rewrote the file under the turn's appends.
func TestKickIdleTurn_RunningTurnKeepsBinding(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	if ok, _, err := srv.acquireSessionBinding(sess.ID, agent.EntryWeb, false); !ok {
		t.Fatalf("acquire: %v", err)
	}
	srv.turnRunning[sess.ID] = true
	srv.enqueueSteer(sess.ID, agent.InboxItem{Text: "note"})

	if srv.kickIdleSteerTurn(sess.ID) {
		t.Fatal("kick started a turn while one was running")
	}
	got, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.BoundEntry != agent.EntryWeb {
		t.Fatalf("BoundEntry = %q, want the running turn's %q", got.BoundEntry, agent.EntryWeb)
	}
}
