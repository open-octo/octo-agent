package permission

import "testing"

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
