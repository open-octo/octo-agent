package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ─── GET /_apps/{slug}/{path...} ────────────────────────────────────────────
//
// A saved Light App renders from /_apps/<slug>/ under the same rules as a
// session artifact (artifact_pages.go): the entry and its siblings, the shim
// spliced into HTML, the page on the app's own origin. The slug is the
// localStorage namespace, so an app's data persists across sessions, reloads
// and server restarts and no two apps collide.
//
// The route is registered directly on the mux rather than through Server.api
// because an app can be made public (manifest `public`): a public app is
// served to anyone, every other one goes through requireAuth like /api.

// safeLightAppSlug is the check handleGetLightApp applies: a single path
// segment. Looser than validLightAppSlug (landing), which also fixes the
// character set.
func safeLightAppSlug(slug string) bool {
	return slug != "" && !strings.Contains(slug, "..") && !strings.ContainsAny(slug, `/\`)
}

// lightAppIsPublic re-reads the manifest on every request, so switching the
// flag off takes effect at once.
func lightAppIsPublic(slug string) bool {
	data, err := os.ReadFile(filepath.Join(lightAppsDir(), slug, "manifest.json"))
	if err != nil {
		return false
	}
	var m struct {
		Public bool `json:"public"`
	}
	return json.Unmarshal(data, &m) == nil && m.Public
}

func (s *Server) handleLightAppPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if safeLightAppSlug(slug) && lightAppIsPublic(slug) {
		s.serveLightAppPage(w, r)
		return
	}
	s.requireAuth(s.serveLightAppPage)(w, r)
}

func (s *Server) serveLightAppPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !safeLightAppSlug(slug) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	appDir := filepath.Join(lightAppsDir(), slug)
	entry := filepath.Join(appDir, "index.html")
	if fi, err := os.Stat(entry); err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	shim := s.pageShim(slug, s.cfg.Native != nil)
	rel := r.PathValue("path")
	if rel == "" || rel == "index.html" {
		servePageEntry(w, r, entry, shim)
		return
	}
	servePageFile(w, r, appDir, rel, shim)
}

// ─── Storage export from the retired `<slug>.apps.localhost` origin ─────────
//
// Light Apps used to render from `<slug>.apps.localhost`, and an app's data
// may still sit in that origin's localStorage. The host page frames
// http://<slug>.apps.localhost:<port>/__octo_export once per app (laStorage.ts)
// and copies what it posts into the app's namespace. Nothing else is served on
// that host. Local peers only, and only this machine's UI may frame it.

const (
	lightAppHostSuffix = ".apps.localhost"
	lightAppHostBare   = "apps.localhost"
)

// lightAppHostLabel splits a Host header into the slug when it names the
// retired apps host. Bare and over-deep hosts still count as the apps host
// (and 404) so no shape of it reaches the app's mux.
func lightAppHostLabel(host string) (slug string, isLightAppHost bool) {
	h := canonicalHost(host)
	if h == lightAppHostBare {
		return "", true
	}
	if !strings.HasSuffix(h, lightAppHostSuffix) {
		return "", false
	}
	slug = strings.TrimSuffix(h, lightAppHostSuffix)
	if slug == "" || strings.Contains(slug, ".") {
		return "", true
	}
	return slug, true
}

// hostRouter sits outside the CORS middleware and the mux: a request for the
// retired apps host never reaches either.
func (s *Server) hostRouter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := lightAppHostLabel(r.Host); ok {
			s.serveLightAppExport(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalPeer is isLocalRequest minus the Host check, for the retired apps
// host itself: its Host is by construction not a local name, so the remaining
// two signals — a loopback peer that no relay forwarded — decide.
func isLocalPeer(r *http.Request) bool {
	return isLoopbackRemote(r.RemoteAddr) && !isForwarded(r)
}

// The export page posts its origin's whole localStorage to the frame's parent.
// The data never leaves this machine's browser: the frame-ancestors clause
// lets only the local UI embed the page.
const lightAppExportPage = `<!doctype html><script>(function(){var d={};try{for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);d[k]=localStorage.getItem(k);}}catch(e){}parent.postMessage({__laBridge:1,op:'export',ns:__NS__,value:d},'*');})();</script>`

func (s *Server) serveLightAppExport(w http.ResponseWriter, r *http.Request) {
	slug, _ := lightAppHostLabel(r.Host)
	if r.Method != http.MethodGet || !isLocalPeer(r) || r.URL.Path != "/__octo_export" || !safeLightAppSlug(slug) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ns, _ := json.Marshal(slug)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "frame-ancestors http://localhost:* http://127.0.0.1:*")
	_, _ = w.Write([]byte(strings.Replace(lightAppExportPage, "__NS__", string(ns), 1)))
}
