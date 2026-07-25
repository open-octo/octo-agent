package channel

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// The multi-agent session-key contract (dev-docs/multi-agent-system-design.md
// §3): the default agent keeps byte-identical legacy keys and store IDs —
// zero migration — while expert agents derive isolation from the
// "<agentID>#" prefix.

func expertProfile(id string) *agentprofile.Profile {
	return &agentprofile.Profile{ID: id, Description: "test expert"}
}

func TestSessionKeyFor_AgentNamespace(t *testing.T) {
	ev := InboundEvent{Platform: "weixin", ChatID: "c1", UserID: "u1"}
	legacy := SessionKey("weixin:c1:u1")

	if got := sessionKeyFor(BindByChatUser, ev, ""); got != legacy {
		t.Errorf("empty agentID = %q, want legacy %q", got, legacy)
	}
	if got := sessionKeyFor(BindByChatUser, ev, "default"); got != legacy {
		t.Errorf("default agentID = %q, want legacy %q", got, legacy)
	}
	if got := sessionKeyFor(BindByChatUser, ev, "code-review"); got != "code-review#weixin:c1:u1" {
		t.Errorf("expert key = %q", got)
	}
}

func TestSessionStoreID_AgentNamespace(t *testing.T) {
	ev := InboundEvent{Platform: "weixin", ChatID: "c1", UserID: "u1"}
	legacyKey := sessionKeyFor(BindByChatUser, ev, "")
	expertKey := sessionKeyFor(BindByChatUser, ev, "code-review")

	if legacyKey != SessionKey("weixin:c1:u1") {
		t.Fatalf("legacy key changed shape: %q", legacyKey)
	}
	if sessionStoreID(expertKey) == sessionStoreID(legacyKey) {
		t.Error("expert and default agents must not share a store file")
	}
	// The default agent's store ID is byte-identical to the pre-multi-agent
	// derivation: the key is the same string, hashed the same way.
	if !strings.HasPrefix(sessionStoreID(legacyKey), "im-weixin-c1-u1-") {
		t.Errorf("legacy store ID shape changed: %q", sessionStoreID(legacyKey))
	}
}

func TestManager_AgentNamespacesIsolateSessions(t *testing.T) {
	tempHome(t)
	m := testManager()
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	def := m.GetOrCreateSession(ev, nil)
	exp := m.GetOrCreateSession(ev, expertProfile("code-review"))

	if def == exp {
		t.Fatal("same chat under two agents must yield two sessions")
	}
	if def.AgentID != "default" || exp.AgentID != "code-review" {
		t.Fatalf("AgentID stamps = %q / %q", def.AgentID, exp.AgentID)
	}
	if def.Store.ID == exp.Store.ID {
		t.Fatal("sessions of different agents must not share a store file")
	}
	// The expert store carries agent_id; the default store keeps it empty
	// (legacy shape).
	if exp.Store.EffectiveAgentID() != "code-review" {
		t.Fatalf("expert store agent_id = %q", exp.Store.AgentID)
	}
	if def.Store.AgentID != "" && def.Store.AgentID != "default" {
		t.Fatalf("default store agent_id = %q", def.Store.AgentID)
	}
	// Re-resolving either agent returns its own session.
	if got := m.GetSession(ev, "code-review"); got != exp {
		t.Fatal("GetSession with expert ID returned wrong session")
	}
	if got := m.GetSession(ev, ""); got != def {
		t.Fatal("GetSession with default ID returned wrong session")
	}
}

func TestManager_CrossAgentBindRejected(t *testing.T) {
	tempHome(t)
	m := testManager()

	// An expert-agent session exists on disk.
	exp := m.GetOrCreateSession(InboundEvent{Platform: "feishu", ChatID: "cX", UserID: "uX"}, expertProfile("code-review"))
	if err := exp.Persist(); err != nil {
		t.Fatal(err)
	}
	// A default-agent session exists too.
	def := m.GetOrCreateSession(InboundEvent{Platform: "feishu", ChatID: "cY", UserID: "uY"}, nil)
	if err := def.Persist(); err != nil {
		t.Fatal(err)
	}

	evB := InboundEvent{Platform: "feishu", ChatID: "cB", UserID: "uB"}

	// /list under default sees only default-owned sessions.
	if got := m.cmdList(""); !strings.Contains(got, def.Store.ShortID()) || strings.Contains(got, exp.Store.ShortID()) {
		t.Fatalf("default /list leaked expert session: %q", got)
	}
	// /list under the expert sees only its own.
	if got := m.cmdList("code-review"); !strings.Contains(got, exp.Store.ShortID()) || strings.Contains(got, def.Store.ShortID()) {
		t.Fatalf("expert /list leaked default session: %q", got)
	}
	// Binding the expert's session from a default-routed chat is rejected.
	if reply := m.cmdBind(evB, []string{exp.Store.ID}, ""); !strings.Contains(reply, "No session matches") {
		t.Fatalf("cross-agent bind = %q, want rejection", reply)
	}
	// Same-agent bind works.
	if reply := m.cmdBind(evB, []string{def.Store.ID}, ""); !strings.Contains(strings.ToLower(reply), "bound") {
		t.Fatalf("same-agent bind = %q, want success", reply)
	}
}

func TestSplitSessionKey_AgentPrefix(t *testing.T) {
	cases := []struct {
		key                  SessionKey
		platform, chat, user string
	}{
		{"weixin:c1:u1", "weixin", "c1", "u1"},
		{"code-review#weixin:c1:u1", "weixin", "c1", "u1"},
		// A chat ID containing '#' is NOT mistaken for an agent prefix: the
		// segment before '#' is not a valid profile ID.
		{"feishu:oc_4a/b#c:ou_x", "feishu", "oc_4a/b#c", "ou_x"},
		{"feishu:c1", "feishu", "c1", ""},
	}
	for _, tt := range cases {
		p, c, u := splitSessionKey(tt.key)
		if p != tt.platform || c != tt.chat || u != tt.user {
			t.Errorf("splitSessionKey(%q) = %q,%q,%q want %q,%q,%q", tt.key, p, c, u, tt.platform, tt.chat, tt.user)
		}
	}
}

// A restored legacy store (no agent_id) keeps working under the default
// agent: byte-identical key, byte-identical store ID, effective default.
func TestManager_LegacyStoreRestoresUnderDefault(t *testing.T) {
	tempHome(t)
	m := testManager()
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	sess := m.GetOrCreateSession(ev, nil)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "legacy"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	legacyStoreID := sess.Store.ID
	if sess.Store.AgentID != "" && sess.Store.AgentID != "default" {
		t.Fatalf("legacy-shaped store agent_id = %q", sess.Store.AgentID)
	}

	m2 := testManager()
	restored := m2.GetOrCreateSession(ev, nil)
	if restored.Store.ID != legacyStoreID {
		t.Fatalf("default agent store ID changed: %q → %q", legacyStoreID, restored.Store.ID)
	}
	if got := len(restored.Agent.History.Snapshot()); got != 1 {
		t.Fatalf("restored history = %d messages, want 1", got)
	}
	if restored.Store.EffectiveAgentID() != "default" {
		t.Fatalf("legacy store effective agent = %q", restored.Store.EffectiveAgentID())
	}
}
