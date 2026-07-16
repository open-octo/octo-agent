package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/audit"
	"github.com/open-octo/octo-agent/internal/permission"
)

func newEngine(t *testing.T, mode permission.Mode) *permission.Engine {
	t.Helper()
	eng, err := permission.New("", "/work", mode)
	if err != nil {
		t.Fatalf("permission.New: %v", err)
	}
	return eng
}

// allowAsk is a PermissionAsk that records calls and returns a fixed verdict.
func recordingAsk(allow, remember bool, err error, calls *int) PermissionAsk {
	return func(_ context.Context, _ string, _ map[string]any) (bool, bool, error) {
		*calls++
		return allow, remember, err
	}
}

func TestGate_AuditLogsDenyAndAsk(t *testing.T) {
	// Inject an audit logger at a temp path instead of the default
	// ~/.octo/audit.log. (Redirecting $HOME would not work on Windows, where
	// os.UserHomeDir reads %USERPROFILE%.)
	logPath := filepath.Join(t.TempDir(), "audit.log")
	g := &permissionGate{engine: newEngine(t, permission.ModeInteractive), audit: audit.NewAt(logPath)}

	g.Check(context.Background(), "terminal", map[string]any{"command": "rm -rf /"})
	g.Check(context.Background(), "terminal", map[string]any{"command": "sudo apt update"})

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(lines))
	}

	var ev audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.Tool != "terminal" || ev.Decision != "deny" || ev.Input["command"] != "rm -rf /" {
		t.Errorf("deny event mismatch: %+v", ev)
	}

	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil {
		t.Fatalf("unmarshal second event: %v", err)
	}
	if ev.Tool != "terminal" || ev.Decision != "ask-denied" || ev.Input["command"] != "sudo apt update" {
		t.Errorf("ask-denied event mismatch: %+v", ev)
	}
}

func TestGate_AllowPassesThrough(t *testing.T) {
	calls := 0
	g := NewPermissionGate(newEngine(t, permission.ModeInteractive), recordingAsk(true, false, nil, &calls))
	ok, _ := g.Check(context.Background(), "terminal", map[string]any{"command": "ls"})
	if !ok {
		t.Error("ls should be allowed")
	}
	if calls != 0 {
		t.Errorf("allow must not prompt; asked %d time(s)", calls)
	}
}

func TestGate_DenyReturnsReason(t *testing.T) {
	calls := 0
	g := NewPermissionGate(newEngine(t, permission.ModeInteractive), recordingAsk(true, false, nil, &calls))
	ok, reason := g.Check(context.Background(), "terminal", map[string]any{"command": "rm -rf /"})
	if ok {
		t.Error("rm -rf / must be denied")
	}
	if !strings.Contains(reason, "permission_denied") {
		t.Errorf("expected structured denial reason, got %q", reason)
	}
	if calls != 0 {
		t.Errorf("deny must not prompt; asked %d time(s)", calls)
	}
}

func TestGate_AskInteractive_Allow(t *testing.T) {
	calls := 0
	g := NewPermissionGate(newEngine(t, permission.ModeInteractive), recordingAsk(true, false, nil, &calls))
	ok, _ := g.Check(context.Background(), "terminal", map[string]any{"command": "sudo apt update"})
	if !ok || calls != 1 {
		t.Errorf("ask-class should prompt once and allow; ok=%v calls=%d", ok, calls)
	}
}

func TestGate_AskInteractive_DeclineOrError(t *testing.T) {
	for _, c := range []struct {
		name  string
		allow bool
		err   error
	}{
		{"declined", false, nil},
		{"error", false, errors.New("reader closed")},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			g := NewPermissionGate(newEngine(t, permission.ModeInteractive), recordingAsk(c.allow, false, c.err, &calls))
			ok, reason := g.Check(context.Background(), "terminal", map[string]any{"command": "sudo apt update"})
			if ok {
				t.Error("decline/error must deny")
			}
			if !strings.Contains(reason, "declined") {
				t.Errorf("reason should note the user declined, got %q", reason)
			}
		})
	}
}

func TestGate_AskRemembers(t *testing.T) {
	calls := 0
	g := NewPermissionGate(newEngine(t, permission.ModeInteractive), recordingAsk(true, true, nil, &calls))
	input := map[string]any{"command": "sudo apt update"}
	if ok, _ := g.Check(context.Background(), "terminal", input); !ok {
		t.Fatal("first ask should allow")
	}
	if ok, _ := g.Check(context.Background(), "terminal", input); !ok {
		t.Error("remembered decision should allow on repeat")
	}
	if calls != 1 {
		t.Errorf("second call should not prompt again; total asks=%d, want 1", calls)
	}
}

func TestGate_NonInteractive_DeniesAsk(t *testing.T) {
	// A nil PermissionAsk is the server/IM posture: ask-class verdicts deny
	// without prompting, carrying the policy's reason.
	g := NewPermissionGate(newEngine(t, permission.ModeInteractive), nil)
	ok, reason := g.Check(context.Background(), "terminal", map[string]any{"command": "sudo apt update"})
	if ok {
		t.Error("non-interactive gate must deny ask-class commands")
	}
	if !strings.Contains(reason, "permission_denied") {
		t.Errorf("expected denial reason, got %q", reason)
	}
}

func TestGate_UnwrapsToolCall(t *testing.T) {
	// A Tool Search mcp_call must be evaluated against the wrapped tool name.
	g := NewPermissionGate(newEngine(t, permission.ModeInteractive), nil)
	ok, reason := g.Check(context.Background(), "mcp_call", map[string]any{
		"name":      "terminal",
		"arguments": map[string]any{"command": "rm -rf /"},
	})
	if ok {
		t.Error("mcp_call wrapping rm -rf / must be denied via the unwrapped name")
	}
	if !strings.Contains(reason, "permission_denied") {
		t.Errorf("expected denial reason, got %q", reason)
	}
}
