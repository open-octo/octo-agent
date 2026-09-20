package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func seededIDs(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, ".octo", "themes"))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// TestSeedThemes_FirstRun: a fresh install gets the themes octo ships, each a
// complete directory the theme API can serve.
func TestSeedThemes_FirstRun(t *testing.T) {
	home := themeHome(t)
	if err := seedThemes(); err != nil {
		t.Fatal(err)
	}

	got := seededIDs(t, home)
	want := []string{"blossom", "ocean", "vogue"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("seeded %v, want %v", got, want)
	}

	for _, id := range want {
		dir := filepath.Join(home, ".octo", "themes", id)
		data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			t.Fatalf("%s: no manifest: %v", id, err)
		}
		var m themeManifest
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s: manifest is not JSON: %v", id, err)
		}
		if m.ID != id {
			t.Errorf("%s: manifest id is %q", id, m.ID)
		}
		// The picker needs a chip, and the Chinese UI needs the name these
		// themes had as i18n keys before they became files.
		if normalizeSwatch(m.Swatch) == nil {
			t.Errorf("%s: swatch missing or not a hex pair: %v", id, m.Swatch)
		}
		if m.Names["zh"] == "" {
			t.Errorf("%s: no Chinese name", id)
		}
		if _, err := os.Stat(filepath.Join(dir, "theme.css")); err != nil {
			t.Errorf("%s: no theme.css: %v", id, err)
		}
	}
}

// TestSeedThemes_ListsAfterSeeding: the seeded themes are ordinary user themes
// — nothing downstream tells them apart.
func TestSeedThemes_ListsAfterSeeding(t *testing.T) {
	themeHome(t)
	if err := seedThemes(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	var ids []string
	for _, th := range listThemes(t, srv) {
		ids = append(ids, th.ID)
	}
	sort.Strings(ids)
	if strings.Join(ids, ",") != "blossom,ocean,vogue" {
		t.Fatalf("listed %v", ids)
	}

	// And their stylesheets serve, along with the wallpapers they name.
	w := doJSON(t, srv, "GET", "/api/themes/blossom/theme.css", "")
	if w.Code != 200 {
		t.Fatalf("theme.css: expected 200, got %d", w.Code)
	}
	// The full path, not just the filename: a relative url() in a custom
	// property resolves against the stylesheet that *uses* it (ChatView's
	// bundle), so a theme that shortened this would silently lose its
	// wallpaper.
	if !strings.Contains(w.Body.String(), "url('/api/themes/blossom/wallpaper-light.webp')") {
		t.Error("blossom's stylesheet does not reference its wallpaper by absolute path")
	}
	w = doJSON(t, srv, "GET", "/api/themes/blossom/wallpaper-light.webp", "")
	if w.Code != 200 {
		t.Fatalf("wallpaper: expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/webp" {
		t.Errorf("wallpaper served as %q", ct)
	}
}

// TestSeedThemes_DeletedStaysDeleted is the reason the stamp file exists: a
// theme the user removed must not come back on the next start.
func TestSeedThemes_DeletedStaysDeleted(t *testing.T) {
	home := themeHome(t)
	if err := seedThemes(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".octo", "themes", "blossom")); err != nil {
		t.Fatal(err)
	}
	if err := seedThemes(); err != nil {
		t.Fatal(err)
	}

	got := seededIDs(t, home)
	if strings.Join(got, ",") != "ocean,vogue" {
		t.Fatalf("after deleting blossom and restarting, got %v", got)
	}
}

// TestSeedThemes_EditedIsNotOverwritten: the whole point of shipping these as
// files is that they can be edited.
func TestSeedThemes_EditedIsNotOverwritten(t *testing.T) {
	home := themeHome(t)
	if err := seedThemes(); err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(home, ".octo", "themes", "ocean", "theme.css")
	if err := os.WriteFile(css, []byte("/* mine now */"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := seedThemes(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(css)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "/* mine now */" {
		t.Error("a second start overwrote an edited theme")
	}
}

// TestSeedThemes_ExistingDirIsClaimedNotCopied: a directory already holding
// the id is left alone, and stamped, so the user's copy survives both this
// start and the next.
func TestSeedThemes_ExistingDirIsClaimedNotCopied(t *testing.T) {
	home := themeHome(t)
	dir := filepath.Join(home, ".octo", "themes", "ocean")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte("/* pre-existing */"), 0o644); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := seedThemes(); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, "theme.css"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "/* pre-existing */" {
		t.Error("seeding overwrote a theme directory that was already there")
	}
}
