package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
)

// fakeLatest serves only the releases/latest redirect — enough for --check.
func fakeLatest(t *testing.T, ver string) {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/releases/tag/v"+ver, http.StatusFound)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	orig := upgrade.BaseURL
	upgrade.BaseURL = srv.URL
	t.Cleanup(func() { upgrade.BaseURL = orig })
}

func TestRunUpgrade_Check(t *testing.T) {
	fakeLatest(t, "9.9.9")
	origV := version.Version
	version.Version = "0.18.0"
	t.Cleanup(func() { version.Version = origV })

	var stdout, stderr bytes.Buffer
	code := runUpgrade([]string{"--check"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"latest:  9.9.9", "update available"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunUpgrade_CheckUpToDate(t *testing.T) {
	fakeLatest(t, "0.18.0")
	origV := version.Version
	version.Version = "0.18.0"
	t.Cleanup(func() { version.Version = origV })

	var stdout, stderr bytes.Buffer
	if code := runUpgrade([]string{"--check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Errorf("expected up-to-date message, got:\n%s", stdout.String())
	}
}

func TestRunUpgrade_DevRefusalExitCode(t *testing.T) {
	// No fake server needed: the eligibility refusal fires before any
	// network access (the test binary has no release metadata). Pin the
	// origin to an unroutable address anyway.
	orig := upgrade.BaseURL
	upgrade.BaseURL = "http://127.0.0.1:0"
	t.Cleanup(func() { upgrade.BaseURL = orig })
	isolatePidFile(t)

	var stdout, stderr bytes.Buffer
	code := runUpgrade(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("refusal should hint at --force, got: %s", stderr.String())
	}
	if strings.Contains(stderr.String(), "hint:") {
		t.Errorf("no backend is registered, so no hint should print, got: %s", stderr.String())
	}
}

func TestRunUpgrade_BadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runUpgrade([]string{"--no-such-flag"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2 for flag errors", code)
	}
}

func TestRunningServeDaemon(t *testing.T) {
	isolatePidFile(t)
	if pid, ok := runningServeDaemon(); ok {
		t.Fatalf("no pid file: got running daemon pid %d", pid)
	}
	pidFile, err := daemonPidFile()
	if err != nil {
		t.Fatal(err)
	}
	// Our own pid is guaranteed alive.
	if err := writePidFile(pidFile, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	pid, ok := runningServeDaemon()
	if !ok || pid != os.Getpid() {
		t.Fatalf("got (%d, %v), want (%d, true)", pid, ok, os.Getpid())
	}
}

// TestRunUpgrade_FailureHintsRunningServeDaemon pins the #1842 follow-up: when
// the upgrade fails while an `octo serve` daemon is running, the error is
// followed by a hint that the daemon may be locking the binary (Windows) and
// how to stop/retry/restart.
func TestRunUpgrade_FailureHintsRunningServeDaemon(t *testing.T) {
	// The fake origin serves only releases/latest, so the asset download 404s
	// and Run fails after the version check. Clear the mirrors so the failing
	// download never leaves the httptest server.
	fakeLatest(t, "9.9.9")
	origMirrors := upgrade.MirrorBaseURLs
	upgrade.MirrorBaseURLs = nil
	t.Cleanup(func() { upgrade.MirrorBaseURLs = origMirrors })
	origV := version.Version
	version.Version = "0.18.0"
	t.Cleanup(func() { version.Version = origV })

	isolatePidFile(t)
	pidFile, err := daemonPidFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := writePidFile(pidFile, os.Getpid()); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runUpgrade([]string{"--force"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "hint: an octo backend is running") {
		t.Errorf("stderr should hint at the running backend, got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "`octo serve stop`") {
		t.Errorf("hint should mention `octo serve stop`, got:\n%s", stderr.String())
	}
}
