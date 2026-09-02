package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// newArtifactSession persists a session whose transcript wrote the given
// paths — one write_file tool_use per path, each answered by a successful
// tool_result, which is what the whitelist requires — and returns its id.
func newArtifactSession(t *testing.T, paths ...string) string {
	t.Helper()
	return newArtifactSessionWith(t, "write_file", writeOK, paths...)
}

// writeOutcome describes the tool_result answering a recorded write.
type writeOutcome int

const (
	writeOK     writeOutcome = iota // succeeded
	writeDenied                     // the permission gate refused it
)

// newArtifactSessionWith builds a transcript with one call per path under the
// given tool name, each paired with the described outcome.
func newArtifactSessionWith(t *testing.T, tool string, outcome writeOutcome, paths ...string) string {
	t.Helper()
	sess := agent.NewSession("stub-model", "")
	for i, p := range paths {
		id := "t" + string(rune('0'+i))
		sess.Messages = append(sess.Messages, agent.Message{
			Role: agent.RoleAssistant,
			Blocks: []agent.ContentBlock{{
				Type:  "tool_use",
				ID:    id,
				Name:  tool,
				Input: map[string]any{"path": p, "content": "x"},
			}},
		})
		result := agent.NewToolResultBlock(id, "wrote "+p, false)
		if outcome == writeDenied {
			// Exactly what dispatchTools synthesises for a denied call.
			result = agent.NewToolResultBlock(id, "permission_denied: user declined to run "+tool, true)
		}
		sess.Messages = append(sess.Messages, agent.Message{
			Role:   agent.RoleUser,
			Blocks: []agent.ContentBlock{result},
		})
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func getArtifact(t *testing.T, srv *Server, sessionID, path string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"path": {path}}.Encode()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/artifacts?"+q, nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	return w
}

func TestHandleGetArtifact(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	artDir := t.TempDir()
	htmlPath := filepath.Join(artDir, "bundle.html")
	if err := os.WriteFile(htmlPath, []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Exists on disk but is NOT in any transcript — must stay unreachable.
	secretPath := filepath.Join(artDir, "secret.html")
	if err := os.WriteFile(secretPath, []byte("<h1>secret</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Written by the session, but a source file — not an artifact kind.
	goPath := filepath.Join(artDir, "main.go")
	if err := os.WriteFile(goPath, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Written by the session but not a previewable type.
	binPath := filepath.Join(artDir, "tool.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}

	// UI payloads carry absolute paths, so the transcript stores absolute paths.
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id := newArtifactSession(t, htmlPath, goPath, binPath)
	otherID := newArtifactSession(t /* wrote nothing */)

	// Whitelisted write → 200 with explicit headers.
	w := getArtifact(t, srv, id, htmlPath)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), []byte("<h1>hi</h1>")) {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Error("missing CSP sandbox header")
	}

	// On-disk but unwritten by the session → 404.
	if w := getArtifact(t, srv, id, secretPath); w.Code != http.StatusNotFound {
		t.Errorf("unwritten path: status = %d, want 404", w.Code)
	}
	// Written by a different session → 404.
	if w := getArtifact(t, srv, otherID, htmlPath); w.Code != http.StatusNotFound {
		t.Errorf("other session: status = %d, want 404", w.Code)
	}
	// A written source file is not previewable either → 404, even though the
	// session wrote it.
	if w := getArtifact(t, srv, id, goPath); w.Code != http.StatusNotFound {
		t.Errorf("source file: status = %d, want 404", w.Code)
	}
	// Written but not a previewable extension → 404.
	if w := getArtifact(t, srv, id, binPath); w.Code != http.StatusNotFound {
		t.Errorf("non-previewable ext: status = %d, want 404", w.Code)
	}
	// Unknown session → 404.
	if w := getArtifact(t, srv, "nope", htmlPath); w.Code != http.StatusNotFound {
		t.Errorf("unknown session: status = %d, want 404", w.Code)
	}
	// Relative paths are rejected outright to avoid resolving against the
	// server's process CWD.
	if w := getArtifact(t, srv, id, "bundle.html"); w.Code != http.StatusBadRequest {
		t.Errorf("relative path: status = %d, want 400", w.Code)
	}
	// Missing path param → 400.
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+id+"/artifacts", nil)
	rec := httptest.NewRecorder()
	serveLoopback(srv.mux, rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing path: status = %d, want 400", rec.Code)
	}
}

func TestHandleGetArtifact_SizeCap(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	artDir := t.TempDir()
	big := filepath.Join(artDir, "big.html")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), artifactMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	id := newArtifactSession(t, big)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	if w := getArtifact(t, srv, id, big); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

// Models routinely pass relative paths (or ~/…) to write_file/edit_file; the
// tools resolve them against the session working dir and record the absolute
// result in the ui payload, which is also what the panel lists. The whitelist
// must serve from that resolved path — matching only the raw tool_use input
// 404s every relative-path write in the panel.
func TestHandleGetArtifact_RelativeInputPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	artDir := t.TempDir()
	abs := filepath.Join(artDir, "report.md")
	if err := os.WriteFile(abs, []byte("# report"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Exactly what the transcript records when the model passes a relative
	// path: the raw relative input on the tool_use, the tool-resolved absolute
	// path on the answering result's ui payload.
	sess := agent.NewSession("stub-model", "")
	sess.Messages = append(sess.Messages, agent.Message{
		Role: agent.RoleAssistant,
		Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "t1", Name: "write_file",
			Input: map[string]any{"path": "report.md", "content": "# report"},
		}},
	})
	res := agent.NewToolResultBlock("t1", "wrote report.md", false)
	res.UI = map[string]any{"type": "write", "path": abs, "size_bytes": 8}
	sess.Messages = append(sess.Messages, agent.Message{
		Role:   agent.RoleUser,
		Blocks: []agent.ContentBlock{res},
	})
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := getArtifact(t, srv, sess.ID, abs)
	if w.Code != http.StatusOK {
		t.Fatalf("relative-input write: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "# report" {
		t.Errorf("body = %q", w.Body.String())
	}

	// The resolved path must still come from an *authorized* call: a ui
	// payload on an error result (e.g. a denied write) proves nothing ran.
	denied := agent.NewSession("stub-model", "")
	denied.Messages = append(denied.Messages, agent.Message{
		Role: agent.RoleAssistant,
		Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "t1", Name: "write_file",
			Input: map[string]any{"path": "report.md", "content": "x"},
		}},
	})
	deniedRes := agent.NewToolResultBlock("t1", "permission_denied: user declined to run write_file", true)
	deniedRes.UI = map[string]any{"type": "write", "path": abs}
	denied.Messages = append(denied.Messages, agent.Message{
		Role:   agent.RoleUser,
		Blocks: []agent.ContentBlock{deniedRes},
	})
	if err := denied.Save(); err != nil {
		t.Fatal(err)
	}
	if w := getArtifact(t, srv, denied.ID, abs); w.Code != http.StatusNotFound {
		t.Errorf("ui path on a denied write: status = %d, want 404", w.Code)
	}
}

func TestHandleGetArtifact_ShowArtifactCountsAsWrite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Simulates a script-built file surfaced via the show_artifact tool: no
	// write_file in the transcript, only the show_artifact tool_use.
	artDir := t.TempDir()
	p := filepath.Join(artDir, "bundle.html")
	if err := os.WriteFile(p, []byte("<h1>built</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("stub-model", "")
	sess.Messages = append(sess.Messages, agent.Message{
		Role: agent.RoleAssistant,
		Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "t1", Name: "show_artifact",
			Input: map[string]any{"path": p},
		}}})
	sess.Messages = append(sess.Messages, agent.Message{
		Role:   agent.RoleUser,
		Blocks: []agent.ContentBlock{agent.NewToolResultBlock("t1", "shown", false)},
	})
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := getArtifact(t, srv, sess.ID, p)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "<h1>built</h1>" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestHandleGetArtifact_EditCountsAsWrite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	artDir := t.TempDir()
	p := filepath.Join(artDir, "doc.md")
	if err := os.WriteFile(p, []byte("# doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("stub-model", "")
	sess.Messages = append(sess.Messages, agent.Message{
		Role: agent.RoleAssistant,
		Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "t1", Name: "edit_file",
			Input: map[string]any{"path": p, "old_string": "a", "new_string": "b"},
		}},
	})
	sess.Messages = append(sess.Messages, agent.Message{
		Role:   agent.RoleUser,
		Blocks: []agent.ContentBlock{agent.NewToolResultBlock("t1", "edited", false)},
	})
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := getArtifact(t, srv, sess.ID, p)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHandleGetArtifact_WorktreeOutsideServerCWD(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Simulate a session that ran in a worktree outside the server's cwd.
	serverCWD := t.TempDir()
	worktree := t.TempDir()
	p := filepath.Join(worktree, "report.html")
	if err := os.WriteFile(p, []byte("<p>worktree</p>"), 0o644); err != nil {
		t.Fatal(err)
	}

	id := newArtifactSession(t, p)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.cwd = serverCWD

	w := getArtifact(t, srv, id, p)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "<p>worktree</p>" {
		t.Errorf("body = %q", w.Body.String())
	}
}

// A write the user refused must not become a readable path. The tool_use lands
// in the transcript before the permission gate rules on it, so the call alone
// cannot be the whitelist — otherwise denying a write grants a read.
func TestHandleGetArtifact_DeniedWriteIsNotServed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// The realistic shape: the file already exists, the agent proposes
	// overwriting it, and the user says no. The bytes on disk are the user's.
	artDir := t.TempDir()
	private := filepath.Join(artDir, "private.md")
	if err := os.WriteFile(private, []byte("# my notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	denied := newArtifactSessionWith(t, "write_file", writeDenied, private)
	if w := getArtifact(t, srv, denied, private); w.Code != http.StatusNotFound {
		t.Errorf("denied write: status = %d, want 404; body=%s", w.Code, w.Body.String())
	}

	// An unanswered call that something else has already moved past is stale:
	// this is what stops a denial from being harvestable after the fact, and it
	// is the difference from the in-flight case covered by the test below.
	stale := agent.NewSession("stub-model", "")
	stale.Messages = append(stale.Messages,
		agent.Message{Role: agent.RoleAssistant, Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "t0", Name: "write_file",
			Input: map[string]any{"path": private, "content": "x"},
		}}},
		agent.Message{Role: agent.RoleUser, Content: "never mind"},
	)
	if err := stale.Save(); err != nil {
		t.Fatal(err)
	}
	if w := getArtifact(t, srv, stale.ID, private); w.Code != http.StatusNotFound {
		t.Errorf("stale unanswered write: status = %d, want 404", w.Code)
	}

	// Same for a refused show_artifact, the other way into the whitelist.
	deniedShow := newArtifactSessionWith(t, "show_artifact", writeDenied, private)
	if w := getArtifact(t, srv, deniedShow, private); w.Code != http.StatusNotFound {
		t.Errorf("denied show_artifact: status = %d, want 404", w.Code)
	}

	// The control: the same call, allowed, still serves.
	allowed := newArtifactSessionWith(t, "write_file", writeOK, private)
	if w := getArtifact(t, srv, allowed, private); w.Code != http.StatusOK {
		t.Errorf("allowed write: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

// Tool-call ids come from the model, so "this id succeeded somewhere in the
// transcript" is not a fact about the call being asked about. Both of these
// borrow an unrelated success to vouch for a denied write.
func TestHandleGetArtifact_ForgedSuccessIsRejected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	artDir := t.TempDir()
	private := filepath.Join(artDir, "private.md")
	if err := os.WriteFile(private, []byte("# my notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	deniedWrite := agent.ContentBlock{
		Type: "tool_use", ID: "X", Name: "write_file",
		Input: map[string]any{"path": private, "content": "x"},
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// Across turns: the denied write in turn 1, then any harmless call in turn 2
	// reusing its id and succeeding.
	crossTurn := agent.NewSession("stub-model", "")
	crossTurn.Messages = append(crossTurn.Messages,
		agent.Message{Role: agent.RoleAssistant, Blocks: []agent.ContentBlock{deniedWrite}},
		agent.Message{Role: agent.RoleUser, Blocks: []agent.ContentBlock{
			agent.NewToolResultBlock("X", "permission_denied: user declined", true),
		}},
		agent.Message{Role: agent.RoleAssistant, Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "X", Name: "terminal",
			Input: map[string]any{"command": "true"},
		}}},
		agent.Message{Role: agent.RoleUser, Blocks: []agent.ContentBlock{
			agent.NewToolResultBlock("X", "ok", false),
		}},
	)
	if err := crossTurn.Save(); err != nil {
		t.Fatal(err)
	}
	if w := getArtifact(t, srv, crossTurn.ID, private); w.Code != http.StatusNotFound {
		t.Errorf("id reused across turns: status = %d, want 404; body=%s", w.Code, w.Body.String())
	}

	// Within one batch: two calls share an id, and the surviving result is the
	// sibling's success (normalizeMessages keeps the first of a duplicate pair).
	sameBatch := agent.NewSession("stub-model", "")
	sameBatch.Messages = append(sameBatch.Messages,
		agent.Message{Role: agent.RoleAssistant, Blocks: []agent.ContentBlock{
			{Type: "tool_use", ID: "X", Name: "terminal", Input: map[string]any{"command": "true"}},
			deniedWrite,
		}},
		agent.Message{Role: agent.RoleUser, Blocks: []agent.ContentBlock{
			agent.NewToolResultBlock("X", "ok", false),
		}},
	)
	if err := sameBatch.Save(); err != nil {
		t.Fatal(err)
	}
	if w := getArtifact(t, srv, sameBatch.ID, private); w.Code != http.StatusNotFound {
		t.Errorf("id reused in one batch: status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// The live case, which is the whole reason an unanswered call is servable. The
// panel is told to fetch from inside dispatchTools: EventToolDone carries the
// ui_payload, the web handler broadcasts it and then persists (ws_handlers.go),
// and the tool_result message is only appended once dispatchTools returns. So
// the transcript the client's fetch reads ends at the tool_use. Requiring a
// result would 404 every write the user just watched happen.
func TestHandleGetArtifact_ServesAWriteStillInFlight(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	artDir := t.TempDir()
	p := filepath.Join(artDir, "report.md")
	if err := os.WriteFile(p, []byte("# report"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Exactly the mid-turn shape: user message, then the assistant's tool_use,
	// and nothing yet after it.
	sess := agent.NewSession("stub-model", "")
	sess.Messages = append(sess.Messages,
		agent.Message{Role: agent.RoleUser, Content: "write the report"},
		agent.Message{Role: agent.RoleAssistant, Blocks: []agent.ContentBlock{{
			Type: "tool_use", ID: "t0", Name: "write_file",
			Input: map[string]any{"path": p, "content": "# report"},
		}}},
	)
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := getArtifact(t, srv, sess.ID, p)
	if w.Code != http.StatusOK {
		t.Fatalf("in-flight write: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "# report" {
		t.Errorf("body = %q", w.Body.String())
	}
}
