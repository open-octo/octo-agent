package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedTheme writes a theme directory. A blank css argument omits theme.css, so
// a test can cover the half-written directory case.
func seedTheme(t *testing.T, home, id, manifest, css string) {
	t.Helper()
	dir := filepath.Join(home, ".octo", "themes", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if css != "" {
		if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte(css), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func listThemes(t *testing.T, srv *Server) []themeManifest {
	t.Helper()
	w := doJSON(t, srv, "GET", "/api/themes", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out struct {
		Themes []themeManifest
		Dir    string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Themes
}

func themeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// TestListThemes_EmptyDir: no themes directory at all still answers 200 with an
// empty array — the picker shows the built-in packs and nothing breaks.
func TestListThemes_EmptyDir(t *testing.T) {
	home := themeHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "GET", "/api/themes", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out struct {
		Themes []themeManifest
		Dir    string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Themes) != 0 {
		t.Errorf("expected 0 themes, got %d", len(out.Themes))
	}
	if want := filepath.Join(home, ".octo", "themes"); out.Dir != want {
		t.Errorf("expected dir %q, got %q", want, out.Dir)
	}
}

// TestListThemes_RoundTrip: a well-formed theme lists with its fields intact.
func TestListThemes_RoundTrip(t *testing.T) {
	home := themeHome(t)
	seedTheme(t, home, "ocean",
		`{"id":"ocean","name":"Ocean","author":"someone","homepage":"https://example.com/t"}`,
		`:root[data-theme-pack="ocean"]{--text:#0F272E}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	themes := listThemes(t, srv)
	if len(themes) != 1 {
		t.Fatalf("expected 1 theme, got %d", len(themes))
	}
	got := themes[0]
	if got.ID != "ocean" || got.Name != "Ocean" || got.Author != "someone" {
		t.Errorf("unexpected manifest: %+v", got)
	}
	if got.Homepage != "https://example.com/t" {
		t.Errorf("homepage not carried: %q", got.Homepage)
	}
}

// TestListThemes_NameDefaultsToID: a manifest without a name still lists, so a
// minimal hand-written theme works.
func TestListThemes_NameDefaultsToID(t *testing.T) {
	home := themeHome(t)
	seedTheme(t, home, "ocean", `{"id":"ocean"}`, `/* css */`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	themes := listThemes(t, srv)
	if len(themes) != 1 || themes[0].Name != "ocean" {
		t.Fatalf("expected name to default to id, got %+v", themes)
	}
}

// TestListThemes_SkipsUnusable covers every directory shape the picker must not
// offer: each would list a theme that cannot apply.
func TestListThemes_SkipsUnusable(t *testing.T) {
	home := themeHome(t)
	// No theme.css — half written.
	seedTheme(t, home, "nocss", `{"id":"nocss"}`, "")
	// No manifest.
	seedTheme(t, home, "nomanifest", "", `/* css */`)
	// Manifest is not JSON.
	seedTheme(t, home, "broken", `{not json`, `/* css */`)
	// Manifest id disagrees with the directory, which is the id the CSS
	// selector has to match.
	seedTheme(t, home, "mismatch", `{"id":"something-else"}`, `/* css */`)
	// Shadows a built-in pack.
	seedTheme(t, home, "azure", `{"id":"azure"}`, `/* css */`)
	// Not a valid id: uppercase cannot round-trip through the host's
	// lowercasing, and the selector would never match.
	seedTheme(t, home, "Ocean", `{"id":"Ocean"}`, `/* css */`)
	// One good theme, to prove the scan does not just fail wholesale.
	seedTheme(t, home, "good", `{"id":"good","name":"Good"}`, `/* css */`)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	themes := listThemes(t, srv)
	if len(themes) != 1 {
		t.Fatalf("expected only the good theme, got %+v", themes)
	}
	if themes[0].ID != "good" {
		t.Errorf("expected id %q, got %q", "good", themes[0].ID)
	}
}

// TestGetThemeCSS_Serves: the stylesheet comes back as CSS, not as a download
// or a sniffable type.
func TestGetThemeCSS_Serves(t *testing.T) {
	home := themeHome(t)
	css := `:root[data-theme-pack="ocean"]{--text:#0F272E}`
	seedTheme(t, home, "ocean", `{"id":"ocean"}`, css)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "GET", "/api/themes/ocean/theme.css", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != css {
		t.Errorf("body mismatch: %q", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("expected text/css, got %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected nosniff")
	}
}

// TestGetThemeCSS_Rejects: ids that are missing, traversing or shadowing a
// built-in pack all answer 404 — a probe learns nothing about which it was.
func TestGetThemeCSS_Rejects(t *testing.T) {
	home := themeHome(t)
	seedTheme(t, home, "ocean", `{"id":"ocean"}`, `/* css */`)
	// A file the traversal attempt would reach if the id were used as a path.
	if err := os.WriteFile(filepath.Join(home, ".octo", "secret.css"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, path := range []string{
		"/api/themes/missing/theme.css",
		"/api/themes/azure/theme.css",
		"/api/themes/..%2f..%2fsecret/theme.css",
		"/api/themes/Ocean/theme.css",
	} {
		w := doJSON(t, srv, "GET", path, "")
		if w.Code == 200 {
			t.Errorf("%s: expected non-200, got 200 with body %q", path, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Errorf("%s: served a file outside the themes dir", path)
		}
	}
}

func TestValidThemeID(t *testing.T) {
	ok := []string{"ocean", "deep-ocean", "x", "theme2"}
	bad := []string{"", "Ocean", "deep_ocean", "../etc", "a/b", "théme", strings.Repeat("a", 65)}
	for _, id := range ok {
		if !validThemeID(id) {
			t.Errorf("expected %q to be valid", id)
		}
	}
	for _, id := range bad {
		if validThemeID(id) {
			t.Errorf("expected %q to be rejected", id)
		}
	}
}

// TestListThemes_SwatchValidation: a swatch reaches the page as a CSS custom
// property, so only a hex pair survives; anything else is dropped while the
// theme itself still lists.
func TestListThemes_SwatchValidation(t *testing.T) {
	home := themeHome(t)
	seedTheme(t, home, "good", `{"id":"good","swatch":["#0E7490","#F0F7F9"]}`, `/* css */`)
	seedTheme(t, home, "short", `{"id":"short","swatch":["#ABC","#DEF"]}`, `/* css */`)
	seedTheme(t, home, "inject", `{"id":"inject","swatch":["red;background:url(http://x)","#FFF"]}`, `/* css */`)
	seedTheme(t, home, "named", `{"id":"named","swatch":["red","white"]}`, `/* css */`)
	seedTheme(t, home, "onlyone", `{"id":"onlyone","swatch":["#000000"]}`, `/* css */`)
	seedTheme(t, home, "none", `{"id":"none"}`, `/* css */`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := map[string][]string{}
	for _, th := range listThemes(t, srv) {
		got[th.ID] = th.Swatch
	}
	if len(got) != 6 {
		t.Fatalf("expected all 6 themes to list, got %d: %v", len(got), got)
	}
	if len(got["good"]) != 2 || got["good"][0] != "#0E7490" {
		t.Errorf("valid swatch not carried: %v", got["good"])
	}
	if len(got["short"]) != 2 {
		t.Errorf("3-digit hex should be accepted: %v", got["short"])
	}
	for _, id := range []string{"inject", "named", "onlyone", "none"} {
		if got[id] != nil {
			t.Errorf("%s: expected swatch to be dropped, got %v", id, got[id])
		}
	}
}

func TestValidSwatchColor(t *testing.T) {
	ok := []string{"#fff", "#FFF", "#ffffff", "#FFFFFFFF", "#0E7490", "#abcd"}
	bad := []string{"", "#", "fff", "#ff", "#fffff", "red", "#ggg", "#fff;x", "rgb(0,0,0)", "var(--x)"}
	for _, c := range ok {
		if !validSwatchColor(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}
	for _, c := range bad {
		if validSwatchColor(c) {
			t.Errorf("expected %q to be rejected", c)
		}
	}
}
