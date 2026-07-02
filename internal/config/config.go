// Package config holds the user's persisted CLI defaults at
// ~/.octo/config.yml — a list of named model configurations plus global
// settings, so a fresh `octo` works without re-typing flags or
// re-exporting env vars every session.
//
// Precedence is resolved by the caller (cmd/octo): an explicit CLI flag beats
// this file, which beats the built-in default. API keys are read from the
// environment first; storing one here is opt-in and discouraged (it lands in
// plaintext, mode 0600), so callers fall back to the entry's APIKey only when
// the matching env var is empty.
//
// The file was previously ~/.octo/config.yaml with a single top-level
// provider/model pair. Load reads that legacy file (and legacy fields) when
// config.yml is absent, normalising it into a one-entry Models list; the
// first Save writes the new schema to config.yml and parks the legacy file
// as config.yaml.bak.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelEntry is one model configuration: everything needed to build a sender
// for it. Model is the entry's identity — the HTTP API uses it as the id,
// `--model <model>` selects the whole entry, and default_model / lite_model
// reference it. Two entries may not share a model string.
type ModelEntry struct {
	Provider string `yaml:"provider,omitempty"`
	Model    string `yaml:"model,omitempty"`
	// Protocol ("anthropic" | "openai") is the wire format for the Custom
	// vendor, which has no registry-pinned protocol. Ignored for named vendors.
	Protocol string `yaml:"protocol,omitempty"`
	// BaseURL is an optional endpoint override; empty uses the vendor default.
	BaseURL string `yaml:"base_url,omitempty"`
	// APIKey, when set, is a plaintext fallback used only if the provider's
	// env var is empty. Opt-in via `octo config` and stored mode 0600. Prefer
	// the env var.
	APIKey string `yaml:"api_key,omitempty"`
	// ReasoningEffort sets the unified reasoning intensity ("low" | "medium" |
	// "high"). OpenAI sends it as reasoning_effort; Anthropic maps it to an
	// extended-thinking token budget. Empty means off (no extended reasoning).
	ReasoningEffort string `yaml:"reasoning_effort,omitempty"`
	// ShowReasoning controls whether a reasoning model's thinking trace is
	// streamed to the terminal (dimmed). nil means the built-in default
	// (enabled).
	ShowReasoning *bool `yaml:"show_reasoning,omitempty"`
	// Vision controls whether tools may hand this model images (e.g. the
	// browser tool's screenshot). It is always recorded: predefined models get
	// their catalogue value at add time, custom models are answered by the user.
	// Set false for text-only models (e.g. qwen-plus) so image content isn't
	// sent and rejected. A legacy file with no `vision:` key is backfilled from
	// ModelSupportsVision at load (see UnmarshalYAML).
	Vision bool `yaml:"vision"`
}

// UnmarshalYAML backfills Vision for legacy config files written before the
// field existed. A non-pointer bool can't distinguish an absent key from an
// explicit `vision: false`, so a small probe detects presence: when the key is
// missing, the heuristic (ModelSupportsVision) supplies the value — matching the
// runtime behaviour those files had before — and the next Save records it.
func (e *ModelEntry) UnmarshalYAML(node *yaml.Node) error {
	type plain ModelEntry
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	*e = ModelEntry(p)

	var probe struct {
		Vision *bool `yaml:"vision"`
	}
	if err := node.Decode(&probe); err != nil {
		return err
	}
	if probe.Vision == nil {
		e.Vision = ModelSupportsVision(e.Model)
	}
	return nil
}

// Config is the persisted set of CLI defaults. Every field is optional; a
// missing file (or a missing field) leaves the zero value, and the caller
// substitutes its built-in default.
type Config struct {
	// Models is the list of named model configurations.
	Models []ModelEntry `yaml:"models,omitempty"`
	// DefaultModel names the entry used when nothing else selects one.
	// Empty falls back to the first entry.
	DefaultModel string `yaml:"default_model,omitempty"`
	// LiteModel optionally names the entry used for cheap internal calls
	// (history compaction). Empty means no lite model.
	LiteModel string `yaml:"lite_model,omitempty"`

	PermissionMode string `yaml:"permission_mode,omitempty"`
	// Coauthor, when true, instructs the agent to append a Co-authored-by line
	// to every git commit message it writes. Default is true (enabled).
	Coauthor *bool `yaml:"coauthor,omitempty"`
	// AccessKey is the shared secret for Web UI / API authentication.
	// When empty, `octo serve` falls back to OCTO_ACCESS_KEY env var or
	// generates a random key on startup.
	AccessKey string `yaml:"access_key,omitempty"`
	// CompactAutoPct is the auto-compaction threshold as a percentage of the
	// model's context window (0–100). When CompactThreshold == 0, the agent
	// compacts once the context exceeds this share of the window. Zero means
	// the built-in default (75%).
	CompactAutoPct int `yaml:"compact_auto_pct,omitempty"`
	// CompactBatchThreshold controls compaction after a tool batch. Semantics
	// mirror CompactThreshold: <0 disables, 0 follows the between-turns trigger,
	// >0 is an explicit token count.
	CompactBatchThreshold int `yaml:"compact_batch_threshold,omitempty"`
	// Tools holds opt-in tooling behaviour (Tool Search for MCP, etc.). A
	// missing block leaves the built-in defaults.
	Tools ToolsConfig `yaml:"tools,omitempty"`
	// ShowReasoning is the global default for whether a reasoning model's
	// thinking trace is surfaced. Individual model entries can override this.
	// nil means the built-in default (enabled).
	ShowReasoning *bool `yaml:"show_reasoning,omitempty"`
	// Browser configures the built-in browser automation backend.
	Browser BrowserConfig `yaml:"browser,omitempty"`
	// Goal configures the session-goal feature (/goal and the goal tools).
	Goal GoalConfig `yaml:"goal,omitempty"`
	// WorkspaceDir sets the default working directory new web sessions are
	// created with. Empty (default) changes nothing: a session still falls
	// back to the server's own launch directory. "auto" resolves to
	// ~/Desktop/octo (see tools.ResolveWorkspaceDir); anything else is used
	// as a literal path. Does not affect CLI/TUI sessions or the server's
	// own cwd, and composes with the per-session working_dir override.
	WorkspaceDir string `yaml:"workspace_dir,omitempty"`
}

// GoalConfig configures session goals (persistent objectives the agent keeps
// pursuing across turns).
type GoalConfig struct {
	// Enabled gates the /goal surface and the get_goal/create_goal/
	// update_goal tools. nil means enabled — the feature is inert until a
	// goal is explicitly created, so there is no default-behavior change.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// GoalEnabled reports whether the session-goal feature is on (default true).
func (c *Config) GoalEnabled() bool {
	return c.Goal.Enabled == nil || *c.Goal.Enabled
}

// BrowserConfig configures how the browser tool connects to Chrome.
type BrowserConfig struct {
	// AttachRunning attaches to the user's already-running Chrome (discovered via
	// its default profile's DevToolsActivePort), reusing the logged-in session
	// without relaunching. Requires that Chrome was started with remote
	// debugging enabled.
	AttachRunning bool `yaml:"attach_running,omitempty"`
	// ConnectPort attaches to a Chrome already running with
	// --remote-debugging-port=<port> via the /json HTTP endpoint. Use when the
	// port is known and that endpoint is served.
	ConnectPort int `yaml:"connect_port,omitempty"`
	// UserDataDir is the Chrome profile dir used when launching (the fallback
	// when no running Chrome is attached). Empty uses a throwaway profile.
	UserDataDir string `yaml:"user_data_dir,omitempty"`
	// ExecPath overrides Chrome executable auto-detection.
	ExecPath string `yaml:"exec_path,omitempty"`
	// Headless launches Chrome headless. Interactive workflows want this off so
	// the user can watch and intervene.
	Headless bool `yaml:"headless,omitempty"`
	// DownloadDir is where captured downloads are written. Empty uses a temp dir.
	DownloadDir string `yaml:"download_dir,omitempty"`
}

// EffectiveShowReasoning resolves the effective show-reasoning flag for a
// model entry. The entry-level value overrides the global default; when both
// are unset the built-in default is enabled (true).
func (c Config) EffectiveShowReasoning(entry *bool) bool {
	if entry != nil {
		return *entry
	}
	if c.ShowReasoning != nil {
		return *c.ShowReasoning
	}
	return true
}

// ModelVision reports whether the named model accepts image content. When the
// model matches a configured entry, its recorded Vision value is authoritative
// (Load backfills legacy entries, so it is always set). A model not present in
// the config — e.g. a bare `octo --model X` — falls back to the heuristic.
func (c Config) ModelVision(model string) bool {
	for _, m := range c.Models {
		if m.Model == model {
			return m.Vision
		}
	}
	return ModelSupportsVision(model)
}

// ModelSupportsVision is a best-effort guess at whether a model id accepts
// image input, used when an entry doesn't set Vision explicitly so vision
// tracks the model automatically. It errs toward true (most frontier chat
// models are multimodal); only well-known text-only families return false. An
// explicit vision marker (e.g. qwen-vl) wins over its text-only family. The
// heuristic is necessarily incomplete — ModelEntry.Vision is the override.
func ModelSupportsVision(model string) bool {
	m := strings.ToLower(model)
	for _, mark := range []string{"-vl", "vl-", "vision", "-omni", "gpt-4o", "gpt-4.1", "claude-3", "claude-4", "claude-opus", "claude-sonnet", "claude-haiku", "gemini", "pixtral", "llava", "internvl"} {
		if strings.Contains(m, mark) {
			return true
		}
	}
	for _, textOnly := range []string{"qwen", "deepseek", "kimi", "moonshot", "baichuan", "ernie", "glm-", "spark", "abab", "yi-"} {
		if strings.Contains(m, textOnly) {
			return false
		}
	}
	return true
}

// ToolsConfig groups per-tool behaviour knobs under the `tools:` block.
type ToolsConfig struct {
	// ToolSearch defers MCP tool schemas behind a search/describe/call bridge.
	ToolSearch ToolSearchConfig `yaml:"tool_search,omitempty"`
	// DisabledSkills lists skill names the user has toggled off. Disabled skills
	// are hidden from the model (not injected into the system prompt) and from
	// the UI, but remain on disk.
	DisabledSkills []string `yaml:"disabled_skills,omitempty"`
}

// ToolSearchConfig mirrors the documented tools.tool_search block. Empty fields
// fall back to the tools-package defaults (auto / 10% / 5 / 20).
type ToolSearchConfig struct {
	// Enabled is "auto" (default), "on", or "off".
	Enabled string `yaml:"enabled,omitempty"`
	// ThresholdPct is the auto-mode activation threshold as a percent of the
	// model's context window.
	ThresholdPct int `yaml:"threshold_pct,omitempty"`
	// SearchDefaultLimit is how many hits tool_search returns by default.
	SearchDefaultLimit int `yaml:"search_default_limit,omitempty"`
	// MaxSearchLimit caps the caller-supplied limit.
	MaxSearchLimit int `yaml:"max_search_limit,omitempty"`
}

// DefaultEntry returns the entry whose model matches DefaultModel, falling
// back to the first entry, or a zero ModelEntry when none are configured.
func (c Config) DefaultEntry() ModelEntry {
	if e, ok := c.EntryByModel(c.DefaultModel); ok {
		return e
	}
	if len(c.Models) > 0 {
		return c.Models[0]
	}
	return ModelEntry{}
}

// EntryByModel returns the entry with the given model string. An empty model
// never matches.
func (c Config) EntryByModel(model string) (ModelEntry, bool) {
	if model == "" {
		return ModelEntry{}, false
	}
	for _, e := range c.Models {
		if e.Model == model {
			return e, true
		}
	}
	return ModelEntry{}, false
}

// SetDefaultEntry replaces the current default entry in place, or appends it
// when no entry exists yet, and points DefaultModel at it. The default entry is
// located the same way DefaultEntry resolves it — by DefaultModel, else the
// first entry — so it works even when that entry's model is empty (a floating
// default). Writers that previously mutated the top-level provider/model fields
// go through this.
func (c *Config) SetDefaultEntry(e ModelEntry) {
	idx := -1
	for i := range c.Models {
		if c.DefaultModel != "" && c.Models[i].Model == c.DefaultModel {
			idx = i
			break
		}
	}
	if idx == -1 && len(c.Models) > 0 {
		idx = 0 // DefaultEntry falls back to the first entry
	}
	if idx < 0 {
		c.Models = append(c.Models, e)
		c.DefaultModel = e.Model
		return
	}
	// Changing the default entry's model keeps the lite reference consistent.
	if c.LiteModel != "" && c.LiteModel == c.Models[idx].Model {
		c.LiteModel = e.Model
	}
	c.Models[idx] = e
	c.DefaultModel = e.Model
}

// Path returns the absolute path to the config file (~/.octo/config.yml).
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".octo", "config.yml"), nil
}

// legacyPath returns the pre-rename location (~/.octo/config.yaml).
func legacyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".octo", "config.yaml"), nil
}

// fileConfig is the on-disk superset: the current schema plus the legacy
// top-level model fields, so both file generations unmarshal through one
// struct and normalize identically.
type fileConfig struct {
	Config `yaml:",inline"`

	LegacyProvider        string `yaml:"provider,omitempty"`
	LegacyModel           string `yaml:"model,omitempty"`
	LegacyBaseURL         string `yaml:"base_url,omitempty"`
	LegacyAPIKey          string `yaml:"api_key,omitempty"`
	LegacyReasoningEffort string `yaml:"reasoning_effort,omitempty"`
	// LegacyShowReasoning is intentionally absent: the top-level show_reasoning
	// key is now read into Config.ShowReasoning (the global default). During
	// normalization it is also copied onto the single legacy model entry so
	// behaviour is preserved.
}

// normalize folds legacy top-level model fields into a one-entry Models list.
// Files already on the new schema pass through untouched; legacy fields are
// ignored once a models list exists.
func (f fileConfig) normalize() Config {
	c := f.Config
	if len(c.Models) > 0 {
		for i := range c.Models {
			migrateEntryProvider(&c.Models[i])
		}
		return c
	}
	if f.LegacyProvider == "" && f.LegacyModel == "" && f.LegacyBaseURL == "" && f.LegacyAPIKey == "" {
		return c
	}
	e := ModelEntry{
		Provider:        f.LegacyProvider,
		Model:           f.LegacyModel,
		BaseURL:         f.LegacyBaseURL,
		APIKey:          f.LegacyAPIKey,
		ReasoningEffort: f.LegacyReasoningEffort,
		ShowReasoning:   c.ShowReasoning,
		// The legacy schema predates the vision field; backfill from the id so
		// the first Save records it, mirroring the models-list migration path.
		Vision: ModelSupportsVision(f.LegacyModel),
	}
	migrateEntryProvider(&e)
	c.Models = []ModelEntry{e}
	c.DefaultModel = f.LegacyModel
	return c
}

// migrateEntryProvider folds the retired openai_compatible / anthropic_compatible
// catch-all vendors into the unified "custom" vendor, recovering the wire format
// into the new per-entry Protocol field so existing configs keep working.
func migrateEntryProvider(e *ModelEntry) {
	switch e.Provider {
	case "openai_compatible":
		e.Provider = "custom"
		if e.Protocol == "" {
			e.Protocol = "openai"
		}
	case "anthropic_compatible":
		e.Provider = "custom"
		if e.Protocol == "" {
			e.Protocol = "anthropic"
		}
	}
}

// Load reads ~/.octo/config.yml, falling back to the legacy
// ~/.octo/config.yaml. A missing file is not an error — it returns the zero
// Config so first-run callers need no special-casing. A present but malformed
// file IS an error, so a typo surfaces instead of silently reverting to
// defaults.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		legacy, lerr := legacyPath()
		if lerr != nil {
			return Config{}, lerr
		}
		data, err = os.ReadFile(legacy)
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
	}
	if err != nil {
		return Config{}, err
	}
	var f fileConfig
	if err := yaml.Unmarshal(data, &f); err != nil {
		return Config{}, err
	}
	return f.normalize(), nil
}

// Save writes the config to ~/.octo/config.yml with mode 0600 (it may hold
// API keys), creating ~/.octo if needed. A legacy config.yaml present at that
// moment is renamed to config.yaml.bak — best effort, because config.yml wins
// the read order regardless.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if legacy, lerr := legacyPath(); lerr == nil {
		if _, statErr := os.Stat(legacy); statErr == nil {
			_ = os.Rename(legacy, legacy+".bak")
		}
	}
	return nil
}
