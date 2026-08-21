package agent

import "testing"

// The source-folder configuration joins model and cwd in the freeze identity:
// the env context bakes the mounted folders (and the output-dir marker) into
// the prompt, so mounting or unmounting one must re-freeze on the next turn,
// while an unchanged configuration keeps the frozen prompt (and the provider's
// prompt cache) intact.
func TestSetComposedSystem_SourceDirsChangeForcesRefreeze(t *testing.T) {
	setTempHome(t)
	s := NewSession("m", "")
	s.Messages = []Message{NewUserMessage("hi"), NewAssistantMessage("hello")}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SetComposedSystem("with repo A", "lean", "m", "/w", "hashA"); err != nil {
		t.Fatalf("SetComposedSystem: %v", err)
	}
	if !s.IsComposedFor("m", "/w", "hashA") {
		t.Fatal("expected the freeze to hold for its own source-dir hash")
	}
	if s.IsComposedFor("m", "/w", "hashB") {
		t.Fatal("a freeze composed for hashA must not be reused for hashB")
	}

	if err := s.SetComposedSystem("with repo B", "lean", "m", "/w", "hashB"); err != nil {
		t.Fatalf("SetComposedSystem after mount change: %v", err)
	}
	if s.ComposedSystem != "with repo B" || s.ComposedForSourceDirs != "hashB" {
		t.Fatalf("mount change did not re-freeze: got %q / %q", s.ComposedSystem, s.ComposedForSourceDirs)
	}

	reloaded, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !reloaded.IsComposedFor("m", "/w", "hashB") {
		t.Fatal("source-dir freeze identity did not round-trip")
	}
}

// A task session has no project and freezes with an empty hash; sessions
// written before the field existed load with an empty value — the two must
// match, or every pre-existing session would re-freeze once for nothing.
func TestIsComposedFor_EmptyHashMatchesLegacySessions(t *testing.T) {
	setTempHome(t)
	s := NewSession("m", "")
	s.Messages = []Message{NewUserMessage("hi"), NewAssistantMessage("hello")}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SetComposedSystem("task prompt", "lean", "m", "/w", ""); err != nil {
		t.Fatalf("SetComposedSystem: %v", err)
	}
	reloaded, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !reloaded.IsComposedFor("m", "/w", "") {
		t.Fatal("an empty hash must keep matching an empty stored value")
	}
}
