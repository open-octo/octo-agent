package main

import (
	"bytes"
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
