package workflow

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// newTestCtx returns a background context (a no-op helper that keeps test
// bodies readable while leaving room for per-test deadline control later).
func newTestCtx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func TestNewRunID_Format(t *testing.T) {
	id := NewRunID()
	if !strings.HasPrefix(id, "wf-") {
		t.Errorf("RunID %q does not start with wf-", id)
	}
	// wf-YYYYMMDD-HHMMSS-xxxxxxxx = 3+8+1+6+1+8 = 27 chars
	if len(id) != 27 {
		t.Errorf("RunID %q has unexpected length %d, want 27", id, len(id))
	}
}

func TestCreateAndLoadJournal(t *testing.T) {
	dir := t.TempDir()
	hash := scriptHash(`agent("hi")`)

	j, err := CreateJournal(dir, "wf-test-001", hash)
	if err != nil {
		t.Fatalf("CreateJournal: %v", err)
	}
	entries := []JournalEntry{
		{Seq: 0, Prompt: "alpha", Reply: "r-alpha", InputTokens: 10, OutputTokens: 20},
		{Seq: 1, Prompt: "beta", Reply: "r-beta", InputTokens: 5, OutputTokens: 15},
	}
	for _, e := range entries {
		if err := j.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, gotHash, err := LoadJournal(dir, "wf-test-001")
	if err != nil {
		t.Fatalf("LoadJournal: %v", err)
	}
	if gotHash != hash {
		t.Errorf("hash = %q, want %q", gotHash, hash)
	}
	if len(got) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(got))
	}
	if got[0].Prompt != "alpha" || got[0].Reply != "r-alpha" {
		t.Errorf("entry[0] = %+v", got[0])
	}
	if got[1].Prompt != "beta" || got[1].OutputTokens != 15 {
		t.Errorf("entry[1] = %+v", got[1])
	}
}

func TestLoadJournal_NotFound(t *testing.T) {
	_, _, err := LoadJournal(t.TempDir(), "wf-nonexistent")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not-found", err)
	}
}

func TestRun_JournalCreatedOnCompletion(t *testing.T) {
	dir := t.TempDir()
	res, err := Run(newTestCtx(t), `agent("hello")`, Options{
		Agent:      echoAgent,
		JournalDir: dir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RunID == "" {
		t.Fatal("Result.RunID is empty")
	}
	if !strings.HasPrefix(res.RunID, "wf-") {
		t.Errorf("RunID = %q, want wf- prefix", res.RunID)
	}

	entries, hash, err := LoadJournal(dir, res.RunID)
	if err != nil {
		t.Fatalf("LoadJournal: %v", err)
	}
	if hash != runIdentityHash(`agent("hello")`, "") {
		t.Errorf("journal script hash mismatch")
	}
	if len(entries) != 1 || entries[0].Prompt != "hello" {
		t.Errorf("entries = %v", entries)
	}
}

func TestRun_ResumeSkipsCachedCall(t *testing.T) {
	dir := t.TempDir()
	script := `agent("hi")`
	hash := runIdentityHash(script, "")

	j, err := CreateJournal(dir, "wf-cached", hash)
	if err != nil {
		t.Fatalf("CreateJournal: %v", err)
	}
	_ = j.Append(JournalEntry{Seq: 0, Prompt: "hi", Reply: "cached-reply"})
	_ = j.Close()

	var callCount int
	res, err := Run(newTestCtx(t), script, Options{
		Agent: func(_ context.Context, _ string, _ AgentOptions) AgentResult {
			callCount++
			return AgentResult{Reply: "fresh-reply"}
		},
		JournalDir: dir,
		ResumeFrom: "wf-cached",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 0 {
		t.Errorf("Agent called %d times, want 0 (should replay from journal)", callCount)
	}
	if res.Output != "cached-reply" {
		t.Errorf("Output = %q, want cached-reply", res.Output)
	}
}

// TestRun_ResumeMixedAgentSkill: agent() and skill() share one token space and
// journal, so a resume replays them by token order regardless of kind — a cached
// agent replays without calling Agent while a not-yet-cached skill runs fresh.
func TestRun_ResumeMixedAgentSkill(t *testing.T) {
	dir := t.TempDir()
	script := `a = agent("x"); s = skill("y"); "#{a}|#{s["v"]}"`
	hash := runIdentityHash(script, "")

	// Cache only the first call (the agent at seq 0); the skill (seq 1) runs fresh.
	j, err := CreateJournal(dir, "wf-mixed", hash)
	if err != nil {
		t.Fatalf("CreateJournal: %v", err)
	}
	_ = j.Append(JournalEntry{Seq: 0, Prompt: "x", Reply: "CACHED_A"})
	_ = j.Close()

	var agentCalls, skillCalls int
	res, err := Run(newTestCtx(t), script, Options{
		Agent: func(_ context.Context, _ string, _ AgentOptions) AgentResult {
			agentCalls++
			return AgentResult{Reply: "FRESH_A"}
		},
		Skill: func(_ context.Context, _, _, _ string) AgentResult {
			skillCalls++
			return AgentResult{Reply: `{"v":"FRESH_S"}`}
		},
		JournalDir: dir,
		ResumeFrom: "wf-mixed",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if agentCalls != 0 {
		t.Errorf("agent called %d times, want 0 (replayed from journal)", agentCalls)
	}
	if skillCalls != 1 {
		t.Errorf("skill called %d times, want 1 (fresh)", skillCalls)
	}
	if res.Output != "CACHED_A|FRESH_S" {
		t.Errorf("Output = %q, want CACHED_A|FRESH_S", res.Output)
	}
}

func TestRun_ResumeContinuesFromCrashPoint(t *testing.T) {
	dir := t.TempDir()
	script := `parallel(["a","b","c"]) { |it| agent(it) }.join(",")`
	hash := runIdentityHash(script, "")

	// Only "a" is cached; "b" and "c" must run fresh.
	j, err := CreateJournal(dir, "wf-partial", hash)
	if err != nil {
		t.Fatalf("CreateJournal: %v", err)
	}
	_ = j.Append(JournalEntry{Seq: 0, Prompt: "a", Reply: "C[a]"})
	_ = j.Close()

	// parallel() calls Agent concurrently, so guard the slice with a mutex.
	var mu sync.Mutex
	var called []string
	res, err := Run(newTestCtx(t), script, Options{
		Agent: func(_ context.Context, prompt string, _ AgentOptions) AgentResult {
			mu.Lock()
			called = append(called, prompt)
			mu.Unlock()
			return AgentResult{Reply: "F[" + prompt + "]"}
		},
		JournalDir: dir,
		ResumeFrom: "wf-partial",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range called {
		if p == "a" {
			t.Errorf("Agent called for cached prompt %q", p)
		}
	}
	if !strings.Contains(res.Output, "C[a]") {
		t.Errorf("cached result missing from output: %q", res.Output)
	}
	if !strings.Contains(res.Output, "F[b]") || !strings.Contains(res.Output, "F[c]") {
		t.Errorf("fresh results missing from output: %q", res.Output)
	}
}

func TestRun_ResumeScriptMismatch(t *testing.T) {
	dir := t.TempDir()
	j, _ := CreateJournal(dir, "wf-old", scriptHash(`agent("original")`))
	_ = j.Close()

	_, err := Run(newTestCtx(t), `agent("different")`, Options{
		Agent:      echoAgent,
		JournalDir: dir,
		ResumeFrom: "wf-old",
	})
	if err == nil || !strings.Contains(err.Error(), "different script") {
		t.Errorf("err = %v, want different-script error", err)
	}
}

func TestRun_NewJournalCreatedOnResume(t *testing.T) {
	dir := t.TempDir()
	script := `agent("x")`
	hash := runIdentityHash(script, "")

	j, _ := CreateJournal(dir, "wf-first", hash)
	_ = j.Append(JournalEntry{Seq: 0, Prompt: "x", Reply: "R[x]"})
	_ = j.Close()

	res, err := Run(newTestCtx(t), script, Options{
		Agent:      echoAgent,
		JournalDir: dir,
		ResumeFrom: "wf-first",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RunID == "" || res.RunID == "wf-first" {
		t.Errorf("RunID = %q; want a fresh run ID distinct from wf-first", res.RunID)
	}
	// New journal is self-contained: it must include the replayed entry.
	entries, _, err := LoadJournal(dir, res.RunID)
	if err != nil {
		t.Fatalf("LoadJournal new: %v", err)
	}
	if len(entries) != 1 || entries[0].Reply != "R[x]" {
		t.Errorf("new journal entries = %v, want replayed entry", entries)
	}
}
