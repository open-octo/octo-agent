package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// ─── User theme packs ───────────────────────────────────────────────────────
//
// A theme is a directory under ~/.octo/themes/ holding a manifest and one
// stylesheet that redefines the palette variables of web/src/app.css:
//
//	~/.octo/themes/<id>/
//	├── manifest.json
//	└── theme.css
//
// The web UI lists them (GET /api/themes) to populate the theme picker and
// links each stylesheet by URL (GET /api/themes/{id}/theme.css). Nothing is
// executed: a theme is CSS, and the selector it must write
// (`:root[data-theme-pack="<id>"]`) scopes it to the pack the user picked.

// themesDir returns the absolute path to the user's themes directory.
func themesDir() string {
	dir, err := datahome.Path("themes")
	if err != nil {
		return filepath.Join(".", ".octo", "themes")
	}
	return dir
}

// themeManifest mirrors a theme's manifest.json.
type themeManifest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Author   string `json:"author,omitempty"`
	Homepage string `json:"homepage,omitempty"`
	// Swatch is the accent/surface pair the picker draws so a theme can be
	// told apart without applying it. Optional — the picker falls back to a
	// neutral chip — and validated here rather than in the UI, because it
	// reaches the page as a CSS custom property: see validSwatch.
	Swatch []string `json:"swatch,omitempty"`
}

// builtinPackIDs are the packs compiled into the web UI (web/src/lib/theme.ts,
// PACKS). A user directory reusing one of these ids is skipped rather than
// served: both would write the same `[data-theme-pack=…]` selector, and which
// one won would come down to stylesheet order.
var builtinPackIDs = map[string]bool{
	"azure":    true,
	"blossom":  true,
	"celestia": true,
	"vogue":    true,
}

// validThemeID accepts the ids that are safe as both a path element and a CSS
// attribute value: lowercase letters, digits and hyphens.
func validThemeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// validSwatchColor accepts only a hex literal. The picker interpolates these
// into a style attribute as custom properties, where a value carrying `;` or
// `}` would end the declaration and let a manifest write arbitrary CSS into the
// host page. Restricting the grammar to `#` + hex removes that entirely, and
// costs a theme author nothing — the swatch is two colours.
func validSwatchColor(c string) bool {
	if len(c) != 4 && len(c) != 5 && len(c) != 7 && len(c) != 9 {
		return false
	}
	if c[0] != '#' {
		return false
	}
	for _, r := range c[1:] {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' {
			continue
		}
		return false
	}
	return true
}

// normalizeSwatch keeps a swatch only when it is exactly the accent/surface
// pair the picker draws and both halves are hex. Anything else is dropped, and
// the theme still lists — a bad swatch is a cosmetic mistake, not a reason to
// hide someone's theme.
func normalizeSwatch(sw []string) []string {
	if len(sw) != 2 {
		return nil
	}
	for _, c := range sw {
		if !validSwatchColor(c) {
			return nil
		}
	}
	return sw
}

// handleListThemes lists the user's theme packs by scanning ~/.octo/themes/ for
// directories that hold both a readable manifest.json and a theme.css. A
// directory that fails any check is skipped silently — the picker shows the
// themes that work, and a half-written one simply does not appear yet.
func (s *Server) handleListThemes(w http.ResponseWriter, r *http.Request) {
	dir := themesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"themes": []themeManifest{}, "dir": dir})
			return
		}
		writeError(w, http.StatusInternalServerError, "list_themes_failed")
		return
	}

	var themes []themeManifest
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id := e.Name()
		if !validThemeID(id) || builtinPackIDs[id] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, id, "manifest.json"))
		if err != nil {
			continue
		}
		var m themeManifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		// The directory name is the id the CSS selector has to match, so a
		// manifest disagreeing with it would produce a theme that lists but
		// never applies. The directory wins; an empty id is simply filled in.
		if m.ID != "" && m.ID != id {
			continue
		}
		m.ID = id
		if m.Name == "" {
			m.Name = id
		}
		m.Swatch = normalizeSwatch(m.Swatch)
		if fi, err := os.Stat(filepath.Join(dir, id, "theme.css")); err != nil || fi.IsDir() {
			continue
		}
		themes = append(themes, m)
	}

	if themes == nil {
		themes = []themeManifest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"themes": themes, "dir": dir})
}

// handleGetThemeCSS serves one theme's stylesheet. The web UI links it from a
// <link> element, so it is a same-origin GET that carries the access-key cookie
// like every other API call.
func (s *Server) handleGetThemeCSS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validThemeID(id) || builtinPackIDs[id] {
		writeError(w, http.StatusNotFound, "theme_not_found")
		return
	}
	path := filepath.Join(themesDir(), id, "theme.css")
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "theme_not_found")
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A theme changes while its author edits it; revalidating every load keeps
	// the reload-to-see-it loop the docs promise.
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
