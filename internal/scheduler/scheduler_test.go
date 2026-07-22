package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRenameWithRetry overwrites an existing destination, the case that flakes
// on Windows when the target is momentarily open.
func TestRenameWithRetry(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "t.json")
	if err := os.WriteFile(dst, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := renameWithRetry(tmp, dst); err != nil {
		t.Fatalf("renameWithRetry: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q", got, "new")
	}
}

// recordRunner records every RunTask call for assertions.
type recordRunner struct {
	mu    sync.Mutex
	tasks []Task
}

func (r *recordRunner) CreateSession(t Task) (string, error) {
	return "sess_" + t.ID, nil
}

func (r *recordRunner) RunTask(_ context.Context, t Task) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, t)
	return "sess_" + t.ID, nil
}

func (r *recordRunner) calls() []Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Task(nil), r.tasks...)
}

func newTestScheduler(t *testing.T) (*Scheduler, *recordRunner) {
	t.Helper()
	r := &recordRunner{}
	s, err := New(t.TempDir(), r)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, r
}

func (s *Scheduler) entryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func TestAddRegistersCronEntry(t *testing.T) {
	s, _ := newTestScheduler(t)
	if err := s.Add(&Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "p", Enabled: true}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := len(s.cron.Entries()); got != 1 {
		t.Fatalf("cron entries = %d, want 1", got)
	}
	if got := s.entryCount(); got != 1 {
		t.Fatalf("tracked entries = %d, want 1", got)
	}
}

func TestAddRejectsInvalidCron(t *testing.T) {
	s, _ := newTestScheduler(t)
	// A standard 5-field crontab line is invalid (seconds field required) —
	// it must be rejected even for a disabled task, so a later enable doesn't
	// surface a parse error long after the user typed the expression.
	err := s.Add(&Task{ID: "t1", Name: "n", Cron: "0 9 * * *", Prompt: "p", Enabled: false})
	if err == nil || !strings.Contains(err.Error(), "invalid cron expression") {
		t.Fatalf("Add with 5-field cron: got %v, want invalid-expression error", err)
	}
	if got := len(s.tasks); got != 0 {
		t.Fatalf("tasks stored = %d, want 0", got)
	}
}

func TestNextRun(t *testing.T) {
	s, _ := newTestScheduler(t)
	// cron.Entry.Next is computed by the running scheduler loop, not at
	// registration time — unlike the other tests in this file, which only
	// assert on entry bookkeeping (Entries()/entries map) and never need
	// Start(), this one reads the actual computed time.
	s.Start()
	defer s.Stop()

	if got := s.NextRun("no-such-task"); !got.IsZero() {
		t.Fatalf("NextRun(unknown) = %v, want zero time", got)
	}

	task := Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "p", Enabled: true}
	if err := s.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got := s.NextRun("t1")
	if got.IsZero() {
		t.Fatal("NextRun(enabled) = zero time, want a future time")
	}
	if !got.After(time.Now()) {
		t.Fatalf("NextRun(enabled) = %v, want a time after now", got)
	}

	// Disabling drops the live cron entry, so NextRun has nothing to report.
	task.Enabled = false
	if err := s.Update(task); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := s.NextRun("t1"); !got.IsZero() {
		t.Fatalf("NextRun(disabled) = %v, want zero time", got)
	}

	// Re-enabling with a different expression reschedules and reports the new time.
	task.Enabled = true
	task.Cron = "0 30 18 * * *"
	if err := s.Update(task); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rescheduled := s.NextRun("t1")
	if rescheduled.IsZero() {
		t.Fatal("NextRun(re-enabled) = zero time, want a future time")
	}
	if rescheduled.Equal(got) {
		t.Fatal("NextRun after rescheduling should differ from the disabled reading")
	}
}

func TestUpdateReschedulesEntry(t *testing.T) {
	s, _ := newTestScheduler(t)
	task := Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "p", Enabled: true}
	if err := s.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.mu.Lock()
	oldEntry := s.entries["t1"]
	s.mu.Unlock()

	task.Cron = "0 30 18 * * 1-5"
	if err := s.Update(task); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := len(s.cron.Entries()); got != 1 {
		t.Fatalf("cron entries after update = %d, want 1", got)
	}
	s.mu.Lock()
	newEntry := s.entries["t1"]
	s.mu.Unlock()
	if newEntry == oldEntry {
		t.Fatal("expected a fresh cron entry after cron expression change")
	}
	got, err := s.Get("t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Cron != "0 30 18 * * 1-5" {
		t.Fatalf("stored cron = %q, want updated expression", got.Cron)
	}
}

func TestUpdateRejectsInvalidCron(t *testing.T) {
	s, _ := newTestScheduler(t)
	task := Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "p", Enabled: true}
	if err := s.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	task.Cron = "not a cron"
	if err := s.Update(task); err == nil {
		t.Fatal("Update with bad cron: want error, got nil")
	}
	// The original schedule must survive a rejected update.
	if got := len(s.cron.Entries()); got != 1 {
		t.Fatalf("cron entries = %d, want 1", got)
	}
	got, _ := s.Get("t1")
	if got.Cron != "0 0 9 * * *" {
		t.Fatalf("stored cron = %q, want original expression", got.Cron)
	}
}

func TestDisableRemovesEntryAndStopsRuns(t *testing.T) {
	s, r := newTestScheduler(t)
	task := Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "p", Enabled: true}
	if err := s.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	fire := s.wrap("t1") // simulate the registered cron callback

	task.Enabled = false
	if err := s.Update(task); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := len(s.cron.Entries()); got != 0 {
		t.Fatalf("cron entries after disable = %d, want 0", got)
	}
	// Even a stale callback from before the disable must not run the task.
	fire()
	if got := len(r.calls()); got != 0 {
		t.Fatalf("runner calls after disable = %d, want 0", got)
	}

	// Re-enabling restores the schedule without a restart.
	task.Enabled = true
	if err := s.Update(task); err != nil {
		t.Fatalf("Update re-enable: %v", err)
	}
	if got := len(s.cron.Entries()); got != 1 {
		t.Fatalf("cron entries after re-enable = %d, want 1", got)
	}
}

func TestDeleteRemovesEntryAndStopsRuns(t *testing.T) {
	s, r := newTestScheduler(t)
	if err := s.Add(&Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "p", Enabled: true}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	fire := s.wrap("t1")

	if err := s.Delete("t1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := len(s.cron.Entries()); got != 0 {
		t.Fatalf("cron entries after delete = %d, want 0", got)
	}
	fire()
	if got := len(r.calls()); got != 0 {
		t.Fatalf("runner calls after delete = %d, want 0", got)
	}
}

func TestFireUsesCurrentTaskState(t *testing.T) {
	s, r := newTestScheduler(t)
	task := Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "old prompt", Enabled: true}
	if err := s.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	fire := s.wrap("t1")

	task.Prompt = "new prompt"
	if err := s.Update(task); err != nil {
		t.Fatalf("Update: %v", err)
	}
	fire()
	calls := r.calls()
	if len(calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(calls))
	}
	if calls[0].Prompt != "new prompt" {
		t.Fatalf("fired prompt = %q, want the updated prompt", calls[0].Prompt)
	}
	got, _ := s.Get("t1")
	if got.LastRun.IsZero() || got.SessionID != "sess_t1" {
		t.Fatalf("run bookkeeping not recorded: lastRun=%v session=%q", got.LastRun, got.SessionID)
	}
}

func TestRunNowUnknownTask(t *testing.T) {
	s, r := newTestScheduler(t)
	if _, err := s.RunNow("nope"); err == nil {
		t.Fatal("RunNow on unknown id: want error, got nil")
	}
	if got := len(r.calls()); got != 0 {
		t.Fatalf("runner calls = %d, want 0", got)
	}
}

// waitFor polls cond until it returns true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunNowFiresDisabledTaskAndPersistsBookkeeping(t *testing.T) {
	dir := t.TempDir()
	r := &recordRunner{}
	s, err := New(dir, r)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Disabled on purpose: a manual run must fire regardless — the user
	// explicitly asked for it.
	if err := s.Add(&Task{ID: "t1", Name: "n", Cron: "0 0 9 * * *", Prompt: "p", Enabled: false}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// RunNow creates the fresh session synchronously and returns its id so the
	// UI can open it immediately; the same id is recorded as the task's last
	// run once the turn finishes.
	sessionID, err := s.RunNow("t1")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if sessionID != "sess_t1" {
		t.Fatalf("RunNow sessionID = %q, want sess_t1", sessionID)
	}

	waitFor(t, "runner call", func() bool { return len(r.calls()) == 1 })
	// fire() updates the in-memory task before save() flushes to disk, so wait
	// on the persisted state (via a fresh reload) rather than the in-memory
	// copy — otherwise the reload below can race ahead of the disk write.
	waitFor(t, "persisted bookkeeping", func() bool {
		sr, err := New(dir, r)
		if err != nil {
			return false
		}
		got, err := sr.Get("t1")
		return err == nil && !got.LastRun.IsZero() && got.SessionID == "sess_t1"
	})

	// The bookkeeping must survive a restart, i.e. the JSON file was rewritten.
	s2, err := New(dir, r)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}
	got, err := s2.Get("t1")
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.LastRun.IsZero() || got.SessionID != "sess_t1" {
		t.Fatalf("persisted bookkeeping: lastRun=%v session=%q", got.LastRun, got.SessionID)
	}
}

func TestLoadAllRegistersEnabledTasks(t *testing.T) {
	dir := t.TempDir()
	r := &recordRunner{}
	s, err := New(dir, r)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Add(&Task{ID: "t1", Name: "a", Cron: "0 0 9 * * *", Prompt: "p", Enabled: true}); err != nil {
		t.Fatalf("Add enabled: %v", err)
	}
	if err := s.Add(&Task{ID: "t2", Name: "b", Cron: "@daily", Prompt: "p", Enabled: false}); err != nil {
		t.Fatalf("Add disabled: %v", err)
	}

	// A fresh scheduler over the same dir mirrors a serve restart.
	s2, err := New(dir, r)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}
	if got := len(s2.List()); got != 2 {
		t.Fatalf("loaded tasks = %d, want 2", got)
	}
	if got := len(s2.cron.Entries()); got != 1 {
		t.Fatalf("cron entries after reload = %d, want 1 (only the enabled task)", got)
	}
}

func TestNotifyTargetsUnmarshal_ObjectAndArray(t *testing.T) {
	// A task file written when notify was a single object must keep loading.
	var fromObject Task
	if err := json.Unmarshal([]byte(`{"id":"t1","notify":{"platform":"feishu","chat_id":"oc_1"}}`), &fromObject); err != nil {
		t.Fatalf("unmarshal object form: %v", err)
	}
	if len(fromObject.Notify) != 1 || fromObject.Notify[0].ChatID != "oc_1" {
		t.Fatalf("object form: notify = %v", fromObject.Notify)
	}

	var fromArray Task
	if err := json.Unmarshal([]byte(`{"id":"t2","notify":[{"platform":"feishu","chat_id":"oc_1"},{"platform":"weixin","chat_id":"u_2"}]}`), &fromArray); err != nil {
		t.Fatalf("unmarshal array form: %v", err)
	}
	if len(fromArray.Notify) != 2 || fromArray.Notify[1].Platform != "weixin" {
		t.Fatalf("array form: notify = %v", fromArray.Notify)
	}

	// Round-trip: marshal writes an array, which unmarshals back unchanged.
	b, err := json.Marshal(fromArray)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Task
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(again.Notify) != 2 {
		t.Fatalf("round-trip: notify = %v", again.Notify)
	}
}

// SessionGroupID survives a full save → reload cycle and can be set later via
// SetSessionGroup (the lazy-group path for tasks that predate grouping), so the
// scheduler preserves the task→group link across restarts.
func TestSessionGroupID_PersistsThroughReload(t *testing.T) {
	dir := t.TempDir()
	r := &recordRunner{}
	s, err := New(dir, r)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	task := Task{
		ID:             "t1",
		Name:           "daily report",
		Cron:           "0 0 9 * * *",
		Prompt:         "p",
		Enabled:        true,
		SessionGroupID: "g-abc",
	}
	if err := s.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Reload from disk — a fresh scheduler over the same dir mirrors a serve restart.
	s2, err := New(dir, r)
	if err != nil {
		t.Fatalf("New (reload): %v", err)
	}
	got, err := s2.Get("t1")
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.SessionGroupID != "g-abc" {
		t.Fatalf("SessionGroupID after reload = %q, want %q", got.SessionGroupID, "g-abc")
	}

	// SetSessionGroup persists a new link immediately (used when the group is
	// created lazily on the first run of an old task).
	if err := s2.SetSessionGroup("t1", "g-xyz"); err != nil {
		t.Fatalf("SetSessionGroup: %v", err)
	}
	s3, _ := New(dir, r)
	reloaded, _ := s3.Get("t1")
	if reloaded.SessionGroupID != "g-xyz" {
		t.Fatalf("SessionGroupID after SetSessionGroup+reload = %q, want %q", reloaded.SessionGroupID, "g-xyz")
	}
}

// sessionTracker is a Runner that hands out unique, distinguishable session
// IDs per CreateSession call — so a test can tell whether RunNow handed the
// UI back a session that RunTask later reused, or whether two independent
// sessions were created (the bug).
type sessionTracker struct {
	mu       sync.Mutex
	createID int
	tasks    []Task
	created  []string // session IDs returned by CreateSession, in order
}

func (r *sessionTracker) CreateSession(t Task) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := fmt.Sprintf("sess_%d", r.createID)
	r.createID++
	r.created = append(r.created, id)
	return id, nil
}

func (r *sessionTracker) RunTask(_ context.Context, t Task) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, t)
	// RunTask creates its own session in the real flow; mirror that with a
	// fresh unique ID so the test can see which session the turn landed in.
	id := fmt.Sprintf("sess_%d", r.createID)
	r.createID++
	return id, nil
}

// TestRunNow_CreatesFreshSessionAndReturnsID verifies that RunNow creates
// exactly one fresh session (returning its id for the UI), and that the turn
// runs in that same session — never a second, orphaned one. The old bug
// pre-created a session A, then RunTask created session B, leaving the user
// staring at an empty A while work landed in B.
func TestRunNow_CreatesFreshSessionAndReturnsID(t *testing.T) {
	dir := t.TempDir()
	r := &sessionTracker{}
	s, err := New(dir, r)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	task := Task{
		ID:      "t_fresh",
		Name:    "weekly triage",
		Cron:    "0 0 9 * * *",
		Prompt:  "triage",
		Enabled: true,
	}
	if err := s.Add(&task); err != nil {
		t.Fatalf("Add: %v", err)
	}

	returnedID, err := s.RunNow("t_fresh")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if returnedID == "" {
		t.Fatal("RunNow should return the freshly created session id")
	}

	// Let the background run finish.
	time.Sleep(200 * time.Millisecond)

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.created) != 1 {
		t.Fatalf("expected exactly 1 session created (by RunNow), got %d: %v", len(r.created), r.created)
	}
	if r.created[0] != returnedID {
		t.Errorf("RunNow returned %q but CreateSession produced %q", returnedID, r.created[0])
	}
	if len(r.tasks) != 1 {
		t.Fatalf("expected exactly 1 RunTask call, got %d", len(r.tasks))
	}
	// The turn must run in the pre-created session, not a fresh one.
	if r.tasks[0].SessionID != returnedID {
		t.Errorf("RunTask ran in session %q; want the pre-created %q", r.tasks[0].SessionID, returnedID)
	}
}
