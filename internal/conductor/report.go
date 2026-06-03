package conductor

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Report writes a structured human-readable summary of a ledger: overall
// status, per-unit state, and the blocked/abandoned units that need
// attention. Used by `octo conduct status/report` and printed at the end of
// an unattended run.
func Report(w io.Writer, l *Ledger) {
	fmt.Fprintf(w, "Ledger %s  [%s]  (%d iterations)\n", l.ShortID(), l.Status, l.Iterations)
	fmt.Fprintf(w, "Goal: %s\n\n", oneLine(l.Goal, 0))

	counts := map[UnitStatus]int{}
	for _, u := range l.Units {
		counts[u.Status]++
	}
	fmt.Fprintf(w, "Units: %d done · %d in-progress · %d pending · %d blocked · %d abandoned\n\n",
		counts[UnitDone], counts[UnitInProgress], counts[UnitPending], counts[UnitBlocked], counts[UnitAbandoned])

	hasResult := false
	for _, u := range l.Units {
		fmt.Fprintf(w, "  %s #%-3d %s\n", unitGlyph(u.Status), u.ID, oneLine(u.Description, 80))
		if len(u.BlockedBy) > 0 {
			fmt.Fprintf(w, "        ↳ blocked_by: %s\n", joinInts(u.BlockedBy))
		}
		// One-line preview of a done unit's result, so `status` shows what the
		// worker produced — the full text is via `octo conduct show`.
		if u.Status == UnitDone && strings.TrimSpace(u.ResultSummary) != "" {
			hasResult = true
			fmt.Fprintf(w, "        ↳ %s\n", oneLine(u.ResultSummary, 100))
		}
		if u.Status == UnitBlocked && u.LastError != "" {
			fmt.Fprintf(w, "        ✗ %s\n", oneLine(u.LastError, 100))
		}
	}

	// Call out what needs a human, if anything.
	var blocked []int
	for _, u := range l.Units {
		if u.Status == UnitBlocked {
			blocked = append(blocked, u.ID)
		}
	}
	if len(blocked) > 0 {
		sort.Ints(blocked)
		fmt.Fprintf(w, "\nNeeds attention: %d blocked unit(s): %s\n", len(blocked), joinInts(blocked))
		fmt.Fprintf(w, "Resume after intervention with: octo conduct resume %s\n", l.ShortID())
	}
	if hasResult {
		fmt.Fprintf(w, "\nFull results: octo conduct show %s\n", l.ShortID())
	}
}

// HasResults reports whether any done unit has a result worth showing. Callers
// use it to decide whether to offer `conduct show` / a "view details?" prompt.
func (l *Ledger) HasResults() bool {
	for _, u := range l.Units {
		if u.Status == UnitDone && strings.TrimSpace(u.ResultSummary) != "" {
			return true
		}
	}
	return false
}

// ShowResults writes the FULL result of each done unit (and the error of each
// blocked unit). unitID==0 shows every unit; a positive unitID shows just that
// one. Backs `octo conduct show <id> [unit-id]` and the TUI "view details" path.
func ShowResults(w io.Writer, l *Ledger, unitID int) {
	fmt.Fprintf(w, "Ledger %s  [%s] — %s\n", l.ShortID(), l.Status, oneLine(l.Goal, 0))

	shown := 0
	for _, u := range l.Units {
		if unitID != 0 && u.ID != unitID {
			continue
		}
		switch u.Status {
		case UnitDone:
			fmt.Fprintf(w, "\n%s #%d %s\n", unitGlyph(u.Status), u.ID, oneLine(u.Description, 0))
			body := strings.TrimSpace(u.ResultSummary)
			if body == "" {
				body = "(no result text recorded)"
			}
			fmt.Fprintf(w, "%s\n", body)
			shown++
		case UnitBlocked:
			fmt.Fprintf(w, "\n%s #%d %s\n", unitGlyph(u.Status), u.ID, oneLine(u.Description, 0))
			fmt.Fprintf(w, "FAILED: %s\n", strings.TrimSpace(u.LastError))
			shown++
		default:
			if unitID != 0 { // explicitly asked for a not-yet-done unit
				fmt.Fprintf(w, "\n%s #%d %s — %s (no result yet)\n", unitGlyph(u.Status), u.ID, oneLine(u.Description, 0), u.Status)
				shown++
			}
		}
	}
	if shown == 0 {
		if unitID != 0 {
			fmt.Fprintf(w, "\nNo unit #%d in this ledger.\n", unitID)
		} else {
			fmt.Fprintln(w, "\nNo results yet.")
		}
	}
}

func unitGlyph(s UnitStatus) string {
	switch s {
	case UnitDone:
		return "✓"
	case UnitInProgress:
		return "▶"
	case UnitBlocked:
		return "✗"
	case UnitAbandoned:
		return "∅"
	default:
		return "·"
	}
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ", ")
}
