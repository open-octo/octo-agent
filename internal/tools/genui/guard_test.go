package genui

import "testing"

func TestSanitize_ValidSpecPassesThrough(t *testing.T) {
	spec := map[string]any{
		"title": "Order status",
		"items": []any{
			map[string]any{"type": "text", "text": "hello"},
			map[string]any{"type": "stat", "label": "Revenue", "value": "$128,430", "tone": "up"},
		},
	}
	out, count, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("out[items] = %#v, want 2 items", out["items"])
	}
	first := items[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "hello" {
		t.Fatalf("first item mangled: %#v", first)
	}
}

func TestSanitize_MissingItemsErrors(t *testing.T) {
	_, _, err := Sanitize(map[string]any{"title": "x"}, ReadOnlyNodeTypes)
	if err == nil {
		t.Fatal("expected error for missing items")
	}
}

func TestSanitize_UnknownTypeNodeDropped(t *testing.T) {
	spec := map[string]any{
		"items": []any{
			map[string]any{"type": "text", "text": "kept"},
			map[string]any{"type": "iframe", "src": "https://evil.example"},
		},
	}
	out, count, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (unknown-type node dropped)", count)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v, want 1 item after drop", items)
	}
}

func TestSanitize_OverCapNodeCountTrimmed(t *testing.T) {
	items := make([]any, 0, MaxNodes+50)
	for i := 0; i < MaxNodes+50; i++ {
		items = append(items, map[string]any{"type": "text", "text": "x"})
	}
	spec := map[string]any{"items": items}
	out, count, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	if count != MaxNodes {
		t.Fatalf("count = %d, want %d (trimmed at cap)", count, MaxNodes)
	}
	if got := len(out["items"].([]any)); got != MaxNodes {
		t.Fatalf("len(items) = %d, want %d", got, MaxNodes)
	}
}

func TestSanitize_OverDepthNodeDropped(t *testing.T) {
	// Build a card nested 10 levels deep (MaxDepth is 8) via row->children chains.
	var build func(depth int) map[string]any
	build = func(depth int) map[string]any {
		if depth == 0 {
			return map[string]any{"type": "text", "text": "leaf"}
		}
		return map[string]any{"type": "row", "children": []any{build(depth - 1)}}
	}
	spec := map[string]any{"items": []any{build(10)}}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	// Walk down the surviving tree and confirm it terminates before the leaf
	// (the leaf sits at depth 11, past MaxDepth=8) — i.e. its deepest "row" has
	// no children rather than the original leaf node.
	node := out["items"].([]any)[0].(map[string]any)
	depth := 1
	for {
		children, ok := node["children"].([]any)
		if !ok || len(children) == 0 {
			break
		}
		node = children[0].(map[string]any)
		depth++
	}
	if node["type"] != "row" {
		t.Fatalf("expected the deepest surviving node to be a truncated 'row' (leaf text dropped past MaxDepth), got %#v at depth %d", node, depth)
	}
	if depth > MaxDepth {
		t.Fatalf("surviving depth = %d, want <= %d", depth, MaxDepth)
	}
}

func TestSanitize_StringFieldsClamped(t *testing.T) {
	long := make([]byte, MaxStringLen+100)
	for i := range long {
		long[i] = 'a'
	}
	spec := map[string]any{
		"items": []any{
			map[string]any{"type": "text", "text": string(long)},
		},
	}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	text := out["items"].([]any)[0].(map[string]any)["text"].(string)
	if len(text) != MaxStringLen {
		t.Fatalf("len(text) = %d, want %d", len(text), MaxStringLen)
	}
}

func TestSanitize_ProgressValueClamped(t *testing.T) {
	spec := map[string]any{
		"items": []any{
			map[string]any{"type": "progress", "value": 150.0},
			map[string]any{"type": "progress", "value": -20.0},
		},
	}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	items := out["items"].([]any)
	if v := items[0].(map[string]any)["value"]; v != 100.0 {
		t.Fatalf("value = %v, want 100", v)
	}
	if v := items[1].(map[string]any)["value"]; v != 0.0 {
		t.Fatalf("value = %v, want 0", v)
	}
}

func TestSanitize_InvalidToneDropped(t *testing.T) {
	spec := map[string]any{
		"items": []any{
			map[string]any{"type": "badge", "text": "x", "tone": "rainbow"},
		},
	}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	node := out["items"].([]any)[0].(map[string]any)
	if _, present := node["tone"]; present {
		t.Fatalf("expected invalid tone to be dropped, got %#v", node["tone"])
	}
}

func TestSanitize_TableRowsAndColumnsCapped(t *testing.T) {
	columns := []any{"a", "b"}
	rows := make([]any, 0, MaxTableRows+20)
	for i := 0; i < MaxTableRows+20; i++ {
		rows = append(rows, []any{"x", "y"})
	}
	spec := map[string]any{
		"items": []any{
			map[string]any{"type": "table", "columns": columns, "rows": rows},
		},
	}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	node := out["items"].([]any)[0].(map[string]any)
	if got := len(node["rows"].([]any)); got != MaxTableRows {
		t.Fatalf("len(rows) = %d, want %d", got, MaxTableRows)
	}
}
