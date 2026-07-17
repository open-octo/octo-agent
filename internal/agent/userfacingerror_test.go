package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUserFacingError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"loop wrap + provider prefix",
			"agent: loop[0]: anthropic: HTTP 403 (permission_error): invalid api key",
			"HTTP 403 (permission_error): invalid api key",
		},
		{
			"dispatch wrap + provider prefix",
			"agent: dispatch tools[1]: openai: HTTP 429 rate limit",
			"HTTP 429 rate limit",
		},
		{
			"bare anthropic prefix",
			"anthropic: HTTP 403 forbidden",
			"HTTP 403 forbidden",
		},
		{
			"bare openai prefix",
			"openai: HTTP 500 internal error",
			"HTTP 500 internal error",
		},
		{
			// No ": " after the "agent: " segment — nothing to strip, the
			// message must survive intact.
			"agent prefix without inner separator preserved",
			"agent: no Sender configured",
			"agent: no Sender configured",
		},
		{
			"non-provider prefix preserved",
			"workflow: step failed",
			"workflow: step failed",
		},
		{
			"loop wrap around non-provider keeps tool prefix",
			"agent: loop[0]: workflow: boom",
			"workflow: boom",
		},
		{
			"plain error unchanged",
			"something broke",
			"something broke",
		},
		{
			"provider prefix match is case sensitive",
			"Anthropic: HTTP 403",
			"Anthropic: HTTP 403",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UserFacingError(errors.New(tc.in)); got != tc.want {
				t.Errorf("UserFacingError(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestKnownProviderPrefixesInSync guards against silent drift: every protocol
// implementation under internal/provider/ must have its error-prefix name in
// knownProviderPrefixes (and vice versa), or user-facing errors leak a raw
// "provider: " prefix again.
func TestKnownProviderPrefixesInSync(t *testing.T) {
	// retry is a shared retry helper, not a protocol implementation.
	nonProtocol := map[string]bool{"retry": true}

	entries, err := os.ReadDir(filepath.Join("..", "provider"))
	if err != nil {
		t.Fatalf("read provider dir: %v", err)
	}
	impls := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && !nonProtocol[e.Name()] {
			impls[e.Name()] = true
		}
	}
	known := map[string]bool{}
	for _, p := range knownProviderPrefixes {
		known[p] = true
	}
	for name := range impls {
		if !known[name] {
			t.Errorf("internal/provider/%s/ exists but %q is not in knownProviderPrefixes — its errors will leak a raw prefix", name, name)
		}
	}
	for name := range known {
		if !impls[name] {
			t.Errorf("knownProviderPrefixes entry %q has no matching internal/provider/%s/ directory", name, name)
		}
	}
}
