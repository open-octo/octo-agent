// Package genui implements the server-side half of the GenUI spec guard: the
// same node whitelist and structural caps the frontend's guard.ts enforces
// (see dev-docs/genui-design.md, "Security design"), run once here before a
// render_ui tool result ever leaves the process and persists with the
// session. The frontend re-runs an equivalent guard independently — this is
// defense in depth, not the only checkpoint.
package genui

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Structural caps shared by every node type. Mirrored in the TS guard
// (web/src/lib/genui/guard.ts) — a change here must be mirrored there.
const (
	MaxDepth = 8
	MaxNodes = 200
	// MaxStringLen clamps by byte length here (clampString below), while the
	// TS guard clamps the same numeric value in UTF-16 code units. Both are
	// correct for their own string representation, but the same non-ASCII
	// input can survive to a different length through the two guards — the
	// shared constant value does not imply a shared character count kept.
	MaxStringLen    = 500
	MaxTableCellLen = 2000
	MaxListItems    = 200
	// Raised from 100 when local filtering arrived on the inline-fence path:
	// the core case is "here is the data set, narrow it down", and 100 rows is
	// below the size where offering a filter is worth it. Kept in lockstep
	// with the TS guard. See dev-docs/genui-interactive-panels-design.md.
	MaxTableRows = 500
	// MaxTableColumns bounds both a table's "columns" header length and each
	// row's cell count. The design doc only names row-count (100) and
	// cell-length (2000) caps for tables; this column-count cap is this
	// package's own addition, not a "select/radio options" cap — it happens
	// to share the value 50 with that unrelated cap by coincidence, not
	// because the two are the same limit.
	MaxTableColumns = 50

	// Interactive-panel caps — see dev-docs/genui-interactive-panels-design.md.
	MaxMermaidLen = 5000
	MaxCodeLen    = 5000
	MaxPlotPoints = 100
	// MaxHrefLen is well past any real URL; a longer one is dropped rather
	// than truncated into something pointing elsewhere.
	MaxHrefLen = 2000
	// MaxPlotSeries matches the fixed colour sequence the renderer draws from;
	// a ninth series would have no distinct colour left to take.
	MaxPlotSeries = 8
	// MaxNumeric bounds numeric fields rather than trusting them, the same
	// posture progress.value already takes.
	MaxNumeric = 1e9
)

// ReadOnlyNodeTypes is the whitelist of node "type" values the render_ui
// tool accepts (the "Slice A" read-only component set in the design doc).
// Interactive types (button/input/select/…) are inline-fence-only and never
// reach this Go guard.
var ReadOnlyNodeTypes = map[string]bool{
	"text":     true,
	"row":      true,
	"col":      true,
	"card":     true,
	"list":     true,
	"table":    true,
	"keyvalue": true,
	"stat":     true,
	"badge":    true,
	"progress": true,
	"callout":  true,
	// These carry no field and fire no action, so a render_ui tool-result card
	// can hold them too. collapsible does toggle, but folding is presentation
	// rather than input: it reports nothing back and needs no field.
	"collapsible": true,
	"code":        true,
	"link":        true,
	"divider":     true,
	"plot":        true,
	"mermaid":     true,
}

// safeHrefSchemes mirrors the frontend's isSafeHref whitelist
// (web/src/lib/markdown.ts). Deliberately a second, independent
// implementation rather than a shared one — same posture as the rest of this
// guard, which duplicates the TS policy so neither side is the only check.
var safeHrefSchemes = []string{"http://", "https://", "mailto:", "tel:"}

func isSafeHref(href string) bool {
	lower := strings.ToLower(strings.TrimSpace(href))
	for _, scheme := range safeHrefSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// Sanitize validates and clamps a GenUI spec against the allowed node-type
// whitelist. It returns the cleaned spec (safe to store in ToolResult.UI and
// render) and the number of nodes it kept. Unknown-type nodes (and their
// subtree) are dropped rather than failing the whole spec; a spec whose node
// count would exceed MaxNodes is trimmed, not rejected. The only hard error is
// a structurally invalid top-level spec (no "items" array) — that is a
// tool-call contract violation the model should see and retry.
func Sanitize(spec map[string]any, allowed map[string]bool) (map[string]any, int, error) {
	itemsRaw, ok := spec["items"]
	if !ok {
		return nil, 0, fmt.Errorf("spec.items is required")
	}
	items, ok := toSlice(itemsRaw)
	if !ok {
		return nil, 0, fmt.Errorf("spec.items must be an array")
	}

	count := 0
	cleaned := make([]any, 0, len(items))
	for _, raw := range items {
		if count >= MaxNodes {
			break
		}
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if c := sanitizeNode(node, allowed, 1, &count); c != nil {
			cleaned = append(cleaned, c)
		}
	}

	// A panel "id" is deliberately not carried through: identity exists so a
	// panel can be re-addressed by a later turn, and a render_ui result is a
	// one-shot card that no turn addresses again. Tool cards stay anonymous.
	out := map[string]any{"items": cleaned}
	if title, ok := spec["title"].(string); ok {
		out["title"] = clampString(title, MaxStringLen)
	}
	return out, count, nil
}

// sanitizeNode validates one node at the given depth, incrementing *count on
// acceptance. It returns nil when the node is dropped (unknown type, past
// MaxDepth, or the node budget is already spent) — the caller skips a nil
// result rather than including it.
func sanitizeNode(node map[string]any, allowed map[string]bool, depth int, count *int) map[string]any {
	if depth > MaxDepth || *count >= MaxNodes {
		return nil
	}
	typ, _ := node["type"].(string)
	if !allowed[typ] {
		return nil
	}
	// Reserve this node's own slot in the budget *before* recursing into any
	// children — sanitizeChildren must see a budget that already accounts
	// for this node, or a chain of containers can push the total past
	// MaxNodes by up to MaxDepth-1 (each container's own increment landing
	// "underneath" a child count that already spent the full budget).
	*count++

	out := map[string]any{"type": typ}
	switch typ {
	case "text":
		out["text"] = clampString(stringField(node, "text"), MaxStringLen)
		setEnum(out, node, "tone", []string{"default", "muted", "danger"})

	case "row", "col":
		if gap, ok := numberField(node, "gap"); ok {
			out["gap"] = clampNumber(gap, 0, 64)
		}
		out["children"] = sanitizeChildren(node, "children", allowed, depth, count)

	case "card":
		if title, ok := node["title"].(string); ok {
			out["title"] = clampString(title, MaxStringLen)
		}
		out["children"] = sanitizeChildren(node, "children", allowed, depth, count)

	case "list":
		out["items"] = sanitizeListItems(node)

	case "table":
		out["columns"] = sanitizeStringArray(node, "columns", MaxTableColumns, MaxStringLen)
		out["rows"] = sanitizeTableRows(node)
		// sortable is kept: sorting reads no field, so it works on a tool card
		// too. filterBy is dropped for the same reason visibleWhen is — it
		// names a field, and this path has no field-bearing node to set one.
		if sortable, ok := node["sortable"].(bool); ok {
			out["sortable"] = sortable
		}

	case "keyvalue":
		out["items"] = sanitizeKeyValueItems(node)

	case "stat":
		out["label"] = clampString(stringField(node, "label"), MaxStringLen)
		out["value"] = clampString(stringField(node, "value"), MaxStringLen)
		if delta, ok := node["delta"].(string); ok {
			out["delta"] = clampString(delta, MaxStringLen)
		}
		setEnum(out, node, "tone", []string{"up", "down", "neutral"})

	case "badge":
		out["text"] = clampString(stringField(node, "text"), MaxStringLen)
		setEnum(out, node, "tone", []string{"default", "success", "warning", "danger", "info"})

	case "progress":
		v, _ := numberField(node, "value")
		out["value"] = clampNumber(v, 0, 100)
		if label, ok := node["label"].(string); ok {
			out["label"] = clampString(label, MaxStringLen)
		}

	case "callout":
		setEnum(out, node, "tone", []string{"info", "success", "warning", "danger"})
		if title, ok := node["title"].(string); ok {
			out["title"] = clampString(title, MaxStringLen)
		}
		if text, ok := node["text"].(string); ok {
			out["text"] = clampString(text, MaxStringLen)
		}

	case "collapsible":
		out["title"] = clampString(stringField(node, "title"), MaxStringLen)
		if open, ok := node["open"].(bool); ok {
			out["open"] = open
		}
		out["children"] = sanitizeChildren(node, "children", allowed, depth, count)

	case "code":
		out["code"] = clampString(stringField(node, "code"), MaxCodeLen)
		if lang, ok := node["lang"].(string); ok {
			out["lang"] = clampString(lang, MaxStringLen)
		}

	case "link":
		href := strings.TrimSpace(stringField(node, "href"))
		// A rejected or over-long href drops the node rather than rendering an
		// inert or truncated link: either still looks clickable and isn't, or
		// points somewhere other than where it claims.
		if !isSafeHref(href) || len(href) > MaxHrefLen {
			return nil
		}
		text := clampString(stringField(node, "text"), MaxStringLen)
		if text == "" {
			text = href
		}
		out["text"] = text
		out["href"] = href

	case "divider":
		// No fields at all.

	case "plot":
		kind, _ := node["plot"].(string)
		switch kind {
		case "bar", "line", "area", "pie":
		default:
			return nil
		}
		series := sanitizePlotSeries(node)
		if len(series) == 0 {
			return nil
		}
		out["plot"] = kind
		out["series"] = series
		if stacked, ok := node["stacked"].(bool); ok {
			out["stacked"] = stacked
		}
		if legend, ok := node["legend"].(bool); ok {
			out["legend"] = legend
		}
		if xLabel, ok := node["xLabel"].(string); ok {
			out["xLabel"] = clampString(xLabel, MaxStringLen)
		}
		if yLabel, ok := node["yLabel"].(string); ok {
			out["yLabel"] = clampString(yLabel, MaxStringLen)
		}
		if h, ok := numberField(node, "height"); ok {
			out["height"] = clampNumber(math.Round(h), 80, 400)
		}

	case "mermaid":
		out["code"] = clampString(stringField(node, "code"), MaxMermaidLen)
	}

	// visibleWhen is deliberately NOT carried through here. It is a condition
	// over a field value, and the render_ui path this guard serves accepts no
	// field-bearing node at all (ReadOnlyNodeTypes above), so a condition on a
	// tool card could only ever compare against an unset field — hiding the
	// node for no reason a reader could act on. The inline-fence path, which
	// does have fields, is guarded in TypeScript and keeps it there.

	return out
}

// sanitizePlotSeries clamps a plot's series list to MaxPlotSeries entries and
// each series' points to MaxPlotPoints, dropping points whose value is not a
// finite number — a NaN would poison every axis calculation downstream.
func sanitizePlotSeries(node map[string]any) []any {
	raw, ok := toSlice(node["series"])
	if !ok {
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, s := range raw {
		if len(out) >= MaxPlotSeries {
			break
		}
		rec, ok := s.(map[string]any)
		if !ok {
			continue
		}
		pointsRaw, ok := toSlice(rec["points"])
		if !ok {
			continue
		}
		points := make([]any, 0, len(pointsRaw))
		for _, p := range pointsRaw {
			if len(points) >= MaxPlotPoints {
				break
			}
			pr, ok := p.(map[string]any)
			if !ok {
				continue
			}
			v, ok := numberField(pr, "value")
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			points = append(points, map[string]any{
				"label": clampString(stringField(pr, "label"), MaxStringLen),
				"value": clampNumber(v, -MaxNumeric, MaxNumeric),
			})
		}
		if len(points) == 0 {
			continue
		}
		entry := map[string]any{"points": points}
		if name, ok := rec["name"].(string); ok {
			entry["name"] = clampString(name, MaxStringLen)
		}
		out = append(out, entry)
	}
	return out
}

// sanitizeChildren sanitizes a "children" array field one level deeper,
// dropping non-object entries and any child sanitizeNode itself drops.
func sanitizeChildren(node map[string]any, key string, allowed map[string]bool, depth int, count *int) []any {
	raw, ok := node[key]
	if !ok {
		return []any{}
	}
	items, ok := toSlice(raw)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, len(items))
	for _, r := range items {
		if *count >= MaxNodes {
			break
		}
		child, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if c := sanitizeNode(child, allowed, depth+1, count); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// sanitizeListItems clamps a "list" node's items — each either a plain
// string or a {label, value?} object — to MaxListItems entries.
func sanitizeListItems(node map[string]any) []any {
	raw, ok := node["items"]
	if !ok {
		return []any{}
	}
	items, ok := toSlice(raw)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, min(len(items), MaxListItems))
	for _, r := range items {
		if len(out) >= MaxListItems {
			break
		}
		switch v := r.(type) {
		case string:
			out = append(out, clampString(v, MaxStringLen))
		case map[string]any:
			entry := map[string]any{"label": clampString(stringField(v, "label"), MaxStringLen)}
			if val, ok := v["value"].(string); ok {
				entry["value"] = clampString(val, MaxStringLen)
			}
			out = append(out, entry)
		}
	}
	return out
}

// sanitizeKeyValueItems clamps a "keyvalue" node's {label, value} pairs to
// MaxListItems entries.
func sanitizeKeyValueItems(node map[string]any) []any {
	raw, ok := node["items"]
	if !ok {
		return []any{}
	}
	items, ok := toSlice(raw)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, min(len(items), MaxListItems))
	for _, r := range items {
		if len(out) >= MaxListItems {
			break
		}
		v, ok := r.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"label": clampString(stringField(v, "label"), MaxStringLen),
			"value": clampString(stringField(v, "value"), MaxStringLen),
		})
	}
	return out
}

// sanitizeStringArray clamps an array-of-strings field to maxItems entries,
// each string clamped to maxLen.
func sanitizeStringArray(node map[string]any, key string, maxItems, maxLen int) []any {
	raw, ok := node[key]
	if !ok {
		return []any{}
	}
	items, ok := toSlice(raw)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, min(len(items), maxItems))
	for _, r := range items {
		if len(out) >= maxItems {
			break
		}
		if s, ok := r.(string); ok {
			out = append(out, clampString(s, maxLen))
		}
	}
	return out
}

// sanitizeTableRows clamps a "table" node's rows to MaxTableRows entries,
// each row's cells to MaxTableColumns entries, and each cell to a string
// (clamped to MaxTableCellLen) or a number, passed through unchanged. A cell
// of any other JSON type becomes an empty string rather than being skipped —
// skipping would shift every later cell in that row left by one, silently
// misaligning it against the table's "columns" header.
func sanitizeTableRows(node map[string]any) []any {
	raw, ok := node["rows"]
	if !ok {
		return []any{}
	}
	rows, ok := toSlice(raw)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, min(len(rows), MaxTableRows))
	for _, r := range rows {
		if len(out) >= MaxTableRows {
			break
		}
		cells, ok := toSlice(r)
		if !ok {
			continue
		}
		row := make([]any, 0, min(len(cells), MaxTableColumns))
		for _, c := range cells {
			if len(row) >= MaxTableColumns {
				break
			}
			switch v := c.(type) {
			case string:
				row = append(row, clampString(v, MaxTableCellLen))
			case float64:
				// The only numeric kind encoding/json (and the tool-call
				// argument map) ever decodes a JSON number into.
				row = append(row, v)
			default:
				row = append(row, "")
			}
		}
		out = append(out, row)
	}
	return out
}

// setEnum copies node[key] into out[key] only when it's a string present in
// allowed; an invalid or absent value is simply omitted, letting the
// renderer fall back to its own default styling.
func setEnum(out, node map[string]any, key string, allowed []string) {
	v, ok := node[key].(string)
	if !ok {
		return
	}
	for _, a := range allowed {
		if v == a {
			out[key] = v
			return
		}
	}
}

func stringField(node map[string]any, key string) string {
	s, _ := node[key].(string)
	return s
}

// numberField reads a numeric field. JSON numbers decode to float64 through
// encoding/json and through the agent's tool-call argument map, so that's
// the only numeric kind accepted here.
func numberField(node map[string]any, key string) (float64, bool) {
	v, ok := node[key].(float64)
	return v, ok
}

// clampString truncates s to at most maxLen bytes, backing off to the
// nearest earlier rune boundary so a multi-byte UTF-8 character (Chinese,
// emoji, …) is never split — a byte-blind s[:maxLen] can produce invalid
// UTF-8 that downstream JSON encoding silently mangles into U+FFFD.
func clampString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func clampNumber(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// toSlice normalizes a JSON-decoded array value ([]any, the only shape
// encoding/json and the tool-call argument map ever produce for a JSON
// array) into []any.
func toSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}
