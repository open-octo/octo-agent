package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Leihb/octo-agent/internal/hooks"
)

// mkScript writes an executable /bin/sh script and returns its path. Skips on
// Windows, where the hook runner uses PowerShell rather than sh -c.
func mkScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook scripts use sh -c; not portable to Windows")
	}
	p := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// recordingGate is a stub inner PermissionGate: it returns a fixed verdict and
// records whether it was consulted.
type recordingGate struct {
	allow  bool
	reason string
	called bool
}

func (g *recordingGate) Check(_ context.Context, _ string, _ map[string]any) (bool, string) {
	g.called = true
	return g.allow, g.reason
}

func preToolAgent(t *testing.T, body string, inner PermissionGate) *Agent {
	t.Helper()
	a := New(&fakeSender{}, "m")
	a.Gate = inner
	a.Hooks = hooks.NewEngine(nil)
	if err := a.Hooks.RegisterShellMatched(hooks.EventPreToolUse, mkScript(t, body), "", 0); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestHookGate_BlockDeniesWithoutInner(t *testing.T) {
	inner := &recordingGate{allow: true}
	a := preToolAgent(t, "echo nope >&2; exit 2", inner)
	allowed, reason := a.effectiveGate().Check(context.Background(), "terminal", nil)
	if allowed {
		t.Error("PreToolUse block must deny")
	}
	if reason != "nope" {
		t.Errorf("reason = %q, want stderr text", reason)
	}
	if inner.called {
		t.Error("a blocked call must not reach the inner gate")
	}
}

func TestHookGate_AllowBypassesInner(t *testing.T) {
	// Inner gate would DENY, but the hook approves → tool runs, gate skipped.
	inner := &recordingGate{allow: false, reason: "gate would deny"}
	a := preToolAgent(t, `echo '{"decision":"approve"}'`, inner)
	allowed, _ := a.effectiveGate().Check(context.Background(), "terminal", nil)
	if !allowed {
		t.Error("PreToolUse approve must allow, bypassing a denying inner gate")
	}
	if inner.called {
		t.Error("approve must bypass the inner gate entirely")
	}
}

func TestHookGate_NoOpinionDelegatesToInner(t *testing.T) {
	inner := &recordingGate{allow: false, reason: "inner denies"}
	a := preToolAgent(t, "exit 0", inner)
	allowed, reason := a.effectiveGate().Check(context.Background(), "terminal", nil)
	if !inner.called {
		t.Error("no-opinion must delegate to the inner gate")
	}
	if allowed || reason != "inner denies" {
		t.Errorf("inner verdict should pass through: allowed=%v reason=%q", allowed, reason)
	}
}

func TestHookGate_NoOpinionNoInnerAllows(t *testing.T) {
	a := preToolAgent(t, "exit 0", nil) // no inner gate
	if allowed, _ := a.effectiveGate().Check(context.Background(), "terminal", nil); !allowed {
		t.Error("no-opinion with no inner gate should allow")
	}
}

func TestEffectiveGate_NoHookReturnsBareGate(t *testing.T) {
	inner := &recordingGate{allow: true}
	a := New(&fakeSender{}, "m")
	a.Gate = inner
	a.Hooks = hooks.NewEngine(nil) // no PreToolUse registered
	if a.effectiveGate() != PermissionGate(inner) {
		t.Error("with no PreToolUse hook, effectiveGate must return the bare gate")
	}
}
