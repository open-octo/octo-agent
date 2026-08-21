package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceDirsHash_Stability(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	p1 := &sessionGroup{ID: "g-1", SourceDirs: []string{a, b}}
	p2 := &sessionGroup{ID: "g-2", SourceDirs: []string{b, a}} // same set, other order
	if sourceDirsHash(p1) != sourceDirsHash(p2) {
		t.Error("mount order changed the hash — that would churn the prompt cache for nothing")
	}
	if sourceDirsHash(p1) == "" {
		t.Error("a project with mounts must hash to a non-empty identity")
	}
	if got := sourceDirsHash(&sessionGroup{ID: "g-3"}); got == "" {
		t.Error("a zero-folder project must still hash to a non-empty identity — being in a project changes the prompt")
	}
	if got := sourceDirsHash(nil); got != "" {
		t.Errorf("no project must hash to the empty identity, got %q", got)
	}
	withOut := &sessionGroup{ID: "g-1", SourceDirs: []string{a, b}, OutputDir: a}
	if sourceDirsHash(withOut) == sourceDirsHash(p1) {
		t.Error("marking an output dir changes the env context and must change the hash")
	}
}

func TestAppendProjectEnvContext_ListsFoldersAndOutputDir(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()
	proj := &sessionGroup{ID: "g-1", Name: "订单", WorkingDir: t.TempDir(), SourceDirs: []string{src, out}, OutputDir: out}

	got := appendProjectEnvContext("# Environment\n\n- Working directory: /w\n", proj)
	for _, want := range []string{src, out, "scratch"} {
		if !strings.Contains(got, want) {
			t.Errorf("project env context missing %q:\n%s", want, got)
		}
	}
	// The output marker must single out the output dir, and the instruction
	// must tell the model deliverables go there.
	if !strings.Contains(got, "output") && !strings.Contains(got, "Output") {
		t.Errorf("no output-dir instruction in:\n%s", got)
	}
}

func TestAppendProjectEnvContext_TaskUnchanged(t *testing.T) {
	base := "# Environment\n\n- Working directory: /w\n"
	if got := appendProjectEnvContext(base, nil); got != base {
		t.Errorf("a task's env context must be byte-for-byte unchanged, got:\n%s", got)
	}
}

func TestAppendProjectEnvContext_GitBranchForRepoFolder(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	run("init", "-b", "feature-x")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "c")

	proj := &sessionGroup{ID: "g-1", WorkingDir: t.TempDir(), SourceDirs: []string{repo}}
	got := appendProjectEnvContext("base\n", proj)
	if !strings.Contains(got, "feature-x") {
		t.Errorf("repo folder's branch missing from:\n%s", got)
	}
}
