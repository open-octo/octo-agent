package server

import (
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
}

// handleListLightApps lists all Light Apps by scanning ~/.octo/light-apps/ for
// subdirectories containing a valid manifest.json.
func (s *Server) handleListLightApps(w http.ResponseWriter, r *http.Request) {
	dir := lightAppsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"apps": []lightAppManifest{}})
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
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
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
	var in struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
		HTML        string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
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
	}

	mData, _ := json.MarshalIndent(m, "", "  ")
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
		s = fmt.Sprintf("app-%d", time.Now().UnixMilli()%100000)
	}
	return s
}
