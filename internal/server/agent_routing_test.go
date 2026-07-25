package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/channel"
)

// End-to-end IM routing (dev-docs/multi-agent-system-design.md §2-3): a
// profile bound to a chat owns that chat's messages; multiple bindings with
// no @-mention stay silent; an @-mention picks the agent explicitly.

// writeAgentProfile drops a user-level profile into the test HOME.
func writeAgentProfile(t *testing.T, id, content string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".octo", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func routingHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
}

// agentStoreDir returns the test HOME's profile dir.
func agentStoreDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "agents")
}

// routeAndWait resolves the profile for ev, runs handleChannelMessage to
// completion, and returns. Using the stub sender this is fast and avoids
// racing with the async goroutine that routeChannelEvent would spawn.
func routeAndWait(t *testing.T, srv *Server, ad *fullFakeAdapter, ev channel.InboundEvent, profile *agentprofile.Profile) {
	t.Helper()
	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		srv.handleChannelMessage(context.Background(), ad, ev, profile)
	}()
	<-turnDone
}

func TestRouteChannelEvent_RoutesToBoundExpertAgent(t *testing.T) {
	routingHome(t)
	writeAgentProfile(t, "reviewer", `---
description: reviews code
channel_bindings:
  - {platform: fake, chat_id: c1}
---
You review code.
`)
	srv := chanServer(t)
	ad := &fullFakeAdapter{}
	ev := evFor("hello")
	profile := agentprofile.NewRouter(agentprofile.New(agentStoreDir(), nil)).Route(agentprofile.RouteInput{Platform: ev.Platform, ChatID: ev.ChatID, UserID: ev.UserID, Text: ev.Text})
	if profile == nil {
		t.Fatal("expected routing to reviewer, got nil")
	}
	routeAndWait(t, srv, ad, ev, profile)

	sess := srv.channelMgr.GetSession(ev, "reviewer")
	if sess == nil {
		t.Fatal("message to a bound chat did not create the expert's session")
	}
	if sess.AgentID != "reviewer" {
		t.Fatalf("session AgentID = %q, want reviewer", sess.AgentID)
	}
	if sess.Store.EffectiveAgentID() != "reviewer" {
		t.Fatalf("store agent_id = %q, want reviewer", sess.Store.AgentID)
	}
	// The default agent's namespace for the same chat stays empty.
	if got := srv.channelMgr.GetSession(ev, "default"); got != nil {
		t.Fatal("bound chat leaked a default-agent session")
	}
}

func TestRouteChannelEvent_MultiBindingNoMentionDrops(t *testing.T) {
	routingHome(t)
	writeAgentProfile(t, "agent-a", `---
description: a
channel_bindings:
  - {platform: fake, chat_id: c1}
---
A.
`)
	writeAgentProfile(t, "agent-b", `---
description: b
channel_bindings:
  - {platform: fake, chat_id: c1}
---
B.
`)
	srv := chanServer(t)
	ad := &fullFakeAdapter{}
	ev := evFor("hello")
	profile := agentprofile.NewRouter(agentprofile.New(agentStoreDir(), nil)).Route(agentprofile.RouteInput{Platform: ev.Platform, ChatID: ev.ChatID, UserID: ev.UserID, Text: ev.Text})
	if profile != nil {
		t.Fatalf("expected nil route (multi-binding), got %q", profile.ID)
	}
	// No handleChannelMessage: routeChannelEvent would have dropped the event.
	// Verify silence: no session under either agent, no reply sent.
	if got := srv.channelMgr.GetSession(ev, "agent-a"); got != nil {
		t.Fatal("multi-bound chat created agent-a session")
	}
	if got := srv.channelMgr.GetSession(ev, "agent-b"); got != nil {
		t.Fatal("multi-bound chat created agent-b session")
	}
	if texts := ad.texts(); len(texts) != 0 {
		t.Fatalf("expected silence, got replies: %v", texts)
	}
}

func TestRouteChannelEvent_UnboundChatFallsBackToDefault(t *testing.T) {
	routingHome(t)
	writeAgentProfile(t, "reviewer", `---
description: reviews code
channel_bindings:
  - {platform: fake, chat_id: other-chat}
---
You review code.
`)
	srv := chanServer(t)
	ad := &fullFakeAdapter{}
	ev := evFor("hello")
	profile := agentprofile.NewRouter(agentprofile.New(agentStoreDir(), nil)).Route(agentprofile.RouteInput{Platform: ev.Platform, ChatID: ev.ChatID, UserID: ev.UserID, Text: ev.Text})
	if profile == nil || !profile.IsDefault() {
		t.Fatalf("expected default fallback, got %+v", profile)
	}
	routeAndWait(t, srv, ad, ev, profile)

	sess := srv.channelMgr.GetSession(ev, "default")
	if sess == nil {
		t.Fatal("unbound chat did not fall back to the default agent")
	}
	// chanServer uses BindByChat: the default agent's key must remain the
	// legacy, un-prefixed shape.
	if sess.Key != channel.SessionKey("fake:c1") {
		t.Fatalf("default session key = %q, want legacy byte-identical key", sess.Key)
	}
}

// Per-turn: a profile with its own SystemPrompt replaces the base system
// prompt (the profile has full control). A profile WITHOUT a system prompt
// falls back to the server's base system.
func TestRunChannelTurns_ProfileComposesPerTurn(t *testing.T) {
	routingHome(t)
	writeAgentProfile(t, "ops", `---
description: ops
model: claude-sonnet-4-20250514
channel_bindings:
  - {platform: fake, chat_id: c1}
---
You are the OPS expert. Base prompt.
`)
	// A profile WITHOUT a system prompt — should fall back to base.
	writeAgentProfile(t, "bare", `---
description: bare
channel_bindings:
  - {platform: fake, chat_id: c2}
---
`)
	dir := filepath.Join(os.Getenv("HOME"), ".octo", "agents")
	store := agentprofile.New(dir, func() string { return "" })
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.system = "SERVER BASE SYSTEM"
	srv.agentStore = store
	srv.agentRouterVal = agentprofile.NewRouter(store)
	srv.channelMgr = channel.NewManager(&channel.Config{}, func(p *agentprofile.Profile) *agent.Agent {
		return agent.New(&stubSender{}, "model-"+p.ID)
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}

	// Profile with system prompt → its prompt replaces base.
	evOps := channel.InboundEvent{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "diag"}
	router := agentprofile.NewRouter(store)
	routeAndWait(t, srv, ad, evOps, router.Route(agentprofile.RouteInput{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "diag"}))
	opsSess := srv.channelMgr.GetSession(evOps, "ops")
	if opsSess == nil {
		t.Fatal("ops session not created")
	}
	if !strings.Contains(opsSess.Agent.System, "OPS expert") {
		t.Fatalf("profile system prompt not applied: %q", opsSess.Agent.System)
	}

	// Profile without system prompt → falls back to server base.
	evBare := channel.InboundEvent{Platform: "fake", ChatID: "c2", UserID: "u2", Text: "diag"}
	routeAndWait(t, srv, ad, evBare, router.Route(agentprofile.RouteInput{Platform: "fake", ChatID: "c2", UserID: "u2", Text: "diag"}))
	bareSess := srv.channelMgr.GetSession(evBare, "bare")
	if bareSess == nil {
		t.Fatal("bare session not created")
	}
	if !strings.Contains(bareSess.Agent.System, "SERVER BASE SYSTEM") {
		t.Fatalf("bare profile did not fall back to base system: %q", bareSess.Agent.System)
	}
}

// A profile deleted mid-session must fall back to the default system prompt
// on the next turn. After the profile is removed, the read-through Store
// misses on the next route, so the same chat now resolves to the default
// agent — whose base system prompt is used (not the deleted profile's).
func TestRunChannelTurns_DeletedProfileFallsBackToDefault(t *testing.T) {
	routingHome(t)
	dir := filepath.Join(os.Getenv("HOME"), ".octo", "agents")
	writeAgentProfile(t, "temp", `---
description: temp
channel_bindings:
  - {platform: fake, chat_id: c1}
---
TEMP PROFILE PROMPT.
`)
	store := agentprofile.New(dir, func() string { return "" })
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.system = "SERVER BASE"
	srv.agentStore = store
	srv.agentRouterVal = agentprofile.NewRouter(store)
	srv.channelMgr = channel.NewManager(&channel.Config{}, func(p *agentprofile.Profile) *agent.Agent {
		return agent.New(&stubSender{}, "stub-model")
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}
	router := agentprofile.NewRouter(store)

	// First turn: profile exists → routes to "temp" → temp prompt applied.
	ev1 := channel.InboundEvent{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "hi"}
	routeAndWait(t, srv, ad, ev1, router.Route(agentprofile.RouteInput{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "hi"}))
	tempSess := srv.channelMgr.GetSession(ev1, "temp")
	if tempSess == nil {
		t.Fatal("temp session not created")
	}
	if !strings.Contains(tempSess.Agent.System, "TEMP PROFILE PROMPT") {
		t.Fatalf("initial profile prompt not applied: %q", tempSess.Agent.System)
	}
	// Delete the profile file — read-through Store misses on next route.
	if err := os.Remove(filepath.Join(dir, "temp.md")); err != nil {
		t.Fatal(err)
	}
	// Second turn: profile gone → resolves to default → base prompt.
	ev2 := channel.InboundEvent{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "again"}
	routeAndWait(t, srv, ad, ev2, router.Route(agentprofile.RouteInput{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "again"}))
	defSess := srv.channelMgr.GetSession(ev2, "default")
	if defSess == nil {
		t.Fatal("default session not created after profile delete")
	}
	if strings.Contains(defSess.Agent.System, "TEMP PROFILE PROMPT") {
		t.Fatalf("deleted profile's prompt still applied: %q", defSess.Agent.System)
	}
	if !strings.Contains(defSess.Agent.System, "SERVER BASE") {
		t.Fatalf("did not fall back to base prompt: %q", defSess.Agent.System)
	}
}

// A profile with an unresolvable model must warn-and-fall-through to the
// server default, not panic or start with the bogus unresolvable model.
func TestBuildChannelAgent_UnresolvedModelFallsBack(t *testing.T) {
	routingHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	got := srv.buildChannelAgent(&agentprofile.Profile{ID: "x", Description: "d", CapabilitySpec: agentprofile.CapabilitySpec{Model: "no-such-model"}})
	if got == nil {
		t.Fatal("buildChannelAgent returned nil")
	}
	// The bogus model must NOT be used — it falls back to the server default.
	if got.Model == "no-such-model" {
		t.Fatalf("unresolved profile model leaked into the agent: %q", got.Model)
	}
}
