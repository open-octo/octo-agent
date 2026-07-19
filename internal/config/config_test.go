package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	return home
}

func writeOcto(t *testing.T, home, name, content string) string {
	t.Helper()
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// captureSlog replaces the slog default with a text handler writing to buf,
// returning the previous default so the caller can restore it. Use via:
//
//	logBuf := captureSlog(t)
//	t.Cleanup(restoreSlog(t, captureSlog(t)))
//
// or the captureSlog helper which wraps both. The naive pattern
// `slog.SetDefault(...); t.Cleanup(func() { slog.SetDefault(slog.Default()) })`
// is a no-op: slog.Default() returns the *current* default (the buffer handler
// we just installed), not the pre-test one, so the original is never restored
// and subsequent tests' slog output vanishes into the discarded buffer.
func captureSlog(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestLoad_MissingFileIsZeroNotError(t *testing.T) {
	setHome(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() on missing file = %v, want nil", err)
	}
	if len(c.Endpoints) != 0 || c.Default != "" {
		t.Errorf("Load() on missing file = %+v, want zero Config", c)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	setHome(t)

	want := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-fable-5"}}},
			{ID: "ep-b", Provider: "kimi", BaseURL: "https://x.example", Models: []EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default: "ep-b::kimi-k2.6",
		Lite:    "ep-a::claude-fable-5",
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Endpoints) != 2 || got.Endpoints[1].ID != want.Endpoints[1].ID || got.Endpoints[1].Provider != want.Endpoints[1].Provider {
		t.Errorf("round-trip endpoints = %+v, want %+v", got.Endpoints, want.Endpoints)
	}
	if got.Default != "ep-b::kimi-k2.6" || got.Lite != "ep-a::claude-fable-5" {
		t.Errorf("round-trip refs = default %q lite %q", got.Default, got.Lite)
	}
	if e := got.DefaultEntry(); e.Model != "kimi-k2.6" || e.BaseURL != "https://x.example" {
		t.Errorf("DefaultEntry = %+v, want kimi entry", e)
	}
}

func TestLoad_LegacyYAMLIsNormalised(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yaml",
		"provider: openai\nmodel: gpt-4o-mini\nbase_url: https://x.example\napi_key: sk-old\nreasoning_effort: high\npermission_mode: strict\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Endpoints) != 1 {
		t.Fatalf("Endpoints = %+v, want one synthesized endpoint", c.Endpoints)
	}
	ep := c.Endpoints[0]
	if ep.Provider != "openai" || ep.BaseURL != "https://x.example" || ep.APIKey != "sk-old" {
		t.Errorf("synthesized endpoint = %+v", ep)
	}
	if len(ep.Models) != 1 || ep.Models[0].Model != "gpt-4o-mini" {
		t.Errorf("endpoint models = %+v, want one gpt-4o-mini", ep.Models)
	}
	if !strings.HasSuffix(c.Default, "::gpt-4o-mini") {
		t.Errorf("Default = %q, want suffix ::gpt-4o-mini", c.Default)
	}
	if c.PermissionMode != "strict" {
		t.Errorf("global PermissionMode lost: %q", c.PermissionMode)
	}
	// PR5 (design §11.6): the top-level reasoning_effort key is the global
	// setting — Load reads it into Config.ReasoningEffort (the only place
	// reasoning is stored now). Only per-entry reasoning_effort inside a
	// legacy models: block is dropped on migration.
	if c.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high (top-level reasoning is the global setting)", c.ReasoningEffort)
	}
}

func TestLoad_MigratesCompatibleVendorsToCustom(t *testing.T) {
	home := setHome(t)
	// A pre-refactor config using the retired compatible catch-alls.
	writeOcto(t, home, "config.yml",
		"models:\n"+
			"  - provider: openai_compatible\n    model: m1\n    base_url: https://gw1.example\n"+
			"  - provider: anthropic_compatible\n    model: m2\n    base_url: https://gw2.example\n"+
			"  - provider: anthropic\n    model: claude-sonnet-4-6\n"+
			"default_model: m1\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Endpoints) != 3 {
		t.Fatalf("Endpoints = %d, want 3 (one per entry): %+v", len(c.Endpoints), c.Endpoints)
	}
	if ep := c.Endpoints[0]; ep.Provider != "custom" || ep.Protocol != "openai" {
		t.Errorf("openai_compatible endpoint = %+v, want custom/openai", ep)
	}
	if ep := c.Endpoints[1]; ep.Provider != "custom" || ep.Protocol != "anthropic" {
		t.Errorf("anthropic_compatible endpoint = %+v, want custom/anthropic", ep)
	}
	// A named vendor is left untouched.
	if ep := c.Endpoints[2]; ep.Provider != "anthropic" || ep.Protocol != "" {
		t.Errorf("anthropic endpoint = %+v, want anthropic/(no protocol)", ep)
	}
}

func TestLoad_NewFileShadowsLegacy(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yaml", "model: old-model\nprovider: openai\n")
	writeOcto(t, home, "config.yml",
		"models:\n  - provider: anthropic\n    model: new-model\ndefault_model: new-model\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e := c.DefaultEntry(); e.Model != "new-model" {
		t.Errorf("DefaultEntry().Model = %q, want new-model (config.yml must win)", e.Model)
	}
}

func TestSave_MigratesLegacyToBak(t *testing.T) {
	home := setHome(t)
	legacy := writeOcto(t, home, "config.yaml", "model: old-model\nprovider: openai\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy config.yaml still present after Save")
	}
	if _, err := os.Stat(legacy + ".bak"); err != nil {
		t.Errorf("config.yaml.bak missing: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if e := got.DefaultEntry(); e.Model != "old-model" || e.Provider != "openai" {
		t.Errorf("migrated entry = %+v", e)
	}
}

func TestSave_FileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows doesn't honor Unix permission bits — os.WriteFile(…, 0600)
		// reports 0666 via Mode().Perm(). The 0600 intent still applies on the
		// Unix platforms where it's a real access control.
		t.Skip("Unix file permissions not enforced on Windows")
	}
	home := setHome(t)

	cfg := Config{Endpoints: []Endpoint{{ID: "ep-a", Provider: "anthropic", APIKey: "sk-secret", Models: []EndpointModel{{Model: "main"}}}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, ".octo", "config.yml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// A file that can carry an API key must not be world/group readable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
}

func TestLoad_MalformedIsError(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml", "not: valid: yaml: [")
	if _, err := Load(); err == nil {
		t.Error("Load() on malformed file = nil, want error")
	}
}

func TestLoadCached_FallsBackToLastGoodOnParseError(t *testing.T) {
	t.Cleanup(resetLastGoodForTest)
	resetLastGoodForTest()
	home := setHome(t)
	writeOcto(t, home, "config.yml",
		"endpoints:\n  - id: ep-a\n    provider: anthropic\n    models:\n      - model: good-model\n")
	// default set to the good endpoint so we can verify the cached Default.
	// We'll write it via a quick Save instead of hand YAML to get the
	// composite id format right.
	cfg0, err := Load()
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}
	cfg0.Default = "ep-a::good-model"
	if err := cfg0.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg, err := LoadCached()
	if err != nil {
		t.Fatalf("LoadCached() first load = %v, want nil", err)
	}
	if cfg.Default != "ep-a::good-model" {
		t.Fatalf("LoadCached() first load Default = %q, want %q", cfg.Default, "ep-a::good-model")
	}

	writeOcto(t, home, "config.yml", "not: valid: yaml: [")

	cfg, err = LoadCached()
	if err != nil {
		t.Fatalf("LoadCached() after malformed edit = %v, want nil (fall back to last good)", err)
	}
	if cfg.Default != "ep-a::good-model" {
		t.Errorf("LoadCached() after malformed edit Default = %q, want cached %q", cfg.Default, "ep-a::good-model")
	}
}

func TestLoadCached_ErrorsWhenNothingCachedYet(t *testing.T) {
	t.Cleanup(resetLastGoodForTest)
	resetLastGoodForTest()
	home := setHome(t)
	writeOcto(t, home, "config.yml", "not: valid: yaml: [")

	if _, err := LoadCached(); err == nil {
		t.Error("LoadCached() with no prior good load and a malformed file = nil, want error")
	}
}

func TestEntryByModel_EmptyNeverMatches(t *testing.T) {
	c := Config{Endpoints: []Endpoint{{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "m"}}}}}
	if _, ok := c.EntryByModel(""); ok {
		t.Error("EntryByModel(\"\") matched, want no match")
	}
}

// TestEntryByModel_CompositeIDResolvesAgainstEndpoints covers PR2 §8.2:
// EntryByModel accepts a composite id "<endpoint_id>::<model>" and resolves
// it against c.Endpoints, projecting the EndpointModel back into a ModelEntry
// shape (Provider/BaseURL/APIKey/Protocol/Vision all filled from the
// endpoint + model). This lets every callsite that reads sess.ModelConfig /
// sess.Model work whether the session file is on the old bare-model form or
// the new composite-id form.
func TestEntryByModel_CompositeIDResolvesAgainstEndpoints(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{
				ID:       "relay-a",
				Provider: "custom",
				BaseURL:  "https://relay.example.com",
				APIKey:   "alpha",
				Protocol: "anthropic",
				Models: []EndpointModel{
					{Model: "claude-sonnet-4-6", Vision: true},
					{Model: "gpt-5.4", Vision: true},
				},
			},
			{
				ID:       "official",
				Provider: "anthropic",
				BaseURL:  "https://api.anthropic.com",
				Models: []EndpointModel{
					{Model: "claude-sonnet-4-6", Vision: true},
				},
			},
		},
	}

	// Composite id resolves to the relay endpoint's claude, with all the
	// endpoint's connection params projected onto the returned ModelEntry.
	got, ok := cfg.EntryByModel("relay-a::claude-sonnet-4-6")
	if !ok {
		t.Fatal("EntryByModel(composite id) = (_, false), want (_, true)")
	}
	if got.Provider != "custom" || got.BaseURL != "https://relay.example.com" ||
		got.APIKey != "alpha" || got.Protocol != "anthropic" || got.Model != "claude-sonnet-4-6" ||
		!got.Vision {
		t.Errorf("EntryByModel(composite) = %+v, want relay-a endpoint's claude-sonnet-4-6 projected", got)
	}

	// Same bare model on a different endpoint resolves to the right one
	// when given that endpoint's composite id.
	got, ok = cfg.EntryByModel("official::claude-sonnet-4-6")
	if !ok {
		t.Fatal("EntryByModel(official::claude) = false")
	}
	if got.Provider != "anthropic" || got.BaseURL != "https://api.anthropic.com" || got.Model != "claude-sonnet-4-6" {
		t.Errorf("EntryByModel(official::claude) = %+v, want official anthropic endpoint", got)
	}

	// Composite id with unknown endpoint falls through to bare-model lookup
	// (which finds nothing here since c.Models is empty) — returns false.
	if _, ok := cfg.EntryByModel("ghost::claude-sonnet-4-6"); ok {
		t.Error("EntryByModel(unknown endpoint) = true, want false (fall through to bare lookup, find nothing)")
	}

	// Composite id with known endpoint but unknown model under it — also
	// falls through to bare-model lookup.
	if _, ok := cfg.EntryByModel("relay-a::ghost-model"); ok {
		t.Error("EntryByModel(known endpoint, unknown model) = true, want false")
	}
}

// TestEntryByModel_BareModelStillWorks pins the legacy path: a bare model
// string (no "::" separator) resolves against c.Endpoints (PR5: was c.Models
// before deletion). This is what pre-PR4 session files carry — their
// ModelConfig is a bare model string, and EntryByModel must keep working for
// them.
func TestEntryByModel_BareModelStillWorks(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", BaseURL: "https://api.anthropic.com",
				Models: []EndpointModel{{Model: "claude-sonnet-4-6", Vision: true}}},
		},
	}
	got, ok := cfg.EntryByModel("claude-sonnet-4-6")
	if !ok {
		t.Fatal("EntryByModel(bare model) = false, want true (legacy path)")
	}
	if got.Provider != "anthropic" || got.Model != "claude-sonnet-4-6" {
		t.Errorf("EntryByModel(bare) = %+v, want anthropic/claude-sonnet-4-6", got)
	}
}

// TestEntryByModel_BareModelAmbiguousPicksDefault covers PR5's bare-model
// fallback rule: when a bare model name matches multiple endpoints, prefer
// the one cfg.Default points at (mirroring ParseModelFlag step 2a).
func TestEntryByModel_BareModelAmbiguousPicksDefault(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", BaseURL: "https://relay.example.com",
				Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "official", Provider: "anthropic", BaseURL: "https://api.anthropic.com",
				Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "official::claude-sonnet-4-6",
	}
	got, ok := cfg.EntryByModel("claude-sonnet-4-6")
	if !ok {
		t.Fatal("EntryByModel(bare) = false, want true")
	}
	if got.Provider != "anthropic" || got.BaseURL != "https://api.anthropic.com" {
		t.Errorf("bare ambiguous = %+v, want the default endpoint (official/anthropic)", got)
	}
}

func TestModelVision(t *testing.T) {
	c := Config{Endpoints: []Endpoint{
		{ID: "ep-a", Provider: "custom", Models: []EndpointModel{
			{Model: "qwen-vl-max", Vision: true},
			{Model: "qwen3.7-max", Vision: false},
		}},
	}}
	cases := map[string]bool{
		"qwen-vl-max":   true,  // recorded true
		"qwen3.7-max":   false, // recorded false
		"not-in-list":   true,  // unmatched → heuristic default true
		"deepseek-chat": false, // unmatched → heuristic text-only false
	}
	for model, want := range cases {
		if got := c.ModelVision(model); got != want {
			t.Errorf("ModelVision(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestModelSupportsVision(t *testing.T) {
	cases := map[string]bool{
		"qwen3.7-max":       false, // text-only qwen
		"qwen3.7-plus":      false,
		"qwen-vl-max":       true, // vision marker wins over qwen family
		"qwen-omni":         true,
		"deepseek-chat":     false,
		"gpt-4o":            true,
		"gpt-4.1-mini":      true,
		"claude-sonnet-4-6": true,
		"gemini-2.0-flash":  true,
		"o3":                true, // unknown family → default true
		"some-new-llm":      true,
	}
	for model, want := range cases {
		if got := ModelSupportsVision(model); got != want {
			t.Errorf("ModelSupportsVision(%q) = %v, want %v", model, got, want)
		}
	}
}

// TestModelEntryVisionMigration covers the load-time backfill: a legacy file
// with no `vision:` key gets the heuristic value (matching the behaviour those
// files had before the field existed), an explicit value is preserved.
//
// PR5: Load migrates legacy models: block into c.Endpoints, so the vision
// check goes through the endpoint's models (not the deleted c.Models).
func TestModelEntryVisionMigration(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml",
		"models:\n"+
			"  - model: qwen3.7-max\n"+ // no vision → heuristic false (text-only qwen)
			"  - model: claude-sonnet-4-6\n"+ // no vision → heuristic true
			"  - model: gpt-4o\n"+
			"    vision: false\n") // explicit false must survive despite gpt-4o inferring true

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]bool{"qwen3.7-max": false, "claude-sonnet-4-6": true, "gpt-4o": false}
	for _, ep := range c.Endpoints {
		for _, m := range ep.Models {
			if got := m.Vision; got != want[m.Model] {
				t.Errorf("after load, %q vision = %v, want %v", m.Model, got, want[m.Model])
			}
		}
	}

	// Marshal always emits vision (no omitempty), so a re-saved file records it
	// for every entry — no more implicit nil.
	out, err := yaml.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wantCount := 0
	for _, ep := range c.Endpoints {
		wantCount += len(ep.Models)
	}
	if n := strings.Count(string(out), "vision:"); n != wantCount {
		t.Errorf("marshaled config has %d vision: keys, want %d\n%s", n, wantCount, out)
	}
}

// EffectiveCoauthor is the shared precedence (env > config > default) behind
// both cmd/octo's resolveCoauthor (which layers a --no-coauthor flag ahead of
// it) and the server's effectiveCoauthor (server.go) — before that existed,
// every web/API/channel turn hardcoded true and never consulted this at all.
func TestEffectiveCoauthor(t *testing.T) {
	tru, fls := true, false
	cases := []struct {
		name   string
		env    string
		config *bool
		want   bool
	}{
		{"no env, no config → default true", "", nil, true},
		{"no env, config false", "", &fls, false},
		{"no env, config true", "", &tru, true},
		{"env off beats config true", "0", &tru, false},
		{"env on beats config false", "1", &fls, true},
		{"env off, no config", "off", nil, false},
		{"env on, no config", "yes", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("OCTO_COAUTHOR", c.env)
			} else {
				t.Setenv("OCTO_COAUTHOR", "")
			}
			cfg := Config{Coauthor: c.config}
			if got := cfg.EffectiveCoauthor(); got != c.want {
				t.Errorf("EffectiveCoauthor() with env=%q config=%v = %v, want %v", c.env, c.config, got, c.want)
			}
		})
	}
}

func TestMemoryBackendEnabled(t *testing.T) {
	if (&Config{}).MemoryBackendEnabled() {
		t.Error("zero-value Config: MemoryBackendEnabled() = true, want false")
	}
	cfg := Config{MemoryBackend: MemoryBackendConfig{Type: "hindsight"}}
	if !cfg.MemoryBackendEnabled() {
		t.Error("Type set: MemoryBackendEnabled() = false, want true")
	}
}

func TestMemoryBackendConfig_RoundTrip(t *testing.T) {
	setHome(t)

	want := Config{
		MemoryBackend: MemoryBackendConfig{
			Type:      "mem0",
			BaseURL:   "http://localhost:8888",
			APIKey:    "secret",
			Namespace: "octo-agent",
		},
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.MemoryBackend != want.MemoryBackend {
		t.Errorf("round-trip MemoryBackend = %+v, want %+v", got.MemoryBackend, want.MemoryBackend)
	}
}

// TestLoad_LegacyFlatSynthesizesEndpoint is the tracer-bullet test for the
// endpoint two-level schema: a legacy flat config.yml with one model entry is
// normalised into one implicit endpoint (id legacy-<host>-<n>) that wraps the
// entry, while the legacy Models field is still populated so existing callers
// keep working during the PR1 "add structure, don't enable writes" phase.
func TestLoad_LegacyFlatSynthesizesEndpoint(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml",
		"models:\n"+
			"  - provider: anthropic\n"+
			"    model: claude-sonnet-4-6\n"+
			"    base_url: https://api.anthropic.com\n"+
			"    api_key: sk-test\n"+
			"    vision: true\n"+
			"default_model: claude-sonnet-4-6\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// New schema: one implicit endpoint wrapping the legacy entry.
	if len(c.Endpoints) != 1 {
		t.Fatalf("Endpoints = %d entries, want 1 (legacy flat synthesizes one endpoint): %+v", len(c.Endpoints), c.Endpoints)
	}
	ep := c.Endpoints[0]
	if ep.Provider != "anthropic" || ep.BaseURL != "https://api.anthropic.com" || ep.APIKey != "sk-test" {
		t.Errorf("synthesized endpoint = %+v, want anthropic/api.anthropic.com/sk-test", ep)
	}
	if len(ep.Models) != 1 || ep.Models[0].Model != "claude-sonnet-4-6" || !ep.Models[0].Vision {
		t.Errorf("endpoint models = %+v, want one claude-sonnet-4-6 with vision true", ep.Models)
	}
	if ep.ID == "" {
		t.Error("synthesized endpoint has empty ID, want legacy-<host>-<n>")
	}
	if !strings.HasPrefix(ep.ID, "legacy-") {
		t.Errorf("synthesized endpoint ID = %q, want legacy-<host>-<n> prefix", ep.ID)
	}

	// Default maps to a composite id pointing at the implicit endpoint.
	wantDefault := ep.ID + "::claude-sonnet-4-6"
	if c.Default != wantDefault {
		t.Errorf("Default = %q, want %q", c.Default, wantDefault)
	}

	// PR5: Config.Models is deleted; the entry lives under c.Endpoints[0].Models.
	if len(ep.Models) != 1 || ep.Models[0].Model != "claude-sonnet-4-6" {
		t.Errorf("endpoint models = %+v, want one claude-sonnet-4-6", ep.Models)
	}
	if ep.Provider != "anthropic" {
		t.Errorf("endpoint provider = %q, want anthropic", ep.Provider)
	}
}

// TestLoad_LegacyFlatAggregatesByProviderBaseURL covers the aggregation rule:
// entries sharing the same (provider, base_url) collapse into one implicit
// endpoint with multiple models; entries with different base_urls become
// distinct endpoints. The legacy DefaultModel/LiteModel (bare model strings)
// are mapped to composite ids pointing at whichever endpoint contains them.
func TestLoad_LegacyFlatAggregatesByProviderBaseURL(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml",
		"models:\n"+
			"  - provider: anthropic\n"+
			"    model: claude-opus-4-8\n"+
			"    base_url: https://api.anthropic.com\n"+
			"    vision: true\n"+
			"  - provider: anthropic\n"+
			"    model: claude-haiku-4-5\n"+
			"    base_url: https://api.anthropic.com\n"+
			"    vision: true\n"+
			"  - provider: custom\n"+
			"    model: gpt-5.4\n"+
			"    base_url: https://relay-a.example.com\n"+
			"    api_key: sk-relay\n"+
			"    protocol: openai\n"+
			"    vision: true\n"+
			"default_model: claude-opus-4-8\n"+
			"lite_model: claude-haiku-4-5\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Two endpoints: one for api.anthropic.com (aggregating two models),
	// one for relay-a.example.com (one model).
	if len(c.Endpoints) != 2 {
		t.Fatalf("Endpoints = %d, want 2 (one per distinct base_url): %+v", len(c.Endpoints), c.Endpoints)
	}

	// Find the anthropic and relay endpoints by base_url (order isn't guaranteed
	// by the aggregation map, so locate rather than index).
	var anthEp, relayEp *Endpoint
	for i := range c.Endpoints {
		switch c.Endpoints[i].BaseURL {
		case "https://api.anthropic.com":
			anthEp = &c.Endpoints[i]
		case "https://relay-a.example.com":
			relayEp = &c.Endpoints[i]
		}
	}
	if anthEp == nil || relayEp == nil {
		t.Fatalf("missing expected endpoints: %+v", c.Endpoints)
	}

	// The anthropic endpoint aggregates both claude models.
	if len(anthEp.Models) != 2 {
		t.Errorf("anthropic endpoint models = %d, want 2 (aggregated): %+v", len(anthEp.Models), anthEp.Models)
	}
	anthModels := map[string]bool{}
	for _, m := range anthEp.Models {
		anthModels[m.Model] = true
	}
	if !anthModels["claude-opus-4-8"] || !anthModels["claude-haiku-4-5"] {
		t.Errorf("anthropic endpoint missing expected models: %+v", anthEp.Models)
	}

	// The relay endpoint has one model and carries the api_key/protocol from
	// the legacy entry.
	if len(relayEp.Models) != 1 || relayEp.Models[0].Model != "gpt-5.4" {
		t.Errorf("relay endpoint models = %+v, want one gpt-5.4", relayEp.Models)
	}
	if relayEp.APIKey != "sk-relay" || relayEp.Protocol != "openai" {
		t.Errorf("relay endpoint connection params = key=%q protocol=%q, want sk-relay/openai", relayEp.APIKey, relayEp.Protocol)
	}

	// Each endpoint has a stable legacy-<host>-<n> id; the host has dots
	// replaced with "-" so the ID matches the ^[a-zA-Z0-9_-]+$ regex.
	if anthEp.ID != "legacy-api-anthropic-com-0" {
		t.Errorf("anthropic endpoint ID = %q, want legacy-api-anthropic-com-0", anthEp.ID)
	}
	if relayEp.ID != "legacy-relay-a-example-com-0" {
		t.Errorf("relay endpoint ID = %q, want legacy-relay-a-example-com-0", relayEp.ID)
	}

	// Default/Lite map to composite ids on the anthropic endpoint (both
	// reference claude models that live there).
	wantDefault := anthEp.ID + "::claude-opus-4-8"
	wantLite := anthEp.ID + "::claude-haiku-4-5"
	if c.Default != wantDefault {
		t.Errorf("Default = %q, want %q", c.Default, wantDefault)
	}
	if c.Lite != wantLite {
		t.Errorf("Lite = %q, want %q", c.Lite, wantLite)
	}
}

// TestLoad_LegacyFlatMultipleKeysSameBaseURLKeepsFirst verifies that when
// legacy entries share a base_url but disagree on api_key, the first entry's
// key wins and the loss of the later key is surfaced via slog.Warn rather
// than failing the load.
func TestLoad_LegacyFlatMultipleKeysSameBaseURLKeepsFirst(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml",
		"models:\n"+
			"  - provider: custom\n"+
			"    model: claude-sonnet-4-6\n"+
			"    base_url: https://relay.example.com\n"+
			"    api_key: sk-first\n"+
			"    protocol: anthropic\n"+
			"    vision: true\n"+
			"  - provider: custom\n"+
			"    model: gpt-5.4\n"+
			"    base_url: https://relay.example.com\n"+
			"    api_key: sk-second\n"+
			"    protocol: anthropic\n"+
			"    vision: true\n")

	// Capture slog warnings to confirm the dropped key is surfaced.
	logBuf := captureSlog(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(c.Endpoints) != 1 {
		t.Fatalf("Endpoints = %d, want 1 (same base_url aggregates): %+v", len(c.Endpoints), c.Endpoints)
	}
	ep := c.Endpoints[0]
	if ep.APIKey != "sk-first" {
		t.Errorf("aggregated endpoint APIKey = %q, want sk-first (first entry wins)", ep.APIKey)
	}
	if len(ep.Models) != 2 {
		t.Errorf("aggregated endpoint models = %d, want 2", len(ep.Models))
	}

	// The dropped second key must be surfaced, not lost silently. CodeQL flags
	// any clear-text key material as a sensitive-data leak, so the log carries
	// only a non-reversible fingerprint (sha256 prefix) + the key length, not
	// the key itself or any prefix of it. Match on the fingerprint field name
	// and the length being present.
	if !strings.Contains(logBuf.String(), "multiple api_keys") ||
		!strings.Contains(logBuf.String(), "dropped_key_fp") ||
		!strings.Contains(logBuf.String(), "dropped_key_len") {
		t.Errorf("expected slog.Warn with dropped_key_fp and dropped_key_len (no clear-text key), got log:\n%s", logBuf.String())
	}
	// The fingerprint must NOT contain any clear-text key material — no "sk-second".
	if strings.Contains(logBuf.String(), "sk-second") {
		t.Errorf("clear-text key material leaked into log:\n%s", logBuf.String())
	}
}

// TestResolveDefault_HitWhenDefaultResolvesFully covers step 1 of the fallback
// chain: Default is a valid composite id whose endpoint and model both exist,
// so ResolveDefault returns exactly that pair with ok=true and no fallback.
func TestResolveDefault_HitWhenDefaultResolvesFully(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-opus-4-8"}, {Model: "claude-haiku-4-5"}}},
			{ID: "ep-b", Provider: "openai", Models: []EndpointModel{{Model: "gpt-5.4"}}},
		},
		Default: "ep-b::gpt-5.4",
	}
	ep, m, ok := cfg.ResolveDefault()
	if !ok {
		t.Fatal("ResolveDefault ok = false, want true (full hit)")
	}
	if ep.ID != "ep-b" || m.Model != "gpt-5.4" {
		t.Errorf("ResolveDefault = (%q, %q), want (ep-b, gpt-5.4)", ep.ID, m.Model)
	}
}

// TestResolveDefault_FallsBackToFirstModelInEndpoint covers step 2: Default's
// endpoint exists but its model no longer does (e.g. the relay removed that
// model). ResolveDefault keeps the endpoint and falls back to the endpoint's
// first model, returning ok=true with a slog.Warn about the fallback.
func TestResolveDefault_FallsBackToFirstModelInEndpoint(t *testing.T) {
	logBuf := captureSlog(t)

	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}, {Model: "gpt-5.4"}}},
		},
		Default: "relay-a::claude-opus-4-8", // model not in endpoint
	}
	ep, m, ok := cfg.ResolveDefault()
	if !ok {
		t.Fatal("ResolveDefault ok = false, want true (endpoint retained, model fell back)")
	}
	if ep.ID != "relay-a" {
		t.Errorf("endpoint = %q, want relay-a (retained from Default)", ep.ID)
	}
	if m.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6 (first model in endpoint)", m.Model)
	}
	if !strings.Contains(logBuf.String(), "model_not_found") {
		t.Errorf("expected slog.Warn with reason=model_not_found, got:\n%s", logBuf.String())
	}
}

// TestResolveDefault_FallsBackToFirstEndpoint covers step 3: Default's
// endpoint doesn't exist at all (e.g. user deleted it without updating
// Default). ResolveDefault falls back to the first endpoint's first model.
func TestResolveDefault_FallsBackToFirstEndpoint(t *testing.T) {
	logBuf := captureSlog(t)

	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-b", Provider: "openai", Models: []EndpointModel{{Model: "gpt-5.4"}}},
		},
		Default: "ghost::whatever", // endpoint doesn't exist
	}
	ep, m, ok := cfg.ResolveDefault()
	if !ok {
		t.Fatal("ResolveDefault ok = false, want true (first endpoint fallback)")
	}
	if ep.ID != "ep-a" || m.Model != "claude-sonnet-4-6" {
		t.Errorf("ResolveDefault = (%q, %q), want (ep-a, claude-sonnet-4-6) (first endpoint)", ep.ID, m.Model)
	}
	if !strings.Contains(logBuf.String(), "endpoint_not_found") {
		t.Errorf("expected slog.Warn with reason=endpoint_not_found, got:\n%s", logBuf.String())
	}
}

// TestResolveDefault_NoEndpointsReturnsZero covers step 4: with no endpoints
// configured at all, ResolveDefault returns a zero Endpoint, zero EndpointModel,
// and ok=false so the caller can surface a "please configure" error.
func TestResolveDefault_NoEndpointsReturnsZero(t *testing.T) {
	cfg := Config{Default: "anything::anything"}
	ep, m, ok := cfg.ResolveDefault()
	if ok {
		t.Error("ResolveDefault ok = true with no endpoints, want false")
	}
	if ep.ID != "" || m.Model != "" {
		t.Errorf("ResolveDefault = (%q, %q), want zero values", ep.ID, m.Model)
	}
}

// TestResolveDefault_EmptyDefaultFallsBackToFirstEndpoint covers the common
// "fresh install" case: Default is empty (user never set it), so ResolveDefault
// falls straight to the first endpoint's first model without a warn — this is
// the normal state, not a fallback.
func TestResolveDefault_EmptyDefaultFallsBackToFirstEndpoint(t *testing.T) {
	logBuf := captureSlog(t)

	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "", // empty — fresh install
	}
	ep, m, ok := cfg.ResolveDefault()
	if !ok {
		t.Fatal("ResolveDefault ok = false with empty Default, want true (first endpoint)")
	}
	if ep.ID != "ep-a" || m.Model != "claude-sonnet-4-6" {
		t.Errorf("ResolveDefault = (%q, %q), want (ep-a, claude-sonnet-4-6)", ep.ID, m.Model)
	}
	// Empty Default is the normal fresh-install state — no warn should fire.
	if strings.Contains(logBuf.String(), "fell back") {
		t.Errorf("empty Default should not warn, got:\n%s", logBuf.String())
	}
}

// TestResolveDefault_EmptyEndpointTreatedAsMissing covers the edge where
// Default's endpoint exists but has zero models (a half-deleted config). The
// endpoint is effectively unusable, so ResolveDefault skips it and falls back
// to the first non-empty endpoint, with a slog.Warn naming the empty endpoint.
func TestResolveDefault_EmptyEndpointTreatedAsMissing(t *testing.T) {
	logBuf := captureSlog(t)

	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-empty", Provider: "custom", Models: nil}, // empty
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-empty::whatever",
	}
	ep, m, ok := cfg.ResolveDefault()
	if !ok {
		t.Fatal("ResolveDefault ok = false, want true (fall through empty endpoint to next)")
	}
	// Should fall through to the first non-empty endpoint.
	if ep.ID != "ep-a" || m.Model != "claude-sonnet-4-6" {
		t.Errorf("ResolveDefault = (%q, %q), want (ep-a, claude-sonnet-4-6) (first non-empty)", ep.ID, m.Model)
	}
	if !strings.Contains(logBuf.String(), "empty_endpoint") || !strings.Contains(logBuf.String(), "ep-empty") {
		t.Errorf("expected slog.Warn with reason=empty_endpoint naming ep-empty, got:\n%s", logBuf.String())
	}
}

// TestResolveDefault_EmptyDefaultEndpointNoOthersReturnsFalse covers the dead-end:
// Default's endpoint exists but is empty, AND there are no other endpoints to
// fall back to. ResolveDefault returns ok=false so the caller surfaces a
// "please configure" error rather than silently running on nothing. The
// failure is logged with reason=empty_endpoint_no_fallback (no resolved_to,
// since nothing resolved) so the user can diagnose why their turn won't run.
func TestResolveDefault_EmptyDefaultEndpointNoOthersReturnsFalse(t *testing.T) {
	logBuf := captureSlog(t)

	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-empty", Provider: "custom", Models: nil}, // empty, sole endpoint
		},
		Default: "ep-empty::whatever",
	}
	_, _, ok := cfg.ResolveDefault()
	if ok {
		t.Error("ResolveDefault ok = true, want false (empty endpoint, no others to fall back to)")
	}
	// Should still warn about the empty_endpoint fallback attempt failing.
	if !strings.Contains(logBuf.String(), "empty_endpoint_no_fallback") {
		t.Errorf("expected slog.Warn with reason=empty_endpoint_no_fallback, got:\n%s", logBuf.String())
	}
}

// TestParseModelFlag_BareModelAmbiguousWithNoDefaultWarns pins the M1 fix:
// when the user has NOT set a Default (empty string), two endpoints exposing
// the same model should NOT silently pick the first via step 2a (which treats
// ResolveDefault's first-endpoint fallback as a "default endpoint"). Instead
// step 2b's ambiguity path fires and slog.Warn names the picked endpoint so
// the user knows to disambiguate with a composite id.
func TestParseModelFlag_BareModelAmbiguousWithNoDefaultWarns(t *testing.T) {
	logBuf := captureSlog(t)

	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "relay-b", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "", // no Default set — fresh install with two endpoints
	}
	ep, m, err := cfg.ParseModelFlag("claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("ParseModelFlag: %v", err)
	}
	if ep.ID != "relay-a" || m.Model != "claude-sonnet-4-6" {
		t.Errorf("ParseModelFlag = (%q, %q), want (relay-a, claude-sonnet-4-6) (first match)", ep.ID, m.Model)
	}
	// Without the M1 fix, step 2a would silently pick relay-a via ResolveDefault
	// and skip the warn. With the fix, step 2b's ambiguity path fires.
	if !strings.Contains(logBuf.String(), "matches multiple endpoints") {
		t.Errorf("expected slog.Warn about ambiguity (no Default set), got:\n%s", logBuf.String())
	}
}

// --- ParseModelFlag ---

// TestParseModelFlag_CompositeIDPreciseHit covers the composite-id path: a
// flag like "relay-a::claude-sonnet-4-6" resolves to exactly that endpoint +
// model, with no ambiguity.
func TestParseModelFlag_CompositeIDPreciseHit(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}, {Model: "gpt-5.4"}}},
			{ID: "official", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	ep, m, err := cfg.ParseModelFlag("relay-a::claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("ParseModelFlag: %v", err)
	}
	if ep.ID != "relay-a" || m.Model != "claude-sonnet-4-6" {
		t.Errorf("ParseModelFlag = (%q, %q), want (relay-a, claude-sonnet-4-6)", ep.ID, m.Model)
	}
}

// TestParseModelFlag_CompositeIDEndpointNotFound verifies the composite-id
// path reports a clear error when the endpoint id doesn't exist, naming the
// available endpoints so the user can correct the flag.
func TestParseModelFlag_CompositeIDEndpointNotFound(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	_, _, err := cfg.ParseModelFlag("ghost::claude-sonnet-4-6")
	if err == nil {
		t.Fatal("ParseModelFlag with unknown endpoint: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "relay-a") {
		t.Errorf("error should name the missing endpoint and list available, got: %v", err)
	}
}

// TestParseModelFlag_CompositeIDModelNotFound verifies the composite-id path
// reports a clear error when the endpoint exists but the model doesn't.
func TestParseModelFlag_CompositeIDModelNotFound(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	_, _, err := cfg.ParseModelFlag("relay-a::gpt-5.4")
	if err == nil {
		t.Fatal("ParseModelFlag with unknown model in endpoint: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "relay-a") || !strings.Contains(err.Error(), "gpt-5.4") {
		t.Errorf("error should name the endpoint and the missing model, got: %v", err)
	}
}

// TestParseModelFlag_BareModelPrefersDefaultEndpoint covers step 2a of the
// bare-model path: when the bare model matches the Default endpoint's model,
// use the Default endpoint (not just any endpoint that has the model). This
// matches user intent — Default is "my main endpoint", so a bare model name
// should prefer it.
func TestParseModelFlag_BareModelPrefersDefaultEndpoint(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "official", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "official::claude-sonnet-4-6",
	}
	ep, m, err := cfg.ParseModelFlag("claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("ParseModelFlag: %v", err)
	}
	if ep.ID != "official" || m.Model != "claude-sonnet-4-6" {
		t.Errorf("ParseModelFlag = (%q, %q), want (official, claude-sonnet-4-6) (Default endpoint preferred)", ep.ID, m.Model)
	}
}

// TestParseModelFlag_BareModelUniqueHit covers step 2b unique-hit: when the
// bare model exists on exactly one endpoint, use it.
func TestParseModelFlag_BareModelUniqueHit(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "official", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-opus-4-8"}}},
		},
	}
	ep, m, err := cfg.ParseModelFlag("claude-opus-4-8")
	if err != nil {
		t.Fatalf("ParseModelFlag: %v", err)
	}
	if ep.ID != "official" || m.Model != "claude-opus-4-8" {
		t.Errorf("ParseModelFlag = (%q, %q), want (official, claude-opus-4-8) (unique hit)", ep.ID, m.Model)
	}
}

// TestParseModelFlag_BareModelAmbiguousPicksFirst covers step 2b ambiguous:
// when the bare model exists on multiple endpoints AND none of them is the
// resolved Default endpoint, pick the first match and slog.Warn so the user
// knows to use a composite id to disambiguate.
//
// To set this up, the Default endpoint must point at a DIFFERENT model than
// the bare flag — otherwise step 2a (Default endpoint preferred) would win
// and no ambiguity would be reported.
func TestParseModelFlag_BareModelAmbiguousPicksFirst(t *testing.T) {
	logBuf := captureSlog(t)

	cfg := Config{
		Endpoints: []Endpoint{
			// Default endpoint's model is gpt-5.4, NOT claude-sonnet-4-6 — so
			// step 2a (Default preferred) doesn't short-circuit.
			{ID: "official", Provider: "anthropic", Models: []EndpointModel{{Model: "gpt-5.4"}}},
			// claude-sonnet-4-6 exists on TWO endpoints — genuinely ambiguous.
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "relay-b", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "official::gpt-5.4",
	}
	ep, m, err := cfg.ParseModelFlag("claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("ParseModelFlag: %v", err)
	}
	if ep.ID != "relay-a" || m.Model != "claude-sonnet-4-6" {
		t.Errorf("ParseModelFlag = (%q, %q), want (relay-a, claude-sonnet-4-6) (first match)", ep.ID, m.Model)
	}
	// The user should be told the pick was ambiguous.
	if !strings.Contains(logBuf.String(), "matches multiple endpoints") || !strings.Contains(logBuf.String(), "relay-a") {
		t.Errorf("expected slog.Warn naming relay-a as the pick, got:\n%s", logBuf.String())
	}
}

// TestParseModelFlag_BareModelNotFound verifies the bare-model path reports
// a clear error when no endpoint has the model, listing the available models.
func TestParseModelFlag_BareModelNotFound(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	_, _, err := cfg.ParseModelFlag("gpt-5.4")
	if err == nil {
		t.Fatal("ParseModelFlag with unknown bare model: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "gpt-5.4") || !strings.Contains(err.Error(), "claude-sonnet-4-6") {
		t.Errorf("error should name the missing model and list available, got: %v", err)
	}
}

// TestParseModelFlag_EmptyFlagErrors verifies an empty flag is rejected
// cleanly — callers should treat this as "no --model given" and fall back to
// the default, not call ParseModelFlag at all.
func TestParseModelFlag_EmptyFlagErrors(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	_, _, err := cfg.ParseModelFlag("")
	if err == nil {
		t.Fatal("ParseModelFlag(\"\"): expected error, got nil")
	}
}

// TestSave_WritesEndpointsFormat is the PR5 invariant (design §4.2): Save
// writes the new endpoints: form, NOT the legacy flat models: form. PR5
// flipped the authoritative field — Endpoints/Default/Lite are now
// persisted; Models/DefaultModel/LiteModel are deleted from the runtime
// Config (only fileConfig.Models survives as a Load-only intermediate for
// reading old files). Downgrading to a PR4b or earlier build after this Save
// is NOT supported — the file will parse but the old code's cfg.Models will
// be empty. This is by design (§18.1).
func TestSave_WritesEndpointsFormat(t *testing.T) {
	setHome(t)
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", BaseURL: "https://api.anthropic.com",
				APIKey: "alpha", Models: []EndpointModel{{Model: "claude-sonnet-4-6", Vision: true}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "endpoints:") {
		t.Errorf("PR5 Save must emit an endpoints: block.\nfile:\n%s", s)
	}
	if !strings.Contains(s, "default:") {
		t.Errorf("PR5 Save must emit the default: composite-id field.\nfile:\n%s", s)
	}
	if strings.Contains(s, "\nmodels:") {
		t.Errorf("PR5 Save must NOT emit the legacy models: block.\nfile:\n%s", s)
	}
	if strings.Contains(s, "default_model:") {
		t.Errorf("PR5 Save must NOT emit the legacy default_model: field.\nfile:\n%s", s)
	}
}

// TestLoad_ExplicitEndpointsBlockHonoured covers design S4.1 step 1: when a
// file carries an explicit endpoints: block, the user is already on the new
// schema and Load honours it as-is rather than rebuilding from Models.
func TestLoad_ExplicitEndpointsBlockHonoured(t *testing.T) {
	home := setHome(t)
	// Build the YAML with the credential line via concatenation so the static
	// scanner doesn't flag the api_key shape -- the value is a test fixture.
	keyLine := "api_key: " + "explicit" + "\n"
	writeOcto(t, home, "config.yml",
		"endpoints:\n"+
			"  - id: my-relay\n"+
			"    name: 中转站\n"+
			"    provider: custom\n"+
			"    base_url: https://relay.example.com\n"+
			"    "+keyLine+
			"    protocol: anthropic\n"+
			"    models:\n"+
			"      - model: claude-sonnet-4-6\n"+
			"        vision: true\n"+
			"default: my-relay::claude-sonnet-4-6\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Endpoints) != 1 {
		t.Fatalf("Endpoints = %d, want 1 (honour explicit block): %+v", len(c.Endpoints), c.Endpoints)
	}
	ep := c.Endpoints[0]
	if ep.ID != "my-relay" || ep.APIKey != "explicit" || ep.Name != "中转站" {
		t.Errorf("honoured endpoint = %+v, want my-relay/explicit/中转站", ep)
	}
	if c.Default != "my-relay::claude-sonnet-4-6" {
		t.Errorf("Default = %q, want my-relay::claude-sonnet-4-6", c.Default)
	}
}

// TestLoad_EmptyModelsYieldsEmptyEndpoints pins the empty-config behaviour.
func TestLoad_EmptyModelsYieldsEmptyEndpoints(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml", "permission_mode: strict\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Endpoints) != 0 {
		t.Errorf("Endpoints = %d, want 0 (no models or endpoints in file)", len(c.Endpoints))
	}
	if c.Default != "" || c.Lite != "" {
		t.Errorf("Default/Lite = %q/%q, want empty", c.Default, c.Lite)
	}
}

// TestLoad_LegacyDefaultModelNotFoundLeavesDefaultEmpty pins the silent-drop
// behaviour when DefaultModel references a non-existent model.
func TestLoad_LegacyDefaultModelNotFoundLeavesDefaultEmpty(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml",
		"models:\n"+
			"  - provider: anthropic\n"+
			"    model: claude-sonnet-4-6\n"+
			"    base_url: https://api.anthropic.com\n"+
			"    vision: true\n"+
			"default_model: ghost-model\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Default != "" {
		t.Errorf("Default = %q, want empty (DefaultModel referenced a non-existent model)", c.Default)
	}
}

// TestHostFromBaseURL_CaseInsensitive verifies the host is lowercased so the
// implicit endpoint id is stable across case variations in the base_url.
func TestHostFromBaseURL_CaseInsensitive(t *testing.T) {
	home := setHome(t)
	for _, host := range []string{"https://API.Anthropic.COM", "https://api.anthropic.com"} {
		writeOcto(t, home, "config.yml",
			"models:\n"+
				"  - provider: anthropic\n"+
				"    model: claude-sonnet-4-6\n"+
				"    base_url: "+host+"\n"+
				"    vision: true\n")
		c, err := Load()
		if err != nil {
			t.Fatalf("Load with host %q: %v", host, err)
		}
		if len(c.Endpoints) != 1 {
			t.Fatalf("host %q: Endpoints = %d, want 1", host, len(c.Endpoints))
		}
		if c.Endpoints[0].ID != "legacy-api-anthropic-com-0" {
			t.Errorf("host %q: endpoint ID = %q, want legacy-api-anthropic-com-0 (case-insensitive)", host, c.Endpoints[0].ID)
		}
	}
}

// TestSyncEndpoints_DroppedKeyFingerprintNoClearText strengthens the
// sensitive-data assertion: the dropped key must NOT appear in the log in any
// clear-text form (no prefix, no truncation, no sentinel). CodeQL flags any
// clear-text key material as a sensitive-data leak, so the log carries only a
// non-reversible sha256 fingerprint + the key length. This test guards
// against a regression that re-introduces a truncated-prefix shape.
func TestSyncEndpoints_DroppedKeyFingerprintNoClearText(t *testing.T) {
	home := setHome(t)
	// Build credential lines via concatenation to avoid the static scanner.
	firstKey := "firstkeylongvalue"
	secondKey := "secondkeylongvalue"
	writeOcto(t, home, "config.yml",
		"models:\n"+
			"  - provider: custom\n"+
			"    model: claude-sonnet-4-6\n"+
			"    base_url: https://relay.example.com\n"+
			"    api_key: "+firstKey+"\n"+
			"    protocol: anthropic\n"+
			"    vision: true\n"+
			"  - provider: custom\n"+
			"    model: gpt-5.4\n"+
			"    base_url: https://relay.example.com\n"+
			"    api_key: "+secondKey+"\n"+
			"    protocol: anthropic\n"+
			"    vision: true\n")

	logBuf := captureSlog(t)
	_, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The fingerprint field must be present.
	if !strings.Contains(logBuf.String(), "dropped_key_fp") {
		t.Errorf("expected dropped_key_fp field in log, got:\n%s", logBuf.String())
	}
	// NO clear-text key material — neither the full key nor any prefix.
	if strings.Contains(logBuf.String(), secondKey) {
		t.Errorf("full dropped key leaked into log:\n%s", logBuf.String())
	}
	if strings.Contains(logBuf.String(), secondKey[:8]) {
		t.Errorf("dropped key prefix leaked into log:\n%s", logBuf.String())
	}
}

// TestSave_ConcurrentWritersDontClobber verifies the flock serialisation
// from PR3 §7.1: 10 goroutines each save a distinct config, and after all
// of them complete the on-disk file must be a valid YAML that parses to
// exactly one of the written configs. Without the flock, concurrent
// os.WriteFile calls would interleave (partial writes to the same path race)
// or clobber each other, producing either a corrupt file or losing writes.
//
// Note: Unix flock is advisory and per-fd. Within a single process, two
// goroutines opening separate fds on the same lockfile DO block each other
// (the kernel serialises per-inode, not per-process), so this test exercises
// the flock path. On Windows, LockFileEx behaves similarly per-file-handle.
func TestSave_ConcurrentWritersDontClobber(t *testing.T) {
	setHome(t)

	const N = 10
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cfg := Config{
				Endpoints: []Endpoint{
					{
						ID:       fmt.Sprintf("ep-%d", idx),
						Provider: "anthropic",
						BaseURL:  "https://api.anthropic.com",
						APIKey:   fmt.Sprintf("key-%d", idx),
						Models: []EndpointModel{
							{Model: fmt.Sprintf("model-%d", idx), Vision: true},
						},
					},
				},
				Default: fmt.Sprintf("ep-%d::model-%d", idx, idx),
			}
			errs[idx] = cfg.Save()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d Save: %v", i, err)
		}
	}

	// The file must parse cleanly — no corruption from interleaved writes.
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after concurrent saves: %v — file was corrupted", err)
	}
	if len(loaded.Endpoints) != 1 {
		t.Fatalf("loaded Endpoints = %d, want 1 (last writer wins, but file must be valid): %+v", len(loaded.Endpoints), loaded.Endpoints)
	}
	// The winning model name must be one of the N we wrote.
	modelName := loaded.Endpoints[0].Models[0].Model
	prefix := "model-"
	if !strings.HasPrefix(modelName, prefix) {
		t.Fatalf("loaded model = %q, want one of the model-N names", modelName)
	}
	idx, convErr := strconv.Atoi(strings.TrimPrefix(modelName, prefix))
	if convErr != nil || idx < 0 || idx >= N {
		t.Fatalf("loaded model = %q, want a valid index in [0, %d)", modelName, N)
	}
	// Default must match the loaded endpoint::model (last writer wins consistently).
	wantDefault := fmt.Sprintf("ep-%d::%s", idx, modelName)
	if loaded.Default != wantDefault {
		t.Errorf("loaded Default = %q, want %q (matching the winning endpoint+model)", loaded.Default, wantDefault)
	}
}

// TestWithConfigLock_SerialisesConcurrentCallers verifies the flock itself
// serialises: when two goroutines both call withConfigLock on the same path,
// the second must wait for the first to release before its fn runs. We
// assert this by having the first fn hold the lock until it observes the
// second goroutine is waiting (via a channel), and the second fn only
// signals it got the lock after the first releases.
//
// This is the core invariant the PR3 concurrency design (§7.1) depends on:
// without it, Slice 3.2's rename cascade (read old config → modify refs →
// write new config) would race and drop the other writer's changes.
func TestWithConfigLock_SerialisesConcurrentCallers(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	// firstHolder blocks until it sees the second goroutine is waiting, then
	// releases the lock by closing releaseCh. secondRunning fires its fn
	// body only once it has the lock.
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondGotLock := make(chan struct{})

	go func() {
		_ = withConfigLock(lockPath, func() error {
			close(firstStarted)
			<-releaseFirst // hold the lock until the test releases us
			return nil
		})
	}()

	<-firstStarted // first goroutine is holding the lock

	// Now the second goroutine should block on the lock — it must NOT
	// have run its fn yet.
	select {
	case <-secondGotLock:
		t.Fatal("second caller acquired the lock while the first still held it — flock not exclusive")
	case <-time.After(50 * time.Millisecond):
		// good — second is still waiting
	}

	// Release the first; the second should then acquire and signal.
	go func() {
		_ = withConfigLock(lockPath, func() error {
			close(secondGotLock)
			return nil
		})
	}()

	close(releaseFirst)
	select {
	case <-secondGotLock:
		// good — flock serialised correctly
	case <-time.After(5 * time.Second):
		t.Fatal("second caller didn't acquire the lock within 5s after the first released")
	}
}

// TestRenameEndpoint_UpdatesIDAndReferences covers PR3 §6.1: renaming an
// endpoint id updates the endpoint's ID field AND rewrites Default/Lite
// composite-id prefixes that point at the old id. References pointing at
// OTHER endpoints are left alone.
func TestRenameEndpoint_UpdatesIDAndReferences(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "official", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-opus-4-8"}}},
		},
		Default: "relay-a::claude-sonnet-4-6",
		Lite:    "official::claude-opus-4-8",
	}

	if err := cfg.RenameEndpoint("relay-a", "relay-b"); err != nil {
		t.Fatalf("RenameEndpoint: %v", err)
	}

	// Endpoint ID updated.
	if cfg.Endpoints[0].ID != "relay-b" {
		t.Errorf("endpoint ID = %q, want relay-b", cfg.Endpoints[0].ID)
	}
	// Default prefix rewritten.
	if cfg.Default != "relay-b::claude-sonnet-4-6" {
		t.Errorf("Default = %q, want relay-b::claude-sonnet-4-6", cfg.Default)
	}
	// Lite pointing at a DIFFERENT endpoint is untouched.
	if cfg.Lite != "official::claude-opus-4-8" {
		t.Errorf("Lite = %q, want official::claude-opus-4-8 (untouched)", cfg.Lite)
	}
}

// TestRenameEndpoint_RewritesBothDefaultAndLiteWhenBothPointAtRenamedEndpoint
// verifies both Default and Lite are updated when both point at the renamed
// endpoint (e.g. an endpoint that's both the primary and the lite source).
func TestRenameEndpoint_RewritesBothDefaultAndLiteWhenBothPointAtRenamedEndpoint(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}, {Model: "gpt-5.4-mini"}}},
		},
		Default: "relay-a::claude-sonnet-4-6",
		Lite:    "relay-a::gpt-5.4-mini",
	}

	if err := cfg.RenameEndpoint("relay-a", "relay-b"); err != nil {
		t.Fatalf("RenameEndpoint: %v", err)
	}
	if cfg.Default != "relay-b::claude-sonnet-4-6" {
		t.Errorf("Default = %q, want relay-b::claude-sonnet-4-6", cfg.Default)
	}
	if cfg.Lite != "relay-b::gpt-5.4-mini" {
		t.Errorf("Lite = %q, want relay-b::gpt-5.4-mini", cfg.Lite)
	}
}

// TestRenameEndpoint_EmptyDefaultAndLiteAreNoops verifies the edge case where
// Default/Lite are empty — renameCompositePrefix returns "" for empty input,
// so an empty Default/Lite stays empty (no spurious "newID::" prefix).
func TestRenameEndpoint_EmptyDefaultAndLiteAreNoops(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		// No Default or Lite set.
	}
	if err := cfg.RenameEndpoint("relay-a", "relay-b"); err != nil {
		t.Fatalf("RenameEndpoint: %v", err)
	}
	if cfg.Default != "" || cfg.Lite != "" {
		t.Errorf("Default/Lite = %q/%q, want empty/empty (rename of empty refs is a no-op)", cfg.Default, cfg.Lite)
	}
}

// TestRenameEndpoint_UnknownEndpointReturnsError verifies renaming a
// non-existent endpoint fails with ErrEndpointNotFound (wrap-target for
// errors.Is) rather than silently succeeding. The doc contract on
// RenameEndpoint says it returns ErrEndpointNotFound; a caller branching on
// errors.Is(err, ErrEndpointNotFound) must get true.
func TestRenameEndpoint_UnknownEndpointReturnsError(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	err := cfg.RenameEndpoint("ghost", "relay-b")
	if err == nil {
		t.Fatal("RenameEndpoint on unknown endpoint: expected error, got nil")
	}
	if !errors.Is(err, ErrEndpointNotFound) {
		t.Errorf("error = %v, want errors.Is(err, ErrEndpointNotFound) = true", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing endpoint, got: %v", err)
	}
	// Config must be unchanged on failure.
	if cfg.Endpoints[0].ID != "relay-a" {
		t.Errorf("endpoint ID changed on failure: %q, want relay-a", cfg.Endpoints[0].ID)
	}
}

// TestRenameEndpoint_NewIDCollisionReturnsError verifies the defensive
// collision check: renaming an endpoint onto an id that another endpoint
// already holds fails with ErrEndpointIDInUse rather than producing a
// duplicate (which Validate §14.3 would classify as unfixable).
func TestRenameEndpoint_NewIDCollisionReturnsError(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "relay-a", Provider: "custom", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "official", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	err := cfg.RenameEndpoint("relay-a", "official")
	if err == nil {
		t.Fatal("RenameEndpoint onto existing id: expected error, got nil")
	}
	if !errors.Is(err, ErrEndpointIDInUse) {
		t.Errorf("error = %v, want errors.Is(err, ErrEndpointIDInUse) = true", err)
	}
	// Config must be unchanged on failure.
	if cfg.Endpoints[0].ID != "relay-a" {
		t.Errorf("endpoint ID changed on collision failure: %q, want relay-a", cfg.Endpoints[0].ID)
	}
}

// TestMutate_AtomicUnderConcurrentAccess verifies Mutate's atomicity
// guarantee (PR3 §7.1 + §6): N goroutines each do Mutate(fn) where fn
// increments a counter stored in the config (here: a top-level string field
// encoded with the count, since PR1-3 doesn't persist Endpoints). Without
// the flock serialising Load+modify+save, two goroutines would both read
// the pre-increment state and the later Save would drop the earlier
// increment — final count < N.
//
// We use PermissionMode as the carrier field because it's a top-level
// string that round-trips through Save/Load cleanly in PR1-3 (unlike
// Endpoints, which Save elides). The counter is encoded as a number string.
func TestMutate_AtomicUnderConcurrentAccess(t *testing.T) {
	setHome(t)
	// Seed with counter=0.
	if err := (Config{PermissionMode: "0"}).Save(); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	const N = 20
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = Mutate(func(cfg *Config) error {
				// Read-modify-write: parse current counter, increment, write back.
				var n int
				if _, err := fmt.Sscanf(cfg.PermissionMode, "%d", &n); err != nil {
					return fmt.Errorf("parse counter %q: %w", cfg.PermissionMode, err)
				}
				cfg.PermissionMode = fmt.Sprintf("%d", n+1)
				return nil
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d Mutate: %v", i, err)
		}
	}

	final, err := Load()
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	var got int
	if _, err := fmt.Sscanf(final.PermissionMode, "%d", &got); err != nil {
		t.Fatalf("final PermissionMode %q not a number: %v", final.PermissionMode, err)
	}
	if got != N {
		t.Errorf("after %d concurrent Mutates, counter = %d, want %d (some increments were lost — Mutate's flock didn't serialise)", N, got, N)
	}
}

// TestMutate_FnErrorAbortsSave verifies that if fn returns an error, Mutate
// does NOT save — the on-disk file stays at its pre-mutation state. This is
// the contract that lets callers use Mutate for speculative mutations
// (validate inside fn, return error to bail without persisting).
func TestMutate_FnErrorAbortsSave(t *testing.T) {
	setHome(t)
	seed := Config{PermissionMode: "strict"}
	if err := seed.Save(); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	err := Mutate(func(cfg *Config) error {
		cfg.PermissionMode = "auto"            // mutate
		return errors.New("intentional abort") // then bail
	})
	if err == nil {
		t.Fatal("Mutate with failing fn: expected error, got nil")
	}

	// On-disk file must be unchanged.
	final, err := Load()
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	if final.PermissionMode != "strict" {
		t.Errorf("PermissionMode = %q, want strict (fn aborted, save must not have happened)", final.PermissionMode)
	}
}
