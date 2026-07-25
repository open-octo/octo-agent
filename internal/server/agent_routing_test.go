package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// waitSession polls for the asynchronously spawned turn to create the
// session (routeChannelEvent runs handleChannelMessage in a goroutine).
func waitSession(t *testing.T, srv *Server, agentID string) *channel.Session {
	return waitSessionFor(t, srv, agentID, evFor("x"))
}

// waitSessionFor polls for a session matching the routed event.
func waitSessionFor(t *testing.T, srv *Server, agentID string, ev channel.InboundEvent) *channel.Session {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sess := srv.channelMgr.GetSession(ev, agentID); sess != nil {
			return sess
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

// waitPrompt polls for the per-turn system prompt to be composed (the turn
// that composes it runs in a goroutine).
func waitPrompt(t *testing.T, sess *channel.Session, substring string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(sess.Agent.System, substring) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("prompt never contained %q; got %q", substring, sess.Agent.System)
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

	srv.routeChannelEvent(context.Background(), ad, evFor("hello"))

	sess := waitSession(t, srv, "reviewer")
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
	if got := srv.channelMgr.GetSession(evFor("x"), "default"); got != nil {
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

	srv.routeChannelEvent(context.Background(), ad, evFor("hello"))
	time.Sleep(100 * time.Millisecond) // prove nothing async happens either

	// Silence: no session under either agent, no reply sent.
	if got := srv.channelMgr.GetSession(evFor("x"), "agent-a"); got != nil {
		t.Fatal("multi-bound chat without @ created agent-a session")
	}
	if got := srv.channelMgr.GetSession(evFor("x"), "agent-b"); got != nil {
		t.Fatal("multi-bound chat without @ created agent-b session")
	}
	if texts := ad.texts(); len(texts) != 0 {
		t.Fatalf("expected silence, got replies: %v", texts)
	}
}

func TestRouteChannelEvent_MentionSelectsAgent(t *testing.T) {
	routingHome(t)
	writeAgentProfile(t, "agent-a", `---
description: a
mention_as: ["@aa"]
channel_bindings:
  - {platform: fake, chat_id: c1}
---
A.
`)
	writeAgentProfile(t, "agent-b", `---
description: b
mention_as: ["@bb"]
channel_bindings:
  - {platform: fake, chat_id: c1}
---
B.
`)
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	srv.routeChannelEvent(context.Background(), ad, evFor("@bb hello"))

	if got := waitSession(t, srv, "agent-b"); got == nil {
		t.Fatal("@bb mention did not route to agent-b")
	}
	if got := srv.channelMgr.GetSession(evFor("x"), "agent-a"); got != nil {
		t.Fatal("@bb mention leaked a session to agent-a")
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

	srv.routeChannelEvent(context.Background(), ad, evFor("hello"))

	sess := waitSession(t, srv, "default")
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
	srv.routeChannelEvent(context.Background(), ad, channel.InboundEvent{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "diag"})
	opsSess := waitSession(t, srv, "ops")
	if opsSess == nil {
		t.Fatal("ops session not created")
	}
	waitPrompt(t, opsSess, "OPS expert")

	// Profile without system prompt → falls back to server base.
	evBare := channel.InboundEvent{Platform: "fake", ChatID: "c2", UserID: "u2", Text: "diag"}
	srv.routeChannelEvent(context.Background(), ad, evBare)
	bareSess := waitSessionFor(t, srv, "bare", evBare)
	if bareSess == nil {
		t.Fatal("bare session not created")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(bareSess.Agent.System, "SERVER BASE SYSTEM") {
			return // passed
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("bare profile did not fall back to base system: %q", bareSess.Agent.System)
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

	// First turn: profile exists → routes to "temp" → temp prompt applied.
	srv.routeChannelEvent(context.Background(), ad, channel.InboundEvent{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "hi"})
	tempSess := waitSession(t, srv, "temp")
	if tempSess == nil {
		t.Fatal("temp session not created")
	}
	waitPrompt(t, tempSess, "TEMP PROFILE PROMPT")
	// Delete the profile file — read-through Store misses on next route.
	if err := os.Remove(filepath.Join(dir, "temp.md")); err != nil {
		t.Fatal(err)
	}
	// Second turn: profile gone → resolves to default → base prompt. A new
	// session under "default" is created (different key namespace).
	srv.routeChannelEvent(context.Background(), ad, channel.InboundEvent{Platform: "fake", ChatID: "c1", UserID: "u1", Text: "again"})
	defSess := waitSession(t, srv, "default")
	if defSess == nil {
		t.Fatal("default session not created after profile delete")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sys := defSess.Agent.System
		if sys != "" && !strings.Contains(sys, "TEMP PROFILE PROMPT") && strings.Contains(sys, "SERVER BASE") {
			return // passed
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("new turn did not use base prompt: %q", defSess.Agent.System)
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
