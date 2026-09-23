package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// lightAppsDir returns the absolute path to the user's Light Apps directory.
func lightAppsDir() string {
	dir, err := datahome.Path("light-apps")
	if err != nil {
		return filepath.Join(".", ".octo", "light-apps")
	}
	return dir
}

// lightAppManifest mirrors the frontmatter of a Light App's manifest.json.
type lightAppManifest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon,omitempty"`
	CreatedAt   string `json:"created_at"`
	// Mount is where the app claims a permanent place in the UI: "view" gives
	// it its own entry in the left navigation. Empty — the default, and what
	// every app written before this field does — means the Light Apps page
	// only. A panel slot was the other option once; the right-hand panel is
	// the session's own (artifacts, diff), and an app is not part of a
	// session.
	Mount string `json:"mount,omitempty"`
	// Public serves the app's page (/_apps/<slug>/) without auth. Set only
	// from the UI through handleSetLightAppPublic.
	Public bool `json:"public,omitempty"`
	// Databases names the databases under ~/.octo/databases/ the page
	// queries. Only a public app is held to it: it may read those and no
	// others (db_pages.go).
	Databases looseStrings `json:"databases,omitempty"`
	// UpdatedAt is index.html's mtime, stamped at read time so the web UI can
	// tell that an app it has open was rewritten on disk. Derived, never
	// persisted: the writers leave it empty and omitempty keeps it out of
	// manifest.json.
	UpdatedAt string `json:"updated_at,omitempty"`
}

// looseStrings decodes a JSON string list, or a lone string as a list of
// one. Any other shape decodes to nothing rather than failing: the field is
// agent-written, and a strict decode would drop the whole app from the
// listing over it. For a public app nothing means no database is readable.
type looseStrings []string

func (l *looseStrings) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*l = looseStrings{one}
		return nil
	}
	var list []string
	if json.Unmarshal(b, &list) == nil {
		*l = list
		return nil
	}
	*l = nil
	return nil
}

// normalizeMount drops a mount value the UI has no slot for, so a typo (or a
// field written by a future version) degrades to the default placement rather
// than failing the whole listing. The retired "panel" goes the same way as a
// typo: an app that still asks for it lands on the Light Apps page.
func normalizeMount(mount string) string {
	if mount == "view" {
		return mount
	}
	return ""
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
		m.Mount = normalizeMount(m.Mount)
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

	manifest.Mount = normalizeMount(manifest.Mount)

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

// handleSetLightAppPublic switches whether the app's page is served without
// auth.
func (s *Server) handleSetLightAppPublic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Public *bool `json:"public"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	if req.Public == nil {
		writeError(w, http.StatusBadRequest, "missing public")
		return
	}
	s.rewriteLightAppManifest(w, r.PathValue("slug"), func(raw map[string]json.RawMessage) {
		if *req.Public {
			raw["public"] = json.RawMessage("true")
		} else {
			delete(raw, "public")
		}
	})
}

// handleSetLightAppMount gives the app its own entry in the left navigation
// ("view") or takes it away (""). The same field the agent writes when the
// user asks it to; this is the switch for doing it by hand.
func (s *Server) handleSetLightAppMount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mount *string `json:"mount"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	if req.Mount == nil || (*req.Mount != "" && normalizeMount(*req.Mount) == "") {
		writeError(w, http.StatusBadRequest, "mount must be \"view\" or \"\"")
		return
	}
	s.rewriteLightAppManifest(w, r.PathValue("slug"), func(raw map[string]json.RawMessage) {
		if *req.Mount == "" {
			delete(raw, "mount")
		} else {
			raw["mount"], _ = json.Marshal(*req.Mount)
		}
	})
}

// rewriteLightAppManifest applies change to the keys of the app's
// manifest.json and writes it back, keeping every other key — ones this
// version does not know included — as it was, then answers with the updated
// manifest the way the list reports it.
func (s *Server) rewriteLightAppManifest(w http.ResponseWriter, slug string, change func(raw map[string]json.RawMessage)) {
	if !safeLightAppSlug(slug) {
		writeError(w, http.StatusBadRequest, "invalid_lightapp_slug")
		return
	}
	manifestPath := filepath.Join(lightAppsDir(), slug, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "lightapp_not_found")
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_lightapp_manifest")
		return
	}
	change(raw)
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_lightapp_manifest")
		return
	}
	if err := os.WriteFile(manifestPath, append(out, '\n'), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "write_lightapp_manifest_failed")
		return
	}
	var m lightAppManifest
	_ = json.Unmarshal(out, &m)
	if m.Slug == "" {
		m.Slug = slug
	}
	m.Mount = normalizeMount(m.Mount)
	stampLightApp(&m, filepath.Join(lightAppsDir(), slug, "index.html"))
	writeJSON(w, http.StatusOK, m)
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
