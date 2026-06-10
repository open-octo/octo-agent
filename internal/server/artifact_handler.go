package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
)

// ─── GET /api/sessions/{id}/artifacts?path=… ────────────────────────────────
//
// Serves a previewable file the session's agent wrote, for the web Artifacts
// panel (dev-docs/web-artifacts-panel-design.md). The path must be one this
// session actually wrote: the whitelist is derived from the transcript's
// tool_use blocks on each request, so it needs no extra state and survives
// restarts. Anything not on the whitelist — including files that exist but
// were never written by this session — is a 404.

// artifactMaxBytes caps what the panel will serve inline; bigger files get a
// 413 and the panel offers no preview. Artifact HTML bundles run 200 KB–2 MB.
const artifactMaxBytes = 10 << 20

// artifactContentTypes maps the previewable extensions to the explicit
// Content-Type the response carries (never sniffed). Extensions outside this
// table are not artifacts and are refused.
var artifactContentTypes = map[string]string{
	".html":     "text/html; charset=utf-8",
	".htm":      "text/html; charset=utf-8",
	".md":       "text/markdown; charset=utf-8",
	".markdown": "text/markdown; charset=utf-8",
	".png":      "image/png",
	".jpg":      "image/jpeg",
	".jpeg":     "image/jpeg",
	".gif":      "image/gif",
	".svg":      "image/svg+xml",
	".webp":     "image/webp",
}

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reqPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if id == "" || reqPath == "" {
		writeError(w, http.StatusBadRequest, "missing session id or path")
		return
	}

	ctype, ok := artifactContentTypes[strings.ToLower(filepath.Ext(reqPath))]
	if !ok {
		writeError(w, http.StatusNotFound, "not a previewable artifact type")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if !sessionWrotePath(sess, reqPath) {
		writeError(w, http.StatusNotFound, "path was not written by this session")
		return
	}

	clean := filepath.Clean(reqPath)
	fi, err := os.Stat(clean)
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if fi.Size() > artifactMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact exceeds the 10 MB preview cap")
		return
	}

	f, err := os.Open(clean)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Defense in depth for a URL opened directly in a tab; the panel's primary
	// isolation is the sandboxed iframe (no allow-same-origin).
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// sessionWrotePath reports whether the transcript contains a write_file or
// edit_file tool_use whose path input matches reqPath (after Clean on both
// sides — the payloads carry absolute paths, so no base-dir join is needed).
func sessionWrotePath(sess *agent.Session, reqPath string) bool {
	want := filepath.Clean(reqPath)
	for _, m := range sess.Messages {
		for _, b := range m.Blocks {
			if b.Type != "tool_use" || (b.Name != "write_file" && b.Name != "edit_file") {
				continue
			}
			p, ok := b.Input["path"].(string)
			if ok && filepath.Clean(p) == want {
				return true
			}
		}
	}
	return false
}
