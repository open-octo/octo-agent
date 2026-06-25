package main

import (
	"strings"
	"testing"
)

func TestSplitCommittableMarkdown(t *testing.T) {
	cases := []struct {
		name             string
		buf              string
		wantCommit, rest string
	}{
		{"no boundary yet", "a single growing line", "", "a single growing line"},
		{"one paragraph break", "para one\n\nstart of two", "para one\n\n", "start of two"},
		{"two breaks commit both", "a\n\nb\n\nc", "a\n\nb\n\n", "c"},
		{
			"blank line inside a fence does not commit",
			"```go\nfunc x() {\n\n}\n```\n",
			"", "```go\nfunc x() {\n\n}\n```\n",
		},
		{
			"closed fence then blank line commits",
			"```go\nx\n```\n\nafter",
			"```go\nx\n```\n\n", "after",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			commit, rest := splitCommittableMarkdown(c.buf)
			if commit != c.wantCommit || rest != c.rest {
				t.Errorf("split(%q) = (%q, %q), want (%q, %q)", c.buf, commit, rest, c.wantCommit, c.rest)
			}
		})
	}
}

func TestInjectAssistantPrefix(t *testing.T) {
	const prefix = "◆ "
	cases := []struct {
		name, in, want string
	}{
		{"plain leading spaces", "  hello", "◆ hello"},
		{"no indent", "hello", "◆ hello"},
		// glamour shape: margin spaces sit behind zero-width SGR escapes; the
		// colour escape that styles the text must survive.
		{
			"glamour margin behind escapes",
			"\x1b[0m\x1b[0m  \x1b[38;5;252mhello\x1b[0m",
			"◆ \x1b[0m\x1b[0m\x1b[38;5;252mhello\x1b[0m",
		},
		{"escape then space then text", "\x1b[1m  hi", "◆ \x1b[1mhi"},
		{"empty", "", ""},
		{"only spaces", "   ", "   "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := injectAssistantPrefix(c.in, prefix); got != c.want {
				t.Errorf("injectAssistantPrefix(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestInjectAssistantPrefix_RealGlamour guards the original bug: glamour's
// margin must not leave visible leading spaces between the ◆ prefix and the
// text. We assert no space immediately follows the prefix once escapes are
// stripped from the visible run.
func TestInjectAssistantPrefix_RealGlamour(t *testing.T) {
	var md markdownRenderer
	rendered := md.render("hello world", 80)
	out := injectAssistantPrefix(rendered, "◆ ")
	if !strings.HasPrefix(out, "◆ ") {
		t.Fatalf("output should start with prefix; got %q", out)
	}
	// Strip the prefix and all leading escapes; the next byte must be the
	// glyph 'h', never a leftover margin space.
	rest := strings.TrimPrefix(out, "◆ ")
	for {
		if n := ansiPrefixLen(rest); n > 0 {
			rest = rest[n:]
			continue
		}
		break
	}
	if strings.HasPrefix(rest, " ") {
		t.Errorf("margin space survived after prefix+escapes; full output: %q", out)
	}
}

func TestMarkdownRenderer_RendersAndIsBestEffort(t *testing.T) {
	var md markdownRenderer

	if got := md.render("", 80); got != "" {
		t.Errorf("empty input should render empty; got %q", got)
	}
	if got := md.render("   \n  ", 80); got != "" {
		t.Errorf("whitespace-only should render empty; got %q", got)
	}

	out := md.render("# Title\n\nsome **bold** text", 80)
	if !strings.Contains(out, "Title") || !strings.Contains(out, "bold") {
		t.Errorf("rendered markdown should retain the words; got:\n%s", out)
	}

	// Width 0 must not panic and should still render the text.
	if got := md.render("hello", 0); !strings.Contains(got, "hello") {
		t.Errorf("width 0 render = %q, want it to contain 'hello'", got)
	}
}
