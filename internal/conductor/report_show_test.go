package conductor

import (
	"bytes"
	"strings"
	"testing"
)

func resultLedger() *Ledger {
	return &Ledger{
		ID: "20260603-020753-f5127d34", Goal: "research workflows", Status: LedgerDone,
		Units: []Unit{
			{ID: 1, Description: "research the docs", Status: UnitDone, ResultSummary: "Found it: Dynamic Workflows ship JS orchestration."},
			{ID: 2, Description: "impossible bit", Status: UnitBlocked, LastError: "build broke: undefined Foo"},
			{ID: 3, Description: "not started", Status: UnitPending},
		},
	}
}

func TestHasResults(t *testing.T) {
	if !resultLedger().HasResults() {
		t.Error("HasResults = false, want true (unit 1 has a result)")
	}
	empty := &Ledger{Units: []Unit{{ID: 1, Status: UnitPending}}}
	if empty.HasResults() {
		t.Error("HasResults = true on a ledger with no done results")
	}
}

func TestReportShowsResultPreviewAndHint(t *testing.T) {
	var b bytes.Buffer
	Report(&b, resultLedger())
	out := b.String()
	if !strings.Contains(out, "Found it: Dynamic Workflows") {
		t.Errorf("report missing result preview:\n%s", out)
	}
	if !strings.Contains(out, "octo conduct show f5127d34") {
		t.Errorf("report missing 'conduct show' hint:\n%s", out)
	}
}

func TestShowResultsAll(t *testing.T) {
	var b bytes.Buffer
	ShowResults(&b, resultLedger(), 0)
	out := b.String()
	// Full result body of the done unit…
	if !strings.Contains(out, "Dynamic Workflows ship JS orchestration") {
		t.Errorf("ShowResults missing done-unit body:\n%s", out)
	}
	// …and the blocked unit's failure.
	if !strings.Contains(out, "FAILED: build broke: undefined Foo") {
		t.Errorf("ShowResults missing blocked-unit error:\n%s", out)
	}
}

func TestShowResultsSingleUnit(t *testing.T) {
	var b bytes.Buffer
	ShowResults(&b, resultLedger(), 1)
	out := b.String()
	if !strings.Contains(out, "Dynamic Workflows ship JS orchestration") {
		t.Errorf("ShowResults(1) missing unit 1 body:\n%s", out)
	}
	if strings.Contains(out, "FAILED") {
		t.Errorf("ShowResults(1) should not include unit 2:\n%s", out)
	}
}

func TestShowResultsUnknownUnit(t *testing.T) {
	var b bytes.Buffer
	ShowResults(&b, resultLedger(), 99)
	if !strings.Contains(b.String(), "No unit #99") {
		t.Errorf("ShowResults(99) should report the missing unit:\n%s", b.String())
	}
}
