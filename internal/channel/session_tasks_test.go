package channel

import "testing"

// TestSessionTaskStorePersists verifies each session owns a task store that
// survives across messages in the same chat — so the IM bridge's task list
// doesn't reset every turn. (A fresh per-message store would lose it.)
func TestSessionTaskStorePersists(t *testing.T) {
	cfg := &Config{Channels: map[string]PlatformConfig{"mock": {"enabled": true}}}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ev := InboundEvent{Platform: "mock", ChatID: "c1", UserID: "u1"}

	s1 := mgr.GetOrCreateSession(ev)
	if s1.Tasks == nil {
		t.Fatal("session should have a task store")
	}
	if _, err := s1.Tasks.Create("ship it", "", ""); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// A later message for the same chat reuses the session and its store.
	s2 := mgr.GetOrCreateSession(ev)
	if s2 != s1 {
		t.Fatal("same chat should reuse the session")
	}
	if got := len(s2.Tasks.List()); got != 1 {
		t.Errorf("task store should persist across messages, got %d tasks", got)
	}

	// A different chat gets its own, independent store.
	other := mgr.GetOrCreateSession(InboundEvent{Platform: "mock", ChatID: "c2", UserID: "u2"})
	if len(other.Tasks.List()) != 0 {
		t.Error("a different chat should have an independent task store")
	}
}
