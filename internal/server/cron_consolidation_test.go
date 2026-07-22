package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
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

// groupByID returns the session group with the given id, or nil.
func groupByID(t *testing.T, id string) *sessionGroup {
	t.Helper()
	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("loadSessionGroups: %v", err)
	}
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i]
		}
	}
	return nil
}

// TestCreateTask_CreatesSessionGroup: creating a task creates a session group
// named after it and links the task to it via session_group_id.
func TestCreateTask_CreatesSessionGroup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	id := createTaskForTest(t, srv, `{"name":"daily-report","cron":"0 0 9 * * *","prompt":"go"}`)

	tasks := listTasksForTest(t, srv)
	var task *taskResponse
	for i := range tasks {
		if tasks[i].ID == id {
			task = &tasks[i]
		}
	}
	if task == nil {
		t.Fatalf("task %q not in list", id)
	}
	if task.SessionGroupID == "" {
		t.Fatal("task.session_group_id is empty; create should link a group")
	}
	g := groupByID(t, task.SessionGroupID)
	if g == nil {
		t.Fatalf("no group with id %q", task.SessionGroupID)
	}
	if g.Name != "daily-report" {
		t.Errorf("group name = %q, want %q", g.Name, "daily-report")
	}
}

// TestPatchTask_RenamesSessionGroup: renaming a task via PATCH renames its
// session group in lockstep.
func TestPatchTask_RenamesSessionGroup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	id := createTaskForTest(t, srv, `{"name":"old-name","cron":"0 0 9 * * *","prompt":"go"}`)
	groupID := listTasksForTest(t, srv)[0].SessionGroupID

	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id, strings.NewReader(`{"name":"new-name"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200: %s", w.Code, w.Body.String())
	}

	g := groupByID(t, groupID)
	if g == nil {
		t.Fatalf("group %q vanished after rename", groupID)
	}
	if g.Name != "new-name" {
		t.Errorf("group name = %q, want %q (should follow task rename)", g.Name, "new-name")
	}
}

// TestPatchTask_RejectsEmptyName: PATCH must reject an empty/blank name rather
// than blanking the task and its group.
func TestPatchTask_RejectsEmptyName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	id := createTaskForTest(t, srv, `{"name":"keep","cron":"0 0 9 * * *","prompt":"go"}`)

	for _, body := range []string{`{"name":""}`, `{"name":"   "}`} {
		req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("patch %s status = %d, want 400", body, w.Code)
		}
	}
	// The task name is untouched.
	if got := listTasksForTest(t, srv)[0].Name; got != "keep" {
		t.Errorf("task name = %q, want unchanged %q", got, "keep")
	}
}

// TestDeleteTask_DeletesGroupKeepsSessions: deleting a task deletes its group,
// but the group's member sessions stay on disk (they fall back to ungrouped).
func TestDeleteTask_DeletesGroupKeepsSessions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	id := createTaskForTest(t, srv, `{"name":"doomed","cron":"0 0 9 * * *","prompt":"go"}`)
	groupID := listTasksForTest(t, srv)[0].SessionGroupID

	// Put a real session in the group so we can prove it survives.
	sess := agent.NewSession("m", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if err := addSessionToGroup(groupID, sess.ID); err != nil {
		t.Fatalf("addSessionToGroup: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/tasks/"+id, nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200: %s", w.Code, w.Body.String())
	}

	if g := groupByID(t, groupID); g != nil {
		t.Errorf("group %q still present after task delete", groupID)
	}
	if _, err := agent.LoadSession(sess.ID); err != nil {
		t.Errorf("session %q was deleted with the task; it must survive: %v", sess.ID, err)
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
