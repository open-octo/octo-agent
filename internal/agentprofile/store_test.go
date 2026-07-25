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
	writeMD(t, projectDir, "ops.md", "---\ndescription: d\nmention_as: [\"@ops\"]\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n---\nbody\n")

	if _, ok := s.ByMention("@ops"); ok {
		t.Fatal("project-level alias must not be routable")
	}
	if got := s.ByChannel("weixin", "g1"); len(got) != 0 {
		t.Fatal("project-level binding must not be routable")
	}
	p, _ := s.Get("ops")
	if len(p.MentionAs) != 0 || len(p.ChannelBindings) != 0 {
		t.Fatalf("project profile kept platform slice: %+v", p)
	}
}

func TestStoreSkipsBrokenAndNonSlugFiles(t *testing.T) {
	s, userDir, _ := newTestStore(t)
	writeMD(t, userDir, "broken.md", "no frontmatter here\n")
	writeMD(t, userDir, "Bad_Name.md", "---\ndescription: d\n---\nbody\n")
	writeMD(t, userDir, "good.md", "---\ndescription: d\n---\nbody\n")
	writeMD(t, userDir, "notes.txt", "---\ndescription: d\n---\nbody\n") // not .md

	got := s.List()
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("List() = %+v, want only [good]", got)
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

	if got := s.ByChannel("weixin", "g1"); len(got) != 2 {
		t.Fatalf("ByChannel(weixin,g1) = %d, want 2", len(got))
	}
	if got := s.ByChannel("feishu", "g2"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("ByChannel(feishu,g2) = %+v", got)
	}
	if got := s.ByChannel("weixin", "unknown"); len(got) != 0 {
		t.Fatalf("ByChannel unknown = %+v", got)
	}
	p, ok := s.ByMention("@review")
	if !ok || p.ID != "a" {
		t.Fatalf("ByMention(@review) = %v, %v", p, ok)
	}
	if _, ok := s.ByMention("@nobody"); ok {
		t.Fatal("ByMention(@nobody) should miss")
	}
}
