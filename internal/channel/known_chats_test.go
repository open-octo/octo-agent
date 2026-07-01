package channel

import "testing"

func TestSplitSessionKey(t *testing.T) {
	cases := []struct {
		key                 SessionKey
		wantP, wantC, wantU string
	}{
		{"feishu:c1:u1", "feishu", "c1", "u1"},
		{"telegram:42", "telegram", "42", ""},
		{"weixin", "weixin", "", ""},
	}
	for _, c := range cases {
		p, ch, u := splitSessionKey(c.key)
		if p != c.wantP || ch != c.wantC || u != c.wantU {
			t.Errorf("splitSessionKey(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.key, p, ch, u, c.wantP, c.wantC, c.wantU)
		}
	}
}

// TestManager_KnownChats merges a live session with a persisted /bind entry and
// checks both surface with the right flags.
func TestManager_KnownChats(t *testing.T) {
	tempHome(t)
	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)

	// A live session (this process run) → Active.
	mgr.GetOrCreateSession(InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"})
	// A persisted binding for a different chat → Bound.
	if err := mgr.bindings.set(SessionKey("telegram:42:u2"), "sess-x"); err != nil {
		t.Fatalf("bind: %v", err)
	}

	byChat := map[string]KnownChat{}
	for _, kc := range mgr.KnownChats() {
		byChat[kc.Platform+":"+kc.ChatID] = kc
	}
	if len(byChat) != 2 {
		t.Fatalf("want 2 known chats, got %d: %+v", len(byChat), byChat)
	}

	live, ok := byChat["feishu:c1"]
	if !ok || !live.Active {
		t.Fatalf("feishu:c1 should be present and Active: %+v", live)
	}
	bound, ok := byChat["telegram:42"]
	if !ok || !bound.Bound {
		t.Fatalf("telegram:42 should be present and Bound: %+v", bound)
	}
	if bound.UserID != "u2" {
		t.Errorf("telegram user recovered from key = %q, want u2", bound.UserID)
	}
}
