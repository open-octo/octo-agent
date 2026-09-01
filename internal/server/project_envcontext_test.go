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
	if sourceDirsHash(p1) == sourceDirsHash(p2) {
		t.Error("mount order rewords the rendered prompt, so it must change the hash — or a reorder silently keeps a stale freeze")
	}
	if sourceDirsHash(p1) != sourceDirsHash(&sessionGroup{ID: "other-id", SourceDirs: []string{a, b}}) {
		t.Error("the hash must depend only on the mounts, not the project id")
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
}

func TestAppendProjectEnvContext_ListsFolders(t *testing.T) {
	src := t.TempDir()
	other := t.TempDir()
	proj := &sessionGroup{ID: "g-1", Name: "订单", WorkingDir: t.TempDir(), SourceDirs: []string{src, other}}

	got := appendProjectEnvContext("# Environment\n\n- Working directory: /w\n", proj)
	for _, want := range []string{src, other, "scratch"} {
		if !strings.Contains(got, want) {
			t.Errorf("project env context missing %q:\n%s", want, got)
		}
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

func TestBuildEnvContext_IncludesTimezone(t *testing.T) {
	out := buildEnvContext("/some/dir")
	if !strings.Contains(out, "Timezone:") {
		t.Errorf("env context missing timezone line:\n%s", out)
	}
}
