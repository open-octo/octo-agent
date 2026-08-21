package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// The diff endpoints run real git against real repositories: mocking git would
// only test our idea of its output, and porcelain/rename/no-commit behaviour is
// exactly what can surprise us.

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		git(t, dir, args...)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// diffSession persists a session pinned to dir, which is what sessionCwdByID
// resolves for a cold session.
func diffSession(t *testing.T, dir string) string {
	t.Helper()
	sess := agent.NewSession("stub-model", "")
	sess.WorkingDir = dir
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

// resolvedRoot is dir as git reports it. On macOS t.TempDir() sits under /var,
// a symlink to /private/var, and `rev-parse --show-toplevel` prints the
// resolved form — so test expectations have to resolve too.
func resolvedRoot(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func getDiff(t *testing.T, srv *Server, id, query string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/sessions/" + id + "/diff"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	return w
}

func decodeDiff(t *testing.T, w *httptest.ResponseRecorder) diffResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp diffResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	return resp
}

func fileByPath(t *testing.T, repo diffRepo, path string) diffFile {
	t.Helper()
	for _, f := range repo.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("file %q not in %v", path, repoPaths(repo))
	return diffFile{}
}

func repoPaths(repo diffRepo) []string {
	var out []string
	for _, f := range repo.Files {
		out = append(out, f.Path)
	}
	return out
}

// TestSessionDiffMixedChanges: staged, unstaged and untracked changes in one
// repository come back as a single merged view against HEAD, with the staged
// flag carried alongside.
func TestSessionDiffMixedChanges(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "app")
	gitRepo(t, repo)
	writeFile(t, repo, "staged.txt", "one\n")
	writeFile(t, repo, "dirty.txt", "one\n")
	writeFile(t, repo, "gone.txt", "one\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "init")

	writeFile(t, repo, "staged.txt", "one\ntwo\n")
	git(t, repo, "add", "staged.txt")
	writeFile(t, repo, "dirty.txt", "one\nthree\n")
	git(t, repo, "rm", "-q", "gone.txt")
	writeFile(t, repo, "fresh.txt", "brand new\nsecond line\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id := diffSession(t, repo)

	resp := decodeDiff(t, getDiff(t, srv, id, ""))
	if len(resp.Repos) != 1 {
		t.Fatalf("repos = %d, want 1: %+v", len(resp.Repos), resp.Repos)
	}
	r := resp.Repos[0]
	if r.Root != resolvedRoot(t, repo) || r.Name != "app" || r.Branch != "main" {
		t.Errorf("repo = %+v", r)
	}
	if len(r.Files) != 4 {
		t.Fatalf("files = %v, want 4", repoPaths(r))
	}

	staged := fileByPath(t, r, "staged.txt")
	if staged.Status != "M" || !staged.Staged || staged.Adds != 1 || staged.Patch == nil {
		t.Errorf("staged.txt = %+v", staged)
	}

	dirty := fileByPath(t, r, "dirty.txt")
	if dirty.Status != "M" || dirty.Staged {
		t.Errorf("dirty.txt = %+v", dirty)
	}

	gone := fileByPath(t, r, "gone.txt")
	if gone.Status != "D" || gone.Dels != 1 {
		t.Errorf("gone.txt = %+v", gone)
	}

	// Untracked: synthesised as a new-file patch without touching the index.
	fresh := fileByPath(t, r, "fresh.txt")
	if fresh.Status != "?" || fresh.Staged || fresh.Adds != 2 || fresh.Patch == nil {
		t.Fatalf("fresh.txt = %+v", fresh)
	}
	if fresh.Patch.OldPath != "/dev/null" || fresh.Patch.NewPath != "fresh.txt" {
		t.Errorf("fresh patch paths = %+v", fresh.Patch)
	}
	if got := fresh.Patch.Hunks[0].Lines[0].Content; got != "brand new" {
		t.Errorf("fresh first line = %q", got)
	}
	// The read-only promise: the untracked file must still be untracked.
	if out := git(t, repo, "status", "--porcelain", "fresh.txt"); !strings.HasPrefix(out, "??") {
		t.Errorf("index was modified: status = %q", out)
	}
}

// TestSessionDiffRename: a rename keeps both sides so the UI can show the move.
func TestSessionDiffRename(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "app")
	gitRepo(t, repo)
	writeFile(t, repo, "old.txt", "hello\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "init")
	git(t, repo, "mv", "old.txt", "new.txt")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, repo), ""))

	f := fileByPath(t, resp.Repos[0], "new.txt")
	if f.Status != "R" || f.OldPath != "old.txt" {
		t.Errorf("rename = %+v", f)
	}
	// A pure rename has no content delta, and hunks must still marshal as an
	// array — `"hunks": null` would make every client null-check the field.
	if !strings.Contains(getDiff(t, srv, diffSession(t, repo), "").Body.String(), `"hunks":[]`) {
		t.Error("a hunk-less patch should serialise hunks as [], not null")
	}
}

// TestSessionDiffMultiRepo: a project's mounted source dirs are reviewed
// alongside the session's own directory, and non-repositories drop out.
func TestSessionDiffMultiRepo(t *testing.T) {
	isolatedHome(t)
	base := t.TempDir()

	own := filepath.Join(base, "own")
	gitRepo(t, own)
	writeFile(t, own, "a.txt", "a\n")

	mounted := filepath.Join(base, "mounted")
	gitRepo(t, mounted)
	writeFile(t, mounted, "b.txt", "b\n")

	plain := filepath.Join(base, "plain") // not a repository
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id := diffSession(t, own)
	// EnsureProjectForDir mounts the directory as a source dir and generates
	// the project's own workspace, which is not a repository either.
	for _, dir := range []string{mounted, plain} {
		if err := EnsureProjectForDir(dir, id); err != nil {
			t.Fatalf("EnsureProjectForDir(%s): %v", dir, err)
		}
	}

	resp := decodeDiff(t, getDiff(t, srv, id, ""))
	roots := map[string]bool{}
	for _, r := range resp.Repos {
		roots[r.Root] = true
	}
	if !roots[resolvedRoot(t, own)] || !roots[resolvedRoot(t, mounted)] {
		t.Fatalf("roots = %v, want both %s and %s", roots, own, mounted)
	}
	if roots[resolvedRoot(t, plain)] {
		t.Errorf("non-repository %s was returned", plain)
	}
}

// TestSessionDiffNoRepo: a session whose directory is not a repository gets an
// empty list, not an error — the panel tells the user there is nothing to
// review.
func TestSessionDiffNoRepo(t *testing.T) {
	isolatedHome(t)
	plain := t.TempDir()

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, plain), ""))
	if len(resp.Repos) != 0 {
		t.Errorf("repos = %+v, want empty", resp.Repos)
	}
}

// TestSessionDiffClean: a repository with no uncommitted changes is left out
// entirely, so the panel shows no group header for it.
func TestSessionDiffClean(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "app")
	gitRepo(t, repo)
	writeFile(t, repo, "a.txt", "a\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "init")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, repo), ""))
	if len(resp.Repos) != 0 {
		t.Errorf("repos = %+v, want empty for a clean tree", resp.Repos)
	}
}

// TestSessionDiffWorktree: a linked worktree carries .git as a file rather than
// a directory, and must still resolve and diff.
func TestSessionDiffWorktree(t *testing.T) {
	isolatedHome(t)
	base := t.TempDir()
	main := filepath.Join(base, "main")
	gitRepo(t, main)
	writeFile(t, main, "a.txt", "a\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-qm", "init")

	wt := filepath.Join(base, "wt")
	git(t, main, "worktree", "add", "-q", "-b", "side", wt)
	if fi, err := os.Stat(filepath.Join(wt, ".git")); err != nil || fi.IsDir() {
		t.Fatalf(".git in a linked worktree should be a file: %v", err)
	}
	writeFile(t, wt, "a.txt", "a\nb\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, wt), ""))
	if len(resp.Repos) != 1 || resp.Repos[0].Root != resolvedRoot(t, wt) {
		t.Fatalf("repos = %+v, want root %s", resp.Repos, wt)
	}
	if resp.Repos[0].Branch != "side" {
		t.Errorf("branch = %q, want side", resp.Repos[0].Branch)
	}
	if f := fileByPath(t, resp.Repos[0], "a.txt"); f.Adds != 1 {
		t.Errorf("a.txt = %+v", f)
	}
}

// TestSessionDiffNoCommits: with no HEAD to diff against, the empty tree stands
// in so staged content and later edits still show as one merged new-file view.
func TestSessionDiffNoCommits(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "fresh")
	gitRepo(t, repo)
	writeFile(t, repo, "staged.txt", "one\n")
	git(t, repo, "add", "staged.txt")
	writeFile(t, repo, "staged.txt", "one\ntwo\n") // edited after staging
	writeFile(t, repo, "loose.txt", "loose\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, repo), ""))
	if len(resp.Repos) != 1 {
		t.Fatalf("repos = %+v", resp.Repos)
	}
	r := resp.Repos[0]
	if r.Branch != "main" {
		t.Errorf("branch = %q, want main (no commits yet)", r.Branch)
	}

	staged := fileByPath(t, r, "staged.txt")
	if staged.Status != "A" || !staged.Staged {
		t.Errorf("staged.txt = %+v", staged)
	}
	// Merged view: both the staged line and the one added afterwards.
	if staged.Adds != 2 {
		t.Errorf("staged.txt adds = %d, want 2 (staged + unstaged merged)", staged.Adds)
	}
	if f := fileByPath(t, r, "loose.txt"); f.Status != "?" || f.Adds != 1 {
		t.Errorf("loose.txt = %+v", f)
	}
}

// TestSessionDiffTruncation: both caps fire — one huge file is clipped, and the
// files past the response budget keep their metadata and lose their patch.
func TestSessionDiffTruncation(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "big")
	gitRepo(t, repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	// Eleven untracked files of 2500 lines each: each one is clipped to 2000,
	// and the 20000-line response budget runs out inside the eleventh.
	var b strings.Builder
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	for i := 0; i < 11; i++ {
		writeFile(t, repo, fmt.Sprintf("f%02d.txt", i), b.String())
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, repo), ""))
	r := resp.Repos[0]
	if len(r.Files) != 11 {
		t.Fatalf("files = %d, want 11", len(r.Files))
	}

	first := r.Files[0]
	if !first.Truncated || first.Patch == nil {
		t.Fatalf("first file = %+v, want truncated with a patch", first)
	}
	if got := countPatchLines(first.Patch); got != diffFileMaxLines {
		t.Errorf("first file rendered %d lines, want %d", got, diffFileMaxLines)
	}
	if first.TotalLines != 2500 {
		t.Errorf("first file total_lines = %d, want 2500", first.TotalLines)
	}

	last := r.Files[10]
	if !last.Omitted || last.Patch != nil {
		t.Errorf("last file = %+v, want omitted with no patch", last)
	}
	if resp.TruncatedFiles != 10 || resp.OmittedFiles != 1 {
		t.Errorf("counts = %d truncated / %d omitted, want 10/1", resp.TruncatedFiles, resp.OmittedFiles)
	}
}

// TestSessionDiffBinaryUntracked: an untracked file with a NUL byte is flagged
// rather than rendered as control characters.
func TestSessionDiffBinaryUntracked(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "app")
	gitRepo(t, repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "blob.bin"), []byte{'a', 0x00, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, repo), ""))
	f := fileByPath(t, resp.Repos[0], "blob.bin")
	if !f.Binary || f.Patch != nil {
		t.Errorf("blob.bin = %+v, want binary with no patch", f)
	}
}

// TestSessionDiffUntrackedDirExpanded: an untracked directory must arrive as
// individual files — a bare `?? dir/` entry has no content to render.
func TestSessionDiffUntrackedDirExpanded(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "app")
	gitRepo(t, repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	writeFile(t, repo, "pkg/one.go", "package pkg\n")
	writeFile(t, repo, "pkg/two.go", "package pkg\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	resp := decodeDiff(t, getDiff(t, srv, diffSession(t, repo), ""))
	r := resp.Repos[0]
	if len(r.Files) != 2 {
		t.Fatalf("files = %v, want the two files, not the directory", repoPaths(r))
	}
	for _, f := range r.Files {
		if f.Patch == nil {
			t.Errorf("%s has no patch", f.Path)
		}
	}
}

// TestSessionDiffSummary: summary mode returns counts only, with no patch
// content, and skips clean repositories.
func TestSessionDiffSummary(t *testing.T) {
	isolatedHome(t)
	repo := filepath.Join(t.TempDir(), "app")
	gitRepo(t, repo)
	writeFile(t, repo, "a.txt", "a\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "init")
	writeFile(t, repo, "a.txt", "a\nb\n")
	writeFile(t, repo, "new.txt", "n\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	w := getDiff(t, srv, diffSession(t, repo), "summary=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}

	var resp diffSummaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repos) != 1 || len(resp.Repos[0].Files) != 2 {
		t.Fatalf("summary = %+v", resp.Repos)
	}
	// Patch content must be absent from the wire, not merely null.
	if strings.Contains(w.Body.String(), "\"patch\"") || strings.Contains(w.Body.String(), "\"hunks\"") {
		t.Errorf("summary body carries patch data: %s", w.Body.String())
	}
}

// TestSessionDiffSessionNotFound: an unknown session id is a 404 before any
// git runs.
func TestSessionDiffSessionNotFound(t *testing.T) {
	isolatedHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	w := getDiff(t, srv, "no-such-session", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func getFileDiff(t *testing.T, srv *Server, id, repo, path string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"repo": {repo}, "path": {path}}.Encode()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+id+"/diff/file?"+q, nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	return w
}

// TestSessionFileDiff: the single-file endpoint serves the whole file even when
// the aggregate response clipped it, and refuses anything outside the session's
// repositories or outside git's own list of changed files.
func TestSessionFileDiff(t *testing.T) {
	isolatedHome(t)
	base := t.TempDir()
	repo := filepath.Join(base, "app")
	gitRepo(t, repo)
	writeFile(t, repo, "tracked.txt", "seed\n")
	writeFile(t, repo, "untouched.txt", "same\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "init")

	var b strings.Builder
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	writeFile(t, repo, "tracked.txt", b.String())
	writeFile(t, repo, "fresh.txt", "new file\n")

	// A repository the session has nothing to do with.
	outside := filepath.Join(base, "outside")
	gitRepo(t, outside)
	writeFile(t, outside, "secret.txt", "secret\n")

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id := diffSession(t, repo)

	// The aggregate response clips it…
	agg := fileByPath(t, decodeDiff(t, getDiff(t, srv, id, "")).Repos[0], "tracked.txt")
	if !agg.Truncated {
		t.Fatalf("expected the aggregate response to truncate tracked.txt: %+v", agg)
	}

	// …and the single-file endpoint does not.
	w := getFileDiff(t, srv, id, resolvedRoot(t, repo), "tracked.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var got struct{ File diffFile }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.File.Truncated || got.File.Patch == nil {
		t.Fatalf("file = %+v, want the complete patch", got.File)
	}
	if lines := countPatchLines(got.File.Patch); lines <= diffFileMaxLines {
		t.Errorf("rendered %d lines, want more than the %d cap", lines, diffFileMaxLines)
	}

	// Untracked files work here too, still without staging them.
	if w := getFileDiff(t, srv, id, resolvedRoot(t, repo), "fresh.txt"); w.Code != http.StatusOK {
		t.Errorf("untracked file diff: status = %d; body=%s", w.Code, w.Body.String())
	}

	// A repository outside the session's scope is refused even though it is a
	// perfectly valid repository with changes in it.
	if w := getFileDiff(t, srv, id, resolvedRoot(t, outside), "secret.txt"); w.Code != http.StatusForbidden {
		t.Errorf("outside repo: status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	// An unchanged file in an allowed repository is not readable through here.
	if w := getFileDiff(t, srv, id, resolvedRoot(t, repo), "untouched.txt"); w.Code != http.StatusNotFound {
		t.Errorf("unchanged file: status = %d, want 404", w.Code)
	}

	// Traversal out of the repository fails the same way: git status never
	// reports such a path.
	if w := getFileDiff(t, srv, id, resolvedRoot(t, repo), "../outside/secret.txt"); w.Code != http.StatusNotFound {
		t.Errorf("traversal: status = %d, want 404", w.Code)
	}
}

// TestUntrackedSymlinkEscape: an untracked symlink pointing out of the
// repository must not be read through it.
func TestUntrackedSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(repo, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, _, _, err := synthUntrackedPatch(repo, "link.txt"); err == nil {
		t.Error("symlink out of the repository was read")
	}
	// Plain traversal, same answer.
	if _, _, _, err := synthUntrackedPatch(repo, "../secret.txt"); err == nil {
		t.Error("traversal out of the repository was read")
	}
}

// TestUntrackedReadCap: a file larger than the read cap comes back truncated
// rather than whole.
func TestUntrackedReadCap(t *testing.T) {
	repo := t.TempDir()
	big := strings.Repeat("0123456789abcdef", (untrackedMaxBytes/16)+64)
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	patch, binary, truncated, err := synthUntrackedPatch(repo, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if binary || !truncated || patch == nil {
		t.Fatalf("binary=%v truncated=%v patch=%v, want a truncated text patch", binary, truncated, patch)
	}
}
