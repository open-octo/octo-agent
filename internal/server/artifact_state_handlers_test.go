package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/tools"
)

// putArtifactState relays a snapshot the way the web UI does: multipart, with
// the screenshot as a file part.
func putArtifactState(t *testing.T, srv *Server, sessionID, path, digest string, image []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if digest != "" {
		_ = mw.WriteField("digest", digest)
	}
	if len(image) > 0 {
		part, err := mw.CreateFormFile("image", "state.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(image); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("PUT", "/api/sessions/"+sessionID+"/artifacts/state?path="+url.QueryEscape(path), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	return w
}

func TestPutArtifactState_RoundTrip(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	path := t.TempDir() + "/page.html"
	sid := newArtifactSession(t, path)
	t.Cleanup(func() { tools.DropArtifact(sid, path) })

	w := putArtifactState(t, srv, sid, path, "a bar chart of Q3", []byte("PNGDATA"))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The tools read the same mirror, so asking them is the real assertion.
	res, err := tools.DefaultRegistry{}.Execute(tools.WithSessionID(context.Background(), sid), "artifact_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{path, "a bar chart of Q3", "screenshot"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("mirror is missing %q:\n%s", want, res.Text)
		}
	}
}

// TestPutArtifactState_RejectsForeignPath: the mirror key is not something a
// relaying page gets to assert — a path this session never wrote is the same
// 404 the preview endpoint gives it.
func TestPutArtifactState_RejectsForeignPath(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	sid := newArtifactSession(t, t.TempDir()+"/mine.html")

	w := putArtifactState(t, srv, sid, t.TempDir()+"/not-mine.html", "intruder", nil)
	if w.Code != 404 {
		t.Fatalf("expected 404 for a path the session never wrote, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDeleteArtifactState_Drops: the host's DELETE on iframe unmount is the
// normal way out of the mirror.
func TestDeleteArtifactState_Drops(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	path := t.TempDir() + "/page.html"
	sid := newArtifactSession(t, path)
	t.Cleanup(func() { tools.DropArtifact(sid, path) })

	if w := putArtifactState(t, srv, sid, path, "open", nil); w.Code != 200 {
		t.Fatalf("put: %d", w.Code)
	}

	req := httptest.NewRequest("DELETE", "/api/sessions/"+sid+"/artifacts/state?path="+url.QueryEscape(path), nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}

	res, err := tools.DefaultRegistry{}.Execute(tools.WithSessionID(context.Background(), sid), "artifact_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, path) {
		t.Errorf("a dropped page is still being reported:\n%s", res.Text)
	}
}
