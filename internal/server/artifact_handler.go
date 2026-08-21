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
// in the transcript whose path matches reqPath (after Clean on both sides) and
// which actually ran. show_artifact is how script-produced files (built rather
// than written through the file tools) enter the whitelist. On a match it
// returns the transcript's own copy of the path so callers serve a value sourced
// from what the agent recorded rather than from the raw request.
//
// A call's path is matched in two places, because the two record different
// forms of it:
//
//   - The tool_use input is the model's *raw* path — frequently relative or
//     ~/-prefixed, since the file tools accept those and resolve them against
//     the session working dir. It matches only when the model happened to
//     pass an absolute path.
//   - The answering tool_result's ui payload carries the path as the tool
//     *resolved* it (always absolute; see write_file/edit_file/show_artifact),
//     persisted with the transcript and invisible to the model. This is the
//     same value the panel lists, and the only form that matches for the
//     common relative-input case. The payload lives on the result block, so
//     this form exists only once the call has finished — an in-flight write
//     is still matched by its raw input alone.
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
			served := ""
			if clean := filepath.Clean(p); clean == want {
				served = clean
			} else if resolved, ok := resultUIPath(sess.Messages, i, b.ID, want); ok {
				served = resolved
			}
			if served == "" || !callAuthorized(sess.Messages, i, b.ID) {
				continue
			}
			return served, true
		}
	}
	return "", false
}

// resultUIPath returns the tool-resolved path from the ui payload of the
// result answering the tool_use with the given id in messages[i], when that
// path matches want. Scoping mirrors callAuthorized: tool-call ids come from
// the model, so only the next message's blocks answer this call, and an error
// result (a denial, a failed write) has no resolved path to vouch for. After
// the transcript's JSON round-trip the payload decodes as map[string]any.
func resultUIPath(msgs []agent.Message, i int, id, want string) (string, bool) {
	if id == "" || i+1 >= len(msgs) {
		return "", false
	}
	for _, rb := range msgs[i+1].Blocks {
		if rb.Type != "tool_result" || rb.ToolUseID != id || rb.IsError {
			continue
		}
		ui, ok := rb.UI.(map[string]any)
		if !ok {
			continue
		}
		p, ok := ui["path"].(string)
		if !ok {
			continue
		}
		if clean := filepath.Clean(p); clean == want {
			return clean, true
		}
	}
	return "", false
}

// toolResultUIPath is resultUIPath without the match: it returns the
// tool-resolved path from the answering result's ui payload, for callers that
// collect candidate paths rather than authorise one (sessionTouchedDirs).
func toolResultUIPath(msgs []agent.Message, i int, id string) (string, bool) {
	if id == "" || i+1 >= len(msgs) {
		return "", false
	}
	for _, rb := range msgs[i+1].Blocks {
		if rb.Type != "tool_result" || rb.ToolUseID != id || rb.IsError {
			continue
		}
		ui, ok := rb.UI.(map[string]any)
		if !ok {
			continue
		}
		if p, ok := ui["path"].(string); ok {
			return p, true
		}
	}
	return "", false
}

// callAuthorized reports whether the tool_use with the given id in messages[i]
// may serve its path. A gate denial and a genuine execution failure both come
// back as IsError=true (see dispatchTools), and the result is the only place the
// transcript records what became of a call — so the rule is that an *answered*
// call must have been answered without error.
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
// A call with nothing after it at all is allowed, and that is not laxness — it
// is the live case. The panel is told to fetch from inside dispatchTools:
// EventToolDone carries the ui_payload, the web handler broadcasts it and then
// persists (ws_handlers.go), and the tool_result message is only appended once
// dispatchTools returns. So at the moment the client asks, the newest thing on
// disk is the tool_use itself. Requiring a result here would 404 every write the
// user just watched happen.
//
// The window that leaves open is narrow and bounded: a denied write is
// momentarily unanswered too, until the error result reaches disk on the next
// event. Nothing points a client at that path in the meantime — a denied call
// produces no ui_payload — so reaching it means already knowing the path and
// racing a sub-second write. Once any later message exists, an unanswered call
// is stale and refused, which is what keeps a denial from being harvestable
// afterwards. That is the property #1891 was actually about.
//
// Context reclamation is safe for all of this: it rewrites a stale result's text
// but preserves ToolUseID and IsError.
func callAuthorized(msgs []agent.Message, i int, id string) bool {
	if id == "" {
		return false
	}
	// An id repeated inside one batch is unusable: whichever sibling succeeded
	// would vouch for the other.
	uses := 0
	for _, b := range msgs[i].Blocks {
		if b.Type == "tool_use" && b.ID == id {
			uses++
		}
	}
	if uses != 1 {
		return false
	}
	if i+1 >= len(msgs) {
		return true // in flight, or the process died holding it
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
