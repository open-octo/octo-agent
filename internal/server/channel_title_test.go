package server

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
)

// TestHandleChannelMessage_GeneratesSessionTitle is the regression guard for
// IM sessions never getting an auto-generated title: the channel turn path
// (handleChannelMessage → runChannelTurns) never called the shared title
// mechanism, so an IM session kept agent.NewSession's "*Octo Agent"
// placeholder forever while web/TUI sessions were titled after their first
// turn (web parity: TestDoAgentTurn_GeneratesSessionTitle).
func TestHandleChannelMessage_GeneratesSessionTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	sender := &blockingTurnSender{entered: make(chan struct{}), release: make(chan struct{})}
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(sender, "stub-model")
	}, channel.BindByChat)

	// Observe the global rename broadcast like a browser tab would.
	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn

	ad := &fullFakeAdapter{}
	ev := evFor("hello there")
	sess := srv.channelMgr.GetOrCreateSession(ev) // pre-create to learn the store ID
	storeID := sess.Store.ID
	if sess.Store.Title != "*Octo Agent" {
		t.Fatalf("fresh IM session title = %q, want the placeholder", sess.Store.Title)
	}

	turnDone := make(chan struct{})
	go func() { defer close(turnDone); srv.handleChannelMessage(context.Background(), ad, ev) }()
	<-sender.entered // main turn call is blocked; title generation runs concurrently

	// Mid-turn the generated title is broadcast live...
	if name := waitForRename(t, conn, storeID); name != "early title" {
		t.Fatalf("rename broadcast = %q, want %q", name, "early title")
	}
	close(sender.release)
	<-turnDone

	// ...and adopted + persisted by the turn's serialized write path.
	if sess.Store.Title != "early title" {
		t.Errorf("adopted title = %q, want %q", sess.Store.Title, "early title")
	}
	// The adoption re-broadcast converges any client whose list refetch saw the
	// placeholder since the mid-turn broadcast (web ws_handlers.go:1548 parity).
	if name := waitForRename(t, conn, storeID); name != "early title" {
		t.Fatalf("post-adoption re-broadcast = %q, want %q", name, "early title")
	}
	reloaded, err := agent.LoadSession(storeID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if reloaded.Title != "early title" {
		t.Errorf("persisted title = %q, want %q", reloaded.Title, "early title")
	}
}

// TestHandleChannelMessage_TitleFallsBackToSnippet: when the title-generation
// provider call fails, the shared GenerateTitleOrSnippet mechanism still
// titles the IM session with the first user message — the same guarantee web
// turns have (TestDoAgentTurn_TitleGenerationFailureIsLogged).
func TestHandleChannelMessage_TitleFallsBackToSnippet(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	sender := &blockingTitleFailSender{entered: make(chan struct{}), release: make(chan struct{})}
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(sender, "stub-model")
	}, channel.BindByChat)

	conn := &wsConn{
		hub:        srv.wsHub,
		send:       make(chan []byte, 256),
		subscribed: map[string]struct{}{},
	}
	srv.wsHub.register <- conn

	ad := &fullFakeAdapter{}
	ev := evFor("hello there")
	sess := srv.channelMgr.GetOrCreateSession(ev)
	storeID := sess.Store.ID

	turnDone := make(chan struct{})
	go func() { defer close(turnDone); srv.handleChannelMessage(context.Background(), ad, ev) }()
	<-sender.entered // main turn call is blocked; the snippet fallback lands mid-turn

	if name := waitForRename(t, conn, storeID); name != "hello there" {
		t.Fatalf("rename broadcast = %q, want the first-message snippet %q", name, "hello there")
	}
	close(sender.release)
	<-turnDone

	if sess.Store.Title != "hello there" {
		t.Errorf("adopted title = %q, want the snippet %q", sess.Store.Title, "hello there")
	}
}

// TestHandleChannelMessage_SkipsTitleForEmptyFirstMessage: an attachments-only
// first message (vision model — the image rides content blocks and the text
// resolves to "") must not spend the throwaway title call. Web parity:
// TestDoAgentTurn_SkipsTitleForEmptyFirstMessage.
func TestHandleChannelMessage_SkipsTitleForEmptyFirstMessage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	spy := &titleSpySender{}
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(spy, "stub-model")
	}, channel.BindByChat)

	ad := &fullFakeAdapter{}
	ev := evFor("") // text-free content, the attachments-only shape
	sess := srv.channelMgr.GetOrCreateSession(ev)

	srv.handleChannelMessage(context.Background(), ad, ev)

	if got := spy.titleCalls.Load(); got != 0 {
		t.Errorf("title calls = %d, want 0 for a text-free first message", got)
	}
	if sess.Store.Title != "*Octo Agent" {
		t.Errorf("title = %q, want the placeholder (untouched)", sess.Store.Title)
	}
}

// TestToSessionItem_PlaceholderFallsBackToSnippet: the REST session list must
// collapse the "*Octo Agent" placeholder onto the first-message snippet
// (DisplayTitle), matching the WS brief list — an IM session whose title
// generation hasn't landed yet must not surface the raw placeholder.
func TestToSessionItem_PlaceholderFallsBackToSnippet(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	sess := agent.NewSession("stub-model", "") // Title: "*Octo Agent"
	sess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "hello there"}}

	item := srv.toSessionItem(sess, "channel", "")
	if item.Name == "*Octo Agent" {
		t.Error("toSessionItem surfaced the raw placeholder; want the first-message snippet")
	}
	if item.Name != "hello there" {
		t.Errorf("name = %q, want the snippet %q", item.Name, "hello there")
	}

	// A real title (generated or user-set) still wins over the snippet.
	sess.Title = "deploy staging build"
	item = srv.toSessionItem(sess, "channel", "")
	if item.Name != "deploy staging build" {
		t.Errorf("name = %q, want the session's own title %q", item.Name, "deploy staging build")
	}
}
