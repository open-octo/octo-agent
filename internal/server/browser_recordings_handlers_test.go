package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recordingBody(t *testing.T, yaml string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"yaml": yaml})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const sampleRecordingYAML = `name: demo-flow
description: a demo
params:
  - name: q
    default: hello
steps:
  - action: navigate
    url: https://example.com
  - action: click
    selector: "#go"
`

func seedRecording(t *testing.T, name, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "skills")
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBrowserRecordings_ListGetDelete(t *testing.T) {
	seedRecording(t, "demo-flow", sampleRecordingYAML)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// List
	w := doJSON(t, srv, http.MethodGet, "/api/browser/recordings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "demo-flow") || !strings.Contains(body, `"steps":2`) {
		t.Errorf("list missing recording/step count: %s", body)
	}

	// Get raw YAML
	w = doJSON(t, srv, http.MethodGet, "/api/browser/recordings/demo-flow", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "action: navigate") {
		t.Errorf("get = %d, body=%s", w.Code, w.Body.String())
	}

	// Delete then 404
	if w := doJSON(t, srv, http.MethodDelete, "/api/browser/recordings/demo-flow", ""); w.Code != http.StatusOK {
		t.Errorf("delete = %d", w.Code)
	}
	if w := doJSON(t, srv, http.MethodGet, "/api/browser/recordings/demo-flow", ""); w.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", w.Code)
	}
}

func TestBrowserRecordings_SaveValidatesYAML(t *testing.T) {
	seedRecording(t, "", "")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	// Valid YAML saves.
	if w := doJSON(t, srv, http.MethodPut, "/api/browser/recordings/demo-flow",
		recordingBody(t, sampleRecordingYAML)); w.Code != http.StatusOK {
		t.Fatalf("save valid = %d: %s", w.Code, w.Body.String())
	}
	// Malformed YAML is rejected.
	if w := doJSON(t, srv, http.MethodPut, "/api/browser/recordings/demo-flow",
		recordingBody(t, "name: x\n  bad: [")); w.Code != http.StatusBadRequest {
		t.Errorf("save malformed = %d, want 400", w.Code)
	}
	// A stepless skill is rejected.
	if w := doJSON(t, srv, http.MethodPut, "/api/browser/recordings/demo-flow",
		recordingBody(t, "name: empty\n")); w.Code != http.StatusBadRequest {
		t.Errorf("save stepless = %d, want 400", w.Code)
	}
}

func TestBrowserRecordings_RejectsUnsafeName(t *testing.T) {
	seedRecording(t, "", "")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	for _, bad := range []string{"..%2f", "a/b"} {
		if w := doJSON(t, srv, http.MethodGet, "/api/browser/recordings/"+bad, ""); w.Code == http.StatusOK {
			t.Errorf("unsafe name %q accepted (code %d)", bad, w.Code)
		}
	}
}
