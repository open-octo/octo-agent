package server

import (
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

// TestPR4b_Review_VerifyEntryByModelHandlesCompositeID is a regression guard
// raised by an independent code review: the reviewer claimed EntryByModel only
// matches bare model names, so applyChannelModel would silently fall back to
// the default sender after a /model switch to a composite id. This test
// verifies the reviewer's claim is FALSE — EntryByModel resolves composite ids
// against c.Endpoints (PR2), so the binding round-trips.
func TestPR4b_Review_VerifyEntryByModelHandlesCompositeID(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	seed := config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-anthropic", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-kimi", Provider: "kimi", BaseURL: "https://kimi.example", APIKey: "sk-kimi", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default: "ep-anthropic::claude-sonnet-4-6",
	}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	cfg, _ := config.Load()

	// Find the composite id the way Resolve would produce it for kimi-k2.6.
	var cid string
	for _, ep := range cfg.Endpoints {
		for _, m := range ep.Models {
			if m.Model == "kimi-k2.6" {
				cid = ep.CompositeID(m.Model)
			}
		}
	}
	if cid == "" || !strings.Contains(cid, "::") {
		t.Fatalf("could not construct composite id for kimi-k2.6; got %q", cid)
	}

	// The reviewer claimed this returns !ok. It must return ok=true.
	entry, ok := cfg.EntryByModel(cid)
	if !ok {
		t.Fatalf("EntryByModel(%q) = !ok, want ok=true — composite id must resolve (reviewer's C1 claim was wrong)", cid)
	}
	if entry.Model != "kimi-k2.6" {
		t.Errorf("entry.Model = %q, want kimi-k2.6", entry.Model)
	}
	if entry.Provider != "kimi" {
		t.Errorf("entry.Provider = %q, want kimi", entry.Provider)
	}
	if entry.BaseURL != "https://kimi.example" {
		t.Errorf("entry.BaseURL = %q, want https://kimi.example", entry.BaseURL)
	}
	if entry.APIKey != "sk-kimi" {
		t.Errorf("entry.APIKey = %q, want sk-kimi (must come from the endpoint)", entry.APIKey)
	}
}
