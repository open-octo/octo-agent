package permission

import (
	"os"
	"testing"
)

// TestRemembered_SharedAcrossEngines: the server rebuilds its engine every
// turn; a session-scoped Remembered store attached to each fresh engine must
// carry "always allow" decisions across rebuilds.
func TestRemembered_SharedAcrossEngines(t *testing.T) {
	store := NewRemembered()
	input := map[string]any{"command": "sudo ls /tmp"}

	e1, err := New("", t.TempDir(), ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	e1.AttachRemembered(store)
	if got := e1.Check("terminal", input); got != Ask {
		t.Fatalf("precondition: sudo should be ask-class, got %v", got)
	}

	e1.Remember("terminal", input, Allow)

	e2, err := New("", t.TempDir(), ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	e2.AttachRemembered(store)
	if got := e2.Check("terminal", input); got != Allow {
		t.Errorf("remembered allow must survive an engine rebuild, got %v", got)
	}
	// A different input signature still asks.
	if got := e2.Check("terminal", map[string]any{"command": "sudo rm x"}); got != Ask {
		t.Errorf("a different command must not inherit the remembered allow, got %v", got)
	}

	e3, err := New("", t.TempDir(), ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if got := e3.Check("terminal", input); got != Ask {
		t.Errorf("an engine without the store attached must not see it, got %v", got)
	}
}

// TestRemembered_DenyRuleBeatsCache: a deny rule added to permissions.yml
// mid-session (engines are rebuilt per turn to pick that up) must override a
// previously remembered allow for the same call.
func TestRemembered_DenyRuleBeatsCache(t *testing.T) {
	store := NewRemembered()
	input := map[string]any{"command": "sudo make install"}

	e1, err := New("", t.TempDir(), ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	e1.AttachRemembered(store)
	e1.Remember("terminal", input, Allow)
	if e1.Check("terminal", input) != Allow {
		t.Fatal("precondition: remembered allow should hold")
	}

	// Operator adds a hard deny for sudo; the next turn's engine loads it.
	cfgPath := t.TempDir() + "/permissions.yml"
	if err := os.WriteFile(cfgPath, []byte("terminal:\n  - deny: { pattern: \"sudo\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e2, err := New(cfgPath, t.TempDir(), ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	e2.AttachRemembered(store)
	if got := e2.Check("terminal", input); got != Deny {
		t.Errorf("deny rule must beat the remembered allow, got %v", got)
	}
}
