package app

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/provider/anthropic"
)

func clientFor(t *testing.T, opts SenderOptions) *anthropic.Client {
	t.Helper()
	s, err := NewSender(opts)
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	c, ok := s.(sender).p.(*anthropic.Client)
	if !ok {
		t.Fatalf("client type = %T, want *anthropic.Client", s.(sender).p)
	}
	return c
}

// TestNewSender_SharesOneLimiterPerEndpoint is the property the whole feature
// rests on. Every caller builds its own sender — the main loop, each
// sub-agent, each workflow step, the background title call, the vision helper
// — so per-client limiters would each carry their own quota and the endpoint
// would still be hammered n times over.
func TestNewSender_SharesOneLimiterPerEndpoint(t *testing.T) {
	opts := SenderOptions{
		Provider: "anthropic", APIKey: "sk-test",
		BaseURL: "https://limits.example", RPM: 8, MaxConcurrency: 1,
	}
	a, b := clientFor(t, opts), clientFor(t, opts)
	if a.Limiter == nil {
		t.Fatal("limiter is nil despite configured limits")
	}
	if a.Limiter != b.Limiter {
		t.Error("two senders for the same endpoint got different limiters — each caller would get its own quota")
	}

	other := opts
	other.BaseURL = "https://other.example"
	if c := clientFor(t, other); c.Limiter == a.Limiter {
		t.Error("a different endpoint shares the limiter")
	}
}

// An endpoint that spells out the vendor's own URL and one that leaves
// base_url blank are the same endpoint, and must not get a quota each.
func TestNewSender_BlankBaseURLSharesTheVendorDefaultsLimiter(t *testing.T) {
	blank := SenderOptions{Provider: "anthropic", APIKey: "sk-test", RPM: 4}
	spelled := blank
	spelled.BaseURL = VendorBaseURL("anthropic")
	if a, b := clientFor(t, blank), clientFor(t, spelled); a.Limiter != b.Limiter {
		t.Error("blank base_url and the spelled-out vendor default got different limiters")
	}
}

// No limits configured must leave the client exactly as it was before this
// feature: an unthrottled client, not a limiter that happens to allow
// everything.
func TestNewSender_NoLimitsLeavesClientUngated(t *testing.T) {
	c := clientFor(t, SenderOptions{Provider: "anthropic", APIKey: "sk-test", BaseURL: "https://nolimits.example"})
	if c.Limiter != nil {
		t.Errorf("Limiter = %v, want nil when neither rpm nor max_concurrency is set", c.Limiter)
	}
}
