package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeMCPFile(t *testing.T, dir, content string) {
	t.Helper()
	octoDir := filepath.Join(dir, ".octo")
	if err := os.MkdirAll(octoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(octoDir, "mcp.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setupManageHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	return tmp
}

func TestLoadManaged_MergesAndAnnotates(t *testing.T) {
	home := setupManageHome(t)
	writeMCPFile(t, home, `{"mcpServers": {
		"user-only": {"command": "echo"},
		"shadowed":  {"command": "user-version"},
		"off":       {"url": "https://x.example", "disabled": true},
		"broken":    {}
	}}`)
	proj := t.TempDir()
	writeMCPFile(t, proj, `{"mcpServers": {
		"shadowed":   {"url": "https://proj.example"},
		"proj-only":  {"command": "ls"}
	}}`)

	managed, err := LoadManaged(proj)
	if err != nil {
		t.Fatalf("LoadManaged: %v", err)
	}

	byName := map[string]ManagedServer{}
	for _, m := range managed {
		byName[m.Name] = m
	}
	if len(managed) != 5 {
		t.Fatalf("got %d entries, want 5: %+v", len(managed), managed)
	}
	if byName["user-only"].Source != "user" {
		t.Errorf("user-only source = %q, want user", byName["user-only"].Source)
	}
	if byName["proj-only"].Source != "project" {
		t.Errorf("proj-only source = %q, want project", byName["proj-only"].Source)
	}
	if got := byName["shadowed"]; got.Source != "project" || got.Entry.URL != "https://proj.example" {
		t.Errorf("shadowed should be the project version, got %+v", got)
	}
	if !byName["off"].Entry.Disabled {
		t.Error("disabled entry must stay visible in the managed view")
	}
	if byName["broken"].Invalid == "" {
		t.Error("invalid entry must carry a validation message, not be dropped")
	}
	// Sorted by name.
	for i := 1; i < len(managed); i++ {
		if managed[i-1].Name > managed[i].Name {
			t.Errorf("not sorted: %q before %q", managed[i-1].Name, managed[i].Name)
		}
	}
}

// When `octo serve` is launched from the home directory itself, the
// project-config path (projectDir/.octo/mcp.json) resolves to the exact same
// file as the user config. Every entry must stay labeled "user", not get
// silently relabeled "project" (which would make normal user config
// read-only in the management UI) by re-reading the same file a second time.
func TestLoadManaged_ProjectDirIsHome(t *testing.T) {
	home := setupManageHome(t)
	writeMCPFile(t, home, `{"mcpServers": {
		"codegraph": {"command": "codegraph"}
	}}`)

	managed, err := LoadManaged(home)
	if err != nil {
		t.Fatalf("LoadManaged: %v", err)
	}
	if len(managed) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(managed), managed)
	}
	if managed[0].Source != "user" {
		t.Errorf("source = %q, want user (cwd == home must not double-count as project)", managed[0].Source)
	}
}

// Same collision as TestLoadManaged_ProjectDirIsHome, but $HOME is reached
// through a symlink (e.g. a network home mount) while projectDir is passed as
// the resolved real path — the raw string comparison this guards against
// would see two different strings for the same directory.
func TestLoadManaged_ProjectDirIsHome_ThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on windows")
	}
	real := t.TempDir()
	root := t.TempDir()
	link := filepath.Join(root, "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	t.Setenv("HOME", link)
	t.Setenv("USERPROFILE", link)
	writeMCPFile(t, link, `{"mcpServers": {
		"codegraph": {"command": "codegraph"}
	}}`)

	managed, err := LoadManaged(real)
	if err != nil {
		t.Fatalf("LoadManaged: %v", err)
	}
	if len(managed) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(managed), managed)
	}
	if managed[0].Source != "user" {
		t.Errorf("source = %q, want user (symlinked home == cwd must not double-count as project)", managed[0].Source)
	}
}

func TestLoadManaged_NoFiles(t *testing.T) {
	setupManageHome(t)
	managed, err := LoadManaged(t.TempDir())
	if err != nil {
		t.Fatalf("LoadManaged: %v", err)
	}
	if len(managed) != 0 {
		t.Fatalf("expected empty, got %+v", managed)
	}
}

func TestUpsertUserServer_CreatesAndPreserves(t *testing.T) {
	home := setupManageHome(t)

	// First write creates ~/.octo/mcp.json from nothing.
	if err := UpsertUserServer("alpha", ServerEntry{Command: "echo"}); err != nil {
		t.Fatalf("UpsertUserServer: %v", err)
	}
	// Second write must preserve the first entry.
	if err := UpsertUserServer("beta", ServerEntry{URL: "https://b.example"}); err != nil {
		t.Fatalf("UpsertUserServer: %v", err)
	}
	// Update in place.
	if err := UpsertUserServer("alpha", ServerEntry{Command: "ls", Args: []string{"-la"}}); err != nil {
		t.Fatalf("UpsertUserServer update: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".octo", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(cfg.Servers))
	}
	if cfg.Servers["alpha"].Command != "ls" || len(cfg.Servers["alpha"].Args) != 1 {
		t.Errorf("alpha not updated: %+v", cfg.Servers["alpha"])
	}
	if cfg.Servers["beta"].URL != "https://b.example" {
		t.Errorf("beta lost on second write: %+v", cfg.Servers["beta"])
	}
}

func TestUpsertUserServer_RejectsInvalid(t *testing.T) {
	setupManageHome(t)
	cases := []struct {
		name  string
		entry ServerEntry
		want  string
	}{
		{"both", ServerEntry{Command: "echo", URL: "https://x"}, "cannot set both"},
		{"neither", ServerEntry{}, "must set either"},
		{"bad__name", ServerEntry{Command: "echo"}, "__"},
		{"has space", ServerEntry{Command: "echo"}, "whitespace"},
		{"", ServerEntry{Command: "echo"}, "empty"},
	}
	for _, c := range cases {
		err := UpsertUserServer(c.name, c.entry)
		if err == nil {
			t.Errorf("name=%q: expected error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("name=%q: error %q should mention %q", c.name, err, c.want)
		}
	}
	// Nothing should have been written.
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".octo", "mcp.json")); !os.IsNotExist(err) {
		t.Error("rejected upserts must not create the config file")
	}
}

func TestDeleteUserServer(t *testing.T) {
	home := setupManageHome(t)
	writeMCPFile(t, home, `{"mcpServers": {"a": {"command": "echo"}, "b": {"command": "ls"}}}`)

	if err := DeleteUserServer("a"); err != nil {
		t.Fatalf("DeleteUserServer: %v", err)
	}
	if err := DeleteUserServer("missing"); err == nil {
		t.Error("deleting an unknown name must error")
	}

	managed, err := LoadManaged("")
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 || managed[0].Name != "b" {
		t.Fatalf("expected only b to remain, got %+v", managed)
	}
}

func TestSetUserServerDisabled(t *testing.T) {
	home := setupManageHome(t)
	writeMCPFile(t, home, `{"mcpServers": {"a": {"command": "echo"}}}`)

	if err := SetUserServerDisabled("a", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	managed, _ := LoadManaged("")
	if !managed[0].Entry.Disabled {
		t.Error("entry should be disabled")
	}

	if err := SetUserServerDisabled("a", false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	managed, _ = LoadManaged("")
	if managed[0].Entry.Disabled {
		t.Error("entry should be enabled again")
	}

	if err := SetUserServerDisabled("missing", true); err == nil {
		t.Error("toggling an unknown name must error")
	}
}
