package tools

import (
	"context"
	"strings"
	"testing"
)

func TestDefaultTools_IncludesAllExpected(t *testing.T) {
	defs := DefaultTools()
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{
		"terminal",
		"read_file",
		"write_file",
		"edit_file",
		"glob",
		"grep",
		"web_fetch",
		"web_search",
	} {
		if !names[want] {
			t.Errorf("DefaultTools missing %q", want)
		}
	}
}

func TestDefaultTools_DefinitionsHaveRequiredFields(t *testing.T) {
	for _, d := range DefaultTools() {
		if d.Name == "" {
			t.Errorf("tool with empty Name in DefaultTools")
		}
		if d.Description == "" {
			t.Errorf("tool %q has empty Description", d.Name)
		}
		if d.Parameters["type"] != "object" {
			t.Errorf("tool %q Parameters.type = %v, want 'object'", d.Name, d.Parameters["type"])
		}
	}
}

func TestDefaultRegistry_UnknownTool(t *testing.T) {
	_, err := DefaultRegistry{}.Execute(context.Background(), "no_such_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected unknown-tool error, got %v", err)
	}
}

func TestDefaultRegistry_RoutesByName(t *testing.T) {
	// Pick a tool with very obvious failure mode (read_file missing path)
	// to confirm the registry dispatched to the right tool.
	_, err := DefaultRegistry{}.Execute(context.Background(), "read_file", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected read_file routing, got %v", err)
	}
}

// Regression guard for #1094: TerminalTool has implemented ExecuteStream
// since #49, but the agent loop's executor.(StreamingToolExecutor) assertion
// is on the whole registry, not the individual tool — without
// DefaultRegistry.ExecuteStream forwarding to it, EventToolProgress never
// fires for any tool.
func TestDefaultRegistry_ExecuteStream_StreamsTerminalOutput(t *testing.T) {
	var got []string
	progress := func(chunk string) { got = append(got, chunk) }

	result, err := DefaultRegistry{}.ExecuteStream(context.Background(), "terminal", map[string]any{
		"command": "echo line1 && echo line2",
	}, progress)
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 progress chunks routed through the registry, got %d: %v", len(got), got)
	}
	if !strings.Contains(result.Text, "line1") || !strings.Contains(result.Text, "line2") {
		t.Errorf("aggregated result = %q", result.Text)
	}
}

// A tool that doesn't implement StreamingToolExecutor (e.g. read_file) must
// still route correctly and simply never invoke progress.
func TestDefaultRegistry_ExecuteStream_NonStreamingToolIgnoresProgress(t *testing.T) {
	called := false
	_, err := DefaultRegistry{}.ExecuteStream(context.Background(), "read_file", map[string]any{}, func(string) { called = true })
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected read_file routing, got %v", err)
	}
	if called {
		t.Error("non-streaming tool must not invoke progress")
	}
}

// A nil progress callback (Execute's own delegation path) must behave
// identically to calling Execute directly.
func TestDefaultRegistry_ExecuteStream_NilProgress(t *testing.T) {
	out, err := DefaultRegistry{}.ExecuteStream(context.Background(), "terminal", map[string]any{"command": "echo hi"}, nil)
	if err != nil {
		t.Fatalf("ExecuteStream with nil progress: %v", err)
	}
	if !strings.Contains(out.Text, "hi") {
		t.Errorf("expected output to contain %q, got %q", "hi", out.Text)
	}
}
