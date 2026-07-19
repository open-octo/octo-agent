package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/config"
)

// validateConnection probes a provider/model/key against the live endpoint.
// Package var so tests substitute a fake (the real one makes a network call).
var validateConnection = app.TestConnection

// firstNonEmpty returns the first non-empty string, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveProviderModel applies precedence flag > env > config file > built-in
// default to the provider and model. An empty flagProvider/flagModel means "not
// set on the CLI". A --model value that names a config entry selects the whole
// entry — provider, model, base URL, and key all come from it (overriding even
// an explicit --provider, which can't meaningfully combine with a named
// entry). The returned entry is the config entry the resolution anchored on,
// so base-URL/key/reasoning lookups stay on the same entry. ok is false when
// the resolved provider has no known default model (i.e. an unknown provider
// with no explicit model) — the caller prints an error and exits.
func resolveProviderModel(flagProvider, flagModel string, cfg config.Config) (provider, model string, entry config.ModelEntry, ok bool) {
	if e, found := cfg.EntryByModel(flagModel); found {
		provider = firstNonEmpty(e.Provider, app.ProviderAnthropic)
		model = firstNonEmpty(e.Model, defaultModels[provider])
		return provider, model, e, model != ""
	}

	entry = cfg.DefaultEntry()
	provider = firstNonEmpty(flagProvider, os.Getenv("OCTO_PROVIDER"), entry.Provider, app.ProviderAnthropic)
	// Precedence: --model flag > env (ANTHROPIC_MODEL / OPENAI_MODEL) > config
	// (same-provider only) > built-in default. The env tier mirrors how the
	// base URL and key resolve env-first, so a third-party Claude-/OpenAI-
	// compatible endpoint can be configured entirely through env vars (e.g.
	// ANTHROPIC_BASE_URL + ANTHROPIC_API_KEY + ANTHROPIC_MODEL).
	model = flagModel
	if model == "" {
		model = modelFromEnv(provider)
	}
	// The entry's model is specific to the entry's provider — only honor it
	// when the resolved provider matches, so `--provider <other>` doesn't carry
	// one vendor's model onto another.
	if model == "" && provider == entry.Provider {
		model = entry.Model
	}
	if model == "" {
		model = defaultModels[provider]
	}
	return provider, model, entry, model != ""
}

// providerBaseURL returns the entry's base URL only when the entry targets the
// same provider — a base URL is endpoint-specific to its provider, so it must
// not leak onto a different one selected via --provider.
func providerBaseURL(provider string, entry config.ModelEntry) string {
	if entry.Provider == provider {
		return entry.BaseURL
	}
	return ""
}

// modelFromEnv returns the per-provider model env override, or "" if unset.
func modelFromEnv(provider string) string {
	envVar := strings.ToUpper(provider) + "_MODEL"
	return os.Getenv(envVar)
}

// resolveBaseURL returns the base-URL override for the provider, env-first
// (<PROVIDER>_BASE_URL) then the resolved config entry. "" means no
// override — the provider uses its built-in default endpoint.
func resolveBaseURL(provider string, entry config.ModelEntry) string {
	envVar := strings.ToUpper(provider) + "_BASE_URL"
	return firstNonEmpty(os.Getenv(envVar), providerBaseURL(provider, entry))
}

// effectiveEndpoint is resolveBaseURL for display: it substitutes the
// provider's built-in default (marked) when there's no override, so a verbose
// run always shows the host that will actually be called.
func effectiveEndpoint(provider string, entry config.ModelEntry) string {
	if u := resolveBaseURL(provider, entry); u != "" {
		return u
	}
	if def := app.DefaultBaseURL(provider); def != "" {
		return def + " (default)"
	}
	return "(default)"
}

// runConfig handles `octo config [show|path]` and, with no subcommand, an
// interactive setup wizard that writes ~/.octo/config.yml.
func runConfig(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "setup", "init":
		// Explicit `octo config` is the full editor (expert prefs included).
		return runConfigWizard(stdin, stdout, stderr, false)
	case "show", "get":
		return runConfigShow(stdout, stderr)
	case "path":
		path, err := config.Path()
		if err != nil {
			fmt.Fprintf(stderr, "octo config: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, path)
		return 0
	case "fix", "--fix":
		return runConfigFix(stdout, stderr)
	default:
		fmt.Fprintf(stderr, "octo config: unknown subcommand %q (use: setup | show | path | fix)\n", sub)
		return 2
	}
}

// runConfigFix repairs ~/.octo/config.yml. If it no longer parses (the case that
// stops octo from starting), it restores the last good backup. If it parses but
// has semantic problems, it auto-fixes the safe ones (dangling default_model /
// lite_model) and reports the rest for manual attention.
func runConfigFix(stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		broken, rerr := config.RestoreFromBackup()
		if rerr != nil {
			fmt.Fprintf(stderr, "octo config --fix: config.yml doesn't parse: %v\n", err)
			fmt.Fprintf(stderr, "Could not auto-restore: %v\n", rerr)
			fmt.Fprintln(stderr, "Run `octo config` to rebuild it from scratch.")
			return 1
		}
		fmt.Fprintln(stdout, "Restored config.yml from the last version octo saved (config.yml.bak).")
		if broken != "" {
			fmt.Fprintf(stdout, "Your edited (unparseable) file was kept as %s — copy any recent changes back from it.\n", broken)
		}
		return 0
	}

	repaired, fixed, unfixable := cfg.Repair()
	if len(fixed) == 0 && len(unfixable) == 0 {
		fmt.Fprintln(stdout, "config.yml is healthy — nothing to fix.")
		return 0
	}
	if len(fixed) > 0 {
		if serr := repaired.Save(); serr != nil {
			fmt.Fprintf(stderr, "octo config --fix: save: %v\n", serr)
			return 1
		}
		fmt.Fprintln(stdout, "Fixed:")
		for _, f := range fixed {
			fmt.Fprintf(stdout, "  • %s\n", f)
		}
	}
	if len(unfixable) > 0 {
		fmt.Fprintln(stdout, "Needs manual attention (edit ~/.octo/config.yml):")
		for _, u := range unfixable {
			fmt.Fprintf(stdout, "  • %s\n", u)
		}
		return 1
	}
	return 0
}

// runConfigShow prints the effective provider/model/base-URL and where each
// value comes from (CLI flags aside — those are per-invocation). It never
// prints the API key, only whether one is reachable.
func runConfigShow(stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "octo config: %v\n", err)
		fmt.Fprintln(stderr, "Run `octo config` to rewrite it.")
		return 1
	}
	path, _ := config.Path()

	fmt.Fprintf(stdout, "Config file: %s\n", path)
	if _, statErr := os.Stat(path); statErr != nil {
		fmt.Fprintln(stdout, "  (not created yet — run `octo config` to set it up)")
	}
	fmt.Fprintln(stdout)

	if len(cfg.Endpoints) == 0 {
		fmt.Fprintln(stdout, "endpoints: (none configured)")
	} else {
		fmt.Fprintln(stdout, "endpoints:")
		for _, ep := range cfg.Endpoints {
			header := ep.ID
			if ep.Name != "" && ep.Name != ep.ID {
				header = fmt.Sprintf("%s (%s)", ep.ID, ep.Name)
			}
			// Mark default/lite by which endpoint holds the composite id.
			tags := []string{}
			if strings.HasPrefix(cfg.Default, ep.ID+"::") {
				tags = append(tags, fmt.Sprintf("default: %s", cfg.Default))
			}
			if strings.HasPrefix(cfg.Lite, ep.ID+"::") {
				tags = append(tags, fmt.Sprintf("lite: %s", cfg.Lite))
			}
			modelsCount := len(ep.Models)
			// Compose a single-line endpoint row.
			tail := ""
			if len(tags) > 0 {
				tail = " [" + strings.Join(tags, "; ") + "]"
			}
			// Provider/base_url inline for at-a-glance identification.
			info := ep.Provider
			if ep.BaseURL != "" {
				info += ", " + ep.BaseURL
			} else {
				info += " (provider default)"
			}
			fmt.Fprintf(stdout, "  %s — %s, %d models%s\n", header, info, modelsCount, tail)
		}
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "references:")
	if cfg.Default != "" {
		fmt.Fprintf(stdout, "  default = %s\n", cfg.Default)
	} else {
		fmt.Fprintln(stdout, "  default = (unset, falls back to first endpoint's first model)")
	}
	if cfg.Lite != "" {
		fmt.Fprintf(stdout, "  lite = %s\n", cfg.Lite)
	} else {
		fmt.Fprintln(stdout, "  lite = (none)")
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "reasoning:")
	effort := cfg.ReasoningEffort
	if effort == "" {
		effort = "off (default)"
	}
	fmt.Fprintf(stdout, "  effort = %s\n", effort)
	showStatus := "off (default)"
	if cfg.ShowReasoning != nil {
		if *cfg.ShowReasoning {
			showStatus = "on (config)"
		} else {
			showStatus = "off (config)"
		}
	}
	fmt.Fprintf(stdout, "  show_reasoning = %s\n", showStatus)

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "CLI flags (--provider, --model, --system) and env vars override this file per run.")
	return 0
}

// otherEntryModels lists the configured endpoint+model pairs besides the one
// referenced by skip, for display. PR5: scans Endpoints instead of the deleted
// Models field.
func otherEntryModels(cfg config.Config, skip string) string {
	var names []string
	for _, ep := range cfg.Endpoints {
		for _, m := range ep.Models {
			cid := ep.ID + "::" + m.Model
			if cid != skip && m.Model != skip {
				names = append(names, cid)
			}
		}
	}
	return strings.Join(names, ", ")
}

// apiKeyStatus reports where a key for the given provider would come from,
// without revealing it. Env always wins over a config-stored key.
// apiKeyReachable reports whether a usable API key exists for the entry —
// either via the provider's env var or stored in the entry itself.
func apiKeyReachable(provider string, entry config.ModelEntry) bool {
	envVar := app.VendorAPIKeyEnvVar(provider)
	if envVar == "" {
		envVar = strings.ToUpper(provider) + "_API_KEY"
	}
	if os.Getenv(envVar) != "" {
		return true
	}
	return entry.APIKey != "" && entry.Provider == provider
}

func apiKeyStatus(provider string, entry config.ModelEntry) string {
	envVar := app.VendorAPIKeyEnvVar(provider)
	if envVar == "" {
		envVar = strings.ToUpper(provider) + "_API_KEY"
	}
	if os.Getenv(envVar) != "" {
		return "set via $" + envVar
	}
	if entry.APIKey != "" && entry.Provider == provider {
		return "stored in config (mode 0600)"
	}
	return "not set — export $" + envVar
}

// runConfigWizard prompts for the persisted defaults and writes the file.
// Existing values are offered as the default for each prompt so re-running it
// edits rather than resets.
// runConfigWizard walks the user through setting their default provider/model
// and key. firstRun trims it to the essentials (provider → model → key) and
// asks for the key directly, deferring expert prefs (coauthor, reasoning,
// show-reasoning) to a later `octo config`; the full editor asks everything.
func runConfigWizard(stdin io.Reader, stdout, stderr io.Writer, firstRun bool) int {
	full, _ := config.Load() // a malformed file is treated as empty here — the wizard overwrites it
	existing := full.DefaultEntry()

	// An arrow-key menu needs an editable terminal on both ends. When stdin is
	// piped, the output isn't a TTY, or readline declines (Windows), fall back
	// to the typed-answer flow so scripts and tests keep working unchanged.
	tty := stdinIsTTY(stdin) && writerIsTTY(stdout)

	var reader lineReader
	if tty {
		if rl, err := newReadlineReader(defaultHistoryFile()); err == nil {
			reader = rl
		} else {
			tty = false
		}
	}
	if reader == nil {
		reader = newScannerLineReader(stdin, stdout)
	}
	defer reader.Close()

	fmt.Fprintln(stdout, "octo config — set your default provider and model (~/.octo/config.yml).")
	if tty {
		fmt.Fprintln(stdout, "Use ↑/↓ to choose, Enter to confirm. CLI flags and env vars still override per run.")
	} else {
		fmt.Fprintln(stdout, "Press Enter to keep the shown default. CLI flags and env vars still override per run.")
	}
	fmt.Fprintln(stdout)

	// Provider.
	var provider string
	if tty {
		items := make([]selectItem, len(app.Registry))
		for i, v := range app.Registry {
			items[i] = selectItem{label: v.DisplayName, desc: v.ID, value: v.ID}
		}
		choice, ok := runSelect(stdin, stdout, "Provider", items, firstNonEmpty(existing.Provider, app.ProviderAnthropic))
		if !ok {
			return cancelWizard(stderr)
		}
		provider = choice.value
		fmt.Fprintf(stdout, "Provider: %s (%s)\n\n", app.VendorDisplayName(provider), provider)
	} else {
		provider = strings.ToLower(strings.TrimSpace(promptDefault(reader, stdout,
			"Provider (anthropic | openai | kimi | deepseek | ...)", firstNonEmpty(existing.Provider, app.ProviderAnthropic))))
		if !app.IsKnownVendor(provider) {
			fmt.Fprintf(stderr, "octo config: unknown provider %q\n", provider)
			return 2
		}
	}

	// sameProvider gates reuse of the existing entry's model/base URL — values
	// stored for one vendor must not seed prompts for a different one.
	sameProvider := existing.Provider == provider

	// Protocol. The Custom vendor has no fixed wire format, so the user picks
	// one explicitly; named vendors carry their protocol in the registry and
	// skip this. Stored on the entry (config Protocol field), not the registry.
	var protocol string
	if app.VendorNeedsProtocol(provider) {
		def := "openai"
		if sameProvider && existing.Protocol != "" {
			def = existing.Protocol
		}
		if tty {
			items := []selectItem{
				{label: "OpenAI (Chat Completions)", desc: "openai", value: "openai"},
				{label: "Anthropic (Messages)", desc: "anthropic", value: "anthropic"},
			}
			choice, ok := runSelect(stdin, stdout, "Protocol", items, def)
			if !ok {
				return cancelWizard(stderr)
			}
			protocol = choice.value
			fmt.Fprintf(stdout, "Protocol: %s\n\n", protocol)
		} else {
			protocol = strings.ToLower(strings.TrimSpace(promptDefault(reader, stdout,
				"Protocol (openai | anthropic)", def)))
			if protocol != "openai" && protocol != "anthropic" {
				fmt.Fprintf(stderr, "octo config: protocol must be \"openai\" or \"anthropic\"\n")
				return 2
			}
		}
	}

	// Model. Compatible (custom-endpoint) vendors have no catalogue or default,
	// so the model is a required free-text answer.
	var model string
	if app.VendorCustomEndpoint(provider) {
		def := ""
		if sameProvider {
			def = existing.Model
		}
		model = strings.TrimSpace(promptDefault(reader, stdout, "Model (required)", def))
		if model == "" {
			fmt.Fprintf(stderr, "octo config: provider %q has no default model — enter one\n", provider)
			return 2
		}
		if tty {
			fmt.Fprintf(stdout, "Model: %s\n\n", model)
		}
	} else if tty {
		def := defaultModels[provider]
		startVal := def
		if sameProvider && existing.Model != "" {
			startVal = existing.Model
		}
		items := buildModelItems(app.VendorModels(provider), def, startVal)
		items = append(items, selectItem{label: "Custom model…", desc: "enter a model id", value: customSentinel})
		choice, ok := runSelect(stdin, stdout, "Model", items, startVal)
		if !ok {
			return cancelWizard(stderr)
		}
		if choice.value == customSentinel {
			model = strings.TrimSpace(promptDefault(reader, stdout, "Model", startVal))
		} else {
			model = choice.value
		}
		shown := model
		if shown == "" || shown == def {
			shown = def + " (default)"
		}
		fmt.Fprintf(stdout, "Model: %s\n\n", shown)
	} else {
		model = strings.TrimSpace(promptDefault(reader, stdout, "Model", firstNonEmpty(existing.Model, defaultModels[provider])))
	}

	// Base URL / endpoint. Compatible (custom-endpoint) vendors require a
	// free-text URL; vendors with regional variants get a fixed menu; everyone
	// else is pinned to the default endpoint — no question asked. A legacy
	// custom URL already stored for the same vendor is preserved as-is.
	baseURL := ""
	if sameProvider {
		baseURL = existing.BaseURL
	}
	variants := app.VendorEndpointVariants(provider)
	switch {
	case app.VendorCustomEndpoint(provider):
		baseURL = strings.TrimSpace(promptDefault(reader, stdout, "Base URL (required)", baseURL))
		if baseURL == "" {
			fmt.Fprintf(stderr, "octo config: provider %q requires a base URL\n", provider)
			return 2
		}
		if tty {
			fmt.Fprintf(stdout, "Endpoint: %s\n\n", baseURL)
		}
	case tty && len(variants) > 0:
		items := []selectItem{{label: "Default endpoint", desc: app.DefaultBaseURL(provider), value: ""}}
		for _, v := range variants {
			items = append(items, selectItem{label: v.Label, desc: v.BaseURL, value: v.BaseURL})
		}
		choice, ok := runSelect(stdin, stdout, "Endpoint", items, baseURL)
		if !ok {
			return cancelWizard(stderr)
		}
		baseURL = choice.value
		if baseURL == "" {
			fmt.Fprintf(stdout, "Endpoint: default\n\n")
		} else {
			fmt.Fprintf(stdout, "Endpoint: %s\n\n", baseURL)
		}
	case len(variants) > 0:
		// A stale value that is no longer a vendor endpoint must not become the
		// press-Enter default — it would fail validation below.
		def := ""
		if app.VendorByBaseURL(baseURL) == provider {
			def = baseURL
		}
		ans := strings.TrimSpace(promptDefault(reader, stdout, "Endpoint URL (empty = default)", def))
		if ans != "" && app.VendorByBaseURL(ans) != provider {
			fmt.Fprintf(stderr, "octo config: %q is not an endpoint of %s — for a custom endpoint use the %s provider\n",
				ans, app.VendorDisplayName(provider), app.ProviderCustom)
			return 2
		}
		baseURL = ans
	}

	// The wizard edits the default entry in place; other entries and global
	// settings (permission mode, tools, …) pass through untouched.
	outEntry := existing
	outEntry.Provider = provider
	outEntry.Model = model
	outEntry.BaseURL = baseURL
	// Protocol is stored only for the Custom vendor; clear it for named vendors
	// so a switch away from Custom doesn't leave a stale value behind.
	outEntry.Protocol = protocol

	// ── API key — asked right after the model so a brand-new user reaches the
	// one thing that actually unblocks them, not after a pile of expert
	// questions. env stays the recommended home; we only prompt when it's empty.
	keyEntered := collectAPIKey(&outEntry, existing, provider, firstRun, reader, stdout)

	// Validate a freshly entered key against the live endpoint so a typo is
	// caught here, not on the first turn. Interactive only (TTY) — piped runs and
	// tests must not hit the network. The full editor only warns; first run keeps
	// the user on a rejected key (which won't fix itself) but lets a network blip
	// through, since that says nothing about whether the config is right.
	if keyEntered && tty {
		for {
			res := reportConnectionCheck(stdout, stderr, provider, outEntry.APIKey, baseURL, model, protocol)
			if res == connOK || !firstRun {
				break
			}
			if res == connNetwork {
				// Same stream as the ✗ line it explains, so the two stay together.
				fmt.Fprintln(stderr, "Couldn't verify the connection — looks like a network or endpoint issue, not the key. Saving anyway; if octo won't start, re-run `octo config`.")
				break
			}
			// connRejected: the endpoint turned the request away, so the saved
			// config would be unusable. Push the user to fix it; empty enter is an
			// explicit, eyes-open choice to keep the rejected key.
			again := strings.TrimSpace(promptDefault(reader, stdout,
				"Re-enter API key (empty = save this rejected key anyway)", ""))
			if again == "" {
				break
			}
			outEntry.APIKey = again
		}
	}

	// Expert preferences — skipped on first run (sane defaults: coauthor on,
	// reasoning off). They stay one `octo config` away.
	if !firstRun {
		// Co-authored-by: default on; ask once in wizard.
		coauthorDefault := full.Coauthor == nil || *full.Coauthor
		coauthorVal, ok := pickYesNo(tty, reader, stdin, stdout,
			"Append Co-authored-by to git commits?", coauthorDefault)
		if !ok {
			return cancelWizard(stderr)
		}
		full.Coauthor = &coauthorVal

		// Reasoning effort: off (empty) by default; offer the existing value.
		// PR5: reasoning is global — reads/writes full.ReasoningEffort.
		if tty {
			choice, ok := runSelect(stdin, stdout, "Reasoning effort", []selectItem{
				{label: "Off", value: ""},
				{label: "Low", value: "low"},
				{label: "Medium", value: "medium"},
				{label: "High", value: "high"},
				{label: "Extra-high", value: "xhigh"},
				{label: "Max", value: "max"},
			}, full.ReasoningEffort)
			if !ok {
				return cancelWizard(stderr)
			}
			full.ReasoningEffort = choice.value
		} else {
			effortAns := strings.ToLower(strings.TrimSpace(promptDefault(reader, stdout,
				"Reasoning effort (low | medium | high | xhigh | max, empty = off)", full.ReasoningEffort)))
			if !validReasoningEffort(effortAns) {
				fmt.Fprintf(stderr, "octo config: invalid reasoning effort %q (use 'low', 'medium', 'high', 'xhigh', 'max', or empty)\n", effortAns)
				return 2
			}
			full.ReasoningEffort = effortAns
		}

		// Surface the reasoning/thinking trace for the Web UI to display (the
		// terminal never renders it): default off.
		// Skip when reasoning is off — no reasoning output to show.
		if full.ReasoningEffort != "" {
			showDefault := full.ShowReasoning != nil && *full.ShowReasoning
			showVal, ok := pickYesNo(tty, reader, stdin, stdout,
				"Show the reasoning/thinking trace on the Web UI?", showDefault)
			if !ok {
				return cancelWizard(stderr)
			}
			full.ShowReasoning = &showVal
		}
	}

	// Vision capability is always recorded on the entry. A predefined model
	// carries it in the catalogue (the "floats with default" empty-model case
	// resolves to the vendor default); a custom / unknown model is answered by
	// the user, or on first run defaults to the id heuristic to keep onboarding
	// friction-free.
	effModel := firstNonEmpty(outEntry.Model, defaultModels[provider])
	if v, known := app.VendorModelVision(provider, effModel); known {
		outEntry.Vision = v
	} else if firstRun {
		outEntry.Vision = config.ModelSupportsVision(effModel)
	} else {
		visVal, ok := pickYesNo(tty, reader, stdin, stdout,
			"Does this model accept image input (vision)?", config.ModelSupportsVision(effModel))
		if !ok {
			return cancelWizard(stderr)
		}
		outEntry.Vision = visVal
	}

	// PR5 (design §7.1): the wizard builds one endpoint from the collected
	// ModelEntry (outEntry). The endpoint id follows the implicit-id rule
	// legacy-<host>-0 — the same rule Load uses for old models: blocks — so a
	// user who runs the wizard twice against the same base_url sees the same
	// id and the second run overwrites the first (rather than silently
	// creating a second endpoint).
	host := hostFromBaseURLForWizard(outEntry.BaseURL)
	// Fallback: a named vendor with no base_url yields an ugly "legacy--0"
	// id. Fall back to the provider name so the id is at least recognisable.
	if host == "" {
		host = strings.ToLower(provider)
	}
	endpointID := legacyEndpointIDForWizard(host, 0)

	// PR5 (design §11.4): the same model name may appear under multiple
	// endpoints — that's the whole point of the two-level schema (e.g.
	// claude-sonnet-4-6 via both official anthropic and a relay). So unlike
	// the old flat Models schema (which rejected duplicate model names),
	// the wizard does NOT collision-check the model name across endpoints.
	// The only collision that matters is the endpoint id, and UpsertEndpoint
	// handles that by overwriting the same id.

	ep := config.Endpoint{
		ID:       endpointID,
		Provider: outEntry.Provider,
		BaseURL:  outEntry.BaseURL,
		APIKey:   outEntry.APIKey,
		Protocol: outEntry.Protocol,
	}
	// Carry over any existing models if we're overwriting the same endpoint
	// id, so a user re-running the wizard doesn't lose their other models.
	for _, existingEp := range full.Endpoints {
		if existingEp.ID == endpointID {
			ep.Name = existingEp.Name
			ep.LiteModel = existingEp.LiteModel
			// Only keep models that aren't the one we're about to add/replace.
			for _, m := range existingEp.Models {
				if m.Model != outEntry.Model {
					ep.Models = append(ep.Models, m)
				}
			}
			break
		}
	}
	ep.Models = append(ep.Models, config.EndpointModel{
		Model:  outEntry.Model,
		Vision: outEntry.Vision,
	})
	full.UpsertEndpoint(ep)
	cid := endpointID + "::" + outEntry.Model

	// PR5 (design §7.1 step 7): explicit "set as default?" prompt. Default
	// yes on first run / when no Default exists; default no when overwriting
	// an existing default the user might want to keep.
	setDefaultDefault := firstRun || full.Default == ""
	setDefault, ok := pickYesNo(tty, reader, stdin, stdout,
		fmt.Sprintf("Set %s as the default model?", cid), setDefaultDefault)
	if !ok {
		return cancelWizard(stderr)
	}
	if setDefault {
		full.SetDefaultComposite(cid)
	}

	if err := full.Save(); err != nil {
		fmt.Fprintf(stderr, "octo config: %v\n", err)
		return 1
	}
	path, _ := config.Path()
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "Saved %s\n", path)
	if outEntry.APIKey == "" && os.Getenv(apiKeyEnvVar(provider)) == "" {
		fmt.Fprintf(stdout, "Next: export %s=... (or re-run `octo config` to store it), then `octo`.\n", apiKeyEnvVar(provider))
	} else {
		fmt.Fprintln(stdout, "Run `octo` to start.")
	}
	return 0
}

// hostFromBaseURLForWizard extracts the URL host (lowercased) for use in a
// legacy-<host>-<n> endpoint ID. Mirrors config.hostFromBaseURL, which is
// unexported — kept in lockstep so the wizard generates the same id as Load.
func hostFromBaseURLForWizard(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return strings.ToLower(baseURL)
	}
	return strings.ToLower(u.Host)
}

// legacyEndpointIDForWizard builds a "legacy-<host>-<n>" endpoint id, the
// implicit-id rule shared with config.legacyEndpointID (unexported). Non-
// alphanumeric host chars are replaced with '-' so the id matches the
// ^[a-zA-Z0-9_-]+$ endpoint id regex.
func legacyEndpointIDForWizard(host string, n int) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, host)
	return fmt.Sprintf("legacy-%s-%d", safe, n)
}

// apiKeyEnvVar is the conventional env var holding a provider's key.
func apiKeyEnvVar(provider string) string {
	if v := app.VendorAPIKeyEnvVar(provider); v != "" {
		return v
	}
	return strings.ToUpper(provider) + "_API_KEY"
}

// collectAPIKey prompts for the provider key when none is reachable (env empty
// and no usable stored key), returning whether a non-empty key was just entered.
// First run asks for the key directly and stores it — the wizard auto-launched
// precisely because no key exists, so a "store in file? (not recommended)"
// double-negative that dead-ends in an env-var detour helps nobody. The full
// editor keeps that opt-in prompt (env is the recommended home there).
func collectAPIKey(outEntry *config.ModelEntry, existing config.ModelEntry, provider string, firstRun bool, reader lineReader, stdout io.Writer) bool {
	if os.Getenv(apiKeyEnvVar(provider)) != "" {
		return false
	}
	// A key stored for a different provider is useless for the new one.
	if existing.APIKey != "" && existing.Provider == provider {
		return false
	}
	outEntry.APIKey = ""
	if firstRun {
		key := strings.TrimSpace(promptDefault(reader, stdout, "Paste your "+app.VendorDisplayName(provider)+" API key", ""))
		outEntry.APIKey = key
		return key != ""
	}
	ans := strings.ToLower(strings.TrimSpace(promptDefault(reader, stdout,
		"Store the API key in this file? Not recommended — prefer "+apiKeyEnvVar(provider)+" (y/N)", "n")))
	if ans == "y" || ans == "yes" {
		key := strings.TrimSpace(promptDefault(reader, stdout, "API key", ""))
		outEntry.APIKey = key
		return key != ""
	}
	return false
}

// connResult classifies the outcome of a live connection probe. The wizard
// treats the two failure classes differently: a rejected key/model/endpoint is
// the user's config being wrong and won't fix itself, whereas a network blip is
// orthogonal to whether the config is correct.
type connResult int

const (
	connOK       connResult = iota // probe succeeded
	connRejected                   // endpoint rejected the request (bad key/model/endpoint)
	connNetwork                    // couldn't reach the endpoint (network/timeout/5xx/429)
)

// reportConnectionCheck tests the entered key against the endpoint, printing a
// ✓/✗ line. Resolves the vendor default model/base URL when the user accepted
// the default (left them empty).
func reportConnectionCheck(stdout, stderr io.Writer, provider, key, baseURL, model, protocol string) connResult {
	if model == "" {
		model = app.VendorDefaultModel(provider)
	}
	if baseURL == "" {
		baseURL = app.DefaultBaseURL(provider)
	}
	fmt.Fprintln(stdout, "Testing connection…")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := validateConnection(ctx, provider, key, baseURL, model, protocol); err != nil {
		fmt.Fprintf(stderr, "✗ Couldn't connect: %v\n", err)
		return classifyConnErr(err)
	}
	fmt.Fprintln(stdout, "✓ Connected.")
	return connOK
}

// classifyConnErr decides whether a failed probe means the config is wrong or
// the network just got in the way. Providers format API rejections as
// "<vendor>: HTTP <code>: …" (see anthropic/openai client.go), so the status
// code is the signal. A client-error status (4xx) means the request itself was
// turned away — wrong key/model/endpoint — and won't fix itself, EXCEPT the
// transient 4xx (408/409/425/429) which, like every 5xx and every error with no
// HTTP code at all (dial/timeout), is a network problem the config can't be
// blamed for.
func classifyConnErr(err error) connResult {
	code, ok := httpStatusFromErr(err.Error())
	if !ok {
		return connNetwork // no HTTP response reached us at all
	}
	switch {
	case code == 408 || code == 409 || code == 425 || code == 429:
		return connNetwork // transient — retrying may well succeed
	case code >= 400 && code < 500:
		return connRejected // request rejected: bad key/model/endpoint
	default:
		return connNetwork // 5xx and anything non-4xx: server-side / unverified
	}
}

// httpStatusFromErr extracts the status code from a provider error of the form
// "…: HTTP <code>: …". The ": HTTP " marker (with the leading colon-space the
// providers emit) anchors to the real status prefix, so a status code echoed
// later inside the response body can't be mistaken for it. Returns ok=false
// when no such marker is present.
func httpStatusFromErr(msg string) (int, bool) {
	const marker = ": HTTP "
	i := strings.Index(msg, marker)
	if i < 0 {
		return 0, false
	}
	rest := msg[i+len(marker):]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return 0, false
	}
	code, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return code, true
}

// customSentinel is the menu value standing in for "let me type my own". It
// uses a NUL byte so it can never collide with a real model id or URL.
const customSentinel = "\x00custom"

// cancelWizard reports the wizard was aborted (Esc/Ctrl-C at a menu) and
// returns the process exit code for that.
func cancelWizard(stderr io.Writer) int {
	fmt.Fprintln(stderr, "octo config: cancelled, nothing saved.")
	return 1
}

// buildModelItems turns a vendor's model catalogue into menu rows, marking the
// built-in default and folding in the user's current pick when it isn't part
// of the catalogue (so re-running the wizard shows it pre-selected).
func buildModelItems(models []string, def, current string) []selectItem {
	items := make([]selectItem, 0, len(models)+1)
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		seen[m] = true
		desc := ""
		if m == def {
			desc = "default"
		}
		items = append(items, selectItem{label: m, desc: desc, value: m})
	}
	if current != "" && current != def && !seen[current] {
		items = append(items, selectItem{label: current, desc: "current", value: current})
	}
	return items
}

// pickYesNo asks a boolean question: an arrow-key Yes/No menu on a TTY, the
// typed "(Y/n)" prompt otherwise. ok is false only when a TTY menu is
// cancelled.
func pickYesNo(tty bool, reader lineReader, stdin io.Reader, stdout io.Writer, prompt string, def bool) (val, ok bool) {
	defVal := "n"
	if def {
		defVal = "y"
	}
	if tty {
		choice, ok := runSelect(stdin, stdout, prompt, []selectItem{
			{label: "Yes", value: "y"},
			{label: "No", value: "n"},
		}, defVal)
		if !ok {
			return false, false
		}
		return choice.value == "y", true
	}
	ans := strings.ToLower(strings.TrimSpace(promptDefault(reader, stdout, prompt+" (Y/n)", defVal)))
	return ans != "n" && ans != "no", true
}

// promptDefault asks one question, showing def as the value used on empty input.
func promptDefault(reader lineReader, stdout io.Writer, label, def string) string {
	prompt := label
	if def != "" {
		prompt += " [" + def + "]"
	}
	prompt += ": "
	line, ok := reader.ReadLine(prompt)
	if !ok {
		return def
	}
	if strings.TrimSpace(line) == "" {
		return def
	}
	return line
}
