package genui

import "testing"

func TestStripOctoUIFences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no fence is byte-identical",
			in:   "just a normal reply with no fences at all.\n\nSecond paragraph.",
			want: "just a normal reply with no fences at all.\n\nSecond paragraph.",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "one well-formed fence, surrounding text preserved",
			in:   "Before.\n```octo-ui\n{\"items\":[]}\n```\nAfter.",
			want: "Before.\n" + PlaceholderText + "\nAfter.",
		},
		{
			name: "fence at the very start with no preceding text",
			in:   "```octo-ui\n{\"items\":[]}\n```\nAfter.",
			want: PlaceholderText + "\nAfter.",
		},
		{
			name: "fence at the very end, no trailing newline",
			in:   "Before.\n```octo-ui\n{\"items\":[]}\n```",
			want: "Before.\n" + PlaceholderText,
		},
		{
			name: "multiple fences all replaced",
			in:   "One.\n```octo-ui\n{\"items\":[1]}\n```\nTwo.\n```octo-ui\n{\"items\":[2]}\n```\nThree.",
			want: "One.\n" + PlaceholderText + "\nTwo.\n" + PlaceholderText + "\nThree.",
		},
		{
			name: "multi-line JSON body: whole block replaced, not just first line",
			in:   "Before.\n```octo-ui\n{\n  \"title\": \"x\",\n  \"items\": [\n    {\"type\": \"text\", \"text\": \"hi\"}\n  ]\n}\n```\nAfter.",
			want: "Before.\n" + PlaceholderText + "\nAfter.",
		},
		{
			name: "unclosed trailing fence left untouched",
			in:   "Before.\n```octo-ui\n{\"items\": [{\"type\": \"text\"",
			want: "Before.\n```octo-ui\n{\"items\": [{\"type\": \"text\"",
		},
		{
			name: "unclosed fence at absolute start",
			in:   "```octo-ui\nstill streaming...",
			want: "```octo-ui\nstill streaming...",
		},
		{
			name: "closed fence followed by an unclosed one: first replaced, second left alone",
			in:   "```octo-ui\n{\"items\":[1]}\n```\nMiddle text.\n```octo-ui\n{\"items\": still streaming",
			want: PlaceholderText + "\nMiddle text.\n```octo-ui\n{\"items\": still streaming",
		},
		{
			name: "trailing whitespace on the fence marker lines is tolerated",
			in:   "```octo-ui   \n{\"items\":[]}\n```   \nAfter.",
			want: PlaceholderText + "\nAfter.",
		},
		{
			name: "octo-ui substring not alone on its own line is not a fence",
			in:   "text mentioning ```octo-ui inline, not a real fence.",
			want: "text mentioning ```octo-ui inline, not a real fence.",
		},
		{
			name: "bare ``` fence (no octo-ui tag) is left alone",
			in:   "Before.\n```\nplain code\n```\nAfter.",
			want: "Before.\n```\nplain code\n```\nAfter.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StripOctoUIFences(tc.in)
			if got != tc.want {
				t.Fatalf("StripOctoUIFences(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripOctoUIFences_NoFenceReturnsSameString asserts the no-fence case
// returns the exact same string value (not merely an equal one) — the
// fast-path short-circuit this depends on for zero risk of accidental
// mutation on the overwhelmingly common case.
func TestStripOctoUIFences_NoFenceReturnsSameString(t *testing.T) {
	in := "a perfectly ordinary reply\nwith multiple\nlines of text."
	got := StripOctoUIFences(in)
	if got != in {
		t.Fatalf("got %q, want unchanged %q", got, in)
	}
}

// TestStripOctoUIFences_DoesNotHang guards against a pathological input
// (many open markers, none ever closed) causing quadratic or unbounded
// behavior. Not a benchmark — just a sanity check that this terminates.
func TestStripOctoUIFences_DoesNotHang(t *testing.T) {
	var b []byte
	for i := 0; i < 500; i++ {
		b = append(b, []byte("```octo-ui\nnot json\n")...)
	}
	_ = StripOctoUIFences(string(b))
}
