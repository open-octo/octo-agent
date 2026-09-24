package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
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
	t.Setenv("HOME", "/home/test")
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	out := buildEnvContext("/some/dir", "")
	for _, want := range []string{"# Environment", "/some/dir", "Home directory:", "Timezone:", "OS/arch:", "Locale:"} {
		if !strings.Contains(out, want) {
			t.Errorf("env context missing %q:\n%s", want, out)
		}
	}
}

// The model learns the port this server really listens on, so skills that
// spell the default 127.0.0.1:8088 don't send it there first.
func TestBuildEnvContext_NamesServerAddr(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{"127.0.0.1:18090", "- Octo server: http://127.0.0.1:18090 ("},
		{":9000", "- Octo server: http://127.0.0.1:9000 ("},
		{"0.0.0.0:9000", "- Octo server: http://127.0.0.1:9000 ("},
		{"[::]:9000", "- Octo server: http://127.0.0.1:9000 ("},
		{"192.168.1.5:8088", "- Octo server: http://192.168.1.5:8088 ("},
		{"[::1]:8088", "- Octo server: http://[::1]:8088 ("},
	} {
		if out := buildEnvContext("/some/dir", tc.addr); !strings.Contains(out, tc.want) {
			t.Errorf("addr %q: env context missing %q:\n%s", tc.addr, tc.want, out)
		}
	}
	for addr, wantKey := range map[string]bool{
		"127.0.0.1:8088":   false,
		":8088":            false,
		"[::1]:8088":       false,
		"localhost:8088":   false,
		"192.168.1.5:8088": true,
		"octo.lan:8088":    true,
	} {
		out := buildEnvContext("/some/dir", addr)
		if got := strings.Contains(out, "X-Access-Key"); got != wantKey {
			t.Errorf("addr %q: access-key note present = %v, want %v:\n%s", addr, got, wantKey, out)
		}
	}
	for _, addr := range []string{"", "127.0.0.1:0", "garbage"} {
		if out := buildEnvContext("/some/dir", addr); strings.Contains(out, "Octo server:") {
			t.Errorf("addr %q names no concrete port, but got a server line:\n%s", addr, out)
		}
	}
}

// New carries Config.Addr into the env context every session prompt reuses.
func TestNew_EnvContextNamesConfiguredAddr(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("OCTO_ACCESS_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	// New does not bind, so a fixed port here collides with nothing.
	srv, err := New(Config{Addr: "127.0.0.1:18090", NoChannel: true, NoMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, env := srv.curCwdEnv(); !strings.Contains(env, "- Octo server: http://127.0.0.1:18090 (") {
		t.Errorf("env context missing the server address:\n%s", env)
	}
}

// A session with its own working directory rebuilds the env context rather
// than reusing the launch one; the rebuilt block must still name the server.
func TestSessionCwdEnv_OwnDirNamesServerAddr(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:18090"})
	dir := t.TempDir()
	gotDir, env := srv.sessionCwdEnv(&agent.Session{ID: "own-dir", WorkingDir: dir})
	if gotDir != dir {
		t.Fatalf("session dir = %q, want %q (test no longer reaches the rebuild path)", gotDir, dir)
	}
	if !strings.Contains(env, "- Octo server: http://127.0.0.1:18090 (") {
		t.Errorf("rebuilt env context missing the server address:\n%s", env)
	}
}
