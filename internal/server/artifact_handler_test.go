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
	writeOK         writeOutcome = iota // succeeded
	writeDenied                         // the permission gate refused it
	writeUnanswered                     // no tool_result at all
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
		if outcome == writeUnanswered {
			continue
		}
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
	// Written by the session but not a previewable type.
	goPath := filepath.Join(artDir, "main.go")
	if err := os.WriteFile(goPath, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	// UI payloads carry absolute paths, so the transcript stores absolute paths.
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	id := newArtifactSession(t, htmlPath, goPath)
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
	// Written but not a previewable extension → 404.
	if w := getArtifact(t, srv, id, goPath); w.Code != http.StatusNotFound {
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

	// An unanswered call proves nothing either — a crashed turn, or the orphan
	// repair stamping an error over an interrupted batch.
	unanswered := newArtifactSessionWith(t, "write_file", writeUnanswered, private)
	if w := getArtifact(t, srv, unanswered, private); w.Code != http.StatusNotFound {
		t.Errorf("unanswered write: status = %d, want 404", w.Code)
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
