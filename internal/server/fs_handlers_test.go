package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fsListResponse mirrors the handler's JSON shape for decoding in tests.
type fsListResponse struct {
	Path      string    `json:"path"`
	Parent    string    `json:"parent"`
	Entries   []fsEntry `json:"entries"`
	Truncated bool      `json:"truncated"`
	IsThisPC  bool      `json:"is_this_pc"`
}

func doFsList(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/fs/list"
	if path != "" {
		target += "?path=" + url.QueryEscape(path)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	(&Server{}).handleFsList(rec, req)
	return rec
}

func TestFsListHappyPath(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "beta"))
	mustMkdir(t, filepath.Join(root, "Alpha"))
	mustWrite(t, filepath.Join(root, "go.mod"))
	mustWrite(t, filepath.Join(root, ".hidden"))

	// A symlink pointing at a directory must report is_dir=true and is_symlink=true.
	linkOK := true
	if err := os.Symlink(filepath.Join(root, "beta"), filepath.Join(root, "link-to-beta")); err != nil {
		linkOK = false // some CI filesystems disallow symlinks; skip that assertion
	}

	rec := doFsList(t, root)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var resp fsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Path != root {
		t.Errorf("path: got %q, want %q", resp.Path, root)
	}
	if resp.Parent != filepath.Dir(root) {
		t.Errorf("parent: got %q, want %q", resp.Parent, filepath.Dir(root))
	}
	if resp.Truncated {
		t.Errorf("truncated: got true, want false")
	}

	byName := map[string]fsEntry{}
	for _, e := range resp.Entries {
		byName[e.Name] = e
	}
	if e, ok := byName["Alpha"]; !ok || !e.IsDir {
		t.Errorf("Alpha: got %+v, want dir", e)
	}
	if e, ok := byName["go.mod"]; !ok || e.IsDir {
		t.Errorf("go.mod: got %+v, want file", e)
	}
	if e, ok := byName[".hidden"]; !ok || e.IsDir {
		t.Errorf(".hidden should be listed as a file, got %+v (ok=%v)", e, ok)
	}
	if linkOK {
		if e, ok := byName["link-to-beta"]; !ok || !e.IsDir || !e.IsSymlink {
			t.Errorf("link-to-beta: got %+v, want dir+symlink", e)
		}
	}

	// Directories sort ahead of files, case-insensitive within each group:
	// Alpha, beta, [link-to-beta], .hidden, go.mod.
	if len(resp.Entries) < 2 || !resp.Entries[0].IsDir {
		t.Fatalf("expected directories first, got %+v", resp.Entries)
	}
	firstFileSeen := false
	for _, e := range resp.Entries {
		if !e.IsDir {
			firstFileSeen = true
		} else if firstFileSeen {
			t.Fatalf("a directory appeared after a file: %+v", resp.Entries)
		}
	}
}

func TestFsListErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a-file")
	mustWrite(t, file)

	if rec := doFsList(t, filepath.Join(root, "nope")); rec.Code != http.StatusBadRequest {
		t.Errorf("missing path: got %d, want 400", rec.Code)
	}
	if rec := doFsList(t, file); rec.Code != http.StatusBadRequest {
		t.Errorf("file path: got %d, want 400", rec.Code)
	}
}

func TestFsListTruncates(t *testing.T) {
	root := t.TempDir()
	// A subdirectory whose name ("zzz-dir") sorts after every file by raw
	// readdir byte order — it must still survive truncation because
	// directories are what a folder picker exists to show.
	mustMkdir(t, filepath.Join(root, "zzz-dir"))
	for i := 0; i < fsListCap+50; i++ {
		mustWrite(t, filepath.Join(root, "f"+strconv.Itoa(i)))
	}
	rec := doFsList(t, root)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp fsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Truncated {
		t.Errorf("truncated: got false, want true")
	}
	if len(resp.Entries) != fsListCap {
		t.Errorf("entries: got %d, want %d", len(resp.Entries), fsListCap)
	}
	// The directory sorts first, so it's the very first entry and never dropped.
	if !resp.Entries[0].IsDir || resp.Entries[0].Name != "zzz-dir" {
		t.Errorf("directory dropped by truncation: first entry is %+v, want zzz-dir", resp.Entries[0])
	}
}

func TestFsListDefaultsToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	rec := doFsList(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var resp fsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Path != home {
		t.Errorf("default path: got %q, want home %q", resp.Path, home)
	}
}

// currentDrive returns the volume of the test process's cwd (e.g. "C:") —
// always a mounted, listable drive, unlike a hardcoded letter that may not
// exist on a given CI runner.
func currentDrive(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	vol := filepath.VolumeName(wd)
	if vol == "" {
		t.Fatal("no volume name for cwd")
	}
	return vol
}

func TestFsListWindowsThisPCListsDrives(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: drive enumeration")
	}
	rec := doFsList(t, fsThisPCPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var resp fsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.IsThisPC {
		t.Error("is_this_pc: got false, want true")
	}
	if resp.Parent != "" {
		t.Errorf("parent: got %q, want empty (top of the Windows hierarchy)", resp.Parent)
	}
	want := currentDrive(t) // e.g. "C:"
	found := false
	for _, e := range resp.Entries {
		if e.Name == want {
			found = true
			if !e.IsDir {
				t.Errorf("%s: got IsDir=false, want true", want)
			}
		}
	}
	if !found {
		t.Errorf("drive list %+v missing the test process's own drive %q", resp.Entries, want)
	}
}

func TestFsListWindowsDriveRootParentIsThisPC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: drive roots")
	}
	root := currentDrive(t) + `\`
	rec := doFsList(t, root)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var resp fsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Parent != fsThisPCPath {
		t.Errorf("parent of drive root %q: got %q, want %q", root, resp.Parent, fsThisPCPath)
	}
}

func TestFsListWindowsDriveSelect(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: drive selection")
	}
	letter := strings.TrimSuffix(currentDrive(t), ":")
	rec := doFsList(t, fsThisPCPath+"/"+letter+":")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var resp fsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := letter + `:\`
	if resp.Path != want {
		t.Errorf("path: got %q, want %q", resp.Path, want)
	}
	if resp.IsThisPC {
		t.Error("is_this_pc: got true, want false (this is a real directory listing)")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
