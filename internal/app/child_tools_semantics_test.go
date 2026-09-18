package app

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// filterChildTools reads the same nil-vs-empty rule the session path applies
// to a profile's frontmatter: no list means inherit, an empty list means none.
func TestFilterChildTools_NilInheritsEmptyDenies(t *testing.T) {
	parent := []agent.ToolDefinition{
		{Name: "read_file"},
		{Name: "write_file"},
		{Name: "grep"},
	}

	cases := []struct {
		name    string
		allowed []string
		want    []string
	}{
		{
			name:    "nil allowlist inherits everything",
			allowed: nil,
			want:    []string{"read_file", "write_file", "grep"},
		},
		{
			name:    "empty allowlist denies everything",
			allowed: []string{},
			want:    nil,
		},
		{
			name:    "populated allowlist filters",
			allowed: []string{"grep"},
			want:    []string{"grep"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterChildTools(parent, tc.allowed, nil, false)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tools %v, want %d %v", len(got), names(got), len(tc.want), tc.want)
			}
			for i, n := range tc.want {
				if got[i].Name != n {
					t.Errorf("tool %d = %q, want %q", i, got[i].Name, n)
				}
			}
		})
	}
}

func names(defs []agent.ToolDefinition) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Name
	}
	return out
}
