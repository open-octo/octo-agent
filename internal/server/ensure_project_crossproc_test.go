package server

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/memory"
)

// sharedHomeEnv tells TestMain that this process was launched by a test which
// needs the inherited HOME rather than a private one — see TestMain.
const sharedHomeEnv = "OCTO_TEST_SHARED_HOME"

// childDirEnv carries the parent directory a re-exec'd child creates its
// projects under. Its presence is what puts the test below into child mode.
const childDirEnv = "OCTO_TEST_ENSURE_PROJECT_ROOT"

// startGateEnv names the file a child waits for before its first write.
const startGateEnv = "OCTO_TEST_ENSURE_PROJECT_GATE"

// Each child writes this many projects. One write per child would not test
// anything: process startup dwarfs a registry write, so children launched in
// sequence finish in sequence and never overlap. A burst per child is what puts
// their read-modify-write cycles on top of each other.
const writesPerChild = 25

// TestEnsureProject_CrossProcessLockKeepsEveryGroup is the reason a registry
// write takes a file lock and not just a mutex: the CLI writes the registry
// now, so racing writers can be separate PROCESSES, which no in-process mutex
// can order. Each write is a read-modify-write of the whole file, so without
// cross-process exclusion a writer that read before another's rename silently
// drops the groups that other one added.
//
// Children are released by a shared start file so their bursts overlap rather
// than being staggered by exec. Verified to fail when the file lock is removed.
//
// Re-execs this test binary (os.Args[0]) rather than building a helper, so it
// stays a plain `go test` with no build step.
func TestEnsureProject_CrossProcessLockKeepsEveryGroup(t *testing.T) {
	if root := os.Getenv(childDirEnv); root != "" {
		runProjectWriterChild(t, root)
		return
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	const children = 4
	gate := filepath.Join(t.TempDir(), "go")
	roots := make([]string, children)
	for i := range roots {
		roots[i] = t.TempDir()
	}

	cmds := make([]*exec.Cmd, children)
	outs := make([]*bytes.Buffer, children)
	for i, root := range roots {
		cmd := exec.Command(os.Args[0], "-test.run=TestEnsureProject_CrossProcessLockKeepsEveryGroup")
		cmd.Env = append(os.Environ(),
			sharedHomeEnv+"=1",
			"HOME="+home,
			"USERPROFILE="+home,
			childDirEnv+"="+root,
			startGateEnv+"="+gate,
		)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child %d: %v", i, err)
		}
		cmds[i], outs[i] = cmd, &out
	}

	// Every child is now spinning on the gate; opening it starts them together.
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatalf("open the start gate: %v", err)
	}

	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child %d (%s) failed: %v\n%s", i, roots[i], err, outs[i].String())
		}
	}

	// Read what the children wrote. This process never touched the registry, so
	// nothing is cached from before their writes.
	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	seen := map[string]bool{}
	for i := range groups {
		if wd := groups[i].WorkingDir; wd != "" {
			seen[memory.NormalizeDir(wd)] = true
		}
	}

	missing := 0
	var firstMissing string
	for _, root := range roots {
		for i := 0; i < writesPerChild; i++ {
			dir := memory.NormalizeDir(projectDirForWrite(root, i))
			if !seen[dir] {
				missing++
				if firstMissing == "" {
					firstMissing = dir
				}
			}
		}
	}
	if missing > 0 {
		t.Errorf("%d of %d projects were lost to concurrent writes by other processes (e.g. %s)",
			missing, children*writesPerChild, firstMissing)
	}
}

// runProjectWriterChild is the child half: wait for the gate, then write a
// burst of projects into the shared registry.
func runProjectWriterChild(t *testing.T, root string) {
	t.Helper()
	dirs := make([]string, writesPerChild)
	for i := range dirs {
		dirs[i] = projectDirForWrite(root, i)
		if err := os.MkdirAll(dirs[i], 0o755); err != nil {
			t.Fatalf("child mkdir: %v", err)
		}
	}

	waitForGate(t, os.Getenv(startGateEnv))

	for i, dir := range dirs {
		if err := EnsureProjectForDir(dir, fmt.Sprintf("sess-%s-%02d", filepath.Base(root), i)); err != nil {
			t.Fatalf("child write for %s: %v", dir, err)
		}
	}
}

// waitForGate spins until the gate file appears, so every child's burst starts
// at the same moment rather than whenever its process finished starting up.
func waitForGate(t *testing.T, gate string) {
	t.Helper()
	if gate == "" {
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(gate); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("start gate %s never opened", gate)
}

func projectDirForWrite(root string, i int) string {
	return filepath.Join(root, fmt.Sprintf("p%02d", i))
}
