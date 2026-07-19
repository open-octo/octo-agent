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
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// Endpoint is a user-configured channel: a bundle of models sharing one set
// of connection parameters (base_url + api_key + protocol). The two-level
// schema (endpoints → models) replaces the flat models list so the same model
// can be reached via multiple channels (e.g. an official vendor endpoint and
// a relay endpoint), which the flat list forbade ("Two entries may not share
// a model string", see ModelEntry). An Endpoint is referenced from Default /
// Lite / channel bindings via the composite id "<ID>::<model>".
type Endpoint struct {
	// ID is the unique identifier and the composite-id prefix. Must match
	// ^[a-zA-Z0-9_-]+$ and must not contain "::". Legacy entries migrated
	// from the flat schema get IDs of the form "legacy-<host>-<n>"; users
	// can rename them to anything friendly.
	ID string `yaml:"id"`
	// Name is the optional display name shown in the UI. May differ from ID.
	Name string `yaml:"name,omitempty"`
	// Provider is the vendor id ("anthropic" | "openai" | "custom" | ...).
	// Named vendors pin base_url/protocol via the registry; the "custom"
	// vendor requires an explicit BaseURL and Protocol.
	Provider string `yaml:"provider"`
	// BaseURL is the endpoint URL. Empty for named vendors uses the vendor
	// default; required for the "custom" vendor.
	BaseURL string `yaml:"base_url,omitempty"`
	// APIKey is a plaintext fallback used only if the provider's env var is
	// empty. Stored mode 0600. Prefer the env var.
	APIKey string `yaml:"api_key,omitempty"`
	// Protocol is the wire format ("anthropic" | "openai"). Only the
	// "custom" vendor needs it set explicitly; named vendors pin it.
	Protocol string `yaml:"protocol,omitempty"`
	// LiteModel optionally names a model in Models used as the lite model
	// for compaction. Empty falls back to vendor inference (see
	// ImplicitLiteModel).
	LiteModel string `yaml:"lite_model,omitempty"`
	// Models is the list of models offered through this endpoint. Each
	// carries its own Vision flag (model-level capability, not endpoint-level).
	Models []EndpointModel `yaml:"models"`
}

// EndpointModel is one model entry under an Endpoint.
type EndpointModel struct {
	// Model is the model id sent to the API (e.g. claude-sonnet-4-6).
	Model string `yaml:"model"`
	// Vision reports whether this model accepts image input. It is a
	// model-level capability: the same endpoint may expose a vision model
	// and a text-only model side by side.
	Vision bool `yaml:"vision"`
}

// CompositeID returns the "<ID>::<Model>" reference for this endpoint+model.
// Used by Default / Lite / channel bindings. The caller is responsible for
// ensuring the model exists under this endpoint.
func (e Endpoint) CompositeID(model string) string {
	return e.ID + "::" + model
}

// Config is the persisted set of CLI defaults. Every field is optional; a
// missing file (or a missing field) leaves the zero value, and the caller
// substitutes its built-in default.
type Config struct {
	// Endpoints is the list of user-configured channels (the new two-level
	// schema's authoritative field). Each bundles shared connection params
	// and a list of models. PR1 populates this from the legacy flat Models
	// list during normalize(); the write path switches to emitting this in
	// PR4. Until then Save still writes the flat Models form (Save elides
	// this field at marshal time) so existing callers and on-disk files
	// keep working, while Load still honours an explicit endpoints: block
	// if the user hand-writes one (design §4.1 step 1).
	Endpoints []Endpoint `yaml:"endpoints,omitempty"`
	// Default is the composite id "<endpoint_id>::<model>" selecting the
	// endpoint+model used when nothing else picks one. Empty falls back to
	// the first endpoint's first model (see ResolveDefault). Elided at
	// Save time in PR1; see Save.
	Default string `yaml:"default,omitempty"`
	// Lite is the composite id for the compaction lite model. Empty means
	// fall back to ImplicitLiteModel inference (endpoint.lite_model, else
	// the vendor registry's LiteModel for official endpoints). Elided at
	// Save time in PR1; see Save.
	Lite string `yaml:"lite,omitempty"`

	// Models is the legacy flat list of named model configurations. Kept
	// for read compatibility — normalize() populates it from Endpoints so
	// existing callers keep working. PR4 switches Save to write Endpoints
	// and stops emitting this.
	Models []ModelEntry `yaml:"models,omitempty"`
	// DefaultModel names the entry used when nothing else selects one.
	// Empty falls back to the first entry. Legacy read-compat only; the
	// authoritative field is Default above.
	DefaultModel string `yaml:"default_model,omitempty"`
	// LiteModel optionally names the entry used for cheap internal calls
	// (history compaction). Empty means no lite model. Legacy read-compat
	// only; the authoritative field is Lite above.
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
	// MemoryBackend optionally configures an external semantic memory
	// service (hindsight, mem0, or agentmemory). Empty Type means disabled — this
	// is separate from and does not affect the built-in MEMORY.md layer.
	MemoryBackend MemoryBackendConfig `yaml:"memory_backend,omitempty"`
	// WorkspaceDir sets the default working directory new web sessions are
	// created with. Empty (default) changes nothing: a session still falls
	// back to the server's own launch directory. "auto" resolves to
	// ~/Desktop/octo (see tools.ResolveWorkspaceDir); anything else is used
	// as a literal path. Does not affect CLI/TUI sessions or the server's
	// own cwd, and composes with the per-session working_dir override.
	WorkspaceDir string `yaml:"workspace_dir,omitempty"`
	// Language persists the user's UI language preference ("en" | "zh").
	// Empty means the default (English).
	Language string `yaml:"language,omitempty"`
	// Trash configures the file recycle bin (agent-issued deletes and
	// overwrites are staged there for recovery).
	Trash TrashConfig `yaml:"trash,omitempty"`
	// Notify controls whether the TUI sends a desktop notification when a turn
	// completes and is waiting for input (Ghostty / iTerm2 / Kitty forward the
	// OSC sequence to the OS; other terminals fall back to the terminal bell).
	// nil means the built-in default (enabled).
	Notify *bool `yaml:"notify,omitempty"`
	// TerminalTitle controls whether the TUI sets the terminal tab/window title
	// (via OSC 2) to the session name on startup. nil means the built-in default
	// (enabled).
	TerminalTitle *bool `yaml:"terminal_title,omitempty"`
}

// TrashConfig configures the file recycle bin.
type TrashConfig struct {
	// OverwriteBackup stages a copy of a file into the trash before write_file
	// or edit_file overwrites it, so an overwrite that destroys uncommitted
	// work is recoverable. Files that are tracked by git and clean are skipped
	// regardless — git already holds a recoverable copy. nil means the default
	// (enabled).
	OverwriteBackup *bool `yaml:"overwrite_backup,omitempty"`
	// RetentionDays ages out trash entries older than this at startup. 0 means
	// the default (14); a negative value disables the age-out.
	RetentionDays int `yaml:"retention_days,omitempty"`
	// MaxSizeMB caps the trash: once it exceeds this, the oldest entries are
	// evicted at startup until it's back under. 0 means the default (10240 =
	// 10 GiB); a negative value disables the cap.
	MaxSizeMB int `yaml:"max_size_mb,omitempty"`
}

// OverwriteBackupEnabled reports whether write/edit overwrites are staged into
// the trash first (default true).
func (c *Config) OverwriteBackupEnabled() bool {
	return c.Trash.OverwriteBackup == nil || *c.Trash.OverwriteBackup
}

// NotifyEnabled controls whether the TUI sends a desktop notification when a
// turn completes (default true).
func (c *Config) NotifyEnabled() bool {
	return c.Notify == nil || *c.Notify
}

// TerminalTitleEnabled controls whether the TUI sets the terminal tab/window
// title to the session name on startup (default true).
func (c *Config) TerminalTitleEnabled() bool {
	return c.TerminalTitle == nil || *c.TerminalTitle
}

// TrashRetention returns how long entries are kept before age-out (default 14
// days; a negative config value disables it, returning 0).
func (c *Config) TrashRetention() time.Duration {
	switch {
	case c.Trash.RetentionDays == 0:
		return 14 * 24 * time.Hour
	case c.Trash.RetentionDays < 0:
		return 0
	default:
		return time.Duration(c.Trash.RetentionDays) * 24 * time.Hour
	}
}

// TrashMaxBytes returns the trash size cap in bytes (default 10 GiB; a negative
// config value disables it, returning 0).
func (c *Config) TrashMaxBytes() int64 {
	switch {
	case c.Trash.MaxSizeMB == 0:
		return 10240 * 1024 * 1024
	case c.Trash.MaxSizeMB < 0:
		return 0
	default:
		return int64(c.Trash.MaxSizeMB) * 1024 * 1024
	}
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

// MemoryBackendConfig configures an optional external semantic memory
// backend. Exactly one of hindsight/mem0/agentmemory can be active at a time.
type MemoryBackendConfig struct {
	// Type selects the backend: "hindsight", "mem0", or "agentmemory". Empty
	// disables the feature.
	Type string `yaml:"type,omitempty"`
	// BaseURL is the backend's REST endpoint, e.g. http://localhost:8888.
	BaseURL string `yaml:"base_url,omitempty"`
	// APIKey is optional; required by some backends (mem0 by default),
	// optional for others (hindsight, agentmemory default to no auth).
	APIKey string `yaml:"api_key,omitempty"`
	// Namespace scopes stored/recalled memories (hindsight bank_id, mem0
	// user_id, agentmemory project). Defaults to "default" when empty.
	Namespace string `yaml:"namespace,omitempty"`
	// Mode selects between deployment variants of the same Type. Currently
	// only meaningful for "mem0": "cloud" talks to the hosted mem0 Platform
	// API instead of a self-hosted server — see the memory-backends guide.
	// Ignored by other backend types.
	Mode string `yaml:"mode,omitempty"`
	// AutoRecall, when true, automatically calls Recall with the user's
	// message before every turn and injects the result as context —
	// independent of the memory_recall tool, which stays available either
	// way. Off by default: it adds a bounded synchronous delay to every turn
	// once a backend is configured.
	AutoRecall bool `yaml:"auto_recall,omitempty"`
}

// MemoryBackendEnabled reports whether an external memory backend is
// configured.
func (c *Config) MemoryBackendEnabled() bool {
	return c.MemoryBackend.Type != ""
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
	// UserDataDir is the Chrome profile dir to attach to when AttachRunning is
	// set — its DevToolsActivePort is read to reach the logged-in session.
	UserDataDir string `yaml:"user_data_dir,omitempty"`
	// ExecPath overrides Chrome executable auto-detection (used to locate Chrome
	// for the availability probe).
	ExecPath string `yaml:"exec_path,omitempty"`
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

// EffectiveCoauthor resolves whether commits should get a Co-authored-by
// line: the OCTO_COAUTHOR env var when set, else the config value, else the
// built-in default (true). Mirrors cmd/octo's CLI-only resolveCoauthor
// (which layers a --no-coauthor flag ahead of this same precedence) so every
// caller — CLI or server — resolves the config/env layers identically.
func (c Config) EffectiveCoauthor() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OCTO_COAUTHOR"))) {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}
	if c.Coauthor != nil {
		return *c.Coauthor
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
	for _, textOnly := range []string{"qwen", "deepseek", "kimi", "moonshot", "baichuan", "ernie", "glm-", "spark", "abab", "yi-", "longcat"} {
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
// fall back to the tools-package defaults (auto / 10%).
type ToolSearchConfig struct {
	// Enabled is "auto" (default), "on", or "off".
	Enabled string `yaml:"enabled,omitempty"`
	// ThresholdPct is the auto-mode activation threshold as a percent of the
	// model's context window.
	ThresholdPct int `yaml:"threshold_pct,omitempty"`
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

// Validate reports semantic problems in a config that already parsed cleanly —
// issues Load tolerates by falling back silently (a mistyped default_model, a
// duplicated or half-filled entry) but which usually mean a hand edit went
// wrong. It returns a human-readable list; empty means the config is usable. A
// config with no models (the valid pre-onboarding state) reports nothing.
//
// PR1 layers endpoint-level checks on top of the legacy Models checks (design
// §14.1): endpoint id uniqueness/legality, each endpoint has at least one
// model, model names non-empty, no duplicate models within one endpoint, and
// Default/Lite composite ids resolve.
//
// To avoid double-reporting on a migrated flat config (where Endpoints is
// synthesised from Models and the same duplicate would surface in both
// layers), the endpoint-level checks only run when the config is on the pure
// new schema — i.e. Endpoints is non-empty AND Models is empty (a hand-written
// endpoints: block with no models: block, the shape §4.1 step 1 recognises as
// "user is already on the new schema"). A flat config (Models non-empty,
// Endpoints synthesised in memory) gets only the legacy Models checks, so
// hand-edited flat files keep getting exactly the validation they always did.
// PR4, which switches Save to emit Endpoints and drops the Models write path,
// will lift this guard so endpoint-level checks always run.
func (c Config) Validate() []string {
	var problems []string

	// Endpoint-level checks — only on the pure new schema (Endpoints set,
	// Models empty). See the doc comment for why.
	if len(c.Endpoints) > 0 && len(c.Models) == 0 {
		seenEndpointID := make(map[string]bool, len(c.Endpoints))
		for i, ep := range c.Endpoints {
			if strings.TrimSpace(ep.ID) == "" {
				problems = append(problems, fmt.Sprintf("endpoints[%d] has no id", i))
			} else if !isValidEndpointID(ep.ID) {
				problems = append(problems, fmt.Sprintf("endpoint id %q contains illegal chars (allowed: a-z A-Z 0-9 _ -, and no \"::\")", ep.ID))
			}
			if seenEndpointID[ep.ID] {
				problems = append(problems, fmt.Sprintf("duplicate endpoint id %q", ep.ID))
			}
			seenEndpointID[ep.ID] = true

			if len(ep.Models) == 0 {
				problems = append(problems, fmt.Sprintf("endpoint %q has no models", ep.ID))
			}
			seenModel := make(map[string]bool, len(ep.Models))
			for j, m := range ep.Models {
				if strings.TrimSpace(m.Model) == "" {
					problems = append(problems, fmt.Sprintf("endpoint %q models[%d] has no model name", ep.ID, j))
					continue
				}
				if seenModel[m.Model] {
					problems = append(problems, fmt.Sprintf("duplicate model %q in endpoint %q", m.Model, ep.ID))
				}
				seenModel[m.Model] = true
			}
		}

		// Default/Lite composite-id resolution.
		if c.Default != "" {
			if _, _, ok := c.resolveCompositeID(c.Default); !ok {
				problems = append(problems, fmt.Sprintf("default %q does not resolve to any endpoint+model", c.Default))
			}
		}
		if c.Lite != "" {
			if _, _, ok := c.resolveCompositeID(c.Lite); !ok {
				problems = append(problems, fmt.Sprintf("lite %q does not resolve to any endpoint+model", c.Lite))
			}
		}
	}

	// Legacy flat Models checks.
	if len(c.Models) == 0 {
		return problems
	}
	seen := make(map[string]bool, len(c.Models))
	for i, m := range c.Models {
		if strings.TrimSpace(m.Model) == "" {
			problems = append(problems, fmt.Sprintf("models[%d] has no model name", i))
			continue
		}
		if strings.TrimSpace(m.Provider) == "" {
			problems = append(problems, fmt.Sprintf("model %q has no provider", m.Model))
		}
		if seen[m.Model] {
			problems = append(problems, fmt.Sprintf("duplicate model %q (only the first entry is used)", m.Model))
		}
		seen[m.Model] = true
	}
	if c.DefaultModel != "" && !seen[c.DefaultModel] {
		problems = append(problems, fmt.Sprintf("default_model %q matches no entry (the first model is used instead)", c.DefaultModel))
	}
	if c.LiteModel != "" && !seen[c.LiteModel] {
		problems = append(problems, fmt.Sprintf("lite_model %q matches no entry", c.LiteModel))
	}
	return problems
}

// isValidEndpointID reports whether id matches the endpoint id regex
// ^[a-zA-Z0-9_-]+$ and does not contain the composite-id separator "::". The
// "::" check is redundant with the regex (":" isn't in the allowed set) but
// spelled out for clarity — a future regex relaxation must still reject "::".
func isValidEndpointID(id string) bool {
	if id == "" || strings.Contains(id, "::") {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// resolveCompositeID parses "<endpoint_id>::<model>" and reports whether both
// the endpoint and the model under it exist. Used by Validate to check Default
// and Lite without coupling to ParseModelFlag's error messages (Validate
// produces its own "does not resolve" wording).
func (c Config) resolveCompositeID(id string) (Endpoint, EndpointModel, bool) {
	endpointID, model, ok := splitCompositeID(id)
	if !ok {
		return Endpoint{}, EndpointModel{}, false
	}
	for _, ep := range c.Endpoints {
		if ep.ID != endpointID {
			continue
		}
		for _, m := range ep.Models {
			if m.Model == model {
				return ep, m, true
			}
		}
	}
	return Endpoint{}, EndpointModel{}, false
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

// SetEntry replaces the entry matching e.Model in place. Unlike
// SetDefaultEntry, it never appends a new entry and never touches
// DefaultModel/LiteModel — it's for updating an already-configured entry by
// name (e.g. a per-session setting change that should land on the specific
// model that session runs, not necessarily the default one). Reports whether
// a matching entry was found.
func (c *Config) SetEntry(e ModelEntry) bool {
	for i := range c.Models {
		if c.Models[i].Model == e.Model {
			c.Models[i] = e
			return true
		}
	}
	return false
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
//
// Precedence (design §4.1): if the file carries an explicit endpoints: block
// the user is already on the new schema and we honour it as-is — rebuilding
// from Models would silently drop their endpoints: edits. Otherwise we
// synthesise Endpoints from the flat Models list so new code can read the
// two-level shape while existing callers keep reading Models.
func (f fileConfig) normalize() Config {
	c := f.Config
	if len(c.Endpoints) > 0 {
		// Already on the new schema — leave Endpoints/Default/Lite as the
		// authoritative shape. Migrate any provider aliases on Models too,
		// for callers that still read the flat list.
		for i := range c.Models {
			migrateEntryProvider(&c.Models[i])
		}
		return c
	}
	if len(c.Models) > 0 {
		for i := range c.Models {
			migrateEntryProvider(&c.Models[i])
		}
		syncEndpointsFromModels(&c)
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
	syncEndpointsFromModels(&c)
	return c
}

// syncEndpointsFromModels populates c.Endpoints / c.Default / c.Lite from the
// legacy flat c.Models / c.DefaultModel / c.LiteModel. It runs after the flat
// list is finalised so every Load leaves both shapes in sync: existing callers
// keep reading c.Models (PR1 doesn't switch the write path), while new code
// reads c.Endpoints.
//
// Aggregation is by (provider, base_url): entries sharing the same provider
// and base_url collapse into one endpoint (that bundle is exactly what an
// endpoint represents — shared connection params). Each migrated endpoint gets
// a stable implicit ID "legacy-<host>-<n>" so repeated loads produce the same
// ID; the user can rename it later. api_key/protocol are taken from the first
// entry of each group; if a later entry in the same group carried a different
// key, it's dropped with a slog.Warn so the loss isn't silent.
//
// Default/Lite (composite ids) are mapped from the legacy DefaultModel/LiteModel
// (bare model strings) by locating the first endpoint whose Models contains
// the model.
func syncEndpointsFromModels(c *Config) {
	c.Endpoints = nil
	c.Default = ""
	c.Lite = ""
	if len(c.Models) == 0 {
		return
	}

	type group struct {
		provider string
		baseURL  string
		entries  []ModelEntry
	}
	var groups []group
	groupIdx := map[string]int{} // key: provider+"\x00"+baseURL → index in groups
	for _, e := range c.Models {
		key := e.Provider + "\x00" + e.BaseURL
		if idx, ok := groupIdx[key]; ok {
			groups[idx].entries = append(groups[idx].entries, e)
			continue
		}
		groupIdx[key] = len(groups)
		groups = append(groups, group{provider: e.Provider, baseURL: e.BaseURL, entries: []ModelEntry{e}})
	}

	// Per-baseURL counter for the legacy-<host>-<n> suffix so two endpoints
	// sharing a host (e.g. regional variants) get distinct IDs.
	hostCounter := map[string]int{}
	for _, g := range groups {
		host := hostFromBaseURL(g.baseURL)
		n := hostCounter[host]
		hostCounter[host] = n + 1
		ep := Endpoint{
			ID:       legacyEndpointID(host, n),
			Provider: g.provider,
			BaseURL:  g.baseURL,
		}
		for i, e := range g.entries {
			if i == 0 {
				ep.APIKey = e.APIKey
				ep.Protocol = e.Protocol
			} else if e.APIKey != "" && e.APIKey != g.entries[0].APIKey {
				slog.Warn("config: multiple api_keys found for same base_url, keeping the first",
					"base_url", g.baseURL, "dropped_key", e.APIKey[:min(8, len(e.APIKey))]+"…")
			}
			ep.Models = append(ep.Models, EndpointModel{Model: e.Model, Vision: e.Vision})
		}
		c.Endpoints = append(c.Endpoints, ep)
	}

	if c.DefaultModel != "" {
		if ep, m, ok := findEndpointModel(c.Endpoints, c.DefaultModel); ok {
			c.Default = ep.CompositeID(m.Model)
		}
	}
	if c.LiteModel != "" {
		if ep, m, ok := findEndpointModel(c.Endpoints, c.LiteModel); ok {
			c.Lite = ep.CompositeID(m.Model)
		}
	}
}

// findEndpointModel returns the first endpoint whose Models contains the given
// bare model id, plus the matching EndpointModel. Used to map legacy bare-model
// references (DefaultModel / LiteModel) to composite ids during normalisation.
func findEndpointModel(endpoints []Endpoint, model string) (Endpoint, EndpointModel, bool) {
	for _, ep := range endpoints {
		for _, m := range ep.Models {
			if m.Model == model {
				return ep, m, true
			}
		}
	}
	return Endpoint{}, EndpointModel{}, false
}

// hostFromBaseURL extracts the URL host (lowercased) for use in a
// legacy-<host>-<n> endpoint ID. Hosts are case-insensitive per RFC 3986
// (url.Parse normalises the scheme but not the host), so lowercasing gives a
// stable id regardless of how the user cased the base_url in the YAML —
// design §4.4 requires the implicit id to be stable across repeated
// Load→Save cycles, and a case change in the file must not rotate it. On a
// parse failure it falls back to the raw string lowercased so a malformed
// base_url still yields a stable (if ugly) ID rather than aborting
// normalisation.
func hostFromBaseURL(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return strings.ToLower(baseURL)
	}
	return strings.ToLower(u.Host)
}

// legacyEndpointID builds the "legacy-<host>-<n>" ID for an implicit endpoint
// migrated from the flat schema. Non-alphanumeric characters in the host are
// replaced with "-" so the result matches the endpoint ID regex
// ^[a-zA-Z0-9_-]+$.
func legacyEndpointID(host string, n int) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, host)
	return fmt.Sprintf("legacy-%s-%d", safe, n)
}

// ResolveDefault returns the (endpoint, model) pair the session should run on
// when nothing else selects one. It implements the four-step fallback chain
// from the design doc (§5.3):
//
//  1. Default parses to (endpointID, model) and both resolve → that pair.
//  2. Default's endpoint resolves but the model doesn't (e.g. the relay
//     dropped it) → keep the endpoint, fall back to its first model.
//  3. Default's endpoint doesn't exist (e.g. user deleted it) → fall back
//     to the first endpoint's first model.
//  4. No endpoints at all → zero values, ok=false (caller surfaces a
//     "please configure" error).
//
// An empty Default is the normal fresh-install state, not a fallback: step 3
// applies silently (no warn) so a brand-new config doesn't log noise on every
// turn. Every other step that falls back logs a slog.Warn with the reason so
// the user can see why their turns aren't running on the model they expected.
//
// The (endpoint, model, ok) shape mirrors DefaultEntry/EntryByModel: callers
// that only need a ModelEntry for the legacy sender-building path can read
// the returned Endpoint and EndpointModel directly.
func (c Config) ResolveDefault() (Endpoint, EndpointModel, bool) {
	if len(c.Endpoints) == 0 {
		return Endpoint{}, EndpointModel{}, false
	}

	// Parse Default into (endpointID, model). An empty Default or one missing
	// the separator is treated as "no default set" — step 3 applies silently.
	endpointID, model, hasDefault := splitCompositeID(c.Default)

	if hasDefault {
		// Step 1 + 2: find the endpoint Default points at.
		for i := range c.Endpoints {
			if c.Endpoints[i].ID != endpointID {
				continue
			}
			ep := &c.Endpoints[i]
			if len(ep.Models) == 0 {
				// Step 2 dead-end: endpoint exists but is empty. Fall through
				// to step 3 (first non-empty endpoint), with a warn that the
				// targeted endpoint was empty.
				slog.Warn("config: default model resolution fell back",
					"default", c.Default, "reason", "empty_endpoint",
					"endpoint_id", endpointID)
				if first := firstNonEmptyEndpoint(c.Endpoints); first != nil {
					return *first, first.Models[0], true
				}
				return Endpoint{}, EndpointModel{}, false
			}
			// Step 1: exact model hit.
			for j := range ep.Models {
				if ep.Models[j].Model == model {
					return *ep, ep.Models[j], true
				}
			}
			// Step 2: endpoint found, model not found — fall back to the
			// endpoint's first model.
			slog.Warn("config: default model resolution fell back",
				"default", c.Default, "resolved_to", ep.CompositeID(ep.Models[0].Model),
				"reason", "model_not_found")
			return *ep, ep.Models[0], true
		}

		// Step 3: Default's endpoint doesn't exist.
		if first := firstNonEmptyEndpoint(c.Endpoints); first != nil {
			slog.Warn("config: default model resolution fell back",
				"default", c.Default,
				"resolved_to", first.CompositeID(first.Models[0].Model),
				"reason", "endpoint_not_found")
			return *first, first.Models[0], true
		}
		return Endpoint{}, EndpointModel{}, false
	}

	// Empty Default — fresh install, not a fallback. Step 3 applies silently.
	if first := firstNonEmptyEndpoint(c.Endpoints); first != nil {
		return *first, first.Models[0], true
	}
	return Endpoint{}, EndpointModel{}, false
}

// splitCompositeID splits "<endpoint_id>::<model>" into its parts. hasDefault
// is false when the input is empty or doesn't contain the "::" separator —
// both mean "no default set" to ResolveDefault. A composite id with an empty
// endpoint id or empty model (e.g. "::model" or "ep::") is treated as set
// (hasDefault true) so ResolveDefault can report the specific failure rather
// than silently ignoring it.
func splitCompositeID(id string) (endpointID, model string, hasDefault bool) {
	if id == "" {
		return "", "", false
	}
	idx := strings.Index(id, "::")
	if idx < 0 {
		// Bare model with no endpoint prefix — not a valid composite id. Treat
		// as "no default set" so the caller falls back rather than reporting a
		// parse error mid-turn.
		return "", "", false
	}
	return id[:idx], id[idx+2:], true
}

// firstNonEmptyEndpoint returns the first endpoint in the slice that has at
// least one model, or nil if none do. An endpoint with zero models is
// unusable (nothing to run), so the fallback chain skips them.
func firstNonEmptyEndpoint(endpoints []Endpoint) *Endpoint {
	for i := range endpoints {
		if len(endpoints[i].Models) > 0 {
			return &endpoints[i]
		}
	}
	return nil
}

// ParseModelFlag resolves a --model flag value (or env *_MODEL value) to the
// (endpoint, model) pair it selects. It accepts two forms:
//
//   - Composite id "<endpoint_id>::<model>" — resolves to exactly that pair.
//     An unknown endpoint id or unknown model under a known endpoint is an
//     error naming the missing part and listing the available ones.
//   - Bare model "<model>" — resolves via the precedence in S5.4:
//     1. If the Default endpoint's model matches, use the Default endpoint
//        (Default is "my main endpoint", a bare name prefers it).
//     2. Otherwise scan all endpoints; a unique hit returns it.
//     3. Multiple hits return the first match and slog.Warn naming the
//        picked endpoint so the user can disambiguate with a composite id.
//     4. No hit is an error listing the available models.
//
// An empty flag is an error — callers should treat empty as "no --model given"
// and fall back to ResolveDefault rather than calling ParseModelFlag.
func (c Config) ParseModelFlag(flag string) (Endpoint, EndpointModel, error) {
	if flag == "" {
		return Endpoint{}, EndpointModel{}, fmt.Errorf("model flag is empty")
	}

	// Composite-id path.
	if endpointID, model, ok := splitCompositeID(flag); ok {
		for i := range c.Endpoints {
			if c.Endpoints[i].ID != endpointID {
				continue
			}
			ep := &c.Endpoints[i]
			for j := range ep.Models {
				if ep.Models[j].Model == model {
					return *ep, ep.Models[j], nil
				}
			}
			available := availableModelsInEndpoint(ep)
			return Endpoint{}, EndpointModel{}, fmt.Errorf("model %q not found in endpoint %q (available: %s)", model, endpointID, available)
		}
		available := availableEndpoints(c.Endpoints)
		return Endpoint{}, EndpointModel{}, fmt.Errorf("endpoint %q not found (available: %s)", endpointID, available)
	}

	// Bare-model path.
	// Step 2a: prefer the Default endpoint if its model matches.
	if defEp, defM, ok := c.ResolveDefault(); ok {
		if defM.Model == flag {
			return defEp, defM, nil
		}
	}
	// Step 2b: scan all endpoints for a match.
	var hits []endpointModelIndex
	for i := range c.Endpoints {
		for j := range c.Endpoints[i].Models {
			if c.Endpoints[i].Models[j].Model == flag {
				hits = append(hits, endpointModelIndex{ep: i, m: j})
			}
		}
	}
	switch len(hits) {
	case 0:
		available := availableModelsAcrossEndpoints(c.Endpoints)
		return Endpoint{}, EndpointModel{}, fmt.Errorf("model %q not found in any endpoint (available: %s)", flag, available)
	case 1:
		h := hits[0]
		return c.Endpoints[h.ep], c.Endpoints[h.ep].Models[h.m], nil
	default:
		h := hits[0]
		picked := c.Endpoints[h.ep]
		pickedModel := picked.Models[h.m]
		// Warn so the user knows the bare flag was ambiguous and can switch
		// to a composite id to pin a specific endpoint.
		slog.Warn("config: model flag matches multiple endpoints, using the first match",
			"flag", flag, "picked_endpoint", picked.ID,
			"hint", fmt.Sprintf("use %s::%s to disambiguate", picked.ID, pickedModel.Model))
		return picked, pickedModel, nil
	}
}

// endpointModelIndex pairs an endpoint index with a model index inside it.
type endpointModelIndex struct{ ep, m int }

// availableEndpoints returns a comma-joined list of endpoint ids for use in
// error messages.
func availableEndpoints(endpoints []Endpoint) string {
	ids := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		ids = append(ids, ep.ID)
	}
	return strings.Join(ids, ", ")
}

// availableModelsInEndpoint returns a comma-joined list of model ids in one
// endpoint for use in error messages.
func availableModelsInEndpoint(ep *Endpoint) string {
	models := make([]string, 0, len(ep.Models))
	for _, m := range ep.Models {
		models = append(models, m.Model)
	}
	return strings.Join(models, ", ")
}

// availableModelsAcrossEndpoints returns a comma-joined list of every model
// across every endpoint for use in error messages. Duplicates (same model on
// multiple endpoints) are kept so the user sees the full picture.
func availableModelsAcrossEndpoints(endpoints []Endpoint) string {
	var models []string
	for _, ep := range endpoints {
		for _, m := range ep.Models {
			models = append(models, m.Model)
		}
	}
	return strings.Join(models, ", ")
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

// lastGood caches, per resolved config path, the most recently successfully
// loaded Config for LoadCached — see its doc comment. Keyed by path (like
// permission's cachedUserRules) rather than a single global value so tests
// using different HOME dirs in the same binary can't leak cached state into
// each other.
var lastGood struct {
	mu     sync.Mutex
	byPath map[string]Config
}

// LoadCached is Load with a validate-then-replace cache: octo serve re-reads
// config.yml on every turn (so changes apply without a restart), but with no
// cache a hand-edit that leaves invalid YAML mid-session would revert every
// in-flight, config-derived feature (per-session model binding, the
// compaction lite model, the coauthor flag, browser vision) to its hardcoded
// default the instant the bad file is saved — a config typo turning into a
// live behavior change across every session. LoadCached instead keeps
// serving the last config that parsed cleanly until the file is fixed,
// logging a warning so the failure isn't entirely invisible. Only the very
// first call, before anything has loaded successfully, surfaces the raw
// error, matching Load's own contract for that case.
//
// Callers that need the file's actual current state (validation UI, `octo
// config show`) should call Load directly — LoadCached silently swallows a
// load error into the cached value once one exists.
func LoadCached() (Config, error) {
	cfg, err := Load()
	path, pathErr := Path() // cache key; a Path() failure (no home dir) just skips caching

	lastGood.mu.Lock()
	defer lastGood.mu.Unlock()

	if err == nil {
		if pathErr == nil {
			if lastGood.byPath == nil {
				lastGood.byPath = make(map[string]Config)
			}
			lastGood.byPath[path] = cfg
		}
		return cfg, nil
	}
	if pathErr == nil {
		if cached, ok := lastGood.byPath[path]; ok {
			slog.Warn("config: config.yml failed to load, using last known good config until it's fixed", "path", path, "err", err)
			return cached, nil
		}
	}
	return Config{}, err
}

// resetLastGoodForTest clears LoadCached's cache (tests only).
func resetLastGoodForTest() {
	lastGood.mu.Lock()
	defer lastGood.mu.Unlock()
	lastGood.byPath = nil
}

// Save writes the config to ~/.octo/config.yml with mode 0600 (it may hold
// API keys), creating ~/.octo if needed. A legacy config.yaml present at that
// moment is renamed to config.yaml.bak — best effort, because config.yml wins
// the read order regardless.
// Save writes the config to ~/.octo/config.yml with mode 0600 (it may hold
// API keys), creating ~/.octo if needed. A legacy config.yaml present at that
// moment is renamed to config.yaml.bak — best effort, because config.yml wins
// the read order regardless.
//
// PR1 invariant (design §4.3): Save must write the legacy flat form
// (models: + default_model: + lite_model:), NOT the new endpoints: form.
// The new Endpoints/Default/Lite fields are in-memory only during PR1-3 so
// existing callers and downgrade paths keep working. PR4 will flip this to
// emit endpoints: instead. To enforce that here without changing the field
// tags (Load still needs to honour an explicit endpoints: block the user
// hand-writes, per §4.1 step 1), Save marshals a shallow copy with the new
// fields cleared.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Shallow copy with the new-schema fields cleared so yaml.Marshal emits
	// only the legacy flat form. The Models slice is shared by reference,
	// which is fine — we don't mutate it.
	saved := c
	saved.Endpoints = nil
	saved.Default = ""
	saved.Lite = ""
	data, err := yaml.Marshal(saved)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	// Rolling backup of the last config we wrote (always valid, since it's a
	// marshaled Config): `octo config --fix` restores from it when a later hand
	// edit leaves config.yml unparseable. Best-effort — a backup failure must
	// never fail the save itself.
	_ = os.WriteFile(path+".bak", data, 0o600)
	if legacy, lerr := legacyPath(); lerr == nil {
		if _, statErr := os.Stat(legacy); statErr == nil {
			_ = os.Rename(legacy, legacy+".bak")
		}
	}
	return nil
}

// BackupPath returns the rolling-backup path (config.yml.bak) that Save writes
// and `octo config --fix` restores from.
func BackupPath() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	return p + ".bak", nil
}

// RestoreFromBackup replaces config.yml with config.yml.bak, used by
// `octo config --fix` when the current file no longer parses. It only proceeds
// when the backup itself parses cleanly, and first preserves the current
// (broken) file as config.yml.broken so the mangled edit isn't lost. It returns
// the path the broken file was saved to (empty if there was nothing to save),
// or an error when there is no usable backup.
func RestoreFromBackup() (brokenSaved string, err error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path + ".bak")
	if err != nil {
		return "", fmt.Errorf("no backup at %s: %w", path+".bak", err)
	}
	var f fileConfig
	if err := yaml.Unmarshal(data, &f); err != nil {
		return "", fmt.Errorf("the backup %s is also invalid: %w", path+".bak", err)
	}
	// Preserve the current (broken) file as config.yml.broken BEFORE overwriting
	// it — it holds the user's edits and is otherwise the only copy. If that
	// preservation fails, abort rather than silently destroy it: a failed
	// restore the user can retry beats one that loses their file.
	if cur, rerr := os.ReadFile(path); rerr == nil {
		broken := path + ".broken"
		if werr := os.WriteFile(broken, cur, 0o600); werr != nil {
			return "", fmt.Errorf("refusing to overwrite %s: could not preserve it as %s first: %w", path, broken, werr)
		}
		brokenSaved = broken
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return brokenSaved, nil
}

// Repair auto-fixes the semantic problems Validate reports that have a safe,
// unambiguous resolution, returning the repaired config plus a list of what it
// changed and what still needs a human. Safe fixes: a dangling default_model is
// reset to the first entry; a dangling lite_model is cleared. A duplicate model
// or a half-filled entry is reported as unfixable — the intended value can't be
// guessed, so it's left for the user.
//
// PR1 layers endpoint-level auto-fixes on top of the legacy Models fixes
// (design §14.2): dangling Default resets to the first endpoint's first
// model; dangling Lite clears; an empty endpoint (no models) is deleted; Lite
// == Default clears Lite. Duplicate endpoint id, illegal id chars, missing
// model name, and duplicate model within one endpoint are unfixable (can't
// guess the intended value). Like Validate, the endpoint-level branch only
// runs on the pure new schema (Endpoints set, Models empty) to avoid
// double-acting on a migrated flat config; PR4 lifts that guard.
func (c Config) Repair() (repaired Config, fixed, unfixable []string) {
	repaired = c

	// Endpoint-level repair — only on the pure new schema (Endpoints set,
	// Models empty). See the doc comment for why.
	if len(repaired.Endpoints) > 0 && len(repaired.Models) == 0 {
		// Scan for unfixable problems first so the auto-fixes below don't
		// act on garbage (e.g. an illegal id can't be safely renamed, so
		// the user must fix it before any auto-reset of Default makes
		// sense).
		seenID := make(map[string]bool, len(repaired.Endpoints))
		for i, ep := range repaired.Endpoints {
			if strings.TrimSpace(ep.ID) == "" {
				unfixable = append(unfixable, fmt.Sprintf("endpoints[%d] has no id — add one", i))
			} else if !isValidEndpointID(ep.ID) {
				unfixable = append(unfixable, fmt.Sprintf("endpoint id %q contains illegal chars — rename", ep.ID))
			}
			if seenID[ep.ID] {
				unfixable = append(unfixable, fmt.Sprintf("duplicate endpoint id %q — rename one", ep.ID))
			}
			seenID[ep.ID] = true

			seenModel := make(map[string]bool, len(ep.Models))
			for j, m := range ep.Models {
				if strings.TrimSpace(m.Model) == "" {
					unfixable = append(unfixable, fmt.Sprintf("endpoint %q models[%d] has no model name — add one or remove it", ep.ID, j))
					continue
				}
				if seenModel[m.Model] {
					unfixable = append(unfixable, fmt.Sprintf("duplicate model %q in endpoint %q — remove one", m.Model, ep.ID))
				}
				seenModel[m.Model] = true
			}
		}

		// Auto-fixes (safe, no guessing).

		// Delete empty endpoints (no models). These are unusable and a
		// dangling-Default reset below would have nothing to point at if all
		// endpoints were empty.
		var kept []Endpoint
		for _, ep := range repaired.Endpoints {
			if len(ep.Models) == 0 {
				fixed = append(fixed, fmt.Sprintf("deleted empty endpoint %q (had no models)", ep.ID))
				continue
			}
			kept = append(kept, ep)
		}
		repaired.Endpoints = kept

		// Find the first non-empty endpoint + its first model — the reset
		// target for a dangling Default. If there are none, leave Default
		// for the user (all-empty config is the pre-onboarding state).
		var firstEpID, firstModel string
		for _, ep := range repaired.Endpoints {
			if len(ep.Models) > 0 {
				firstEpID = ep.ID
				firstModel = ep.Models[0].Model
				break
			}
		}
		if repaired.Default != "" {
			if _, _, ok := repaired.resolveCompositeID(repaired.Default); !ok {
				if firstEpID != "" {
					newDefault := firstEpID + "::" + firstModel
					fixed = append(fixed, fmt.Sprintf("reset default %q → %q (first endpoint's first model)", repaired.Default, newDefault))
					repaired.Default = newDefault
				} else {
					fixed = append(fixed, fmt.Sprintf("cleared default %q (no endpoints with models to fall back to)", repaired.Default))
					repaired.Default = ""
				}
			}
		}
		if repaired.Lite != "" {
			// Lite dangles OR Lite == Default → clear. The "==" check is on
			// the resolved composite id, so a Lite that points at the same
			// endpoint+model as Default is caught even if the strings differ
			// in casing or whitespace (they won't for composite ids, but the
			// resolved form is the canonical one).
			_, _, liteOK := repaired.resolveCompositeID(repaired.Lite)
			if !liteOK || repaired.Lite == repaired.Default {
				fixed = append(fixed, fmt.Sprintf("cleared lite %q (matched no endpoint+model or equals default)", repaired.Lite))
				repaired.Lite = ""
			}
		}

		return repaired, fixed, unfixable
	}

	// Legacy flat Models repair.
	if len(repaired.Models) == 0 {
		return repaired, nil, nil
	}
	seen := make(map[string]bool, len(repaired.Models))
	var firstNamed string // first entry with a usable model name — the reset target
	for i, m := range repaired.Models {
		if strings.TrimSpace(m.Model) == "" {
			unfixable = append(unfixable, fmt.Sprintf("models[%d] has no model name — add one or remove the entry", i))
			continue
		}
		if firstNamed == "" {
			firstNamed = m.Model
		}
		if strings.TrimSpace(m.Provider) == "" {
			unfixable = append(unfixable, fmt.Sprintf("model %q has no provider — add one", m.Model))
		}
		if seen[m.Model] {
			unfixable = append(unfixable, fmt.Sprintf("duplicate model %q — remove the extra entry", m.Model))
		}
		seen[m.Model] = true
	}
	// Reset a dangling default_model to the first usable entry. If no entry has a
	// name there's nothing safe to point it at, so leave it for the user (the
	// nameless entries are already reported as unfixable above).
	if repaired.DefaultModel != "" && !seen[repaired.DefaultModel] && firstNamed != "" {
		fixed = append(fixed, fmt.Sprintf("reset default_model %q → %q (first configured model)", repaired.DefaultModel, firstNamed))
		repaired.DefaultModel = firstNamed
	}
	if repaired.LiteModel != "" && !seen[repaired.LiteModel] {
		fixed = append(fixed, fmt.Sprintf("cleared lite_model %q (matched no entry)", repaired.LiteModel))
		repaired.LiteModel = ""
	}
	return repaired, fixed, unfixable
}
