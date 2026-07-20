package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

// oneEntryConfig builds a Config whose default endpoint has the given entry
// projected onto it — the multi-model equivalent of the old top-level
// provider/model literals. PR5: Config.Models is deleted, so the entry is
// wrapped in a single Endpoint with id "ep-a" and Default points at it.
func oneEntryConfig(e config.ModelEntry) config.Config {
	return config.Config{
		Endpoints: []config.Endpoint{{
			ID:       "ep-a",
			Provider: e.Provider,
			BaseURL:  e.BaseURL,
			APIKey:   e.APIKey,
			Protocol: e.Protocol,
			Models:   []config.EndpointModel{{Model: e.Model, Vision: e.Vision}},
		}},
		Default: "ep-a::" + e.Model,
	}
}

func TestResolveBaseURL_Precedence(t *testing.T) {
	// Isolate from host env vars so the test is deterministic.
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")

	// env wins over config.
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.deepseek.com/anthropic")
	entry := config.ModelEntry{Provider: providerAnthropic, BaseURL: "https://cfg.example"}
	if got := resolveBaseURL(providerAnthropic, entry); got != "https://api.deepseek.com/anthropic" {
		t.Errorf("env should win, got %q", got)
	}

	// No env → config entry (same provider only).
	t.Setenv("ANTHROPIC_BASE_URL", "")
	if got := resolveBaseURL(providerAnthropic, entry); got != "https://cfg.example" {
		t.Errorf("config base URL = %q, want https://cfg.example", got)
	}
	// Entry base URL must not leak onto a different provider.
	if got := resolveBaseURL(providerOpenAI, entry); got != "" {
		t.Errorf("openai must not inherit anthropic's config base URL, got %q", got)
	}

	// No override anywhere → effectiveEndpoint shows the marked default.
	empty := config.ModelEntry{}
	if got := resolveBaseURL(providerAnthropic, empty); got != "" {
		t.Errorf("no override should be empty, got %q", got)
	}
	if got := effectiveEndpoint(providerAnthropic, empty); !strings.Contains(got, "api.anthropic.com") || !strings.Contains(got, "default") {
		t.Errorf("effectiveEndpoint default = %q, want the marked anthropic default", got)
	}
	// Override flows through to effectiveEndpoint verbatim.
	if got := effectiveEndpoint(providerAnthropic, entry); got != "https://cfg.example" {
		t.Errorf("effectiveEndpoint override = %q, want the config URL unmarked", got)
	}
}

func TestResolveProviderModel_ModelEnv(t *testing.T) {
	// env beats config + default, but the --model flag still beats env.
	t.Setenv("ANTHROPIC_MODEL", "claude-from-env")
	t.Setenv("OPENAI_MODEL", "")
	cfg := oneEntryConfig(config.ModelEntry{Provider: providerAnthropic, Model: "cfg-model"})

	if _, m, _, _ := resolveProviderModel("", "", cfg); m != "claude-from-env" {
		t.Errorf("env should beat config, got %q", m)
	}
	if _, m, _, _ := resolveProviderModel("", "flag-model", cfg); m != "flag-model" {
		t.Errorf("--model flag should beat env, got %q", m)
	}
	// The model env is per-provider: ANTHROPIC_MODEL must not leak onto openai.
	if _, m, _, _ := resolveProviderModel(providerOpenAI, "", config.Config{}); m != "gpt-5.4" {
		t.Errorf("ANTHROPIC_MODEL must not affect openai, got %q (want default)", m)
	}
	// OPENAI_MODEL drives the openai default slot.
	t.Setenv("OPENAI_MODEL", "deepseek-chat")
	if _, m, _, _ := resolveProviderModel(providerOpenAI, "", config.Config{}); m != "deepseek-chat" {
		t.Errorf("OPENAI_MODEL = %q, want deepseek-chat", m)
	}
}

func TestResolveProviderModel_Precedence(t *testing.T) {
	// Isolate from any model env vars in the host so the flag/config/default
	// cases below are deterministic.
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OCTO_PROVIDER", "")
	cfg := oneEntryConfig(config.ModelEntry{Provider: "openai", Model: "cfg-model"})
	tests := []struct {
		name              string
		flagProv, flagMdl string
		cfg               config.Config
		wantProv, wantMdl string
		wantOK            bool
	}{
		{"flag beats config", "anthropic", "flag-model", cfg, "anthropic", "flag-model", true},
		{"config beats default", "", "", cfg, "openai", "cfg-model", true},
		{"flag provider, default model", "openai", "", config.Config{}, "openai", "gpt-5.4", true},
		{"empty everything → anthropic default", "", "", config.Config{}, "anthropic", "claude-sonnet-4-6", true},
		{"config provider, builtin model", "", "", oneEntryConfig(config.ModelEntry{Provider: "openai"}), "openai", "gpt-5.4", true},
		{"flag provider overrides config provider — no model contamination", "anthropic", "", cfg, "anthropic", "claude-sonnet-4-6", true},
		{"unknown provider, no model → not ok", "bogus", "", config.Config{}, "bogus", "", false},
		{"unknown provider WITH model → ok (buildProvider rejects later)", "bogus", "m", config.Config{}, "bogus", "m", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotProv, gotMdl, _, ok := resolveProviderModel(tc.flagProv, tc.flagMdl, tc.cfg)
			if gotProv != tc.wantProv || gotMdl != tc.wantMdl || ok != tc.wantOK {
				t.Errorf("resolveProviderModel(%q,%q,%+v) = (%q,%q,%v), want (%q,%q,%v)",
					tc.flagProv, tc.flagMdl, tc.cfg, gotProv, gotMdl, ok, tc.wantProv, tc.wantMdl, tc.wantOK)
			}
		})
	}
}

func TestResolveProviderModel_EntryNameSelectsWholeEntry(t *testing.T) {
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OCTO_PROVIDER", "")
	cfg := config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-anthropic", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-kimi", Provider: "kimi", BaseURL: "https://kimi.example", APIKey: "sk-kimi", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default: "ep-anthropic::claude-sonnet-4-6",
	}

	prov, model, entry, ok := resolveProviderModel("", "kimi-k2.6", cfg)
	if !ok || prov != "kimi" || model != "kimi-k2.6" {
		t.Fatalf("named entry: got (%q,%q,%v), want (kimi, kimi-k2.6, true)", prov, model, ok)
	}
	if entry.BaseURL != "https://kimi.example" || entry.APIKey != "sk-kimi" {
		t.Errorf("entry should carry base URL and key: %+v", entry)
	}
	// The entry wins over an explicit --provider — the combination is
	// meaningless and the entry is what the user named.
	if p, _, _, _ := resolveProviderModel("anthropic", "kimi-k2.6", cfg); p != "kimi" {
		t.Errorf("entry name should override --provider, got %q", p)
	}
	// A non-matching --model value stays a raw model string on the default entry.
	if p, m, _, _ := resolveProviderModel("", "some-model", cfg); p != "anthropic" || m != "some-model" {
		t.Errorf("raw model string: got (%q,%q)", p, m)
	}
}

func TestResolveProviderModel_ProviderEnv(t *testing.T) {
	// OCTO_PROVIDER beats config and default, but --provider flag still beats env.
	t.Setenv("OCTO_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "")
	cfg := oneEntryConfig(config.ModelEntry{Provider: providerAnthropic, Model: "cfg-model"})

	if p, _, _, _ := resolveProviderModel("", "", cfg); p != "openai" {
		t.Errorf("OCTO_PROVIDER should beat config, got %q", p)
	}
	if p, _, _, _ := resolveProviderModel("anthropic", "", cfg); p != "anthropic" {
		t.Errorf("--provider flag should beat OCTO_PROVIDER, got %q", p)
	}
	// When OCTO_PROVIDER selects a provider, its model env var is read.
	t.Setenv("OPENAI_MODEL", "deepseek-chat")
	if _, m, _, _ := resolveProviderModel("", "", config.Config{}); m != "deepseek-chat" {
		t.Errorf("OCTO_PROVIDER=openai + OPENAI_MODEL should resolve model, got %q", m)
	}
}

func TestRunConfig_Path(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var stdout, stderr bytes.Buffer
	if code := runConfig([]string{"path"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config.yml") {
		t.Errorf("path output should mention config.yml; got %q", stdout.String())
	}
}

func TestRunConfig_Wizard_WritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTHROPIC_API_KEY", "set-so-wizard-skips-key-prompt")

	// Answers: provider=openai, model=(default). openai is pinned to its
	// default endpoint, so the wizard no longer asks for a base URL.
	in := strings.NewReader("openai\n\n")
	var stdout, stderr bytes.Buffer
	if code := runConfig(nil, in, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load after wizard: %v", err)
	}
	entry := got.DefaultEntry()
	if entry.Provider != "openai" {
		t.Errorf("provider = %q, want openai", entry.Provider)
	}
	// Default model answer writes the concrete model name so the saved entry is
	// self-describing and validates cleanly.
	if entry.Model != "gpt-5.4" {
		t.Errorf("model = %q, want gpt-5.4 (the current openai default)", entry.Model)
	}
	if entry.APIKey != "" {
		t.Errorf("APIKey should not be stored when env var is set; got %q", entry.APIKey)
	}
	if strings.Contains(stdout.String(), "base URL") || strings.Contains(stdout.String(), "Endpoint") {
		t.Errorf("pinned vendor must not be asked about its endpoint; got:\n%s", stdout.String())
	}
}

func TestRunConfig_Wizard_PreservesOtherEntriesAndGlobals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTHROPIC_API_KEY", "set-so-wizard-skips-key-prompt")

	seed := config.Config{
		Endpoints: []config.Endpoint{
			{ID: "ep-anthropic", Provider: "anthropic", Models: []config.EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-kimi", Provider: "kimi", Models: []config.EndpointModel{{Model: "kimi-k2.6"}}},
		},
		Default:        "ep-anthropic::claude-sonnet-4-6",
		PermissionMode: "strict",
	}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	// Answers: provider=openai, model=(default), set-as-default=y. No
	// endpoint question for a pinned vendor.
	in := strings.NewReader("openai\n\ny\n")
	var stdout, stderr bytes.Buffer
	if code := runConfig(nil, in, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load after wizard: %v", err)
	}
	// PR5: the wizard adds a new openai endpoint; the existing kimi endpoint
	// must survive. The exact endpoint count is 3 (anthropic + kimi + new openai)
	// unless the wizard merged onto an existing endpoint id — which it won't
	// here because the new endpoint has a different base_url host.
	if len(got.Endpoints) < 2 {
		t.Fatalf("wizard must not drop other endpoints: %+v", got.Endpoints)
	}
	if _, ok := got.EntryByModel("kimi-k2.6"); !ok {
		t.Error("kimi entry lost")
	}
	if got.PermissionMode != "strict" {
		t.Errorf("permission_mode lost: %q", got.PermissionMode)
	}
	// The new openai endpoint must exist (the wizard added it).
	var hasOpenai bool
	for _, ep := range got.Endpoints {
		if ep.Provider == "openai" {
			hasOpenai = true
			break
		}
	}
	if !hasOpenai {
		t.Errorf("wizard did not add an openai endpoint: %+v", got.Endpoints)
	}
}

func TestRunConfig_Show_ReportsSourcesNotKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTHROPIC_API_KEY", "secret-value-should-not-print")

	if err := oneEntryConfig(config.ModelEntry{Provider: "anthropic", Model: "m1"}).Save(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runConfig([]string{"show"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "secret-value-should-not-print") {
		t.Error("show must never print the API key value")
	}
	// PR5 (design §7.2): show reports the endpoint list + references +
	// reasoning. Key-source reporting is doctor's job, not show's.
	if !strings.Contains(out, "anthropic") {
		t.Errorf("show should report the provider; got:\n%s", out)
	}
	if !strings.Contains(out, "endpoints:") {
		t.Errorf("show should list endpoints; got:\n%s", out)
	}
}

func TestRunConfig_Wizard_SwitchesProviderAndPromptsForKey(t *testing.T) {
	// When the stored config targets one provider and the user switches to
	// another, the wizard must prompt for an API key for the NEW provider —
	// the old provider's key is useless for the new one.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Start with an anthropic config that has a stored key.
	if err := oneEntryConfig(config.ModelEntry{Provider: "anthropic", Model: "claude-sonnet-4-6", APIKey: "old-anthropic-key"}).Save(); err != nil {
		t.Fatal(err)
	}

	// Answers (key now comes right after model): provider=openai,
	// model=(default), store_key=y, key=new-openai-key, coauthor=y,
	// reasoning-effort=(off), show-reasoning=(off), set-as-default=y.
	// No endpoint question for a pinned vendor.
	in := strings.NewReader("openai\n\ny\nnew-openai-key\ny\n\n\ny\n")
	var stdout, stderr bytes.Buffer
	if code := runConfig(nil, in, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load after wizard: %v", err)
	}
	entry := got.DefaultEntry()
	if entry.Provider != "openai" {
		t.Errorf("provider = %q, want openai", entry.Provider)
	}
	if entry.APIKey != "new-openai-key" {
		t.Errorf("APIKey = %q, want new-openai-key", entry.APIKey)
	}
}

// TestRunConfigWizard_FirstRun_MinimalAndKeyDirect verifies the first-run path:
// it asks for the key directly (not the "store? (y/N)" double-negative), stores
// it, and skips the expert questions (coauthor / reasoning / show-reasoning).
func TestRunConfigWizard_FirstRun_MinimalAndKeyDirect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	// Answers: provider=openai, model=(default), key=sk-first-run. That's all a
	// first run asks (non-TTY → no live validation).
	in := strings.NewReader("openai\n\nsk-first-run\n")
	var stdout, stderr bytes.Buffer
	if code := runConfigWizard(in, &stdout, &stderr, true); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}

	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := got.DefaultEntry()
	if entry.Provider != "openai" {
		t.Errorf("provider = %q, want openai", entry.Provider)
	}
	if entry.APIKey != "sk-first-run" {
		t.Errorf("APIKey = %q, want sk-first-run stored directly", entry.APIKey)
	}
	out := stdout.String()
	for _, q := range []string{"Co-authored", "Reasoning effort", "thinking trace", "Store the API key"} {
		if strings.Contains(out, q) {
			t.Errorf("first run must not ask %q; got:\n%s", q, out)
		}
	}
}

// TestReportConnectionCheck exercises the live-key validation helper with a
// stubbed connector (no network), both success and failure.
func TestReportConnectionCheck(t *testing.T) {
	orig := validateConnection
	defer func() { validateConnection = orig }()

	var gotModel, gotBase string
	validateConnection = func(ctx context.Context, provider, key, baseURL, model, protocol string) error {
		gotModel, gotBase = model, baseURL
		switch key {
		case "bad":
			return errors.New("anthropic: HTTP 401: invalid x-api-key")
		case "offline":
			return errors.New("dial tcp: lookup api.anthropic.com: no such host")
		}
		return nil
	}

	var out, errb bytes.Buffer
	// Empty model/baseURL must resolve to the vendor defaults before testing.
	if res := reportConnectionCheck(&out, &errb, "anthropic", "good", "", "", ""); res != connOK {
		t.Fatalf("expected connOK; got %d, stderr=%q", res, errb.String())
	}
	if gotModel == "" || gotBase == "" {
		t.Errorf("defaults not resolved: model=%q base=%q", gotModel, gotBase)
	}
	if !strings.Contains(out.String(), "Connected") {
		t.Errorf("missing success line: %q", out.String())
	}

	out.Reset()
	errb.Reset()
	// A rejected key (HTTP 4xx) is the config being wrong.
	if res := reportConnectionCheck(&out, &errb, "anthropic", "bad", "", "", ""); res != connRejected {
		t.Fatalf("expected connRejected for bad key; got %d", res)
	}
	if !strings.Contains(errb.String(), "Couldn't connect") {
		t.Errorf("missing failure line: %q", errb.String())
	}

	// A network error is orthogonal to whether the config is correct.
	if res := reportConnectionCheck(&out, &errb, "anthropic", "offline", "", "", ""); res != connNetwork {
		t.Fatalf("expected connNetwork for unreachable endpoint; got %d", res)
	}
}

// TestClassifyConnErr pins the HTTP-status-based split between a rejected
// config and a transient network failure.
func TestClassifyConnErr(t *testing.T) {
	cases := []struct {
		msg  string
		want connResult
	}{
		{"anthropic: HTTP 401: invalid x-api-key", connRejected},
		{"openai: HTTP 403: forbidden", connRejected},
		{"openai: HTTP 400: bad request", connRejected},
		{"anthropic: HTTP 404: model not found", connRejected},
		// Non-transient 4xx a compatible gateway may use for a bad model/path —
		// still the config being wrong, must not be saved as a "network blip".
		{"openai: HTTP 422: unknown model", connRejected},
		{"openai: HTTP 405: method not allowed", connRejected},
		{"openai: HTTP 402: payment required", connRejected},
		// Transient codes: retrying may succeed, so they're network-class.
		{"openai: HTTP 429: rate limit exceeded", connNetwork},
		{"openai: HTTP 408: request timeout", connNetwork},
		{"anthropic: HTTP 500: internal error", connNetwork},
		{"openai: HTTP 503: service unavailable", connNetwork},
		{"dial tcp 1.2.3.4:443: i/o timeout", connNetwork},
		{"context deadline exceeded", connNetwork},
		// A status echoed inside the body must not be mistaken for the real one.
		{`openai: HTTP 401: {"detail":"upstream returned HTTP 503"}`, connRejected},
	}
	for _, c := range cases {
		if got := classifyConnErr(errors.New(c.msg)); got != c.want {
			t.Errorf("classifyConnErr(%q) = %d, want %d", c.msg, got, c.want)
		}
	}
}

// TestHTTPStatusFromErr covers the parsing edge cases directly: the body-echo
// guard, a malformed marker, and a non-numeric code.
func TestHTTPStatusFromErr(t *testing.T) {
	cases := []struct {
		msg      string
		wantCode int
		wantOK   bool
	}{
		{"anthropic: HTTP 401: invalid x-api-key", 401, true},
		{`openai: HTTP 500: {"err":"saw HTTP 200 upstream"}`, 500, true},
		{"dial tcp: no such host", 0, false},
		{"weird: HTTP : missing code", 0, false},
		{"weird: HTTP abc: not a number", 0, false},
	}
	for _, c := range cases {
		code, ok := httpStatusFromErr(c.msg)
		if code != c.wantCode || ok != c.wantOK {
			t.Errorf("httpStatusFromErr(%q) = (%d, %v), want (%d, %v)", c.msg, code, ok, c.wantCode, c.wantOK)
		}
	}
}

func TestRunConfig_Wizard_CustomProviderRequiresProtocolModelAndBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CUSTOM_API_KEY", "set-so-wizard-skips-key-prompt")

	// Answers: provider, protocol, model, base URL — the Custom vendor needs a
	// protocol, and both free-text fields are required.
	in := strings.NewReader("custom\nopenai\ndeepseek-chat\nhttps://gw.example/v1\n")
	var stdout, stderr bytes.Buffer
	if code := runConfig(nil, in, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load after wizard: %v", err)
	}
	entry := got.DefaultEntry()
	if entry.Provider != "custom" || entry.Protocol != "openai" || entry.Model != "deepseek-chat" || entry.BaseURL != "https://gw.example/v1" {
		t.Errorf("entry = %+v, want custom/openai/deepseek-chat/https://gw.example/v1", entry)
	}

	// An empty base URL is a hard error, not a silent default.
	in = strings.NewReader("custom\nopenai\ndeepseek-chat\n\n")
	stdout.Reset()
	stderr.Reset()
	// Wipe the entry so its stored URL doesn't become the press-Enter default.
	if err := (config.Config{}).Save(); err != nil {
		t.Fatal(err)
	}
	if code := runConfig(nil, in, &stdout, &stderr); code != 2 {
		t.Errorf("empty base URL: exit = %d, want 2 (stderr=%q)", code, stderr.String())
	}

	// An empty model is equally a hard error.
	in = strings.NewReader("custom\nopenai\n\nhttps://gw.example/v1\n")
	if code := runConfig(nil, in, &stdout, &stderr); code != 2 {
		t.Errorf("empty model: exit = %d, want 2 (stderr=%q)", code, stderr.String())
	}

	// An invalid protocol is rejected.
	in = strings.NewReader("custom\nbogus\ndeepseek-chat\nhttps://gw.example/v1\n")
	if code := runConfig(nil, in, &stdout, &stderr); code != 2 {
		t.Errorf("invalid protocol: exit = %d, want 2 (stderr=%q)", code, stderr.String())
	}
}

func TestRunConfig_Wizard_PinnedVendorRejectsForeignEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MOONSHOT_API_KEY", "set-so-wizard-skips-key-prompt")

	// kimi has regional variants: a variant URL is accepted…
	in := strings.NewReader("kimi\n\nhttps://api.moonshot.ai\n")
	var stdout, stderr bytes.Buffer
	if code := runConfig(nil, in, &stdout, &stderr); code != 0 {
		t.Fatalf("variant URL: exit = %d, stderr=%q", code, stderr.String())
	}
	got, _ := config.Load()
	if got.DefaultEntry().BaseURL != "https://api.moonshot.ai" {
		t.Errorf("base URL = %q, want the picked variant", got.DefaultEntry().BaseURL)
	}

	// …but an arbitrary URL is rejected with a pointer to the custom vendor.
	in = strings.NewReader("kimi\n\nhttps://evil.example\n")
	stdout.Reset()
	stderr.Reset()
	if code := runConfig(nil, in, &stdout, &stderr); code != 2 {
		t.Errorf("foreign URL: exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "custom") {
		t.Errorf("error should point at the custom vendor; got %q", stderr.String())
	}
}

func TestRunConfig_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runConfig([]string{"frobnicate"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
