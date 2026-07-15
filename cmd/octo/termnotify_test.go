package main

import (
	"os"
	"strings"
	"testing"
)

func TestSetTitleSeq(t *testing.T) {
	got := setTitleSeq("my session")
	want := "\033]2;my session\007"
	if got != want {
		t.Errorf("setTitleSeq = %q, want %q", got, want)
	}
}

func TestSanitizeOSC(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"normal title", "normal title"},
		{"with\abell", "withbell"},
		{"with\033esc", "withesc"},
		{"back\\slash", "backslash"},
		{"tab\there", "tab\there"}, // tabs preserved
		{"\x07\x1b\\", ""},
	}
	for _, c := range cases {
		if got := sanitizeOSC(c.in); got != c.want {
			t.Errorf("sanitizeOSC(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNotifySeq(t *testing.T) {
	term := os.Getenv("TERM_PROGRAM")
	defer os.Setenv("TERM_PROGRAM", term)

	cases := []struct {
		termProgram string
		wantSubstr  string // substring we expect in the output
		wantExactly string // if non-empty, exact match expected
	}{
		{"ghostty", "\033]777;notify;", ""},
		{"iTerm.app", "\033]9;body\007", "\033]9;body\007"},
		{"WezTerm", "\033]9;body\007", "\033]9;body\007"},
		{"kitty", "\033]99;i=1:d=0;body\007", "\033]99;i=1:d=0;body\007"},
		{"", "\a", "\a"}, // unknown → bell
		{"vscode", "\a", "\a"},
	}
	for _, c := range cases {
		os.Setenv("TERM_PROGRAM", c.termProgram)
		got := notifySeq("title", "body")
		if c.wantExactly != "" {
			if got != c.wantExactly {
				t.Errorf("TERM_PROGRAM=%q: got %q, want exactly %q", c.termProgram, got, c.wantExactly)
			}
			continue
		}
		if !strings.Contains(got, c.wantSubstr) {
			t.Errorf("TERM_PROGRAM=%q: got %q, want substring %q", c.termProgram, got, c.wantSubstr)
		}
	}
}
