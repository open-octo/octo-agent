package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Artifact state relay ───────────────────────────────────────────────────
//
// An artifact page cannot reach this endpoint itself: it runs on its own
// origin behind a CSP that closes every network exit (artifact_gate.go). It
// posts a snapshot over the postMessage bridge instead, and the web UI —
// which does have the origin and the cookie — relays it here. The model then
// reads the mirror through artifact_state / view_artifact.
//
// So the page gains no capability from this route; the host is still the only
// party holding credentials. What the page gains is a way to be seen.
//
// The path must be one this session actually wrote (the same whitelist the
// artifact preview endpoint enforces): the mirror's key is not something a
// relaying page gets to assert.

// maxArtifactStateBody caps one relayed snapshot. The screenshot dominates
// it; the mirror trims anything still oversized on the way in.
const maxArtifactStateBody = 16 << 20

// artifactStatePath resolves and whitelist-checks the ?path= query the way
// handleGetArtifact does: both ends of the relay must agree on which paths
// are this session's to speak for.
func artifactStatePath(w http.ResponseWriter, r *http.Request) (session, path string, ok bool) {
	session = r.PathValue("id")
	abs, ok := resolveArtifactPath(strings.TrimSpace(r.URL.Query().Get("path")))
	if session == "" || !ok {
		writeError(w, http.StatusBadRequest, "missing session id or path")
		return "", "", false
	}
	sess, err := agent.LoadSession(session)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return "", "", false
	}
	served, ok := sessionWrotePath(sess, abs)
	if !ok {
		writeError(w, http.StatusNotFound, "path was not written by this session")
		return "", "", false
	}
	return session, served, true
}

// handlePutArtifactState records one snapshot pushed by an open artifact.
func (s *Server) handlePutArtifactState(w http.ResponseWriter, r *http.Request) {
	session, path, ok := artifactStatePath(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxArtifactStateBody)
	// Anything past this budget spills to a temp file, which the deferred
	// RemoveAll below deletes — the mirror's "screenshots stay in memory" rule
	// is about octo's own durable store, not about net/http's scratch space.
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_artifact_state")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	snap := tools.ArtifactSnapshot{
		Session:   session,
		Path:      path,
		Digest:    strings.TrimSpace(r.FormValue("digest")),
		UpdatedAt: time.Now(),
	}
	// Summary is relayed verbatim but must at least be JSON: the tools hand it
	// to the model as-is, and a malformed blob there reads as corrupt output
	// rather than as the page's own mistake.
	//
	// Compacted, not just validated: JSON allows raw newlines between tokens,
	// and artifact_state renders the summary into a line-per-page answer. A
	// page could otherwise pad its own JSON into what reads as another
	// artifact's line.
	if raw := strings.TrimSpace(r.FormValue("summary")); raw != "" && json.Valid([]byte(raw)) {
		var buf bytes.Buffer
		if json.Compact(&buf, []byte(raw)) == nil {
			snap.Summary = json.RawMessage(buf.Bytes())
		}
	}

	if file, hdr, err := r.FormFile("image"); err == nil {
		defer file.Close()
		data, rerr := io.ReadAll(file)
		if rerr == nil && len(data) > 0 {
			snap.Image = data
			snap.ImageType = hdr.Header.Get("Content-Type")
		}
	}

	tools.PutArtifact(snap)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleDeleteArtifactState forgets one page, sent when its frame goes away
// so the tools stop describing something nobody has open.
func (s *Server) handleDeleteArtifactState(w http.ResponseWriter, r *http.Request) {
	session, path, ok := artifactStatePath(w, r)
	if !ok {
		return
	}
	tools.DropArtifact(session, path)
	writeJSON(w, http.StatusOK, map[string]any{"status": "dropped"})
}
