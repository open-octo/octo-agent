package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestServeDefaultAddrIsLoopback pins the secure default: `octo serve`
// binds loopback unless the user explicitly exposes it.
func TestServeDefaultAddrIsLoopback(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runServe([]string{"-h"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("-h exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `"127.0.0.1:8088"`) {
		t.Errorf("usage should show loopback default addr, got:\n%s", stderr.String())
	}
}

func TestBindIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		":8080":          false, // wildcard shorthand — all interfaces
		"0.0.0.0:8080":   false,
		"[::]:8080":      false,
		"192.168.1.5:80": false,
		"myhost:8080":    false, // unresolvable name — fail closed
	}
	for addr, want := range cases {
		if got := bindIsLoopback(addr); got != want {
			t.Errorf("bindIsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestDisplayURLHost(t *testing.T) {
	// A specific bind host is used as-is; wildcard binds resolve to a LAN
	// IP (machine-dependent) or the <host> placeholder — both acceptable.
	if got := displayURLHost("192.168.1.5:8080"); got != "192.168.1.5:8080" {
		t.Errorf("specific host: got %q", got)
	}
	got := displayURLHost(":8080")
	if !strings.HasSuffix(got, ":8080") || strings.HasPrefix(got, ":") {
		t.Errorf("wildcard bind should yield host:8080, got %q", got)
	}
}

// TestServeRejectsPositionalArgs pins the #1614 fix: a bare value like
// ":19099" must not silently fall through to fs.Args() (which would leave
// --addr at its default 127.0.0.1:8088). runServe should refuse with a
// helpful error pointing at the flag form.
func TestServeRejectsPositionalArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runServe([]string{":19099", "-d"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "unexpected positional argument") {
		t.Errorf("want positional-argument error, got:\n%s", out)
	}
	if !strings.Contains(out, "-addr :19099") {
		t.Errorf("error should suggest the correct flag form, got:\n%s", out)
	}
}

// TestServeRejectsPositionalArgsAlone covers the bare case from #1614:
// a lone ":19099" with no other flag still errors (no daemon/supervisor
// path to fall through to).
func TestServeRejectsPositionalArgsAlone(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runServe([]string{":19099"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected positional argument") {
		t.Errorf("want positional-argument error, got:\n%s", stderr.String())
	}
}

// isolatePidFile points the daemon pid-file lookup at a temp dir so the
// subcommand tests below never see (or touch) a real ~/.octo/serve.pid.
func isolatePidFile(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	orig := daemonPidFile
	daemonPidFile = func() (string, error) { return filepath.Join(tmp, "serve.pid"), nil }
	t.Cleanup(func() { daemonPidFile = orig })
}

// TestServeStopSubcommand pins the #1842 fix: `octo serve stop` is accepted
// as a positional subcommand equivalent to --stop. With no daemon running it
// reaches stopDaemon and reports that, rather than the positional-arg error.
func TestServeStopSubcommand(t *testing.T) {
	isolatePidFile(t)
	var stdout, stderr bytes.Buffer
	code := runServe([]string{"stop"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (no daemon running)", code)
	}
	if !strings.Contains(stderr.String(), "daemon not running") {
		t.Errorf("want stopDaemon's not-running message, got:\n%s", stderr.String())
	}
}

// TestServeStatusSubcommand: `octo serve status` maps to --status.
func TestServeStatusSubcommand(t *testing.T) {
	isolatePidFile(t)
	var stdout, stderr bytes.Buffer
	code := runServe([]string{"status"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "not running") {
		t.Errorf("want statusDaemon output, got:\n%s", stdout.String())
	}
}

// TestServeStopRejectsExtraArgs: the subcommand form takes nothing after it.
func TestServeStopRejectsExtraArgs(t *testing.T) {
	isolatePidFile(t)
	var stdout, stderr bytes.Buffer
	code := runServe([]string{"stop", "now"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected extra argument") {
		t.Errorf("want extra-argument error, got:\n%s", stderr.String())
	}
}

// TestServeStopRejectsTrailingFlags pins the flag-tokenization behavior the
// dispatch relies on: flag parsing stops at the first non-flag arg, so
// `octo serve stop -d` yields rest = ["stop", "-d"] and is rejected rather
// than executing an ambiguous mix of subcommand and flags.
func TestServeStopRejectsTrailingFlags(t *testing.T) {
	isolatePidFile(t)
	var stdout, stderr bytes.Buffer
	code := runServe([]string{"stop", "-d"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected extra argument") {
		t.Errorf("want extra-argument error, got:\n%s", stderr.String())
	}
}

// TestServePositionalHintNotMisleading: a word that isn't an address must not
// be echoed into the -addr example (`octo serve -addr restart -d` — #1842);
// the error also points at the stop/status subcommands.
func TestServePositionalHintNotMisleading(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runServe([]string{"restart"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	out := stderr.String()
	if strings.Contains(out, "-addr restart") {
		t.Errorf("error must not suggest `-addr restart`, got:\n%s", out)
	}
	if !strings.Contains(out, "`octo serve stop`") {
		t.Errorf("error should mention the stop subcommand, got:\n%s", out)
	}
}
