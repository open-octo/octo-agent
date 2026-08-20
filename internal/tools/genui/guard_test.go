package genui

import (
	"strings"
	"testing"
)

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

// TestSanitize_UnlistedFieldStripped is the core security-property test: a
// node's output must only ever contain fields this package explicitly
// copied and clamped, never anything from the input node it didn't ask for.
func TestSanitize_UnlistedFieldStripped(t *testing.T) {
	spec := map[string]any{
		"items": []any{
			map[string]any{
				"type":    "text",
				"text":    "hi",
				"onclick": "evil()",
				"style":   "position:fixed",
				"href":    "javascript:alert(1)",
			},
		},
	}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	node := out["items"].([]any)[0].(map[string]any)
	for _, unlisted := range []string{"onclick", "style", "href"} {
		if _, present := node[unlisted]; present {
			t.Fatalf("unlisted field %q leaked through: %#v", unlisted, node)
		}
	}
	if len(node) != 2 { // type + text only
		t.Fatalf("node has unexpected fields: %#v", node)
	}
}

// TestSanitize_ExactlyAtNodeCapKeepsAll is the exact-boundary counterpart of
// TestSanitize_OverCapNodeCountTrimmed — MaxNodes flat siblings should all
// survive (this alone would not have caught the container off-by-one below,
// since flat siblings never nest).
func TestSanitize_ExactlyAtNodeCapKeepsAll(t *testing.T) {
	items := make([]any, MaxNodes)
	for i := range items {
		items[i] = map[string]any{"type": "text", "text": "x"}
	}
	_, count, err := Sanitize(map[string]any{"items": items}, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	if count != MaxNodes {
		t.Fatalf("count = %d, want %d (none should be trimmed exactly at the cap)", count, MaxNodes)
	}
}

// TestSanitize_ContainerNodesReserveOwnBudgetSlot reproduces the review
// finding: a chain of containers must count themselves BEFORE their
// children spend the remaining budget, or the total can overshoot MaxNodes.
// Nests MaxNodes text leaves one level down inside a "row", so a container
// that doesn't reserve its own slot first would let the total reach
// MaxNodes+1.
func TestSanitize_ContainerNodesReserveOwnBudgetSlot(t *testing.T) {
	leaves := make([]any, MaxNodes)
	for i := range leaves {
		leaves[i] = map[string]any{"type": "text", "text": "x"}
	}
	spec := map[string]any{
		"items": []any{
			map[string]any{"type": "row", "children": leaves},
		},
	}
	_, count, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	if count > MaxNodes {
		t.Fatalf("count = %d, exceeds MaxNodes = %d — the row didn't reserve its own budget slot before recursing", count, MaxNodes)
	}
}

// TestSanitize_ExactlyAtDepthKept and TestSanitize_OneOverDepthDropped are
// the exact-boundary counterpart of TestSanitize_OverDepthNodeDropped, which
// only exercised depth 10 against MaxDepth=8.
func TestSanitize_ExactlyAtDepthKept(t *testing.T) {
	var build func(depth int) map[string]any
	build = func(depth int) map[string]any {
		if depth == 0 {
			return map[string]any{"type": "text", "text": "leaf"}
		}
		return map[string]any{"type": "row", "children": []any{build(depth - 1)}}
	}
	// MaxDepth nested rows below the top-level item = depth MaxDepth+1 for
	// the leaf itself (top-level items start at depth 1), matching the
	// convention sanitizeNode already uses.
	spec := map[string]any{"items": []any{build(MaxDepth - 1)}}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
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
	if node["type"] != "text" {
		t.Fatalf("leaf at exactly MaxDepth should survive, got %#v at depth %d", node, depth)
	}
}

func TestSanitize_StringExactlyAtCapNotTruncated(t *testing.T) {
	exact := strings.Repeat("a", MaxStringLen)
	spec := map[string]any{
		"items": []any{map[string]any{"type": "text", "text": exact}},
	}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	got := out["items"].([]any)[0].(map[string]any)["text"].(string)
	if got != exact {
		t.Fatalf("a string exactly at MaxStringLen should be untouched; got len %d, want %d", len(got), MaxStringLen)
	}
}

// TestSanitize_TableNonStringNonNumberCellBecomesPlaceholder reproduces the
// review finding: a cell of an unsupported JSON type (bool here) must become
// a placeholder, not be skipped — skipping shifts every later cell in the
// row left by one, misaligning it against the table's "columns" header.
func TestSanitize_TableNonStringNonNumberCellBecomesPlaceholder(t *testing.T) {
	spec := map[string]any{
		"items": []any{
			map[string]any{
				"type":    "table",
				"columns": []any{"Name", "Active", "City"},
				"rows":    []any{[]any{"Alice", true, "NYC"}},
			},
		},
	}
	out, _, err := Sanitize(spec, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	row := out["items"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)
	if len(row) != 3 {
		t.Fatalf("row = %#v, want 3 cells (bool cell replaced, not dropped)", row)
	}
	if row[0] != "Alice" || row[2] != "NYC" {
		t.Fatalf("row = %#v, want column alignment preserved (City still at index 2)", row)
	}
}

func TestClampString_DoesNotSplitMultibyteRune(t *testing.T) {
	// Each CJK character below is 3 bytes in UTF-8; a byte-blind s[:n] at an
	// odd boundary would split one in half and produce invalid UTF-8.
	s := strings.Repeat("你", 10) // 30 bytes
	got := clampString(s, 7)     // cuts mid-character at byte 7 (into the 3rd 你)
	if !strings.HasPrefix(s, got) {
		t.Fatalf("clampString(%q, 7) = %q is not a prefix of the input", s, got)
	}
	for i, r := range got {
		_ = i
		if r == 0xFFFD {
			t.Fatalf("clampString produced a replacement character (invalid UTF-8 split): %q", got)
		}
	}
	// 7 bytes fits 2 complete 你 (6 bytes) — the 3rd is incomplete and must
	// be dropped entirely, not truncated into garbage.
	if got != strings.Repeat("你", 2) {
		t.Fatalf("clampString(%q, 7) = %q, want %q", s, got, strings.Repeat("你", 2))
	}
}
