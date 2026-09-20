package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Light App state relay ──────────────────────────────────────────────────
//
// A Light App cannot reach this endpoint itself: it runs on its own origin
// behind a CSP that closes every network exit (artifact_gate.go). It posts a
// snapshot over the postMessage bridge instead, and the web UI — which does
// have the origin and the cookie — relays it here. The model then reads the
// mirror through lightapp_state / view_lightapp.
//
// So the app gains no capability from this route; the host is still the only
// party holding credentials. What the app gains is a way to be seen.

// maxLightAppStateBody caps one relayed snapshot. The screenshot dominates it;
// the mirror trims anything still oversized on the way in.
const maxLightAppStateBody = 16 << 20

// handlePutLightAppState records one snapshot pushed by a running Light App.
func (s *Server) handlePutLightAppState(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" || strings.Contains(slug, "..") || strings.ContainsAny(slug, `/\`) {
		writeError(w, http.StatusBadRequest, "invalid_lightapp_slug")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLightAppStateBody)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_lightapp_state")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	snap := tools.LightAppSnapshot{
		Slug:      slug,
		Digest:    strings.TrimSpace(r.FormValue("digest")),
		UpdatedAt: time.Now(),
	}
	// Summary is relayed verbatim but must at least be JSON: the tools hand it
	// to the model as-is, and a malformed blob there reads as corrupt output
	// rather than as the app's own mistake.
	if raw := strings.TrimSpace(r.FormValue("summary")); raw != "" && json.Valid([]byte(raw)) {
		snap.Summary = json.RawMessage(raw)
	}

	if file, hdr, err := r.FormFile("image"); err == nil {
		defer file.Close()
		data, rerr := io.ReadAll(file)
		if rerr == nil && len(data) > 0 {
			snap.Image = data
			snap.ImageType = hdr.Header.Get("Content-Type")
		}
	}

	tools.PutLightApp(snap)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleDeleteLightAppState forgets an app, sent when its frame goes away so
// the tools stop describing something nobody has open.
func (s *Server) handleDeleteLightAppState(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" || strings.Contains(slug, "..") || strings.ContainsAny(slug, `/\`) {
		writeError(w, http.StatusBadRequest, "invalid_lightapp_slug")
		return
	}
	tools.DropLightApp(slug)
	writeJSON(w, http.StatusOK, map[string]any{"status": "dropped"})
}
