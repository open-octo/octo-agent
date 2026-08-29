package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// lightAppsDir returns the absolute path to the user's Light Apps directory.
func lightAppsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".octo", "light-apps")
	}
	return filepath.Join(home, ".octo", "light-apps")
}

// lightAppManifest mirrors the frontmatter of a Light App's manifest.json.
type lightAppManifest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon,omitempty"`
	CreatedAt   string `json:"created_at"`
	// SourcePath is the absolute path of the session artifact this app was
	// saved from, when it was. The web UI matches it against the artifact on
	// display to hide the redundant "Save to Light App" action.
	SourcePath string `json:"source_path,omitempty"`
}

// handleListLightApps lists all Light Apps by scanning ~/.octo/light-apps/ for
// subdirectories containing a valid manifest.json. The response also carries
// the directory itself, so the web UI can tell whether a session artifact
// already lives inside it (and skip the redundant "Save to Light App" action).
func (s *Server) handleListLightApps(w http.ResponseWriter, r *http.Request) {
	// no-store: the desktop webview (WKWebView) heuristically caches GET 200s,
	// so without it an updated or newly saved app keeps serving the stale list
	// from cache. Mirrors /api/onboard/status (#1660); the client also passes
	// cache:'no-store'.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	dir := lightAppsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"apps": []lightAppManifest{}, "dir": dir})
			return
		}
		writeError(w, http.StatusInternalServerError, "list_lightapps_failed")
		return
	}

	var apps []lightAppManifest
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		slug := e.Name()
		manifestPath := filepath.Join(dir, slug, "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var m lightAppManifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.Slug == "" {
			m.Slug = slug
		}
		apps = append(apps, m)
	}

	if apps == nil {
		apps = []lightAppManifest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps, "dir": dir})
}

// handleGetLightApp returns the full manifest and index.html content for a
// single Light App identified by its slug.
func (s *Server) handleGetLightApp(w http.ResponseWriter, r *http.Request) {
	// no-store for the same reason as the list above: this response carries the
	// app's whole index.html, so a cached copy IS the stale app the user just
	// replaced.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	slug := r.PathValue("slug")
	if slug == "" || strings.Contains(slug, "..") || strings.ContainsAny(slug, "/\\") {
		writeError(w, http.StatusBadRequest, "invalid_lightapp_slug")
		return
	}

	appDir := filepath.Join(lightAppsDir(), slug)
	manifestPath := filepath.Join(appDir, "manifest.json")
	htmlPath := filepath.Join(appDir, "index.html")

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "lightapp_not_found")
		return
	}
	var manifest lightAppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_lightapp_manifest")
		return
	}
	if manifest.Slug == "" {
		manifest.Slug = slug
	}

	htmlData, err := os.ReadFile(htmlPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "lightapp_index_missing")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"manifest": manifest,
		"html":     string(htmlData),
	})
}

// handleDeleteLightApp removes a Light App dir (recursively) by its slug.
func (s *Server) handleDeleteLightApp(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" || strings.Contains(slug, "..") || strings.ContainsAny(slug, "/\\") {
		writeError(w, http.StatusBadRequest, "invalid_lightapp_slug")
		return
	}

	appDir := filepath.Join(lightAppsDir(), slug)
	if err := os.RemoveAll(appDir); err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, "delete_lightapp_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

// handleCreateLightApp creates a new Light App from the provided manifest and
// HTML content. Slug is auto-derived from name if not supplied.
func (s *Server) handleCreateLightApp(w http.ResponseWriter, r *http.Request) {
	// Cap body at 10 MB — more than enough for any self-contained HTML page.
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	var in struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
		HTML        string `json:"html"`
		SourcePath  string `json:"source_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			writeError(w, http.StatusRequestEntityTooLarge, "lightapp_body_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_lightapp_payload")
		return
	}
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "lightapp_name_required")
		return
	}
	if in.HTML == "" {
		writeError(w, http.StatusBadRequest, "lightapp_html_required")
		return
	}

	slug := in.Slug
	if slug == "" {
		slug = slugFromName(in.Name)
	}
	if slug == "" || strings.Contains(slug, "..") || strings.ContainsAny(slug, "/\\") {
		writeError(w, http.StatusBadRequest, "invalid_lightapp_slug")
		return
	}

	appDir := filepath.Join(lightAppsDir(), slug)

	// Reject if the app already exists — users should delete first or rename.
	if _, err := os.Stat(appDir); err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "slug_exists", "slug": slug})
		return
	}

	if err := os.MkdirAll(appDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "create_lightapp_failed")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if in.Icon == "" {
		in.Icon = "📄"
	}
	m := lightAppManifest{
		Slug:        slug,
		Name:        in.Name,
		Description: in.Description,
		Icon:        in.Icon,
		CreatedAt:   now,
		SourcePath:  in.SourcePath,
	}

	mData, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_lightapp_failed")
		return
	}
	if err := os.WriteFile(filepath.Join(appDir, "manifest.json"), mData, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "create_lightapp_failed")
		return
	}
	if err := os.WriteFile(filepath.Join(appDir, "index.html"), []byte(in.HTML), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "create_lightapp_failed")
		return
	}

	writeJSON(w, http.StatusCreated, m)
}

// slugFromName derives a URL-safe slug from a display name.
var slugNonAlpha = regexp.MustCompile(`[^a-z0-9]+`)

func slugFromName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugNonAlpha.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		// Derive a stable slug from the original name so repeated calls with
		// the same input always produce the same fallback.
		h := sha256.Sum256([]byte(name))
		s = fmt.Sprintf("app-%x", h[:4])
	}
	return s
}
