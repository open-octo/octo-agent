package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sess := srv.channelMgr.GetSession(evFor("x"), agentID); sess != nil {
			return sess
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
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
