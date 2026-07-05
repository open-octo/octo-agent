package channel

import "testing"

func TestFlattenPipeTables(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "basic table",
			in:   "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |",
			want: "Name | Age\nAlice | 30\nBob | 25",
		},
		{
			name: "aligned separator variants",
			in:   "| Name | Age |\n|:---|---:|\n| Alice | 30 |",
			want: "Name | Age\nAlice | 30",
		},
		{
			name: "no leading/trailing pipes",
			in:   "Name | Age\n--- | ---\nAlice | 30",
			want: "Name | Age\nAlice | 30",
		},
		{
			name: "uneven padding is normalized",
			in:   "|Name|Age|\n|---|---|\n|Alice   |30|",
			want: "Name | Age\nAlice | 30",
		},
		{
			name: "prose with a pipe is left alone (no separator row follows)",
			in:   "cost is a|b comparison",
			want: "cost is a|b comparison",
		},
		{
			name: "plain horizontal rule is not mistaken for a table separator",
			in:   "above\n\n---\n\nbelow",
			want: "above\n\n---\n\nbelow",
		},
		{
			name: "table followed by prose",
			in:   "| A | B |\n| --- | --- |\n| 1 | 2 |\nsome trailing text",
			want: "A | B\n1 | 2\nsome trailing text",
		},
		{
			name: "text with no table is unchanged",
			in:   "just some plain text\nwith multiple lines",
			want: "just some plain text\nwith multiple lines",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FlattenPipeTables(tc.in)
			if got != tc.want {
				t.Errorf("FlattenPipeTables(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLooksLikeMarkdownTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"real table", "| A | B |\n| --- | --- |\n| 1 | 2 |", true},
		{"real table no outer pipes", "A | B\n--- | ---\n1 | 2", true},
		{"aligned separator", "| A | B |\n|:--|--:|\n| 1 | 2 |", true},
		{"prose with two pipes, no separator row", "this | is | prose", false},
		{"prose with pipe followed by unrelated text", "a | b\nnot a separator", false},
		{"plain horizontal rule", "above\n\n---\n\nbelow", false},
		{"no pipes at all", "just plain text", false},
		{"empty string", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LooksLikeMarkdownTable(tc.in)
			if got != tc.want {
				t.Errorf("LooksLikeMarkdownTable(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
