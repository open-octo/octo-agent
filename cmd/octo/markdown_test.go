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
