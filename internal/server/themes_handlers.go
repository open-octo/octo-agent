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
// A theme is a directory under ~/.octo/themes/ holding a manifest, a
// stylesheet redefining the palette variables of web/src/app.css, and whatever
// assets that stylesheet references:
//
//	~/.octo/themes/<id>/
//	├── manifest.json
//	├── theme.css
//	└── wallpaper-light.webp   (optional, any name)
//
// The web UI lists them (GET /api/themes) to populate the theme picker and
// links each stylesheet by URL (GET /api/themes/{id}/theme.css). A relative
// url() in the stylesheet resolves against that same path, so a theme
// addresses its own assets without knowing where it lives.
//
// The themes octo ships (themes_seed.go) are written here on first run and are
// ordinary user themes afterwards — nothing downstream tells them apart.
// Nothing is executed: a theme is CSS, scoped by the selector it must write
// (`:root[data-theme-pack="<id>"]`) to the pack the user picked.

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
	ID   string `json:"id"`
	Name string `json:"name"`
	// Names overrides Name per UI locale ("zh"), so a theme can be called
	// 少女 in Chinese and Blossom in English — which the packs that used to
	// live in app.css were, through i18n keys they no longer have.
	Names    map[string]string `json:"names,omitempty"`
	Author   string            `json:"author,omitempty"`
	Homepage string            `json:"homepage,omitempty"`
	// Swatch is the accent/surface pair the picker draws so a theme can be
	// told apart without applying it. Optional — the picker falls back to a
	// neutral chip — and validated here rather than in the UI, because it
	// reaches the page as a CSS custom property: see validSwatchColor.
	Swatch []string `json:"swatch,omitempty"`
}

// builtinPackIDs is the pack compiled into the web UI (web/src/lib/theme.ts,
// PACKS). A user directory reusing it is skipped rather than served: both
// would write the same `[data-theme-pack=…]` selector, and which one won would
// come down to stylesheet order. Only the default pack is left in the binary —
// every other theme octo ships is seeded to disk as a normal theme.
var builtinPackIDs = map[string]bool{"azure": true}

// themeAssetTypes is the set of files a theme may serve, and the type each is
// served as. Deliberately a whitelist of inert formats: these are read from a
// directory the user can write, and served from the app's own origin, so
// anything the browser would execute in that origin — SVG above all, which
// carries script — stays out no matter what a theme puts in its folder.
var themeAssetTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".webp":  "image/webp",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".avif":  "image/avif",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
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

// handleGetThemeFile serves one file from a theme's directory: its stylesheet,
// and the assets that stylesheet references by relative url(). The web UI
// links the stylesheet from a <link>, so these are same-origin GETs carrying
// the access-key cookie like every other API call.
func (s *Server) handleGetThemeFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("file")
	if !validThemeID(id) || builtinPackIDs[id] {
		writeError(w, http.StatusNotFound, "theme_not_found")
		return
	}
	// The mux hands over one path segment, but a decoded one: reject anything
	// that could still climb out of the theme's directory.
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		writeError(w, http.StatusNotFound, "theme_file_not_found")
		return
	}
	ctype, ok := themeAssetTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		writeError(w, http.StatusNotFound, "theme_file_not_found")
		return
	}
	data, err := os.ReadFile(filepath.Join(themesDir(), id, name))
	if err != nil {
		writeError(w, http.StatusNotFound, "theme_file_not_found")
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A theme changes while its author edits it; revalidating every load keeps
	// the reload-to-see-it loop the docs promise.
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
