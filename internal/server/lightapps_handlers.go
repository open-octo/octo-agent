package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	// UpdatedAt is index.html's mtime, stamped at read time so the web UI can
	// tell that an app it has open was rewritten on disk. Derived, never
	// persisted: the writers leave it empty and omitempty keeps it out of
	// manifest.json.
	UpdatedAt string `json:"updated_at,omitempty"`
}

// stampLightApp fills m.UpdatedAt from the app's index.html. A missing file
// leaves it empty rather than failing the read — the manifest still describes
// the app, and the detail handler reports the missing HTML on its own.
func stampLightApp(m *lightAppManifest, htmlPath string) {
	if fi, err := os.Stat(htmlPath); err == nil {
		m.UpdatedAt = fi.ModTime().UTC().Format(time.RFC3339Nano)
	}
}

// handleListLightApps lists all Light Apps by scanning ~/.octo/light-apps/ for
// subdirectories containing a valid manifest.json. The response also carries
// the directory itself.
func (s *Server) handleListLightApps(w http.ResponseWriter, r *http.Request) {
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
		stampLightApp(&m, filepath.Join(dir, slug, "index.html"))
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
	stampLightApp(&manifest, htmlPath)

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
