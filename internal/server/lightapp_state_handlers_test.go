package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/tools"
)

// putState relays a snapshot the way the web UI does: multipart, with the
// screenshot as a file part.
func putState(t *testing.T, srv *Server, slug, digest, summary string, image []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if digest != "" {
		_ = mw.WriteField("digest", digest)
	}
	if summary != "" {
		_ = mw.WriteField("summary", summary)
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

	req := httptest.NewRequest("PUT", "/api/light-apps/"+slug+"/state", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	return w
}

func TestPutLightAppState_RoundTrip(t *testing.T) {
	t.Cleanup(func() { tools.DropLightApp("sketch") })
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := putState(t, srv, "sketch", "2 strokes, 1 selected", `{"nodes":2}`, []byte("PNGDATA"))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The tools read the same mirror, so asking them is the real assertion.
	res, err := tools.DefaultRegistry{}.Execute(t.Context(), "lightapp_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sketch", "2 strokes, 1 selected", `{"nodes":2}`, "screenshot"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("mirror is missing %q:\n%s", want, res.Text)
		}
	}
}

// TestPutLightAppState_RejectsBadSummary: the summary is handed to the model
// verbatim, so a malformed blob is dropped rather than relayed as garbage.
func TestPutLightAppState_RejectsBadSummary(t *testing.T) {
	t.Cleanup(func() { tools.DropLightApp("sketch") })
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	if w := putState(t, srv, "sketch", "a digest", "{not json", nil); w.Code != 200 {
		t.Fatalf("a bad summary should not fail the push: %d", w.Code)
	}
	res, _ := tools.DefaultRegistry{}.Execute(t.Context(), "lightapp_state", nil)
	if !strings.Contains(res.Text, "a digest") {
		t.Error("the digest should still have landed")
	}
	if strings.Contains(res.Text, "not json") {
		t.Error("a malformed summary must not reach the model")
	}
}

func TestPutLightAppState_RejectsBadSlug(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, slug := range []string{"..", "a/b"} {
		req := httptest.NewRequest("PUT", "/api/light-apps/"+slug+"/state", strings.NewReader(""))
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		if w.Code == 200 {
			t.Errorf("slug %q was accepted", slug)
		}
	}
}

// TestDeleteLightAppState: closing the frame takes the app out of what the
// model sees.
func TestDeleteLightAppState(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	if w := putState(t, srv, "sketch", "something", "", nil); w.Code != 200 {
		t.Fatalf("seed push failed: %d", w.Code)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/light-apps/sketch/state", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	res, _ := tools.DefaultRegistry{}.Execute(t.Context(), "lightapp_state", nil)
	if !strings.Contains(res.Text, "No Light App") {
		t.Errorf("the app survived the drop:\n%s", res.Text)
	}
}
