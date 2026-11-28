package agent

import (
	"testing"
)

// TestHistoryEpoch_BumpsOnShrink pins the destructive-rewrite signal the web
// client's reconnect resync relies on (#2745): truncating the persisted
// messages and saving bumps the epoch, the value round-trips through the meta
// line, and a later append-only save leaves it alone.
func TestHistoryEpoch_BumpsOnShrink(t *testing.T) {
	setTempHome(t)

	s := NewSession("m", "")
	s.Messages = []Message{
		NewUserMessage("a"), NewAssistantMessage("b"),
		NewUserMessage("c"), NewAssistantMessage("d"),
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if s.HistoryEpoch != 0 {
		t.Fatalf("epoch after initial save = %d, want 0 (append-only)", s.HistoryEpoch)
	}

	// The edit / retry rollback shape: truncate the tail and save.
	s.TruncateTo(2)
	if err := s.Save(); err != nil {
		t.Fatalf("Save after truncate: %v", err)
	}
	if s.HistoryEpoch != 1 {
		t.Fatalf("epoch after shrink = %d, want 1", s.HistoryEpoch)
	}

	reloaded, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if reloaded.HistoryEpoch != 1 {
		t.Fatalf("reloaded epoch = %d, want 1 (meta line must carry it)", reloaded.HistoryEpoch)
	}

	// An append-only save afterwards must not move the epoch.
	reloaded.Messages = append(reloaded.Messages, NewUserMessage("e"))
	if err := reloaded.Save(); err != nil {
		t.Fatalf("append Save: %v", err)
	}
	if reloaded.HistoryEpoch != 1 {
		t.Fatalf("epoch after append-only save = %d, want 1", reloaded.HistoryEpoch)
	}
}

// TestHistoryEpoch_BumpsOnSameLengthRewrite covers the case a pure length
// check cannot see: compaction folding history into a same-length summary
// (SyncFrom's rewrite-dirty path) is still a destructive rewrite and must bump
// the epoch, or an incremental cursor would keep addressing the old history.
func TestHistoryEpoch_BumpsOnSameLengthRewrite(t *testing.T) {
	setTempHome(t)

	s := NewSession("m", "")
	h := NewHistory()
	for _, m := range []Message{NewUserMessage("one"), NewAssistantMessage("two"), NewUserMessage("three")} {
		h.Append(m)
	}
	s.SyncFrom(h)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if s.HistoryEpoch != 0 {
		t.Fatalf("epoch after initial save = %d, want 0", s.HistoryEpoch)
	}

	h.ReplaceAll([]Message{NewUserMessage("[summary]"), NewUserMessage("three"), NewAssistantMessage("four")})
	s.SyncFrom(h)
	if err := s.Save(); err != nil {
		t.Fatalf("Save after same-length rewrite: %v", err)
	}
	if s.HistoryEpoch != 1 {
		t.Fatalf("epoch after same-length rewrite = %d, want 1", s.HistoryEpoch)
	}

	reloaded, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if reloaded.HistoryEpoch != 1 {
		t.Fatalf("reloaded epoch = %d, want 1", reloaded.HistoryEpoch)
	}
}

// TestHistoryEpoch_UnaffectedByBookkeepingRewrite mirrors the binding churn
// test for ContentUpdatedAt: Bind/Unbind force a full-file rewrite without
// touching any message, and that must NOT bump the epoch — otherwise every
// post-turn cleanup would invalidate the web client's incremental cursor for
// nothing.
func TestHistoryEpoch_UnaffectedByBookkeepingRewrite(t *testing.T) {
	setTempHome(t)

	s := NewSession("m", "")
	s.Messages = []Message{NewUserMessage("hello"), NewAssistantMessage("hi")}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	reloaded.Bind(EntryWeb, false)
	if err := reloaded.Save(); err != nil {
		t.Fatalf("binding Save: %v", err)
	}
	if reloaded.HistoryEpoch != 0 {
		t.Fatalf("epoch after binding-only rewrite = %d, want 0", reloaded.HistoryEpoch)
	}

	final, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("final LoadSession: %v", err)
	}
	if final.HistoryEpoch != 0 {
		t.Fatalf("epoch on disk after binding churn = %d, want 0", final.HistoryEpoch)
	}
}
