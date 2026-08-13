package crashlog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The interesting behaviour — a panic trace surviving the process that produced
// it — can only be observed from outside that process, so the tests re-exec the
// test binary as a child that installs the crash log and then dies.

const crashEnv = "OCTO_CRASHLOG_TEST_PATH"

func TestMain(m *testing.M) {
	if path := os.Getenv(crashEnv); path != "" {
		crashChild(path)
	}
	os.Exit(m.Run())
}

// crashChild never returns: it installs the crash log and panics with a
// goroutine parked elsewhere, so the parent can check both the panicking stack
// and the traceback=all extras.
func crashChild(path string) {
	if err := Install(path, "child-banner"); err != nil {
		fmt.Println("install:", err)
		os.Exit(3)
	}
	fmt.Fprintln(os.Stderr, "written-through-os-stderr")
	started := make(chan struct{})
	go parkedGoroutine(started)
	<-started
	panic("boom-from-child")
}

func parkedGoroutine(started chan struct{}) {
	close(started)
	select {}
}

// runCrashChild re-execs this test binary in crash mode and returns what the
// child wrote to its own (inherited) stderr.
func runCrashChild(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), crashEnv+"="+path)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("child exited 0, want a crash; output:\n%s", out)
	}
	return string(out)
}

func TestInstallCapturesPanicTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	inherited := runCrashChild(t, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"child-banner", // the run banner ties the trace to a launch
		"pid ",         // …with the pid that produced it
		"panic: boom-from-child",
		"goroutine ",
		"written-through-os-stderr", // plain os.Stderr writes land here too
	} {
		if !strings.Contains(got, want) {
			t.Errorf("crash log missing %q; got:\n%s", want, got)
		}
	}

	// traceback=all: the stack of a goroutine that wasn't the one panicking.
	if !strings.Contains(got, "parkedGoroutine") {
		t.Errorf("crash log has no non-panicking goroutine stack (traceback not raised to all); got:\n%s", got)
	}

	// The redirect must move stderr, not tee it — otherwise a windowsgui build
	// would still be writing the trace into the void it was writing to before.
	if strings.Contains(inherited, "boom-from-child") {
		t.Errorf("panic trace still went to the inherited stderr:\n%s", inherited)
	}
}

func TestInstallAppendsAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	runCrashChild(t, path)
	runCrashChild(t, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	// A second crash must not erase the first: users report the crash they can
	// still reproduce, which is rarely the one they had the log open for.
	if n := strings.Count(string(data), "child-banner"); n != 2 {
		t.Errorf("banner count = %d, want 2 (second run truncated the log?)", n)
	}
}
