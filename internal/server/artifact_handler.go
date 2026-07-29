package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── GET /api/sessions/{id}/artifacts?path=… ────────────────────────────────
//
// Serves a previewable file the session's agent wrote, for the web Artifacts
// panel (dev-docs/web-artifacts-panel-design.md). The path must be one this
// session actually wrote: the whitelist is derived on each request from the
// transcript's tool_use blocks *and the results answering them*, so it needs no
// extra state and survives restarts. Anything not on the whitelist — including
// files that exist but were never written by this session — is a 404.

// artifactMaxBytes caps what the panel will serve inline; bigger files get a
// 413 and the panel offers no preview. Artifact HTML bundles run 200 KB–2 MB.
const artifactMaxBytes = 10 << 20

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reqPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if id == "" || reqPath == "" {
		writeError(w, http.StatusBadRequest, "missing session id or path")
		return
	}

	// The previewable-extension table lives in the tools package so this gate
	// and the show_artifact tool's validation can't drift apart.
	ctype, ok := tools.ArtifactContentType(reqPath)
	if !ok {
		writeError(w, http.StatusNotFound, "not a previewable artifact type")
		return
	}

	// Tool UI payloads carry absolute paths (write_file/edit_file/show_artifact
	// all resolve inputs before emitting them). Reject relative inputs before
	// touching the transcript so we never resolve them against the server's
	// arbitrary process CWD.
	abs, ok := resolveArtifactPath(reqPath)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid artifact path")
		return
	}

	sess, err := agent.LoadSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	// served is the path as recorded in the transcript, not the request-derived
	// value: it only matches when the agent itself wrote it, and using the
	// transcript's copy keeps the file ops off the raw user-supplied path.
	served, ok := sessionWrotePath(sess, abs)
	if !ok {
		writeError(w, http.StatusNotFound, "path was not written by this session")
		return
	}
	fi, err := os.Stat(served)
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if fi.Size() > artifactMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact exceeds the 10 MB preview cap")
		return
	}

	f, err := os.Open(served)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Defense in depth for a URL opened directly in a tab; the panel's primary
	// isolation is the sandboxed iframe (no allow-same-origin).
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// sessionWrotePath looks for a write_file, edit_file, or show_artifact tool_use
// in the transcript whose path input matches reqPath (after Clean on both
// sides — the payloads carry absolute paths, so no base-dir join is needed) and
// which actually ran. show_artifact is how script-produced files (built rather
// than written through the file tools) enter the whitelist. On a match it
// returns the transcript's own copy of the path so callers serve a value sourced
// from what the agent recorded rather than from the raw request.
//
// The "actually ran" half matters: the agent records a tool_use before the
// permission gate rules on it, so a write the user *denied* sits in the
// transcript looking exactly like one that succeeded. Trusting the call alone
// means refusing a write grants a read of that path instead — the reverse of
// what the user just decided.
func sessionWrotePath(sess *agent.Session, reqPath string) (string, bool) {
	want := filepath.Clean(reqPath)
	for i, m := range sess.Messages {
		for _, b := range m.Blocks {
			if b.Type != "tool_use" || (b.Name != "write_file" && b.Name != "edit_file" && b.Name != "show_artifact") {
				continue
			}
			p, ok := b.Input["path"].(string)
			if !ok {
				continue
			}
			clean := filepath.Clean(p)
			if clean != want || !callSucceeded(sess.Messages, i, b.ID) {
				continue
			}
			return clean, true
		}
	}
	return "", false
}

// callSucceeded reports whether the tool_use with the given id in messages[i]
// was answered by a non-error result in the message immediately after it. A gate
// denial and a genuine execution failure both come back as IsError=true (see
// dispatchTools), and the result is the only place the transcript records that a
// call ran at all.
//
// Pairing is scoped to the next message rather than searched for across the
// transcript, because tool-call ids come from the model, not the runtime. A
// transcript-wide set of "ids that succeeded" is forgeable: emit a write the
// user is going to deny, then reuse that same id on a trivial call that
// succeeds, and the denial inherits the success. Scoping also just matches the
// wire format — an assistant tool_use message is answered by the next user
// message, and the agent loop appends the two back to back — so nothing
// legitimate depends on a wider search.
//
// Unanswered calls count as not-run. The cost is that a file written by a turn
// whose results never landed — a crash mid-turn, or ensureToolPairing stamping a
// synthetic error over an interrupted batch — stops previewing until something
// writes it again. That is the right way to be wrong about whether a read was
// authorized. Context reclamation is safe for this: it rewrites a stale result's
// text but preserves ToolUseID and IsError.
func callSucceeded(msgs []agent.Message, i int, id string) bool {
	if id == "" || i+1 >= len(msgs) {
		return false
	}
	// An id repeated inside one batch is unusable for the same reason: whichever
	// sibling succeeded would vouch for the other.
	uses := 0
	for _, b := range msgs[i].Blocks {
		if b.Type == "tool_use" && b.ID == id {
			uses++
		}
	}
	if uses != 1 {
		return false
	}
	answered := false
	for _, b := range msgs[i+1].Blocks {
		if b.Type != "tool_result" || b.ToolUseID != id {
			continue
		}
		if b.IsError {
			return false
		}
		answered = true
	}
	return answered
}

// resolveArtifactPath validates a path from a tool UI payload. UI payloads carry
// absolute paths, so relative inputs are rejected outright; this avoids resolving
// them against the server's arbitrary process CWD and prevents path-traversal
// payloads that would otherwise be cleaned into sensitive locations.
func resolveArtifactPath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", false
	}
	return clean, true
}
