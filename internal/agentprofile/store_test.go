package agentprofile

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestStore builds a Store over two temp dirs (user + project).
func newTestStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	userDir := t.TempDir()
	projectDir := t.TempDir()
	return New(userDir, func() string { return projectDir }), userDir, projectDir
}

func TestStoreBuiltinFallback(t *testing.T) {
	s, _, _ := newTestStore(t)
	for _, id := range []string{"explore", "general", "code-review"} {
		p, ok := s.Get(id)
		if !ok || p.Source != SourceBuiltin {
			t.Fatalf("Get(%q) = %v, %v", id, p, ok)
		}
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List() with no user files = %d profiles, want 0 (builtins excluded)", len(got))
	}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
}

func TestStorePrecedence(t *testing.T) {
	s, userDir, projectDir := newTestStore(t)
	// User file shadows the builtin "explore".
	writeMD(t, userDir, "explore.md", "---\ndescription: user explore\n---\nuser persona\n")
	// Project file shadows the user file.
	writeMD(t, projectDir, "explore.md", "---\ndescription: project explore\n---\nproject persona\n")

	p, ok := s.Get("explore")
	if !ok {
		t.Fatal("Get(explore) not found")
	}
	if p.Source != SourceProject || p.SystemPrompt != "project persona" {
		t.Fatalf("precedence project > user > builtin broken: %+v", p)
	}
}

func TestStoreProjectStripsPlatformSlice(t *testing.T) {
	s, _, projectDir := newTestStore(t)
	writeMD(t, projectDir, "ops.md", "---\ndescription: d\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n---\nbody\n")

	if p, _ := s.Get("ops"); p == nil || len(p.ChannelBindings) != 0 {
		t.Fatalf("project profile kept platform slice: %+v", p)
	}
	if got := s.ByChannel("weixin", "", "g1"); len(got) != 0 {
		t.Fatal("project-level binding must not be routable")
	}
}

func TestStoreSkipsBrokenFilesAndReservedID(t *testing.T) {
	s, userDir, _ := newTestStore(t)
	writeMD(t, userDir, "broken.md", "no frontmatter here\n")
	writeMD(t, userDir, "default.md", "---\ndescription: impostor\n---\nbody\n") // reserved ID: skipped
	writeMD(t, userDir, "good.md", "---\ndescription: d\n---\nbody\n")
	writeMD(t, userDir, "notes.txt", "---\ndescription: d\n---\nbody\n") // not .md

	got := s.List()
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("List() = %+v, want only [good]", got)
	}
	// The default profile is code-defined, never the impostor file.
	if p, _ := s.Get(DefaultID); p.Description == "impostor" {
		t.Fatal("default.md shadowed the code-defined default agent")
	}
}

// Delegation lookups (sub_agent) and the profile API keep the zero-migration
// lenient loader: any .md basename is a valid profile ID on the read path,
// even names that fail the write-side slug rule. The write path still
// enforces slug via validateForWrite.
func TestStore_DelegationAcceptsNonSlugFilenames(t *testing.T) {
	s, userDir, _ := newTestStore(t)
	writeMD(t, userDir, "Code_Review.md", "---\ndescription: legacy\n---\nbody\n")

	p, ok := s.Get("Code_Review")
	if !ok || p.Description != "legacy" {
		t.Fatalf("non-slug legacy file not readable via delegation: %v, %v", p, ok)
	}
	if got := s.List(); len(got) != 1 || got[0].ID != "Code_Review" {
		t.Fatalf("List() = %+v", got)
	}
}

// IM routing only honors slug-shaped profile IDs: a hand-placed `a#b.md`
// must never become a routable profile (its '#' would defeat the
// session-key namespace splitter). userProfiles() — the ByChannel/ByMention
// source — skips non-slug files, even though Get() (delegation) still reads
// them.
func TestStore_UserProfilesSkipsNonSlug(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a#b.md"), []byte("---\ndescription: d\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "valid.md"), []byte("---\ndescription: d\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(dir, nil)
	// userProfiles() (IM-routing source) skips non-slug files.
	if p := s.ByChannel("weixin", "", "g1"); len(p) > 0 {
		t.Fatalf("non-slug profile is routable via IM: %+v", p)
	}
	// But Get()/List() (delegation + API) still see it.
	if _, ok := s.Get("a#b"); !ok {
		t.Fatal("non-slug profile not visible to delegation Get")
	}
	if _, ok := s.Get("valid"); !ok {
		t.Fatal("slug profile not found")
	}
}

func TestStoreReadThrough(t *testing.T) {
	s, userDir, _ := newTestStore(t)
	writeMD(t, userDir, "a.md", "---\ndescription: v1\n---\nbody v1\n")

	p, _ := s.Get("a")
	if p.Description != "v1" {
		t.Fatalf("first read = %q", p.Description)
	}
	// Direct .md edit takes effect on the very next read — no reload, no restart.
	writeMD(t, userDir, "a.md", "---\ndescription: v2\n---\nbody v2\n")
	p, _ = s.Get("a")
	if p.Description != "v2" || p.SystemPrompt != "body v2" {
		t.Fatalf("read-through failed: %+v", p)
	}
	// Deletion too.
	if err := os.Remove(filepath.Join(userDir, "a.md")); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("deleted profile still visible")
	}
}

func TestStoreCreateUpdateDelete(t *testing.T) {
	s, userDir, _ := newTestStore(t)

	p := &Profile{ID: "reviewer", Name: "Reviewer", Description: "d",
		CapabilitySpec: CapabilitySpec{SystemPrompt: "prompt", Tools: []string{"read_file"}}}
	if err := s.Create(p); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := os.Stat(filepath.Join(userDir, "reviewer.md")); err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if err := s.Create(p); err == nil {
		t.Fatal("duplicate Create should fail")
	}

	p.Description = "d2"
	if err := s.Update(p); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	got, _ := s.Get("reviewer")
	if got.Description != "d2" || got.SystemPrompt != "prompt" {
		t.Fatalf("after Update: %+v", got)
	}

	if err := s.Update(&Profile{ID: "ghost", Description: "d"}); err == nil {
		t.Fatal("Update of non-existent user profile should fail")
	}
	if err := s.Delete("explore"); err == nil {
		t.Fatal("Delete of builtin should fail")
	}
	if err := s.Delete("ghost"); err == nil {
		t.Fatal("Delete of unknown profile should fail")
	}

	// Bound profile must be unbound before deletion.
	p.ChannelBindings = []ChannelBinding{{Platform: "weixin", ChatID: "g1"}}
	if err := s.Update(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("reviewer"); err == nil {
		t.Fatal("Delete with channel bindings should fail")
	}
	p.ChannelBindings = nil
	if err := s.Update(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("reviewer"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, ok := s.Get("reviewer"); ok {
		t.Fatal("profile still visible after Delete")
	}
}

func TestStoreByChannelByMention(t *testing.T) {
	s, userDir, _ := newTestStore(t)
	writeMD(t, userDir, "a.md", "---\ndescription: da\nmention_as: [\"@review\"]\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n---\nbody\n")
	writeMD(t, userDir, "b.md", "---\ndescription: db\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n  - {platform: feishu, chat_id: g2}\n---\nbody\n")

	if got := s.ByChannel("weixin", "", "g1"); len(got) != 2 {
		t.Fatalf("ByChannel(weixin,g1) = %d, want 2", len(got))
	}
	if got := s.ByChannel("feishu", "", "g2"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("ByChannel(feishu,g2) = %+v", got)
	}
	if got := s.ByChannel("weixin", "", "unknown"); len(got) != 0 {
		t.Fatalf("ByChannel unknown = %+v", got)
	}
}

// A project-level file shadowing a user profile's ID overrides delegation
// (Get) but must never silence the user profile's IM routing.
func TestStoreProjectShadowKeepsUserRouting(t *testing.T) {
	s, userDir, projectDir := newTestStore(t)
	writeMD(t, userDir, "reviewer.md", "---\ndescription: user\nmention_as: [\"@review\"]\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n---\nuser body\n")
	writeMD(t, projectDir, "reviewer.md", "---\ndescription: project\n---\nproject body\n")

	if p, _ := s.Get("reviewer"); p.Source != SourceProject {
		t.Fatalf("delegation precedence broken: %+v", p)
	}
	if got := s.ByChannel("weixin", "", "g1"); len(got) != 1 || got[0].ID != "reviewer" {
		t.Fatalf("project shadow silenced user routing: %+v", got)
	}
}

func TestStoreDefaultReserved(t *testing.T) {
	s, userDir, _ := newTestStore(t)

	if p, ok := s.Get(DefaultID); !ok || !p.IsDefault() {
		t.Fatalf("Get(default) = %v, %v — the default profile must always resolve", p, ok)
	}
	if err := s.Create(&Profile{ID: DefaultID, Description: "impostor"}); err == nil {
		t.Fatal("Create with reserved id 'default' should fail")
	}
	if _, err := os.Stat(filepath.Join(userDir, "default.md")); !os.IsNotExist(err) {
		t.Fatal("impostor default.md was written")
	}
}
