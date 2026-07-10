package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// multipartUpload builds a POST /api/upload body carrying one file under the
// "files" field the handler reads.
func multipartUpload(t *testing.T, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("files", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// A normal-sized upload of any type is saved and gets an /api/uploads/ URL back.
func TestHandleUpload_AcceptsFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()

	body, ct := multipartUpload(t, "report.pdf", []byte("%PDF-1.4 not really"))
	resp, err := http.Post(ts.URL+"/api/upload", ct, body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Files []struct {
			URL   string `json:"url"`
			Error string `json:"error"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Files) != 1 || out.Files[0].Error != "" {
		t.Fatalf("files = %+v", out.Files)
	}
	if !strings.HasPrefix(out.Files[0].URL, "/api/uploads/") {
		t.Errorf("url = %q, want /api/uploads/ prefix", out.Files[0].URL)
	}
}

// A body over maxUploadBytes is rejected before it can spill to disk — the
// MaxBytesReader guard, not the (memory-only) ParseMultipartForm argument.
func TestHandleUpload_RejectsOversized(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()

	body, ct := multipartUpload(t, "huge.bin", make([]byte, maxUploadBytes+1))
	resp, err := http.Post(ts.URL+"/api/upload", ct, body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for oversized upload", resp.StatusCode)
	}
}
