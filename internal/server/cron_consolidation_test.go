package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createTaskForTest POSTs a task and returns its id.
func createTaskForTest(t *testing.T, srv *Server, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.ID == "" {
		t.Fatalf("create response missing id: %v (%s)", err, w.Body.String())
	}
	return resp.ID
}

func listTasksForTest(t *testing.T, srv *Server) []taskResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var tasks []taskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("list decode: %v (%s)", err, w.Body.String())
	}
	return tasks
}

// TestCreateTask_PersistsDirectory guards the fix for directory being silently
// dropped on create: a POSTed directory must round-trip through the list.
func TestCreateTask_PersistsDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	createTaskForTest(t, srv, `{"name":"dirtask","cron":"0 0 9 * * *","prompt":"go","directory":"/srv/repo"}`)

	tasks := listTasksForTest(t, srv)
	if len(tasks) != 1 {
		t.Fatalf("want 1 task, got %d", len(tasks))
	}
	if tasks[0].Directory != "/srv/repo" {
		t.Errorf("directory = %q, want /srv/repo (dropped on create?)", tasks[0].Directory)
	}
}

// TestPatchTask_UpdatesFields verifies the single PATCH endpoint applies a
// partial update across fields (including the former toggle's enabled flip).
func TestPatchTask_UpdatesFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	id := createTaskForTest(t, srv, `{"name":"edit-me","cron":"0 0 9 * * *","prompt":"old"}`)

	patch := `{"prompt":"new prompt","directory":"/w","enabled":false}`
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id, strings.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got taskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("patch decode: %v", err)
	}
	if got.Prompt != "new prompt" {
		t.Errorf("prompt = %q, want updated", got.Prompt)
	}
	if got.Directory != "/w" {
		t.Errorf("directory = %q, want /w", got.Directory)
	}
	if got.Enabled {
		t.Errorf("enabled = true, want false (toggle via PATCH)")
	}
	// Cron was not in the patch — it must be left untouched.
	if got.Cron != "0 0 9 * * *" {
		t.Errorf("cron = %q, want unchanged", got.Cron)
	}

	// A PATCH to an unknown id is a 404, not a silent no-op.
	req = httptest.NewRequest(http.MethodPatch, "/api/tasks/task_missing", strings.NewReader(`{"enabled":true}`))
	w = httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("patch unknown id status = %d, want 404", w.Code)
	}
}

// TestRetiredCronEndpoints_NotRouted confirms the consolidated surface no longer
// serves the removed /toggle and /api/cron-tasks/* routes.
func TestRetiredCronEndpoints_NotRouted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	cases := []struct {
		method, path string
	}{
		{http.MethodPatch, "/api/tasks/task_x/toggle"},
		{http.MethodGet, "/api/cron-tasks"},
		{http.MethodPost, "/api/cron-tasks/foo/run"},
		{http.MethodPatch, "/api/cron-tasks/foo"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404 (route should be gone)", tc.method, tc.path, w.Code)
		}
	}
}
