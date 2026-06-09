package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// step feeds one key and returns the updated concrete model.
func step(m selectModel, k string) selectModel {
	next, _ := m.Update(keyMsg(k))
	return next.(selectModel)
}

func TestSelectModel_Navigation(t *testing.T) {
	items := []selectItem{{value: "a"}, {value: "b"}, {value: "c"}}
	m := selectModel{items: items, cursor: 0}

	// Down moves the cursor; clamps at the last row.
	m = step(m, "down")
	m = step(m, "down")
	m = step(m, "down") // clamp
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (clamped at last)", m.cursor)
	}
	// j/k aliases work too.
	m = step(m, "k")
	if m.cursor != 1 {
		t.Fatalf("k should move up; cursor = %d, want 1", m.cursor)
	}
	// Up clamps at the first row.
	m = step(m, "up")
	m = step(m, "up")
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (clamped at first)", m.cursor)
	}
}

func TestSelectModel_ChooseAndCancel(t *testing.T) {
	items := []selectItem{{value: "a"}, {value: "b"}}

	chosen := step(selectModel{items: items, cursor: 1}, "enter")
	if !chosen.chosen || chosen.cancelled {
		t.Fatalf("enter should choose; got chosen=%v cancelled=%v", chosen.chosen, chosen.cancelled)
	}
	if chosen.View() != "" {
		t.Error("View should be empty once chosen so the menu clears")
	}

	cancelled := step(selectModel{items: items}, "esc")
	if cancelled.chosen || !cancelled.cancelled {
		t.Fatalf("esc should cancel; got chosen=%v cancelled=%v", cancelled.chosen, cancelled.cancelled)
	}
}

func TestBuildModelItems(t *testing.T) {
	models := []string{"m-small", "m-big"}

	// Default model is annotated; current pick already in the catalogue adds no row.
	items := buildModelItems(models, "m-small", "m-big")
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	if items[0].desc != "default" {
		t.Errorf("m-small should be marked default, got %q", items[0].desc)
	}

	// A current pick outside the catalogue is appended so it can be pre-selected.
	items = buildModelItems(models, "m-small", "m-custom")
	if len(items) != 3 || items[2].value != "m-custom" || items[2].desc != "current" {
		t.Fatalf("custom current model not folded in: %+v", items)
	}
}
