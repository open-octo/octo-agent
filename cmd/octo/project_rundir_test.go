package main

import "testing"

// projectRunDir decides where a CLI/TUI run works when the resumed session
// belongs to a project. The cases below pin the two halves of that rule: a
// project relocates the run, and nothing else does — in particular NOT the
// session's own working dir, which is seeded to ~/Octo on every web session
// and would otherwise pull `octo -c` out of the user's current repo.
func TestProjectRunDir(t *testing.T) {
	const here = "/repos/octo"

	tests := []struct {
		name     string
		resumeID string
		lookup   func(string) string
		want     string
	}{
		{
			name:     "fresh run resolves nothing",
			resumeID: "",
			lookup:   func(string) string { t.Fatal("lookup must not run without a resume id"); return "" },
			want:     here,
		},
		{
			name:     "session outside any project stays put",
			resumeID: "sess-1",
			lookup:   func(string) string { return "" },
			want:     here,
		},
		{
			name:     "session in a project relocates the run",
			resumeID: "sess-1",
			lookup:   func(id string) string { return "/work/acme" },
			want:     "/work/acme",
		},
		{
			name:     "project pointing at the launch dir is a no-op",
			resumeID: "sess-1",
			lookup:   func(string) string { return here },
			want:     here,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectRunDir(here, tc.resumeID, tc.lookup); got != tc.want {
				t.Errorf("projectRunDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The lookup receives the resolved session id, not whatever the user typed —
// chat.go runs agent.ResolveSessionID before this point.
func TestProjectRunDir_PassesSessionID(t *testing.T) {
	var seen string
	projectRunDir("/repos/octo", "20260807-abcd1234", func(id string) string {
		seen = id
		return ""
	})
	if seen != "20260807-abcd1234" {
		t.Errorf("lookup got %q, want the resolved session id", seen)
	}
}
