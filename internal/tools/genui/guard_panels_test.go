package genui

import (
	"strings"
	"testing"
)

// Coverage for the node types and fields the interactive-panel work added on
// the Go (render_ui tool-card) side. The pre-existing types are covered in
// guard_test.go.

func sanitizeOne(t *testing.T, node map[string]any) map[string]any {
	t.Helper()
	out, _, err := Sanitize(map[string]any{"items": []any{node}}, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	items := out["items"].([]any)
	if len(items) == 0 {
		return nil
	}
	return items[0].(map[string]any)
}

func TestSanitize_NewContentNodesAccepted(t *testing.T) {
	for _, node := range []map[string]any{
		{"type": "divider"},
		{"type": "code", "code": "ls -la", "lang": "bash"},
		{"type": "mermaid", "code": "graph TD; A-->B;"},
		{"type": "collapsible", "title": "more", "children": []any{map[string]any{"type": "text", "text": "x"}}},
		{"type": "plot", "plot": "bar", "series": []any{map[string]any{"points": []any{map[string]any{"label": "a", "value": 1.0}}}}},
	} {
		got := sanitizeOne(t, node)
		if got == nil {
			t.Fatalf("node %v was dropped, want kept", node["type"])
		}
		if got["type"] != node["type"] {
			t.Fatalf("type = %v, want %v", got["type"], node["type"])
		}
	}
}

func TestSanitize_InteractiveNodesStillRejectedOnToolPath(t *testing.T) {
	// render_ui carries no field-bearing node: a tool card has nothing to set
	// a field with, so accepting one would render a control that does nothing.
	for _, typ := range []string{"slider", "number", "textarea", "quiz", "input", "button"} {
		if got := sanitizeOne(t, map[string]any{"type": typ, "field": "f"}); got != nil {
			t.Fatalf("%s was accepted on the read-only path: %#v", typ, got)
		}
	}
}

func TestSanitize_PanelIDDropped(t *testing.T) {
	// Identity exists so a later turn can re-address a panel; a tool card is a
	// one-shot result nothing addresses again.
	out, _, err := Sanitize(map[string]any{"id": "sales", "items": []any{}}, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	if _, present := out["id"]; present {
		t.Fatalf("id survived on the tool path: %#v", out)
	}
}

func TestSanitize_VisibleWhenDropped(t *testing.T) {
	// A condition compares a field value, and this path has no field to set.
	got := sanitizeOne(t, map[string]any{
		"type":        "text",
		"text":        "x",
		"visibleWhen": map[string]any{"field": "mode", "equals": "advanced"},
	})
	if _, present := got["visibleWhen"]; present {
		t.Fatalf("visibleWhen survived: %#v", got)
	}
}

func TestSanitize_TableSortableKeptFilterDropped(t *testing.T) {
	got := sanitizeOne(t, map[string]any{
		"type":     "table",
		"columns":  []any{"a"},
		"rows":     []any{},
		"sortable": true,
		"filterBy": map[string]any{"field": "q", "column": "a"},
	})
	if got["sortable"] != true {
		t.Fatalf("sortable dropped: %#v", got)
	}
	if _, present := got["filterBy"]; present {
		t.Fatalf("filterBy survived without any field to read: %#v", got)
	}
}

func TestSanitize_LinkSchemeWhitelist(t *testing.T) {
	for _, href := range []string{"http://a.test/x", "https://a.test/x", "mailto:a@b.test", "tel:+8613800138000"} {
		got := sanitizeOne(t, map[string]any{"type": "link", "text": "go", "href": href})
		if got == nil {
			t.Fatalf("href %q was dropped, want kept", href)
		}
		if got["href"] != href {
			t.Fatalf("href = %v, want %q", got["href"], href)
		}
	}

	// A rejected link must not render as inert text — it would still look
	// clickable. Casing and surrounding whitespace must not sneak past.
	for _, href := range []string{
		"javascript:alert(1)",
		"  JavaScript:alert(1)",
		"data:text/html,<script>",
		"file:///etc/passwd",
		"vbscript:x",
		"//a.test",
		"",
	} {
		if got := sanitizeOne(t, map[string]any{"type": "link", "text": "go", "href": href}); got != nil {
			t.Fatalf("href %q was accepted: %#v", href, got)
		}
	}
}

func TestSanitize_LinkOverLongHrefDropped(t *testing.T) {
	// Truncating would produce a link pointing somewhere other than it claims.
	long := "https://a.test/" + strings.Repeat("x", MaxHrefLen)
	if got := sanitizeOne(t, map[string]any{"type": "link", "text": "x", "href": long}); got != nil {
		t.Fatalf("over-long href was kept: %#v", got)
	}
}

func TestSanitize_LinkFallsBackToHrefAsText(t *testing.T) {
	got := sanitizeOne(t, map[string]any{"type": "link", "href": "https://a.test/x"})
	if got["text"] != "https://a.test/x" {
		t.Fatalf("text = %v, want the href", got["text"])
	}
}

func TestSanitize_PlotRejectsUnusableInput(t *testing.T) {
	cases := []map[string]any{
		{"type": "plot", "plot": "sankey", "series": []any{map[string]any{"points": []any{map[string]any{"label": "a", "value": 1.0}}}}},
		{"type": "plot", "plot": "bar", "series": []any{}},
		{"type": "plot", "plot": "bar", "series": []any{map[string]any{"points": []any{}}}},
	}
	for _, c := range cases {
		if got := sanitizeOne(t, c); got != nil {
			t.Fatalf("plot %v was kept, want dropped: %#v", c["plot"], got)
		}
	}
}

func TestSanitize_PlotDropsNonFinitePoints(t *testing.T) {
	got := sanitizeOne(t, map[string]any{
		"type": "plot",
		"plot": "line",
		"series": []any{map[string]any{"points": []any{
			map[string]any{"label": "a", "value": 1.0},
			map[string]any{"label": "b", "value": "not a number"},
			map[string]any{"label": "c", "value": nil},
		}}},
	})
	series := got["series"].([]any)
	points := series[0].(map[string]any)["points"].([]any)
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1 (a NaN would poison every axis calculation)", len(points))
	}
}

func TestSanitize_PlotTrimsToCaps(t *testing.T) {
	series := make([]any, 0, MaxPlotSeries+3)
	for i := 0; i < MaxPlotSeries+3; i++ {
		points := make([]any, 0, MaxPlotPoints+5)
		for j := 0; j < MaxPlotPoints+5; j++ {
			points = append(points, map[string]any{"label": "p", "value": float64(j)})
		}
		series = append(series, map[string]any{"points": points})
	}
	got := sanitizeOne(t, map[string]any{"type": "plot", "plot": "bar", "series": series})
	kept := got["series"].([]any)
	if len(kept) != MaxPlotSeries {
		t.Fatalf("series = %d, want %d", len(kept), MaxPlotSeries)
	}
	pts := kept[0].(map[string]any)["points"].([]any)
	if len(pts) != MaxPlotPoints {
		t.Fatalf("points = %d, want %d", len(pts), MaxPlotPoints)
	}
}

func TestSanitize_CodeAndMermaidTrimmedNotRejected(t *testing.T) {
	long := strings.Repeat("x", MaxCodeLen+100)
	got := sanitizeOne(t, map[string]any{"type": "code", "code": long})
	if len(got["code"].(string)) != MaxCodeLen {
		t.Fatalf("code len = %d, want %d", len(got["code"].(string)), MaxCodeLen)
	}
	long = strings.Repeat("y", MaxMermaidLen+100)
	got = sanitizeOne(t, map[string]any{"type": "mermaid", "code": long})
	if len(got["code"].(string)) != MaxMermaidLen {
		t.Fatalf("mermaid len = %d, want %d", len(got["code"].(string)), MaxMermaidLen)
	}
}

func TestSanitize_TableRowCapRaised(t *testing.T) {
	rows := make([]any, 0, MaxTableRows+25)
	for i := 0; i < MaxTableRows+25; i++ {
		rows = append(rows, []any{"x"})
	}
	got := sanitizeOne(t, map[string]any{"type": "table", "columns": []any{"n"}, "rows": rows})
	if len(got["rows"].([]any)) != MaxTableRows {
		t.Fatalf("rows = %d, want %d", len(got["rows"].([]any)), MaxTableRows)
	}
}

func TestSanitize_CollapsibleChildrenCountAgainstBudget(t *testing.T) {
	// A container must not smuggle children past MaxNodes.
	children := make([]any, 0, MaxNodes+10)
	for i := 0; i < MaxNodes+10; i++ {
		children = append(children, map[string]any{"type": "text", "text": "x"})
	}
	_, count, err := Sanitize(map[string]any{
		"items": []any{map[string]any{"type": "collapsible", "title": "t", "children": children}},
	}, ReadOnlyNodeTypes)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	if count > MaxNodes {
		t.Fatalf("count = %d, want <= %d", count, MaxNodes)
	}
}
