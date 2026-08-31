package agentprofile

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain neutralizes the default-agents root for the whole package so tests
// never read the real ~/.octo/agents-default (which an installed binary
// populates). Tests that exercise defaults opt in via useDefaultAgentsRoot.
func TestMain(m *testing.M) {
	tmp, _ := os.MkdirTemp("", "octo-agents-default-empty")
	defaultAgentsRoot = func() string { return tmp }
	code := m.Run()
	if tmp != "" {
		_ = os.RemoveAll(tmp)
	}
	os.Exit(code)
}

// useDefaultAgentsRoot points defaultAgentsRoot at dir for the test's duration.
func useDefaultAgentsRoot(t *testing.T, dir string) {
	t.Helper()
	orig := defaultAgentsRoot
	defaultAgentsRoot = func() string { return dir }
	t.Cleanup(func() { defaultAgentsRoot = orig })
}

func TestMaterializeDefaults_WritesEmbeddedAndStamps(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents-default")
	useDefaultAgentsRoot(t, root)

	if err := MaterializeDefaults("v1"); err != nil {
		t.Fatalf("MaterializeDefaults: %v", err)
	}
	for _, id := range []string{"copywriter", "trip-planner", "legal-helper"} {
		if _, err := os.Stat(filepath.Join(root, id+".md")); err != nil {
			t.Fatalf("expected %s.md materialized: %v", id, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, defaultStampFile))
	if err != nil || string(b) != "v1" {
		t.Fatalf("stamp = %q, %v; want v1", string(b), err)
	}
}

func TestMaterializeDefaults_NoOpWhenCurrent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents-default")
	useDefaultAgentsRoot(t, root)
	if err := MaterializeDefaults("v1"); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeDefaults("v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("same-version call should be a no-op, but the dir was rewritten")
	}

	if err := MaterializeDefaults("v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("version bump should wipe-and-rewrite the default root")
	}
}

func TestStore_DefaultLayerSurfacesAndIsNotOverridable(t *testing.T) {
	defaultRoot := filepath.Join(t.TempDir(), "agents-default")
	useDefaultAgentsRoot(t, defaultRoot)
	writeMD(t, defaultRoot, "copywriter.md", "---\ndescription: curated copywriter\ntools: [read_file]\ncategory: content-creation\ntags: [writing]\n---\npersona body\n")

	s, userDir := newTestStore(t)

	// Default-only: shows up in List() with Source == SourceDefault, unlike
	// builtins which List() always excludes.
	got := s.List()
	if len(got) != 1 || got[0].ID != "copywriter" || got[0].Source != SourceDefault {
		t.Fatalf("List() = %+v, want one SourceDefault copywriter", got)
	}
	if got[0].Category != "content-creation" || len(got[0].Tags) != 1 {
		t.Fatalf("gallery metadata not threaded through: %+v", got[0])
	}

	// A user file of the same ID does NOT override the curated expert: an
	// official expert is identical on every machine and keeps receiving
	// content updates. A leftover file (older octo forked one on first edit)
	// is ignored, not obeyed.
	writeMD(t, userDir, "copywriter.md", "---\ndescription: my override\n---\nuser body\n")
	p, ok := s.Get("copywriter")
	if !ok || p.Source != SourceDefault || p.Description != "curated copywriter" {
		t.Fatalf("user file must not shadow a curated expert: %+v, %v", p, ok)
	}
	// The ignored file must not leak through the IM-routing path either.
	if _, found := s.userProfiles()["copywriter"]; found {
		t.Error("shadowed file still visible to IM routing")
	}
}

// A curated expert is read-only: Update refuses it rather than forking it
// into a user override, and Create refuses to take its id.
func TestStore_CuratedExpertIsReadOnly(t *testing.T) {
	defaultRoot := filepath.Join(t.TempDir(), "agents-default")
	useDefaultAgentsRoot(t, defaultRoot)
	writeMD(t, defaultRoot, "copywriter.md", "---\ndescription: curated copywriter\ntools: [read_file]\ncategory: content-creation\n---\npersona body\n")

	s, userDir := newTestStore(t)

	existing, ok := s.Get("copywriter")
	if !ok {
		t.Fatal("copywriter not found")
	}
	existing.Description = "edited copywriter"
	if err := s.Update(existing); err == nil {
		t.Error("Update() on a curated expert should be refused")
	}
	if _, err := os.Stat(filepath.Join(userDir, "copywriter.md")); !os.IsNotExist(err) {
		t.Errorf("refused Update must not write a user file: %v", err)
	}
	if err := s.Create(&Profile{ID: "copywriter", Description: "mine", CapabilitySpec: CapabilitySpec{SystemPrompt: "body"}}); err == nil {
		t.Error("Create() at a curated expert's id should be refused")
	}
	// Unchanged, still the shipped copy.
	p, ok := s.Get("copywriter")
	if !ok || p.Source != SourceDefault || p.Description != "curated copywriter" {
		t.Fatalf("curated expert changed: %+v, %v", p, ok)
	}
}

func TestStore_DeleteBlockedForCuratedDefault(t *testing.T) {
	defaultRoot := filepath.Join(t.TempDir(), "agents-default")
	useDefaultAgentsRoot(t, defaultRoot)
	writeMD(t, defaultRoot, "copywriter.md", "---\ndescription: curated copywriter\ntools: [read_file]\n---\npersona body\n")

	s, _ := newTestStore(t)
	if err := s.Delete("copywriter"); err == nil {
		t.Fatal("Delete of a curated default should fail")
	}
}

func TestStore_SetDisabledDefaults(t *testing.T) {
	defaultRoot := filepath.Join(t.TempDir(), "agents-default")
	useDefaultAgentsRoot(t, defaultRoot)
	writeMD(t, defaultRoot, "copywriter.md", "---\ndescription: curated copywriter\ntools: [read_file]\n---\npersona body\n")

	s, _ := newTestStore(t)
	if _, ok := s.Get("copywriter"); !ok {
		t.Fatal("copywriter should be visible before disabling")
	}

	s.SetDisabledDefaults([]string{"copywriter"})
	if _, ok := s.Get("copywriter"); ok {
		t.Fatal("disabled default should be hidden from Get")
	}
	found := false
	for _, p := range s.List() {
		if p.ID == "copywriter" {
			found = true
		}
	}
	if found {
		t.Fatal("disabled default should be hidden from List")
	}
	if _, ok := s.LookupAny("copywriter"); !ok {
		t.Fatal("LookupAny should still see a disabled default")
	}

	// All() is the management-surface view: unlike List(), it must still
	// include the hidden default (with IsEnabled reporting false) so a
	// caller (the gallery UI) can offer a way to re-show it.
	all := s.All()
	if len(all) != 1 || all[0].ID != "copywriter" {
		t.Fatalf("All() should still include the hidden default: %+v", all)
	}
	if s.IsEnabled(all[0]) {
		t.Fatal("IsEnabled should report false for a hidden default")
	}

	s.SetDisabledDefaults(nil)
	if _, ok := s.Get("copywriter"); !ok {
		t.Fatal("re-enabled default should be visible again")
	}
}
