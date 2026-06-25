package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFirstLine_TruncatesOnRunes(t *testing.T) {
	longCJK := "中" + strings.Repeat("文", 80)
	got := firstLine(longCJK)
	if strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("got replacement character in %q", got)
	}
	if utf8.RuneCountInString(got) > 72 {
		t.Errorf("rune count too large: %d, %q", utf8.RuneCountInString(got), got)
	}
}

func TestFirstLine_TakesFirstLine(t *testing.T) {
	got := firstLine("line one\nline two\nline three")
	if got != "line one" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 3); got != "hel" {
		t.Errorf("got %q", got)
	}
	if got := truncateRunes("你好", 3); got != "你好" {
		t.Errorf("got %q", got)
	}
	if got := truncateRunes("", 5); got != "" {
		t.Errorf("got %q", got)
	}
}
