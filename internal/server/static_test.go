package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The built UI is exercised over an in-memory dist: webdist/ holds only
// .gitkeep in a checkout, so the real embed can't be used here.
func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             {Data: []byte(`<html><script src="/assets/index-abc123.js"></script></html>`)},
		"assets/index-abc123.js": {Data: []byte(strings.Repeat("console.log('octo');\n", 200))},
		"assets/logo.png":        {Data: []byte{0x89, 'P', 'N', 'G', 0, 0, 0, 0}},
	}
}

func getStatic(t *testing.T, h http.Handler, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func gunzip(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return string(out)
}

// Hashed assets are immutable and gzipped when the client accepts it; the
// body must round-trip to the original bytes.
func TestStaticFileHandler_HashedAssetImmutableAndGzipped(t *testing.T) {
	dist := testDist()
	w := getStatic(t, staticFileHandler(dist), "/assets/index-abc123.js", map[string]string{"Accept-Encoding": "gzip, deflate, br"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	if v := w.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding", v)
	}
	if cl := w.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q, want unset (describes the uncompressed body)", cl)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
	want := string(dist["assets/index-abc123.js"].Data)
	if got := gunzip(t, w.Body.Bytes()); got != want {
		t.Errorf("gunzipped body differs from the asset (%d vs %d bytes)", len(got), len(want))
	}
	if w.Body.Len() >= len(want) {
		t.Errorf("compressed body %d bytes is not smaller than the %d-byte asset", w.Body.Len(), len(want))
	}
}

// Without Accept-Encoding the asset is served verbatim, with its real length.
func TestStaticFileHandler_IdentityWhenNotAccepted(t *testing.T) {
	dist := testDist()
	w := getStatic(t, staticFileHandler(dist), "/assets/index-abc123.js", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none", ce)
	}
	if got, want := w.Body.String(), string(dist["assets/index-abc123.js"].Data); got != want {
		t.Errorf("body differs from the asset")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
}

// Already-compressed formats are never gzipped, even when accepted — but they
// are still hashed assets and cache forever.
func TestStaticFileHandler_ImageNotCompressed(t *testing.T) {
	dist := testDist()
	w := getStatic(t, staticFileHandler(dist), "/assets/logo.png", map[string]string{"Accept-Encoding": "gzip"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none for a PNG", ce)
	}
	if got, want := w.Body.Bytes(), dist["assets/logo.png"].Data; string(got) != string(want) {
		t.Errorf("PNG body altered")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
}

// A Range request must get real bytes of the file, never a slice of gzip output.
func TestStaticFileHandler_RangeBypassesGzip(t *testing.T) {
	dist := testDist()
	w := getStatic(t, staticFileHandler(dist), "/assets/index-abc123.js", map[string]string{
		"Accept-Encoding": "gzip",
		"Range":           "bytes=0-9",
	})

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", w.Code)
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none on a range", ce)
	}
	if got, want := w.Body.String(), string(dist["assets/index-abc123.js"].Data[:10]); got != want {
		t.Errorf("range body = %q, want %q", got, want)
	}
}

// The entrypoint — served at "/" and for every SPA route — is unhashed, so it
// must be revalidated on each load, and it is gzipped like any other text.
func TestStaticFileHandler_IndexNoCacheAndGzipped(t *testing.T) {
	dist := testDist()
	h := staticFileHandler(dist)
	for _, target := range []string{"/", "/some/spa/route"} {
		w := getStatic(t, h, target, map[string]string{"Accept-Encoding": "gzip"})
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", target, w.Code)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want no-cache", target, cc)
		}
		if ce := w.Header().Get("Content-Encoding"); ce != "gzip" {
			t.Fatalf("%s: Content-Encoding = %q, want gzip", target, ce)
		}
		if got, want := gunzip(t, w.Body.Bytes()), string(dist["index.html"].Data); got != want {
			t.Errorf("%s: gunzipped body differs from index.html", target)
		}
	}
}

// HEAD carries no body, so the gzip writer must not append its header/trailer
// to an otherwise empty response.
func TestStaticFileHandler_HeadHasNoBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodHead, "/assets/index-abc123.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	staticFileHandler(testDist()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("HEAD body = %d bytes, want 0", w.Body.Len())
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none on HEAD", ce)
	}
}
