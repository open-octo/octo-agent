package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestIsAutoNamePlaceholder(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"  ", true},
		{"Session 1", true},
		{"Session 42", true},
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

// TestDoAgentTurn_GeneratesSessionTitle is the regression guard for web
// sessions never getting an auto-generated title. Two historical gaps: only
// the TUI called GenerateTitle, and web sessions are created with a
// "Session N" placeholder title that blocked the untitled-only gate.
func TestDoAgentTurn_GeneratesSessionTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
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
	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn

	srv.doAgentTurn(sess, "hello there", nil, nil)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			if ev["type"] != "session_renamed" {
				continue
			}
			if ev["session_id"] != sess.ID {
				t.Errorf("session_id = %v, want %s", ev["session_id"], sess.ID)
			}
			if ev["name"] != "stub reply" {
				t.Errorf("name = %v, want %q", ev["name"], "stub reply")
			}
			// The title must also be persisted.
			loaded, err := agent.LoadSession(sess.ID)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if loaded.Title != "stub reply" {
				t.Errorf("persisted Title = %q, want %q", loaded.Title, "stub reply")
			}
			return
		case <-deadline:
			t.Fatal("no session_renamed broadcast — title was never generated")
		}
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
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]agent.InboxItem)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "Session 3"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Drain the rename broadcast so we know title generation finished.
	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn

	srv.doAgentTurn(sess, "hello there", nil, nil)

	deadline := time.After(5 * time.Second)
wait:
	for {
		select {
		case b := <-conn.send:
			var ev map[string]any
			if err := json.Unmarshal(b, &ev); err != nil {
				continue
			}
			if ev["type"] == "session_renamed" && ev["session_id"] == sess.ID {
				break wait
			}
		case <-deadline:
			t.Fatal("no session_renamed broadcast — title was never generated")
		}
	}

	// listSessionsBrief is what the frontend REST fallback calls. It must
	// report the generated title, not the placeholder.
	brief := srv.listSessionsBrief()
	if len(brief) != 1 {
		t.Fatalf("listSessionsBrief returned %d sessions, want 1", len(brief))
	}
	if brief[0].Name != "stub reply" {
		t.Errorf("Name = %q, want %q", brief[0].Name, "stub reply")
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

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
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
