package main

import (
	"strings"
	"testing"
)

func TestSoftWrappedRows_LongLineWraps(t *testing.T) {
	m := newTestModel()
	m.ta.SetWidth(20)
	setInput(m, strings.Repeat("a", 40))

	got := m.softWrappedRows()
	if got < 2 {
		t.Errorf("long line should soft-wrap into multiple rows, got %d", got)
	}
}

func TestSoftWrappedRows_HardNewlinesCount(t *testing.T) {
	m := newTestModel()
	m.ta.SetWidth(80)
	setInput(m, "one\ntwo\nthree")

	if got := m.softWrappedRows(); got != 3 {
		t.Errorf("three hard newlines = 3 rows, got %d", got)
	}
}
