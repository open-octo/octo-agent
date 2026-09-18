package agentprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An absent `tools:` key and an explicit `tools: []` mean opposite things —
// inherit everything vs. nothing — so parsing must keep them distinguishable.
// nil-ness is the only carrier of that distinction.
func TestParseFile_ToolsNilVsEmpty(t *testing.T) {
	dir := t.TempDir()
	write := func(name, front string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("---\n"+front+"---\n\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	cases := []struct {
		name     string
		front    string
		wantNil  bool
		wantLen  int
		wantHead string
	}{
		{name: "absent", front: "description: d\n", wantNil: true},
		{name: "explicit empty", front: "description: d\ntools: []\n"},
		{name: "null value reads as absent", front: "description: d\ntools:\n", wantNil: true},
		{name: "list", front: "description: d\ntools: [read_file, grep]\n", wantLen: 2, wantHead: "read_file"},
		// A list of nothing but retired names declared a list, so what's left
		// is "no tools" — not a fallback to inheritance.
		{name: "only retired names", front: "description: d\ntools: [enable_own_skill]\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parseFile(write(strings.ReplaceAll(tc.name, " ", "-")+".md", tc.front))
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Tools == nil; got != tc.wantNil {
				t.Fatalf("Tools == nil is %v, want %v (Tools = %#v)", got, tc.wantNil, p.Tools)
			}
			if len(p.Tools) != tc.wantLen {
				t.Fatalf("len(Tools) = %d, want %d", len(p.Tools), tc.wantLen)
			}
			if tc.wantHead != "" && p.Tools[0] != tc.wantHead {
				t.Errorf("Tools[0] = %q, want %q", p.Tools[0], tc.wantHead)
			}
		})
	}
}

// Writing a profile back must not turn an explicit restriction into
// inheritance — that would silently widen what the agent can do.
func TestSerialize_RoundTripsToolsEmptiness(t *testing.T) {
	cases := []struct {
		name      string
		tools     []string
		wantInFM  bool
		wantAfter func(t *testing.T, got []string)
	}{
		{
			name:  "absent stays absent",
			tools: nil,
			wantAfter: func(t *testing.T, got []string) {
				if got != nil {
					t.Errorf("round-trip turned an absent list into %#v", got)
				}
			},
		},
		{
			name:     "explicit empty survives",
			tools:    []string{},
			wantInFM: true,
			wantAfter: func(t *testing.T, got []string) {
				if got == nil {
					t.Error("round-trip turned `tools: []` back into inheritance")
				}
				if len(got) != 0 {
					t.Errorf("expected an empty list, got %#v", got)
				}
			},
		},
		{
			name:     "list survives",
			tools:    []string{"read_file"},
			wantInFM: true,
			wantAfter: func(t *testing.T, got []string) {
				if len(got) != 1 || got[0] != "read_file" {
					t.Errorf("round-trip mangled the list: %#v", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := serialize(&Profile{
				ID:             "x",
				Description:    "d",
				CapabilitySpec: CapabilitySpec{Tools: tc.tools},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(b), "tools:"); got != tc.wantInFM {
				t.Fatalf("frontmatter contains `tools:` = %v, want %v:\n%s", got, tc.wantInFM, b)
			}

			path := filepath.Join(t.TempDir(), "x.md")
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := parseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tc.wantAfter(t, p.Tools)
		})
	}
}
