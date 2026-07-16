package agent

import (
	"os"
	"strings"
	"testing"
)

// TestHistoryRewriteDirty checks that every non-append mutation marks the
// history as rewritten and that plain appends do not — Session.SyncFrom
// relies on this to know when the persisted file's prefix went stale.
func TestHistoryRewriteDirty(t *testing.T) {
	mutations := []struct {
		name string
		do   func(h *History)
	}{
		{"ReplaceAll", func(h *History) { h.ReplaceAll([]Message{NewUserMessage("x")}) }},
		{"Reset", func(h *History) { h.Reset() }},
		{"TruncateTo", func(h *History) { h.TruncateTo(1) }},
		{"replaceLast", func(h *History) { h.replaceLast(NewUserMessage("y")) }},
		{"popLast", func(h *History) { h.popLast() }},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			h := NewHistory()
			h.Append(NewUserMessage("a"))
			h.Append(NewAssistantMessage("b"))
			if h.RewriteDirty() {
				t.Fatal("Append alone must not mark history rewritten")
			}
			m.do(h)
			if !h.RewriteDirty() {
				t.Fatalf("%s did not mark history rewritten", m.name)
			}
			if !h.takeRewriteDirty() {
				t.Fatal("takeRewriteDirty returned false on a dirty history")
			}
			if h.RewriteDirty() || h.takeRewriteDirty() {
				t.Fatal("takeRewriteDirty did not clear the flag")
			}
		})
	}
}

// TestSessionSave_RewriteAfterHistoryRewrite is the corruption guard for
// incremental mid-turn saves: a history rewrite (compaction) that leaves the
// message count at or above the persisted count used to slip past Save's
// length check, appending on top of a stale prefix. SyncFrom must carry the
// rewrite over so Save rewrites the file.
func TestSessionSave_RewriteAfterHistoryRewrite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := NewSession("m", "")
	h := NewHistory()
	for _, m := range []Message{
		NewUserMessage("one"), NewAssistantMessage("two"), NewUserMessage("three"),
	} {
		h.Append(m)
	}
	sess.SyncFrom(h)
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Rewrite history to the SAME length with a different prefix — the case
	// the pure length check cannot see.
	h.ReplaceAll([]Message{
		NewUserMessage("[summary]"), NewUserMessage("three"), NewAssistantMessage("four"),
	})
	sess.SyncFrom(h)
	if err := sess.Save(); err != nil {
		t.Fatalf("save after rewrite: %v", err)
	}

	got, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Messages) != 3 || got.Messages[0].Content != "[summary]" || got.Messages[2].Content != "four" {
		t.Fatalf("persisted messages do not match rewritten history: %+v", got.Messages)
	}
}

// TestSessionSave_NoopWhenUnchanged checks the fast path the server's
// per-event persister depends on: a Save with no new messages must leave the
// file byte-for-byte untouched.
func TestSessionSave_NoopWhenUnchanged(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := NewSession("m", "")
	sess.Messages = []Message{NewUserMessage("hello")}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	path, _ := sess.SavePath()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := sess.Save(); err != nil {
		t.Fatalf("second save: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("no-op Save modified the file")
	}
}

// TestLoadSession_DropsPartialTail simulates a crash mid-append: the file
// ends in an incomplete JSON line. Loading must succeed with the complete
// records, and the next Save must rewrite the file so the dangling bytes
// can't fuse with a future append into one corrupt line.
func TestLoadSession_DropsPartialTail(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := NewSession("m", "")
	sess.Messages = []Message{NewUserMessage("hello"), NewAssistantMessage("world")}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	path, _ := sess.SavePath()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString(`{"type":"message","message":{"content":"PARTIAL-TAIL`); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	f.Close()

	got, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load with partial tail: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (partial tail dropped)", len(got.Messages))
	}

	// Resume the session: append a message and save. The partial tail must
	// not survive into the rewritten file.
	got.Messages = append(got.Messages, NewUserMessage("again"))
	if err := got.Save(); err != nil {
		t.Fatalf("save after tail drop: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "PARTIAL-TAIL") {
		t.Fatal("partial tail bytes survived the post-recovery save")
	}
	re, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(re.Messages) != 3 || re.Messages[2].Content != "again" {
		t.Fatalf("reloaded messages = %+v, want 3 ending in \"again\"", re.Messages)
	}
}

// TestSessionBoundRoundTrip checks that BoundEntry and BoundAt survive a save/load cycle.
func TestSessionBoundRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := NewSession("m", "")
	sess.Bind(EntryTUI, false)
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.BoundTo(EntryTUI) {
		t.Fatalf("bound entry = %q, want %q", loaded.BoundEntry, EntryTUI)
	}
	if loaded.BoundAt.IsZero() {
		t.Fatal("BoundAt not persisted")
	}
}

// TestSetTitle_RewritesAfterPartialTail: SetTitle appends a raw title record;
// after a crash left a partial tail it must rewrite instead, or the title
// record fuses with the dangling bytes into one corrupt line.
func TestSetTitle_RewritesAfterPartialTail(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := NewSession("m", "")
	sess.Messages = []Message{NewUserMessage("hello")}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	path, _ := sess.SavePath()
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	f.WriteString(`{"type":"mess`)
	f.Close()

	got, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := got.SetTitle("My Title"); err != nil {
		t.Fatalf("set title: %v", err)
	}
	re, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload after SetTitle: %v", err)
	}
	if re.Title != "My Title" || len(re.Messages) != 1 {
		t.Fatalf("reloaded title=%q messages=%d, want \"My Title\"/1", re.Title, len(re.Messages))
	}
}

// TestSetLastContextTokens_AppendsAndRoundTrips covers the path a real turn
// takes: an already-persisted session gets its context-token count via an
// APPENDED "context_tokens" record (O(1), not a full rewrite), the no-op guards
// hold, and the value round-trips through a reload.
func TestSetLastContextTokens_AppendsAndRoundTrips(t *testing.T) {
	setTempHome(t)
	s := NewSession("m", "")
	s.Messages = []Message{NewUserMessage("hi"), NewAssistantMessage("hello")}
	if err := s.Save(); err != nil { // persisted > 0, transcript on disk
		t.Fatalf("Save: %v", err)
	}

	if err := s.SetLastContextTokens(4321); err != nil {
		t.Fatalf("SetLastContextTokens: %v", err)
	}

	path, err := s.SavePath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Must APPEND a context_tokens record, not rewrite the whole file.
	if !strings.Contains(string(data), `"type":"context_tokens"`) {
		t.Fatalf("expected an appended context_tokens record; file was:\n%s", data)
	}

	// No-op guards: an unchanged value and a non-positive value append nothing.
	before := len(data)
	if err := s.SetLastContextTokens(4321); err != nil { // unchanged
		t.Fatal(err)
	}
	if err := s.SetLastContextTokens(0); err != nil { // non-positive
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != before {
		t.Errorf("no-op SetLastContextTokens must not append: file grew %d -> %d bytes", before, len(after))
	}

	// Round-trips through a reload.
	got, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got.LastContextTokens != 4321 {
		t.Errorf("reloaded LastContextTokens = %d, want 4321", got.LastContextTokens)
	}
}

// PersistContextUsage records the agent's current context-token count on the
// session and round-trips it, and is a safe no-op with a nil session or no
// count. Every transport (web, IM, CLI, scheduled) calls it at turn end so a
// session opened in the Web UI shows accurate usage regardless of where it ran.
func TestPersistContextUsage(t *testing.T) {
	setTempHome(t)

	a := New(&summarizeFake{}, "m")
	// The helper's purpose is persisting the real provider-reported count; set it
	// directly (in-package) so the assertion pins the exact value, not an estimate.
	a.usageMu.Lock()
	a.lastInputTokens = 12345
	a.usageMu.Unlock()

	sess := NewSession("m", "")
	sess.Messages = []Message{NewUserMessage("hi")}
	if err := sess.Save(); err != nil { // persisted, on disk
		t.Fatalf("Save: %v", err)
	}

	if err := a.PersistContextUsage(sess); err != nil {
		t.Fatalf("PersistContextUsage: %v", err)
	}
	if sess.LastContextTokens != 12345 {
		t.Fatalf("LastContextTokens = %d, want 12345 (the real provider count)", sess.LastContextTokens)
	}
	reloaded, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LastContextTokens != 12345 {
		t.Errorf("reloaded LastContextTokens = %d, want 12345", reloaded.LastContextTokens)
	}

	// A nil session and an agent with no countable context are safe no-ops.
	if err := a.PersistContextUsage(nil); err != nil {
		t.Errorf("nil session should be a no-op, got %v", err)
	}
	empty := New(&summarizeFake{}, "m")
	s2 := NewSession("m", "")
	if err := empty.PersistContextUsage(s2); err != nil {
		t.Errorf("no-count should be a no-op, got %v", err)
	}
	if s2.LastContextTokens != 0 {
		t.Errorf("no-count must not set a token count, got %d", s2.LastContextTokens)
	}
}

// TestBranchFrom verifies that BranchFrom copies meta fields and the first
// uptoIdx messages, sets BranchedFrom, and survives a Save/Load roundtrip.
func TestBranchFrom(t *testing.T) {
	setTempHome(t)

	src := NewSession("claude-sonnet-4-20250514", "you are a helpful assistant")
	src.WorkingDir = "/tmp/project"
	src.PermissionMode = "auto"
	src.ModelConfig = "claude-sonnet-4-20250514"
	src.Messages = []Message{
		NewUserMessage("hello"),
		NewAssistantMessage("hi there"),
		NewUserMessage("branch from here"),
		NewAssistantMessage("this should not appear in branch"),
	}
	if err := src.Save(); err != nil {
		t.Fatalf("src Save: %v", err)
	}

	branch := BranchFrom(src, 3) // copy first 3 messages (up to "branch from here")
	if branch.BranchedFrom != src.ID {
		t.Errorf("BranchedFrom = %q, want %q", branch.BranchedFrom, src.ID)
	}
	if branch.Model != src.Model {
		t.Errorf("Model = %q, want %q", branch.Model, src.Model)
	}
	if branch.System != src.System {
		t.Errorf("System mismatch")
	}
	if branch.WorkingDir != src.WorkingDir {
		t.Errorf("WorkingDir = %q, want %q", branch.WorkingDir, src.WorkingDir)
	}
	if branch.PermissionMode != src.PermissionMode {
		t.Errorf("PermissionMode = %q, want %q", branch.PermissionMode, src.PermissionMode)
	}
	if len(branch.Messages) != 3 {
		t.Fatalf("Messages len = %d, want 3", len(branch.Messages))
	}
	if branch.Messages[2].Content != "branch from here" {
		t.Errorf("last message = %q, want %q", branch.Messages[2].Content, "branch from here")
	}

	if err := branch.Save(); err != nil {
		t.Fatalf("branch Save: %v", err)
	}
	reloaded, err := LoadSession(branch.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if reloaded.BranchedFrom != src.ID {
		t.Errorf("reloaded BranchedFrom = %q, want %q", reloaded.BranchedFrom, src.ID)
	}
	if len(reloaded.Messages) != 3 {
		t.Errorf("reloaded Messages len = %d, want 3", len(reloaded.Messages))
	}
}

// TestBranchFrom_Clamp verifies that out-of-range indices are clamped.
func TestBranchFrom_Clamp(t *testing.T) {
	src := NewSession("m", "")
	src.Messages = []Message{NewUserMessage("a"), NewAssistantMessage("b")}

	// negative index → 0 messages
	branch := BranchFrom(src, -1)
	if len(branch.Messages) != 0 {
		t.Errorf("negative idx: Messages len = %d, want 0", len(branch.Messages))
	}

	// index beyond length → all messages
	branch = BranchFrom(src, 99)
	if len(branch.Messages) != 2 {
		t.Errorf("overflow idx: Messages len = %d, want 2", len(branch.Messages))
	}
}
