package server

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// ─── The Light App origin ───────────────────────────────────────────────────
//
// A saved Light App renders from `<slug>.apps.localhost`, the same way a
// session artifact renders from the artifact origin (artifact_origin.go) and
// under the same rules: local peers only, the entry through the CDN gate,
// siblings by asset extension, nothing else on the host. Two differences:
//
//   - No token. The app is something the user chose to keep, in a directory
//     any local process can read already; and a *stable* hostname is the
//     point — the app's localStorage persists across sessions, reloads and
//     server restarts, and no other app shares its origin.
//   - A small script is appended to the entry: the one-time migration of the
//     storage the host used to keep for the app, and — in the desktop shell
//     only, whose webview cannot download — the download bridge. Both speak
//     the `__laBridge` protocol the host already routes (laStorage.ts).

const (
	lightAppHostSuffix = ".apps.localhost"
	lightAppHostBare   = "apps.localhost"
)

//go:embed lightapp_bridge.js
var lightAppBridgeJS string

var bodyCloseRe = regexp.MustCompile(`(?i)</body\s*>`)

// lightAppHostLabel is artifactHostLabel for the apps host: the label is the
// slug. Bare and over-deep hosts still count as the apps host (and 404) so
// no shape of it reaches the app's mux.
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

func (s *Server) serveLightAppOrigin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !isLocalPeer(r) {
		writeError(w, http.StatusForbidden, "light apps are available only from the local machine")
		return
	}
	// The hostname is lowercase by the time it gets here (canonicalHost), so a
	// slug with capitals is unreachable; the prompt asks for lowercase slugs.
	slug, _ := lightAppHostLabel(r.Host)
	if slug == "" || strings.Contains(slug, "..") || strings.ContainsAny(slug, `/\`) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	appDir := filepath.Join(lightAppsDir(), slug)
	entry := filepath.Join(appDir, "index.html")
	if fi, err := os.Stat(entry); err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	setArtifactOriginHeaders(w.Header())

	p := path.Clean("/" + r.URL.Path)
	if p == "/" || p == "/index.html" {
		serveArtifactEntry(w, r, entry, s.lightAppBridge(slug))
		return
	}
	serveArtifactAsset(w, r, appDir, strings.TrimPrefix(p, "/"))
}

// lightAppBridge is the script tag pair appended to a Light App entry: the
// configuration literal, then the embedded bridge. json.Marshal escapes `<`,
// `>` and `&`, so a slug can never close the script element early.
func (s *Server) lightAppBridge(slug string) []byte {
	cfg, _ := json.Marshal(map[string]any{
		"ns":       slug,
		"download": s.cfg.Native != nil,
	})
	return []byte("<script>window.__octoLightApp=" + string(cfg) + ";</script>\n<script>" + lightAppBridgeJS + "</script>")
}

// injectBeforeBody places script just before the closing body tag, or at the
// very end when the document has none — where a browser puts the implied one.
//
// The *last* `</body>` is the tag: an earlier one is text inside a script or a
// comment (a page that builds HTML in a template string, say), and splicing a
// script element into the middle of that script would break the page. This is
// a byte scan rather than a parse on purpose — the gate hands back the file's
// own bytes when it stripped nothing, and re-serialising here would undo that.
// The one shape it gets wrong: a page with no closing tag at all whose only
// `</body>` is such text — the browser implies the tag, and the bridge lands in
// the string. Accepted as a corner the parse-free approach pays for.
func injectBeforeBody(doc, script []byte) []byte {
	if len(script) == 0 {
		return doc
	}
	all := bodyCloseRe.FindAllIndex(doc, -1)
	if all == nil {
		return append(append([]byte{}, doc...), script...)
	}
	loc := all[len(all)-1]
	out := append([]byte{}, doc[:loc[0]]...)
	out = append(out, script...)
	return append(out, doc[loc[0]:]...)
}
