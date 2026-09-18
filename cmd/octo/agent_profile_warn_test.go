package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// An empty tools list means opposite things by source: "none" for a user
// profile, "all" for the built-in tiers. Only the first case is a surprise
// worth warning about.
func TestWarnIfProfileHasNoTools(t *testing.T) {
	cases := []struct {
		name    string
		profile *agentprofile.Profile
		warn    bool
	}{
		{
			name:    "user profile without tools",
			profile: &agentprofile.Profile{ID: "executor", Source: agentprofile.SourceUser},
			warn:    true,
		},
		{
			name: "user profile with tools",
			profile: &agentprofile.Profile{
				ID:             "executor",
				Source:         agentprofile.SourceUser,
				CapabilitySpec: agentprofile.CapabilitySpec{Tools: []string{"read_file"}},
			},
		},
		{
			name:    "curated expert without tools is not ours to warn about",
			profile: &agentprofile.Profile{ID: "copywriter", Source: agentprofile.SourceDefault},
		},
		{
			name:    "builtin tier: empty means all",
			profile: &agentprofile.Profile{ID: "general", Source: agentprofile.SourceBuiltin},
		},
		{name: "no profile at all"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			warnIfProfileHasNoTools(&buf, tc.profile)

			got := buf.String()
			if tc.warn {
				if !strings.Contains(got, "tools:") {
					t.Errorf("expected a warning naming the frontmatter key, got %q", got)
				}
				if tc.profile != nil && !strings.Contains(got, tc.profile.ID) {
					t.Errorf("warning does not name the agent: %q", got)
				}
				return
			}
			if got != "" {
				t.Errorf("expected no warning, got %q", got)
			}
		})
	}
}
