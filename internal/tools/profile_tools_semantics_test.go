package tools

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// The session path reads the same rule as delegation: an absent tools list
// inherits everything, an explicit empty one grants nothing. The profile's
// source no longer decides this — the file does.
func TestDefaultToolsForProfile_NilInheritsEmptyDenies(t *testing.T) {
	cases := []struct {
		name     string
		tools    []string
		wantAll  bool
		wantNone bool
	}{
		{name: "absent list inherits every tool", tools: nil, wantAll: true},
		{name: "explicit empty list grants nothing", tools: []string{}, wantNone: true},
		{name: "populated list filters", tools: []string{"grep"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := agentprofile.New(t.TempDir())
			if err := store.Create(&agentprofile.Profile{
				ID:             "scoped",
				Description:    "d",
				CapabilitySpec: agentprofile.CapabilitySpec{Tools: tc.tools},
			}); err != nil {
				t.Fatal(err)
			}

			ctx := WithProfileStore(context.Background(), store)
			ctx = WithSessionAgentID(ctx, "scoped")

			all := DefaultToolsFor("", 0)
			got := DefaultToolsForProfile(ctx, "", 0)

			switch {
			case tc.wantAll:
				if len(got) != len(all) {
					t.Errorf("got %d tools, want the full set of %d", len(got), len(all))
				}
			case tc.wantNone:
				if len(got) != 0 {
					t.Errorf("got %d tools, want none", len(got))
				}
			default:
				if len(got) != 1 || got[0].Name != "grep" {
					t.Errorf("expected only grep, got %d tools", len(got))
				}
			}
		})
	}
}

// A sub_agent call passing `tools: []` is an explicit "no tools" and must not
// silently fall back to the profile's own list.
func TestStringSliceArg_DistinguishesAbsentFromEmpty(t *testing.T) {
	if got := stringSliceArg(map[string]any{}, "tools"); got != nil {
		t.Errorf("absent key should read as nil, got %#v", got)
	}
	got := stringSliceArg(map[string]any{"tools": []any{}}, "tools")
	if got == nil {
		t.Error("an explicit empty array should not read as absent")
	}
	if len(got) != 0 {
		t.Errorf("expected an empty list, got %#v", got)
	}
}
