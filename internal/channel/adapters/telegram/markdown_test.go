package telegram

import (
	"strings"
	"testing"
)

func TestRenderTelegramHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bold", "**bold**", "<b>bold</b>"},
		{"italic underscore", "_italic_", "<i>italic</i>"},
		{"italic star", "*italic*", "<i>italic</i>"},
		{"inline code", "use `go build`", "use <code>go build</code>"},
		{"strikethrough", "~~gone~~", "<s>gone</s>"},
		{"link", "[docs](https://example.com/a)", `<a href="https://example.com/a">docs</a>`},
		{"nested bold in italic", "*_x_*", "<i><i>x</i></i>"},
		{
			"escapes literal html-significant chars",
			"a < b & c > d",
			"a &lt; b &amp; c &gt; d",
		},
		{
			"unbalanced asterisk stays literal, no crash",
			"cost is *not* balanced * here",
			"cost is <i>not</i> balanced * here",
		},
		{
			"fenced code block",
			"```go\nfmt.Println(1 < 2)\n```",
			`<pre><code class="language-go">fmt.Println(1 &lt; 2)</code></pre>`,
		},
		{
			"fenced code block no language",
			"```\nraw & <text>\n```",
			"<pre><code>raw &amp; &lt;text&gt;</code></pre>",
		},
		{
			"unordered list",
			"- a\n- b\n",
			"• a\n• b",
		},
		{
			"ordered list",
			"1. a\n2. b\n",
			"1. a\n2. b",
		},
		{
			"blockquote",
			"> quoted",
			"<blockquote>quoted</blockquote>",
		},
		{
			"bare url autolinked",
			"see https://example.com/x for details",
			`see <a href="https://example.com/x">https://example.com/x</a> for details`,
		},
		{
			"heading becomes bold",
			"# Title",
			"<b>Title</b>",
		},
		{
			"raw inline html escaped, not passed through",
			"click <b>here</b>",
			"click &lt;b&gt;here&lt;/b&gt;",
		},
		{
			"image degrades to alt text plus url",
			"![alt text](https://example.com/img.png)",
			"alt text (https://example.com/img.png)",
		},
		{
			"multiple paragraphs separated by blank line",
			"first\n\nsecond",
			"first\n\nsecond",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderTelegramHTML(tc.in)
			if got != tc.want {
				t.Errorf("renderTelegramHTML(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A single unbalanced special character must never take down formatting for
// the rest of the message — the exact #1119 failure mode under legacy
// Markdown mode, where the whole chunk fell back to plain text.
func TestRenderTelegramHTML_UnbalancedCharDoesNotBreakRestOfMessage(t *testing.T) {
	in := "**bold works** but here is a stray * and then **more bold**"
	got := renderTelegramHTML(in)
	want := "<b>bold works</b> but here is a stray * and then <b>more bold</b>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTelegramHTML_NeverPanics(t *testing.T) {
	inputs := []string{
		"",
		"*",
		"**",
		"***",
		"[broken(",
		"```unterminated fence",
		"<script>alert(1)</script>",
		"a &amp; b",
		strings.Repeat("*", 500),
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("renderTelegramHTML(%q) panicked: %v", in, r)
				}
			}()
			renderTelegramHTML(in)
		}()
	}
}
