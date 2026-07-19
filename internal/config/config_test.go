package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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

func TestLoad_MissingFileIsZeroNotError(t *testing.T) {
	setHome(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() on missing file = %v, want nil", err)
	}
	if len(c.Models) != 0 || c.DefaultModel != "" {
		t.Errorf("Load() on missing file = %+v, want zero Config", c)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	setHome(t)

	want := Config{
		Models: []ModelEntry{
			{Provider: "anthropic", Model: "claude-fable-5"},
			{Provider: "kimi", Model: "kimi-k2.6", BaseURL: "https://x.example"},
		},
		DefaultModel: "kimi-k2.6",
		LiteModel:    "claude-fable-5",
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Models) != 2 || got.Models[1] != want.Models[1] {
		t.Errorf("round-trip models = %+v, want %+v", got.Models, want.Models)
	}
	if got.DefaultModel != "kimi-k2.6" || got.LiteModel != "claude-fable-5" {
		t.Errorf("round-trip refs = default %q lite %q", got.DefaultModel, got.LiteModel)
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
	if len(c.Models) != 1 {
		t.Fatalf("Models = %+v, want one synthesized entry", c.Models)
	}
	e := c.Models[0]
	if e.Provider != "openai" || e.Model != "gpt-4o-mini" ||
		e.BaseURL != "https://x.example" || e.APIKey != "sk-old" || e.ReasoningEffort != "high" {
		t.Errorf("synthesized entry = %+v", e)
	}
	if c.DefaultModel != "gpt-4o-mini" {
		t.Errorf("DefaultModel = %q, want gpt-4o-mini", c.DefaultModel)
	}
	if c.PermissionMode != "strict" {
		t.Errorf("global PermissionMode lost: %q", c.PermissionMode)
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
	if e := c.Models[0]; e.Provider != "custom" || e.Protocol != "openai" {
		t.Errorf("openai_compatible entry = %+v, want custom/openai", e)
	}
	if e := c.Models[1]; e.Provider != "custom" || e.Protocol != "anthropic" {
		t.Errorf("anthropic_compatible entry = %+v, want custom/anthropic", e)
	}
	// A named vendor is left untouched.
	if e := c.Models[2]; e.Provider != "anthropic" || e.Protocol != "" {
		t.Errorf("anthropic entry = %+v, want anthropic/(no protocol)", e)
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

	cfg := Config{Models: []ModelEntry{{Model: "main", APIKey: "sk-secret"}}}
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
	writeOcto(t, home, "config.yml", "default_model: good-model\n")

	cfg, err := LoadCached()
	if err != nil {
		t.Fatalf("LoadCached() first load = %v, want nil", err)
	}
	if cfg.DefaultModel != "good-model" {
		t.Fatalf("LoadCached() first load DefaultModel = %q, want %q", cfg.DefaultModel, "good-model")
	}

	writeOcto(t, home, "config.yml", "not: valid: yaml: [")

	cfg, err = LoadCached()
	if err != nil {
		t.Fatalf("LoadCached() after malformed edit = %v, want nil (fall back to last good)", err)
	}
	if cfg.DefaultModel != "good-model" {
		t.Errorf("LoadCached() after malformed edit DefaultModel = %q, want cached %q", cfg.DefaultModel, "good-model")
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

func TestSetDefaultEntry(t *testing.T) {
	var c Config

	// Appends when empty.
	c.SetDefaultEntry(ModelEntry{Model: "m1"})
	if len(c.Models) != 1 || c.DefaultModel != "m1" {
		t.Fatalf("after first set: %+v", c)
	}

	// Replaces the default in place when its model changes, carrying the lite
	// reference over to the new model.
	c.LiteModel = "m1"
	c.SetDefaultEntry(ModelEntry{Model: "m2"})
	if len(c.Models) != 1 || c.Models[0].Model != "m2" {
		t.Fatalf("after model change: %+v", c.Models)
	}
	if c.DefaultModel != "m2" || c.LiteModel != "m2" {
		t.Errorf("references not updated: default %q lite %q", c.DefaultModel, c.LiteModel)
	}
}

func TestEntryByModel_EmptyNeverMatches(t *testing.T) {
	c := Config{Models: []ModelEntry{{Model: "m"}}}
	if _, ok := c.EntryByModel(""); ok {
		t.Error("EntryByModel(\"\") matched, want no match")
	}
}

func TestModelVision(t *testing.T) {
	c := Config{Models: []ModelEntry{
		{Model: "qwen-vl-max", Vision: true},
		{Model: "qwen3.7-max", Vision: false},
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
// files had before the field existed), an explicit value is preserved, and Save
// always records the field.
func TestModelEntryVisionMigration(t *testing.T) {
	in := []byte("models:\n" +
		"  - model: qwen3.7-max\n" + // no vision → heuristic false (text-only qwen)
		"  - model: claude-sonnet-4-6\n" + // no vision → heuristic true
		"  - model: gpt-4o\n" +
		"    vision: false\n") // explicit false must survive despite gpt-4o inferring true

	var c Config
	if err := yaml.Unmarshal(in, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]bool{"qwen3.7-max": false, "claude-sonnet-4-6": true, "gpt-4o": false}
	for _, e := range c.Models {
		if got := e.Vision; got != want[e.Model] {
			t.Errorf("after load, %q vision = %v, want %v", e.Model, got, want[e.Model])
		}
	}

	// Marshal always emits vision (no omitempty), so a re-saved file records it
	// for every entry — no more implicit nil.
	out, err := yaml.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if n := strings.Count(string(out), "vision:"); n != len(c.Models) {
		t.Errorf("marshaled config has %d vision: keys, want %d\n%s", n, len(c.Models), out)
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

	// Legacy Models field still populated — existing callers keep working.
	if len(c.Models) != 1 {
		t.Fatalf("legacy Models = %d entries, want 1 (must stay populated for existing callers): %+v", len(c.Models), c.Models)
	}
	if c.Models[0].Model != "claude-sonnet-4-6" || c.Models[0].Provider != "anthropic" {
		t.Errorf("legacy Models[0] = %+v, want claude-sonnet-4-6/anthropic", c.Models[0])
	}
	if c.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("legacy DefaultModel = %q, want claude-sonnet-4-6", c.DefaultModel)
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
	var logBuf strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

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

	// The dropped second key must be surfaced, not lost silently. The key is
	// truncated in the log to avoid writing the full secret to disk, so match
	// the truncated prefix.
	if !strings.Contains(logBuf.String(), "multiple api_keys") || !strings.Contains(logBuf.String(), "sk-secon") {
		t.Errorf("expected slog.Warn about dropped sk-second key (truncated to sk-secon…), got log:\n%s", logBuf.String())
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
	var logBuf strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

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
	var logBuf strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

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
	var logBuf strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

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
// to the first non-empty endpoint.
func TestResolveDefault_EmptyEndpointTreatedAsMissing(t *testing.T) {
	var logBuf strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

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
}
