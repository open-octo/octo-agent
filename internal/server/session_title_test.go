package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// syncBuffer wraps bytes.Buffer with a mutex: the title-generation goroutine
// writes to it (via slog) on its own goroutine while the test polls it by
// reading, and bytes.Buffer itself has no concurrency guarantees.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestIsAutoNamePlaceholder(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"  ", true},
		{"Session 1", true},
		{"Session 42", true},
		{"*Octo Agent", true},
		{"Session", false},
		{"修复登录问题", false},
		{"My Session 2", false},
	}
	for _, c := range cases {
		if got := isAutoNamePlaceholder(c.title); got != c.want {
			t.Errorf("isAutoNamePlaceholder(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

// waitForRename blocks until a global session_renamed for sid is observed on
// conn (or fails on timeout), returning the broadcast name. Shared by the
// title tests, which all drive a turn whose main call blocks so the rename is
// guaranteed to land while the turn is still running.
func waitForRename(t *testing.T, conn *wsConn, sid string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if json.Unmarshal(b, &ev) != nil {
				continue
			}
			if ev["type"] == "session_renamed" && ev["session_id"] == sid {
				name, _ := ev["name"].(string)
				return name
			}
		case <-deadline:
			t.Fatal("no session_renamed broadcast — title was never generated")
			return ""
		}
	}
}

// TestDoAgentTurn_GeneratesSessionTitle is the regression guard for web
// sessions never getting an auto-generated title. Historical gaps: only the
// TUI called GenerateTitle, and web sessions are created with a "Session N"
// placeholder that blocked the untitled-only gate. It now also pins the
// on-receipt timing: the rename is broadcast while the turn is still running,
// and the title survives the turn's end-of-turn saves (adopted on the single
// serialized write path — the title goroutine never writes the file itself).
func TestDoAgentTurn_GeneratesSessionTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	sender := &blockingTurnSender{entered: make(chan struct{}), release: make(chan struct{})}
	srv.sender = sender
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	// Born with the frontend's auto-assigned placeholder, like every session
	// created via POST /api/sessions.
	sess := agent.NewSession("stub-model", "")
	sess.Title = "Session 2"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Registered but NOT subscribed to the session: session_renamed must be a
	// global broadcast (the sidebar lists every session in every tab).
	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- conn

	turnDone := make(chan struct{})
	go func() { defer close(turnDone); srv.doAgentTurn(sess, "hello there", nil, nil) }()
	<-sender.entered // main turn call is blocked; title generation runs concurrently

	if name := waitForRename(t, conn, sess.ID); name != "early title" {
		t.Errorf("rename name = %q, want %q", name, "early title")
	}

	close(sender.release)
	<-turnDone

	// The title must survive the turn's end-of-turn saves.
	loaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.Title != "early title" {
		t.Errorf("persisted Title = %q, want %q", loaded.Title, "early title")
	}
}

// TestListSessionsBrief_ReflectsGeneratedTitle guards the REST fallback path
// used by the web UI when the live session_renamed broadcast is missed. The
// sidebar should be able to refresh from listSessions and see the new title.
func TestListSessionsBrief_ReflectsGeneratedTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	sender := &blockingTurnSender{entered: make(chan struct{}), release: make(chan struct{})}
	srv.sender = sender
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "Session 3"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- conn

	turnDone := make(chan struct{})
	go func() { defer close(turnDone); srv.doAgentTurn(sess, "hello there", nil, nil) }()
	<-sender.entered
	waitForRename(t, conn, sess.ID)
	close(sender.release)
	<-turnDone

	// listSessionsBrief is what the frontend REST fallback calls. It must
	// report the generated title, not the placeholder.
	brief := srv.listSessionsBrief()
	if len(brief) != 1 {
		t.Fatalf("listSessionsBrief returned %d sessions, want 1", len(brief))
	}
	if brief[0].Name != "early title" {
		t.Errorf("Name = %q, want %q", brief[0].Name, "early title")
	}
}

// titleFailSender behaves exactly like stubSender for a normal turn, but
// returns an error specifically for GenerateTitle's throwaway prompt (its
// message list always ends with the "Summarize this conversation..."
// instruction) — simulating a provider/model that's misconfigured only for
// this fire-and-forget call, e.g. a bad model alias or a rate limit hit on a
// second request right after the main turn's.
type titleFailSender struct{}

func (titleFailSender) SendMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int) (agent.Reply, error) {
	if isTitlePrompt(msgs) {
		return agent.Reply{}, fmt.Errorf("stub: title provider error")
	}
	return agent.Reply{Content: "stub reply"}, nil
}

func (titleFailSender) StreamMessages(_ context.Context, _, _ string, _ []agent.Message, _ int, onChunk func(string), _ func(string)) (agent.Reply, error) {
	if onChunk != nil {
		onChunk("stub reply")
	}
	return agent.Reply{Content: "stub reply"}, nil
}

func (titleFailSender) SendMessagesWithTools(_ context.Context, _, _ string, msgs []agent.Message, _ int, _ []agent.ToolDefinition) (agent.Reply, error) {
	if isTitlePrompt(msgs) {
		return agent.Reply{}, fmt.Errorf("stub: title provider error")
	}
	return agent.Reply{Content: "stub reply"}, nil
}

func (titleFailSender) StreamMessagesWithTools(_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition, onChunk func(string), _ agent.ToolInputDeltaFunc, _ agent.ThinkingDeltaFunc) (agent.Reply, error) {
	if onChunk != nil {
		onChunk("stub reply")
	}
	return agent.Reply{Content: "stub reply"}, nil
}

// isTitlePrompt reports whether msgs is GenerateTitle's throwaway call
// (identified by its trailing instruction, not by any dedicated flag — the
// provider itself only ever sees an ordinary message list).
func isTitlePrompt(msgs []agent.Message) bool {
	if len(msgs) == 0 {
		return false
	}
	return strings.Contains(msgs[len(msgs)-1].Content, "Summarize this conversation")
}

// blockingTurnSender lets the title-generation call (plain SendMessages, no
// tools in these tests) complete instantly while the main turn's streaming
// call blocks until released — a long agentic first turn in miniature.
type blockingTurnSender struct {
	entered chan struct{} // closed when the main call is in flight
	release chan struct{} // the main call returns once this is closed
}

func (s *blockingTurnSender) SendMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int) (agent.Reply, error) {
	if isTitlePrompt(msgs) {
		return agent.Reply{Content: "early title"}, nil
	}
	return agent.Reply{Content: "stub reply"}, nil
}

func (s *blockingTurnSender) StreamMessages(_ context.Context, _, _ string, _ []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	close(s.entered)
	<-s.release
	return agent.Reply{Content: "stub reply"}, nil
}

// TestDoAgentTurn_TitleGenerationFailureIsLogged is the regression guard for
// a real production symptom: on an install where the title-generation call
// fails every time (bad model config, rate limit, etc.), the sidebar title
// never appears and — before this test — nothing anywhere recorded why. The
// failure is still silent to the user by design (retried on the next turn),
// but it must not be silent to the server operator.
func TestDoAgentTurn_TitleGenerationFailureIsLogged(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	logBuf := &syncBuffer{}
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.sender = &titleFailSender{}
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "Session 5"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn

	srv.doAgentTurn(sess, "hello there", nil, nil)

	// The title goroutine is fire-and-forget, so there's no broadcast to wait
	// on for the failure path — poll the captured log instead.
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(logBuf.String(), "session title generation failed") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected a logged warning for the failed title generation; log so far:\n%s", logBuf.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !strings.Contains(logBuf.String(), sess.ID) {
		t.Errorf("expected the session id in the log line; got:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "stub: title provider error") {
		t.Errorf("expected the underlying error in the log line; got:\n%s", logBuf.String())
	}

	// And the title must genuinely still be unset — the failure path must not
	// have persisted a garbage title.
	loaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !isAutoNamePlaceholder(loaded.Title) {
		t.Errorf("Title = %q, want it to remain a placeholder after a failed generation", loaded.Title)
	}
}
