package tools

import (
	"context"
	"strings"
	"testing"
)

func TestRenderUITool_Definition(t *testing.T) {
	def := RenderUITool{}.Definition()
	if def.Name != "render_ui" {
		t.Fatalf("Name = %q, want render_ui", def.Name)
	}
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters.properties missing or wrong type: %#v", def.Parameters["properties"])
	}
	if _, ok := props["spec"]; !ok {
		t.Fatal("Parameters.properties.spec missing")
	}
	required, ok := def.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "spec" {
		t.Fatalf("required = %#v, want [\"spec\"]", def.Parameters["required"])
	}
}

func TestRenderUITool_Execute_MissingSpec(t *testing.T) {
	_, err := RenderUITool{}.Execute(context.Background(), "render_ui", map[string]any{})
	if err == nil {
		t.Fatal("expected error when spec is missing")
	}
	if !strings.Contains(err.Error(), "render_ui") {
		t.Fatalf("error %q should be attributed to render_ui", err.Error())
	}
}

func TestRenderUITool_Execute_SpecWrongType(t *testing.T) {
	_, err := RenderUITool{}.Execute(context.Background(), "render_ui", map[string]any{"spec": "not an object"})
	if err == nil {
		t.Fatal("expected error when spec is not an object")
	}
}

func TestRenderUITool_Execute_ValidSpec(t *testing.T) {
	input := map[string]any{
		"spec": map[string]any{
			"items": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "badge", "text": "ok", "tone": "success"},
			},
		},
	}
	result, err := RenderUITool{}.Execute(context.Background(), "render_ui", input)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "2") {
		t.Fatalf("Text = %q, want it to mention the 2 rendered components", result.Text)
	}
	ui, ok := result.UI.(map[string]any)
	if !ok {
		t.Fatalf("UI = %#v, want map[string]any", result.UI)
	}
	if ui["type"] != "genui" {
		t.Fatalf("UI[type] = %v, want genui", ui["type"])
	}
	specOut, ok := ui["spec"].(map[string]any)
	if !ok {
		t.Fatalf("UI[spec] = %#v, want map[string]any", ui["spec"])
	}
	items, ok := specOut["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("UI[spec][items] = %#v, want 2 items", specOut["items"])
	}
}

func TestRenderUITool_Execute_UnrecognizedSpecShapeErrors(t *testing.T) {
	// spec.items missing entirely — a genuine tool-call contract violation
	// (guard.Sanitize's one hard error), distinct from a node the guard
	// merely drops.
	input := map[string]any{"spec": map[string]any{"title": "x"}}
	_, err := RenderUITool{}.Execute(context.Background(), "render_ui", input)
	if err == nil {
		t.Fatal("expected error when spec.items is missing")
	}
}
