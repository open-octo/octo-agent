package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_RegistersMultipleHooksPerEvent(t *testing.T) {
	e := NewEngine(nil)
	fc := FileConfig{Hooks: map[string][]HookSpec{
		"UserPromptSubmit": {
			{Command: makeScript(t, "echo one")},
			{Command: makeScript(t, "echo two")},
		},
	}}
	if err := e.LoadConfig(fc); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := e.Inject(context.Background(), Payload{Event: EventUserPromptSubmit})
	if got != "one\n\ntwo" {
		t.Errorf("both hooks should run in order: %q", got)
	}
}

func TestLoadConfig_RejectsUnknownEvent(t *testing.T) {
	e := NewEngine(nil)
	err := e.LoadConfig(FileConfig{Hooks: map[string][]HookSpec{
		"Nonsense": {{Command: "true"}},
	}})
	if err == nil {
		t.Fatal("unknown event name must be rejected, not silently ignored")
	}
}

func TestLoadConfig_RejectsBadMatcher(t *testing.T) {
	e := NewEngine(nil)
	err := e.LoadConfig(FileConfig{Hooks: map[string][]HookSpec{
		"PostToolUse": {{Command: "true", Matcher: "("}}, // unbalanced paren
	}})
	if err == nil {
		t.Fatal("an invalid matcher regexp must be a hard error")
	}
}

func TestMatcher_GatesPostToolUseByToolName(t *testing.T) {
	e := NewEngine(nil)
	if err := e.LoadConfig(FileConfig{Hooks: map[string][]HookSpec{
		"PostToolUse": {{Command: makeScript(t, "echo matched"), Matcher: "terminal"}},
	}}); err != nil {
		t.Fatal(err)
	}

	// Matching tool → hook runs.
	if got := e.Inject(context.Background(), Payload{Event: EventPostToolUse, ToolName: "terminal"}); got != "matched" {
		t.Errorf("matcher should run for terminal; got %q", got)
	}
	// Non-matching tool → skipped.
	if got := e.Inject(context.Background(), Payload{Event: EventPostToolUse, ToolName: "read_file"}); got != "" {
		t.Errorf("matcher should skip read_file; got %q", got)
	}
}

func TestMatcher_IgnoredForNonToolEvents(t *testing.T) {
	e := NewEngine(nil)
	// A matcher on UserPromptSubmit is meaningless (no tool name); it must not
	// suppress the hook.
	if err := e.LoadConfig(FileConfig{Hooks: map[string][]HookSpec{
		"UserPromptSubmit": {{Command: makeScript(t, "echo ran"), Matcher: "terminal"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := e.Inject(context.Background(), Payload{Event: EventUserPromptSubmit}); got != "ran" {
		t.Errorf("matcher must be ignored for non-tool events; got %q", got)
	}
}

func TestLoadFileConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.yml")
	yaml := "hooks:\n  Stop:\n    - command: \"retain\"\n      timeout: 3s\n  PostToolUse:\n    - matcher: \"terminal\"\n      command: \"log\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	fc, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("LoadFileConfig: %v", err)
	}
	if len(fc.Hooks["Stop"]) != 1 || fc.Hooks["Stop"][0].Command != "retain" || fc.Hooks["Stop"][0].Timeout != "3s" {
		t.Errorf("Stop hook parsed wrong: %+v", fc.Hooks["Stop"])
	}
	if fc.Hooks["PostToolUse"][0].Matcher != "terminal" {
		t.Errorf("PostToolUse matcher parsed wrong: %+v", fc.Hooks["PostToolUse"])
	}

	e := NewEngine(nil)
	if err := e.LoadConfig(fc); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !e.Configured(EventStop) || !e.Configured(EventPostToolUse) {
		t.Error("engine should be configured for both events after load")
	}
}

func TestLoadFileConfig_MissingIsNotExist(t *testing.T) {
	_, err := LoadFileConfig(filepath.Join(t.TempDir(), "absent.yml"))
	if !os.IsNotExist(err) {
		t.Errorf("missing file should return an os.IsNotExist error, got %v", err)
	}
}

func TestParseTimeout(t *testing.T) {
	cases := map[string]bool{"5s": true, "": false, "garbage": false, "-1s": false}
	for in, wantNonZero := range cases {
		if got := parseTimeout(in); (got > 0) != wantNonZero {
			t.Errorf("parseTimeout(%q) = %v", in, got)
		}
	}
}
