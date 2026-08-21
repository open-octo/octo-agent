package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/mcp"
	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/prompt"
	"github.com/open-octo/octo-agent/internal/server"
	"github.com/open-octo/octo-agent/internal/skills"
	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/internal/version"
	"golang.org/x/term"
)

// Provider names accepted by `--provider`.
const (
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
)

// soulMissing reports whether the user has no identity profile yet
// (~/.octo/soul.md) — the signal that onboarding hasn't run.
func soulMissing() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	_, statErr := os.Stat(prompt.IdentityPath(filepath.Join(home, ".octo"), "soul.md"))
	return os.IsNotExist(statErr)
}

// identityMissing reports whether the user has neither soul.md nor user.md
// (nor their legacy uppercase spellings) — the signal that they have no
// identity at all. Onboarding nudges only in that case; if either file exists
// the user has some identity set up.
func identityMissing() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	dir := filepath.Join(home, ".octo")
	for _, name := range []string{"soul.md", "user.md"} {
		if _, err := os.Stat(prompt.IdentityPath(dir, name)); err == nil {
			return false
		}
	}
	return true
}

// onboardAttempted reports whether the soul_setup auto-nudge has already fired
// once (see config.OnboardAttempted, backed by ~/.octo/.onboard_attempted). A
// missing marker is treated as "not attempted yet" — the nudge firing once
// more is harmless, while silently skipping it forever would not be.
func onboardAttempted() bool {
	return config.OnboardAttempted()
}

// markOnboardAttempted persists that the soul_setup auto-nudge fired, before
// the TUI takes over — so even if the user interrupts /onboard immediately,
// the marker is already on disk and it won't retrigger on the next startup.
func markOnboardAttempted() {
	_ = config.MarkOnboardAttempted()
}

// shouldAutoOnboard reports whether a fresh CLI/TUI run should auto-launch the
// /onboard ceremony — first-ever session, no identity yet, and the nudge hasn't
// fired. Deciding "yes" writes the one-shot marker as a SIDE EFFECT, on
// purpose: the two are bound in one place so they can never drift apart. That
// drift is exactly the #1660 bug — the web's FirstRunSetup launched /onboard
// but forgot to mark it, so an interrupted first run re-nudged on reopen.
func shouldAutoOnboard() bool {
	if isFirstEverSession() && identityMissing() && !onboardAttempted() {
		markOnboardAttempted()
		return true
	}
	return false
}

// offerOnboarding asks (on a first run) whether to run the onboarding ceremony.
// Default is yes; "n"/"no"/"s"/"skip" declines. Returns true to run /onboard.
// Kept for tests; the CLI/TUI now auto-starts onboarding instead of prompting.
func offerOnboarding(reader lineReader, out io.Writer) bool {
	fmt.Fprintln(out, "octo isn't personalized yet — name it and tell it a bit about you (~2 min).")
	line, ok := reader.ReadLine("Onboard now? [Y/skip]: ")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "n", "no", "s", "skip":
		fmt.Fprintln(out, "Skipped — run /onboard anytime.")
		return false
	default:
		return true
	}
}

// wireSessionHooks completes the agent's hook identity once the session exists:
// the SessionID/transcript/transport the payload envelope carries, the durable
// SessionStarted seed, and the persist callback the engine invokes when
// SessionStart first fires (startup). Persistence rides the session layer's
// existing post-turn Save — MarkHookStarted only marks the meta dirty.
func wireSessionHooks(a *agent.Agent, sess *agent.Session, transport string) {
	a.HookMeta.SessionID = sess.ID
	a.HookMeta.Transport = transport
	if p, err := sess.SavePath(); err == nil {
		a.HookMeta.TranscriptPath = p
	}
	// A session loaded with prior history has effectively been started before,
	// even if it predates the persisted flag (upgraded sessions have no
	// hook_started field) — treat it as resume, not startup, on first touch.
	a.SessionStarted = sess.HookStarted || len(sess.Messages) > 0
	a.OnSessionStart = func() { sess.MarkHookStarted() }
}

// projectRunDir returns the directory this run should work in: the directory
// the resumed session belongs to, otherwise cwd unchanged. lookup is
// sessionBindingDir (injected for testing) — the SAME precedence the listing
// filters by, so a session's own directory outranks its project's workspace
// here exactly as it does there.
//
// Since -c only resolves sessions belonging to this directory
// (resolveSessionInDir), a resumed session's project directory and cwd already
// name the same place, so today this returns cwd in every reachable case. It
// stays as the one place that decides where a resumed run works, keeping that
// decision aligned with the precedence the listing filters by (sessionDir) and
// the server resolves tool cwd by (Server.resolveSessionDir), rather than
// leaving the CLI with no such point at all.
//
// The two values can be the same directory under different spellings — a
// project's directory is stored `~`-expanded and absolute but NOT
// symlink-resolved (validateWorkingDir), while membership is matched on the
// symlink-resolved form. Then the path the user typed is the one to keep:
// switching them to the project's spelling changes nothing except to print a
// relocation notice about a move that did not happen.
func projectRunDir(cwd, resumeID string, lookup func(string) string) string {
	if resumeID == "" {
		return cwd
	}
	dir := lookup(resumeID)
	if dir == "" || memory.NormalizeDir(dir) == memory.NormalizeDir(cwd) {
		return cwd
	}
	return dir
}

// resolveProjectHooksTrust decides whether the project-level <cwd>/.octo/hooks.yml
// should be loaded, implementing trust-on-first-use. It returns false (skip)
// when there is no project file. For an untrusted or changed file it prompts
// once (when interactive, i.e. stdin is a TTY — works for both the TUI and a
// one-shot at a terminal); approving records the content fingerprint so it won't
// ask again until the file changes. A non-interactive session (piped/redirected
// stdin) declines silently — an untrusted repo never runs shell unattended.
//
// The prompt reads a single line directly from stdin byte-by-byte, WITHOUT
// buffering ahead, so it never swallows input meant for the REPL reader or the
// bubbletea TUI that starts afterwards.
func resolveProjectHooksTrust(cwd string, stdin io.Reader, interactive bool, out, errOut io.Writer) bool {
	path := hooks.ProjectConfigPath(cwd)
	if path == "" {
		return false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false // no project hooks.yml (or unreadable) → nothing to trust
	}
	fp := hooks.Fingerprint(content)
	if hooks.IsTrusted(path, fp) {
		return true
	}
	if !interactive {
		return false // can't ask; leave project hooks disabled (no noise)
	}
	fmt.Fprintf(out, "\n⚠ This repository defines hooks in %s that can run shell commands on your machine.\n", path)
	fmt.Fprint(out, "  Trust and run this repo's hooks? [y/N]: ")
	switch readTrustAnswer(stdin) {
	case "y", "yes":
		if rerr := hooks.RecordTrust(path, fp); rerr != nil {
			fmt.Fprintf(errOut, "octo: could not persist hooks trust: %v\n", rerr)
		}
		return true
	default:
		fmt.Fprintln(out, "  Skipped. Project hooks stay disabled until you trust them.")
		return false
	}
}

// readTrustAnswer reads one newline-terminated line from r one byte at a time
// (no read-ahead), returning the lowercased, trimmed answer. Byte-by-byte so it
// consumes exactly the answer line and nothing the REPL/TUI will later read.
func readTrustAnswer(r io.Reader) string {
	var b []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			if buf[0] != '\r' {
				b = append(b, buf[0])
			}
		}
		if err != nil {
			break
		}
	}
	return strings.ToLower(strings.TrimSpace(string(b)))
}

// errMissingAPIKey is returned by resolveAPIKey when no API key is available.
// The caller (runChat) can detect this and auto-launch the config wizard on an
// interactive terminal instead of failing silently.
var errMissingAPIKey = errors.New("missing API key")

// unattendedMaxTurns is the agentic-loop cap applied when --max-turns is left
// at its auto-sentinel (0) and there's no interactive human to continue past
// the limit (piped stdin, --prompt-file). -1 means unlimited because a
// headless task can't be told to keep going.
const unattendedMaxTurns = -1

// resolveMaxTurns picks the agentic-loop cap. An explicit --max-turns (any
// non-zero flagVal) always wins. Otherwise an unattended run — seeded via
// --prompt-file, or reading piped/non-tty stdin, where nobody can type
// "continue" past the limit — gets unattendedMaxTurns; an interactive run
// returns 0 so the agent applies its own (lower) checkpoint default.
func resolveMaxTurns(flagVal int, seeded, interactive bool) int {
	if flagVal != 0 {
		return flagVal
	}
	if seeded || !interactive {
		return unattendedMaxTurns
	}
	return 0
}

// Provider-aware defaults for the escalated output cap retried on truncation.
// Conservative per protocol since octo has no per-model capability table; a
// model whose ceiling is below the target backs off at the API (see the agent
// loop's isMaxTokensTooLargeErr). See dev-docs/truncation-recovery.md.
const (
	escalateMaxTokensOpenAI    = 65536
	escalateMaxTokensAnthropic = 65536
)

// resolveMaxTokensEscalate picks the escalated cap: an explicit flag (>= 0)
// wins, then OCTO_MAX_TOKENS_ESCALATE, then the provider-aware default. 0
// disables escalation.
func resolveMaxTokensEscalate(flagVal int, provName string) int {
	if flagVal >= 0 {
		return flagVal
	}
	if env := strings.TrimSpace(os.Getenv("OCTO_MAX_TOKENS_ESCALATE")); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n >= 0 {
			return n
		}
	}
	if app.VendorProtocol(provName) == "openai" {
		return escalateMaxTokensOpenAI
	}
	return escalateMaxTokensAnthropic
}

// openMCPLogFile opens ~/.octo/logs/mcp.log (append) to receive stdio MCP
// servers' child stderr while the TUI owns the screen, so their diagnostics are
// recoverable rather than corrupting the frame. Returns nil on any failure; the
// caller then discards child stderr — never the terminal.
func openMCPLogFile() *os.File {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	dir := filepath.Join(home, ".octo", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "mcp.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// suggestEnabled resolves whether to offer after-turn follow-up suggestions:
// on by default, off when --no-suggest is set or OCTO_SUGGEST is falsey.
func suggestEnabled(noSuggestFlag bool) bool {
	if noSuggestFlag {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OCTO_SUGGEST"))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// tuiDisabledByEnv reports whether OCTO_TUI is set to a falsey value, the env
// equivalent of --no-tui (handy for dumb terminals / CI without editing the
// command line).
func tuiDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OCTO_TUI"))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// resolveCoauthor determines whether the agent should append a Co-authored-by
// line to git commit messages. Precedence: --no-coauthor flag > OCTO_COAUTHOR
// env > config file > default (true) — the last three layers are shared with
// every other caller (server included) via config.Config.EffectiveCoauthor.
func resolveCoauthor(noCoauthorFlag bool, cfg config.Config) bool {
	if noCoauthorFlag {
		return false
	}
	return cfg.EffectiveCoauthor()
}

// resolveShowReasoning determines whether the reasoning/thinking trace is
// surfaced (i.e. requested from the provider). The terminal never renders it —
// this only governs whether the trace is fetched at all, which the Web UI then
// displays. PR5: reasoning is global — reads Config.ShowReasoning instead of
// the deleted per-entry field. Precedence: an explicit --show-reasoning flag >
// config file > default (false).
func resolveShowReasoning(flagSet, flagVal bool, cfg config.Config) bool {
	if flagSet {
		return flagVal
	}
	if cfg.ShowReasoning != nil {
		return *cfg.ShowReasoning
	}
	return false
}

// toolSearchConfigFrom maps the persisted tools.tool_search block onto the
// tools-package config. Delegates to app.ToolSearchConfigFrom so every entry
// point shares one conversion.
func toolSearchConfigFrom(c config.ToolSearchConfig) tools.ToolSearchConfig {
	return app.ToolSearchConfigFrom(c)
}

// resolveReasoningEffort picks the reasoning intensity: --reasoning-effort flag
// > config file > "" (off). "off" is accepted as an explicit flag value (case
// insensitive) so a config-persisted level can be turned off for one run,
// mirroring `/thinking off` in the TUI and the server's reasoning_effort API.
func resolveReasoningEffort(flagSet bool, flagVal string, cfg config.Config) string {
	if flagSet {
		return normalizeOffEffort(flagVal)
	}
	// PR5: reasoning is global. The config can hold the literal "off" sentinel
	// — the server's session reasoning_effort API historically persisted it
	// verbatim instead of normalizing to "" (see internal/server/handlers.go
	// handleUpdateSessionReasoningEffort) — so normalize on read too.
	return normalizeOffEffort(cfg.ReasoningEffort)
}

// normalizeOffEffort maps the case-insensitive "off" sentinel to "", the
// internal representation of "no reasoning effort configured". Any other
// value passes through unchanged for validReasoningEffort to judge.
func normalizeOffEffort(e string) string {
	if strings.EqualFold(e, "off") {
		return ""
	}
	return e
}

// validReasoningEffort reports whether e is an accepted reasoning intensity.
func validReasoningEffort(e string) bool {
	switch e {
	case "", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

// anthropicThinkingBudget maps a unified reasoning-effort level to an Anthropic
// thinking-token figure. Delegates to app.AnthropicThinkingBudget so the CLI
// and the server share one source of truth for the effort→budget mapping.
func anthropicThinkingBudget(effort string) int {
	return app.AnthropicThinkingBudget(effort)
}

// defaultModels maps each provider to the model used when `--model` isn't
// supplied. Both defaults are the cheapest reasoning-capable model in the
// respective vendor's catalogue at the time of writing — the right pick for
// a scaffold whose primary purpose is verifying the wire end-to-end.
var defaultModels map[string]string

func init() {
	defaultModels = make(map[string]string, len(app.Registry))
	for _, v := range app.Registry {
		defaultModels[v.ID] = v.DefaultModel
	}
}

// resolveResumedModel resolves the (provider, model, entry) a resumed session
// should run on, given the session's saved model reference and the startup
// resolution for the current config default. sessionRef is the session's
// binding — a bare model id (legacy sessions) or a composite
// "<endpoint>::<model>" id (a session that recorded a /model switch) — and
// may come from a different endpoint than the one the config now defaults to
// (e.g. created on the kimi endpoint, resumed after the default moved to
// deepseek). Sending that model through a sender built for another endpoint
// misroutes the request (deepseek endpoint + k3-256k → HTTP 400).
//
// The returned entry anchors the rebuilt sender; rebuild reports whether it
// targets a different provider or base URL than the startup entry (the same
// no-rebuild criterion ensureSender uses: same endpoint → reuse the sender,
// only the wire model name changes). ok is false when the session model is no
// longer present in the config (its endpoint was deleted) — the caller then
// falls back to the current default rather than replaying the stale model.
// An empty sessionRef passes the startup resolution through untouched.
func resolveResumedModel(sessionRef, startProvider string, startEntry config.ModelEntry, cfg config.Config) (provider, model string, entry config.ModelEntry, rebuild, ok bool) {
	if sessionRef == "" {
		return startProvider, startEntry.Model, startEntry, false, true
	}
	// Guard before resolveProviderModel: with an unconfigured model name it
	// would silently fall back to the current provider (matching on the flag),
	// reporting ok=true for a model the config no longer carries — the exact
	// misroute this resolution exists to prevent (mirrors ensureSender).
	if _, found := cfg.EntryByModel(sessionRef); !found {
		return "", "", config.ModelEntry{}, false, false
	}
	p, m, e, ok := resolveProviderModel("", sessionRef, cfg)
	if !ok {
		return "", "", config.ModelEntry{}, false, false
	}
	rebuild = p != startProvider || e.BaseURL != startEntry.BaseURL
	return p, m, e, rebuild, true
}

// resumeModelRef returns the session's model reference for resume: the
// binding recorded by a mid-session /model switch (ModelConfig, a composite
// "<endpoint>::<model>" when an endpoint was addressed explicitly) when
// present, else the bare wire model. The binding keeps the exact endpoint
// when the same model exists on several ones, mirroring the server's
// senderForSession.
func resumeModelRef(sess *agent.Session) string {
	if sess.ModelConfig != "" {
		return sess.ModelConfig
	}
	return sess.Model
}

// resumeLite re-infers the session's implicit lite model after a resume
// changed the active model. The startup lite inference ran against the config
// default; after resume the conversation runs on the session's own model, so
// compaction should follow the session's endpoint, key, and prompt cache.
// Only a lite that came from the startup sender (implicit inference) or was
// absent is touched — an explicit cfg.Lite entry built from its own sender is
// left alone.
func resumeLite(a *agent.Agent, prevSender agent.Sender, provider, model string, entry config.ModelEntry) {
	if a.LiteSender != prevSender && a.LiteSender != nil {
		return
	}
	if lm := app.ImplicitLiteModel(provider, model, resolveBaseURL(provider, entry)); lm != "" {
		a.LiteSender = a.GetSender()
		a.LiteModel = lm
	} else {
		a.LiteSender, a.LiteModel = nil, ""
	}
}

// runChat handles `octo [flags] [message]` — every invocation that isn't a
// named subcommand.
//
// With a positional message argument (or piped stdin): one headless agentic
// turn, then exit. Without one, on a terminal: the interactive TUI.
func runChat(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	startTrashHousekeeping()
	args = normalizeBareContinue(args)
	fs := flag.NewFlagSet("octo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	providerName := fs.String("provider", "", "Provider: anthropic | openai (default from `octo config`, else anthropic)")
	model := fs.String("model", "", "Model name (else ANTHROPIC_MODEL/OPENAI_MODEL env, then `octo config`, then the provider's cheapest reasoning model)")
	system := fs.String("system", "", "System prompt (optional)")
	maxTokens := fs.Int("max-tokens", 0, "max_tokens for the response (0 = provider default)")
	maxTokensEscalate := fs.Int("max-tokens-escalate", -1, "Per-response cap retried once when a reply is truncated by the output cap (-1 = provider-aware default, 0 = disable). Also OCTO_MAX_TOKENS_ESCALATE.")
	stream := fs.Bool("stream", true, "Stream the reply (chunks printed as they arrive); --stream=false buffers")
	continueID := fs.String("c", "", "Resume a session from this directory — accepts 'last', a short ID, or a substring of an ID; omit the value to pick from a list")
	continueIDLong := fs.String("continue", "", "Resume a session from this directory — accepts 'last', a short ID, or a substring of an ID; omit the value to pick from a list")
	takeOver := fs.Bool("take-over", false, "When resuming (-c), take over a session currently bound to another entry")
	noSave := fs.Bool("no-save", false, "Disable session auto-save in the interactive TUI (headless one-shots never persist)")
	enableTools := fs.Bool("tools", true, "Built-in tools (terminal, edit_file, …) for the agentic loop. On by default; use --no-tools to disable.")
	noTools := fs.Bool("no-tools", false, "Disable the built-in tools (and MCP/skill execution) — plain chat only")
	noMemory := fs.Bool("no-memory", false, "Disable cross-session memory (MEMORY.md injection + the writable memory directory)")
	plain := fs.Bool("plain", false, "Render tool events as one-line ↳ status lines instead of rich diff cards")
	promptFile := fs.String("prompt-file", "", "Read the prompt from this file (newlines preserved) and run it as one headless agentic turn, then exit. For scripting/eval.")
	noTUI := fs.Bool("no-tui", false, "Force the headless one-shot path on a terminal instead of the interactive TUI (also OCTO_TUI=0). The prompt comes from a positional message, --prompt-file, or piped stdin.")
	noSuggest := fs.Bool("no-suggest", false, "Disable the after-turn follow-up suggestion (ghost text accepted with Tab/→). Also OCTO_SUGGEST=0.")
	quietFlag := fs.Bool("quiet", false, "Strip all status chrome (no spinner, no banner, no cache line). Also OCTO_VERBOSITY=quiet.")
	verboseFlag := fs.Bool("verbose", false, "Print extra context (provider/model/endpoint, always-on cache line). Also OCTO_VERBOSITY=verbose.")
	permMode := fs.String("permission-mode", "", "Tool permission handling: interactive (prompt on ask) | strict (deny on ask) | auto (allow on ask). Empty = use `octo config` value, else interactive.")
	noCoauthor := fs.Bool("no-coauthor", false, "Disable appending Co-authored-by to git commit messages. Also OCTO_COAUTHOR=0.")
	maxTurns := fs.Int("max-turns", 0, "Max provider round-trips per message in the agentic loop (0 = auto: 1000 interactive, unlimited unattended/--prompt-file)")
	compactThreshold := fs.Int("compact-threshold", 0, "Compact older history once a turn's input crosses this many tokens; 0 = auto (percentage of the model's context window, settable via --compact-auto-pct or config), <0 = disabled")
	compactAutoPct := fs.Int("compact-auto-pct", 0, "Auto-compaction threshold as a percentage of the model's context window (0 = use `octo config` or built-in default 75). Only used when --compact-threshold=0.")
	reasoningEffort := fs.String("reasoning-effort", "", "Reasoning intensity: off | low | medium | high | xhigh | max (empty = use `octo config`/default; 'off' forces it off for this run). OpenAI → reasoning_effort; Anthropic → adaptive thinking + effort.")
	showReasoning := fs.Bool("show-reasoning", false, "Surface the reasoning/thinking trace for the Web UI (octo serve) to display. The terminal never renders it. Default off; also from `octo config`.")
	useSandbox := fs.Bool("sandbox", false, "Confine terminal commands to the project dir + tmp with no network (OS-enforced; macOS/Linux). Fails closed if unavailable.")
	sandboxAllowNet := fs.Bool("sandbox-allow-net", false, "Under --sandbox, permit network access (default: denied)")
	var sandboxWrite, sandboxRead stringList
	fs.Var(&sandboxWrite, "sandbox-write", "Under --sandbox, an extra writable directory (repeatable)")
	fs.Var(&sandboxRead, "sandbox-read", "Under --sandbox, an extra read-only directory (repeatable)")
	agentName := fs.String("agent", "", "Start the session bound to a specific agent (by ID from ~/.octo/agents)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolve -c / --continue (short wins if both somehow set).
	resumeID := *continueIDLong
	if *continueID != "" {
		resumeID = *continueID
	}

	userInput := strings.TrimSpace(strings.Join(fs.Args(), " "))
	isREPL := userInput == ""

	// --prompt-file supplies the one-shot prompt from a file. It's mutually
	// exclusive with a positional message — both are prompt sources, and
	// accepting both would silently drop one.
	var seedPrompt string
	if *promptFile != "" {
		if !isREPL {
			fmt.Fprintln(stderr, "octo: --prompt-file cannot be combined with a positional message")
			return 2
		}
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			fmt.Fprintf(stderr, "octo: --prompt-file: %v\n", err)
			return 1
		}
		seedPrompt = strings.TrimRight(string(b), "\n")
		if strings.TrimSpace(seedPrompt) == "" {
			fmt.Fprintln(stderr, "octo: --prompt-file is empty")
			return 2
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "octo: %v\n", err)
		fmt.Fprintln(stderr, "Run `octo config` to rewrite ~/.octo/config.yml.")
		return 1
	}

	// Resolve permission mode: explicit flag > config file > interactive default.
	resolvedPermMode := *permMode
	if resolvedPermMode == "" {
		resolvedPermMode = cfg.PermissionMode
	}
	if resolvedPermMode == "" {
		resolvedPermMode = string(permission.ModeInteractive)
	}
	// Validate up front. Fail closed on a typo rather than silently falling
	// back to the more-permissive interactive mode.
	if resolvedPermMode != string(permission.ModeInteractive) &&
		resolvedPermMode != string(permission.ModeAutoApprove) &&
		resolvedPermMode != string(permission.ModeStrict) {
		fmt.Fprintf(stderr, "octo: invalid --permission-mode %q (want 'interactive', 'strict' or 'auto')\n", resolvedPermMode)
		return 2
	}

	provName, resolvedModel, entry, ok := resolveProviderModel(*providerName, *model, cfg)
	if !ok {
		fmt.Fprintf(stderr, "octo: unknown provider %q\n", provName)
		return 2
	}

	// Install the Tool Search config so DefaultToolsFor can decide whether to
	// defer MCP schemas behind the search/describe/call bridge for this model.
	tools.SetToolSearchConfig(toolSearchConfigFrom(cfg.Tools.ToolSearch))

	// Resolve reasoning controls: --reasoning-effort sets the intensity (OpenAI
	// reasoning_effort / mapped Anthropic budget); --show-reasoning gates whether
	// the trace is streamed to the terminal. Both fall back to the resolved
	// config entry.
	showReasoningFlagSet := false
	reasoningEffortFlagSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "show-reasoning":
			showReasoningFlagSet = true
		case "reasoning-effort":
			reasoningEffortFlagSet = true
		}
	})
	resolvedEffort := resolveReasoningEffort(reasoningEffortFlagSet, *reasoningEffort, cfg)
	if !validReasoningEffort(resolvedEffort) {
		fmt.Fprintf(stderr, "octo: invalid --reasoning-effort %q (want 'off', 'low', 'medium', 'high', 'xhigh', or 'max')\n", *reasoningEffort)
		return 2
	}
	resolvedShowReasoning := resolveShowReasoning(showReasoningFlagSet, *showReasoning, cfg)

	// Resolve the --agent profile (if specified) from ~/.octo/agents.
	// When the profile carries its own SystemPrompt, it replaces the
	// server's base prompt — so expert agents actually use their persona.
	// The profile ID is stamped onto the session so it routes to the right
	// agent namespace and filters tools/skills per the profile's allowlist.
	var agentProfileID string
	var agentProfile *agentprofile.Profile
	var agentStore *agentprofile.Store
	if *agentName != "" {
		agentStore = agentprofile.New(agentUserDir())
		profile, ok := agentStore.Get(*agentName)
		if !ok {
			ids := append([]string{"default"}, profileIDs(agentStore)...)
			fmt.Fprintf(stderr, "octo: agent %q not found (available: %s)\n",
				*agentName, strings.Join(ids, ", "))
			return 2
		}
		agentProfile = profile
		agentProfileID = profile.ID
	}

	// Single-turn mode requires a message.
	if !isREPL && resumeID != "" {
		fmt.Fprintln(stderr, "octo: -c/--continue requires interactive mode (omit the message argument)")
		return 2
	}

	// --take-over only makes sense when resuming an existing session.
	if *takeOver && resumeID == "" {
		fmt.Fprintln(stderr, "octo: --take-over requires -c/--continue")
		return 2
	}

	// Every -c form resolves against the sessions belonging to THIS directory
	// (see session_scope.go): the picker lists them, "last" means the most
	// recent one here, and an explicit id has to be one of them. The directory
	// is the one the user is standing in — a session's own project directory
	// only takes over once it has been resolved, below.
	//
	// Without a current directory there is no way to tell which sessions belong
	// here, and silently resolving against none of them would read as "your
	// session is gone". Only -c needs it; a fresh session does not.
	resumeScope, scopeErr := os.Getwd()
	if resumeID != "" && scopeErr != nil {
		fmt.Fprintf(stderr, "octo: -c resumes the sessions belonging to the current directory, which could not be determined: %v\n", scopeErr)
		return 1
	}

	// A bare -c / --continue (no ID) pops an arrow-key picker over the recent
	// sessions — nobody remembers session IDs. Interactive by nature: headless
	// callers must pass an ID.
	pickedFromList := false
	if resumeID == pickSessionSentinel {
		if !stdinIsTTY(stdin) {
			fmt.Fprintln(stderr, "octo: -c without an ID picks from a list, which needs a terminal — pass an ID (see `octo sessions`)")
			return 2
		}
		sessions, err := sessionsForDir(resumeScope, 10)
		if err != nil {
			fmt.Fprintf(stderr, "octo: %v\n", err)
			return 1
		}
		if len(sessions) == 0 {
			fmt.Fprintf(stderr, "No sessions to resume in %s — run `octo` to start one, or `octo sessions --all` to see other directories'.\n", resumeScope)
			return 1
		}
		picked, ok := runSelect(stdin, stdout, "Resume which session?", sessionSelectItems(sessions), "")
		if !ok {
			return 0 // cancelled — nothing to do
		}
		resumeID = picked.value
		pickedFromList = true
	}

	// Resolve -c shortcuts ("last", short ID, prefix/substring) against this
	// directory's sessions. A full ID still works, and one naming a session
	// that lives in another directory is refused with that directory named.
	//
	// Skipped for a picked session: the picker chose from this directory's
	// sessions and hands back a full id, so resolving it again would re-read
	// and re-parse every transcript on the machine to reach the same answer.
	if resumeID != "" && !pickedFromList {
		resolved, err := resolveSessionInDir(resumeID, resumeScope)
		if err != nil {
			fmt.Fprintf(stderr, "octo: %v\n", err)
			fmt.Fprintln(stderr, "Run `octo sessions` to see what's available.")
			return 2
		}
		resumeID = resolved
	}

	// Built-in tools are on by default; --no-tools opts out. MCP servers and
	// skill execution ride on the same switch, so a single flag governs the
	// whole agentic surface.
	toolsOn := *enableTools && !*noTools

	// Resuming a tool-using session with tools off is a footgun: the model
	// sees prior tool_use blocks in history but gets no tools array, and
	// falls back to emitting tool calls as text (a wall of
	// `<tool_calls><invoke name="...">...` XML). With tools on by default
	// this can only happen when the user explicitly passes --no-tools, so we
	// respect their choice but warn once.
	if isREPL && resumeID != "" && !toolsOn {
		if peek, perr := agent.LoadSession(resumeID); perr == nil && peek.UsedTools() {
			fmt.Fprintln(stderr, "Warning: this session used tools before; --no-tools may make the model emit tool calls as text.")
		}
	}

	// Buffer the first attempt's diagnostics: when a missing key sends us into
	// the config wizard below, the manual export-a-key walkthrough would only
	// duplicate (and contradict) the wizard — it's shown solely when the
	// wizard can't run.
	var senderDiag bytes.Buffer
	llmSender, err := buildSender(provName, entry, &senderDiag, senderTuning{
		thinkingBudget:  anthropicThinkingBudget(resolvedEffort),
		reasoningEffort: resolvedEffort,
		showReasoning:   resolvedShowReasoning,
	})
	if err != nil {
		// If the sender failed because of a missing API key and we're on an
		// interactive terminal, run the config wizard automatically rather than
		// leaving the user to figure out `octo config` on their own.
		if errors.Is(err, errMissingAPIKey) && stdinIsTTY(stdin) {
			fmt.Fprintln(stderr, "No API key configured — let's set up octo first.")
			fmt.Fprintln(stderr, "")
			if runConfigWizard(stdin, stdout, stderr, true) != 0 {
				return 1
			}
			// Reload config and retry sender construction.
			cfg, _ = config.Load()
			provName, resolvedModel, entry, ok = resolveProviderModel(*providerName, *model, cfg)
			if !ok {
				fmt.Fprintf(stderr, "octo: unknown provider %q\n", provName)
				return 2
			}
			llmSender, err = buildSender(provName, entry, stderr, senderTuning{
				thinkingBudget:  anthropicThinkingBudget(resolvedEffort),
				reasoningEffort: resolvedEffort,
				showReasoning:   resolvedShowReasoning,
			})
			if err != nil {
				return 1
			}
		} else {
			stderr.Write(senderDiag.Bytes())
			return 1
		}
	}

	// Verbose: surface the resolved provider / model / endpoint so a
	// misrouted base URL (e.g. ANTHROPIC_BASE_URL pointed at a third party) is
	// visible at a glance. To stderr so it never pollutes single-turn stdout;
	// shown for every path (single-turn, plain REPL, TUI).
	if resolveVerbosity(*quietFlag, *verboseFlag).verbose() {
		fmt.Fprintf(stderr, "octo: provider=%s model=%s endpoint=%s\n",
			provName, resolvedModel, effectiveEndpoint(provName, entry))
	}

	// Resolve coauthor: flag > env > config > default (true).
	coauthor := resolveCoauthor(*noCoauthor, cfg)

	// Compose the system prompt once (base + project .octorules + user --system)
	// and freeze it for the session — recomputing mid-session would bust the
	// provider's system+tools prompt cache. The session stores only the raw
	// user layer; base/project are recomposed fresh each run.
	// The same directory -c resolved against above, not a second Getwd: one
	// value means the sessions offered, the session's recorded directory, and
	// the directory this run works in can never disagree.
	cwd := resumeScope
	// Resuming a session that belongs to a project relocates this whole run to
	// the project's directory: tools, sandbox roots, project hooks, project
	// memory, and the env context all key off cwd, and letting them disagree
	// (tools in one directory, hooks discovered from another) would be worse
	// than either choice on its own.
	//
	// In practice this now names the directory the user is already standing in
	// — -c only offers sessions belonging to it — so the relocation below no
	// longer fires (see projectRunDir). It stays because the CLI still has to
	// answer "where does a resumed run work" somewhere.
	//
	// Resolved once here, like everything else in this block — switching
	// sessions inside the REPL does not recompute it, matching the
	// compose-once-per-process model the CLI already follows for the system
	// prompt. The CLI has no way to CHANGE a working directory; a project's
	// setting is editable only where it was made (the Web UI / desktop).
	if dir := projectRunDir(cwd, resumeID, sessionBindingDir); dir != cwd {
		// Announce it — silently running tools on a path other than the one
		// the user typed would be baffling.
		fmt.Fprintf(stderr, "Session belongs to a project — working in %s\n", dir)
		cwd = dir
	}
	env := buildEnvContext(cwd)

	// Discover skills once at session start. The manifest goes into the frozen
	// system prompt (L1); the registry backs the `skill` tool so it can serve
	// full bodies on demand (L2).
	skillReg := skills.Discover()
	skillsManifest := tools.SkillsManifest(skillReg)
	tools.SetSkills(skillReg)

	// Cross-session memory (Claude Code model): a per-project directory of
	// markdown files the agent manages with its own file tools. memDir is
	// created up front, injected into the system prompt (below), and
	// whitelisted for writes when the permission engine is built. --no-memory
	// disables it; a resolve error degrades to no memory rather than failing.
	//
	// On the CLI the working directory IS the project — you cd somewhere to
	// work on it — so cwd scopes the memory, whether or not git has ever heard
	// of that directory. Note this runs AFTER the project-run-dir resolution
	// above, so a session filed under a project in the Web UI gets that
	// project's memory here too, not the memory of wherever octo was launched.
	// Running from home needs no special case: its slug directory IS the home
	// directory, so it lands on the shared tier and RenderInjection collapses
	// the two into one.
	var memDir, homeMemDir string
	var memWriteRoots []string
	if !*noMemory {
		if d, err := memory.DirForProject(cwd); err == nil {
			if memory.EnsureDir(d) == nil {
				memDir = d
			}
		}
		if d, err := memory.HomeDir(); err == nil {
			if memory.EnsureDir(d) == nil {
				homeMemDir = d
			}
		}
	}
	memWriteRoots = memoryWriteRoots(memDir, homeMemDir)

	if *useSandbox {
		opts := sandboxOpts{allowNet: *sandboxAllowNet, writeRoots: sandboxWrite, readRoots: sandboxRead}
		if err := activateSandbox(cwd, opts, stderr); err != nil {
			return 1
		}
	}

	// The sender (built above with a stable per-process cache key) lets OpenAI
	// route every turn — and every tool-loop iteration — of this conversation
	// to the same prompt cache.
	a := agent.New(llmSender, resolvedModel)
	a.CWD = cwd
	a.MaxTokens = *maxTokens
	a.MaxTokensEscalate = resolveMaxTokensEscalate(*maxTokensEscalate, provName)
	a.MaxTurns = resolveMaxTurns(*maxTurns, seedPrompt != "", stdinIsTTY(stdin))
	a.CompactThreshold = *compactThreshold
	// Resolve auto-compaction percentage: explicit flag > config > built-in default.
	autoPct := *compactAutoPct
	if autoPct == 0 {
		autoPct = cfg.CompactAutoPct
	}
	if autoPct > 0 {
		a.CompactAutoFraction = float64(autoPct) / 100.0
	}
	// History compaction runs on the configured lite model when one is set.
	// A build failure (missing key, unknown provider) just leaves the primary
	// in charge — never fail startup over the lite entry.
	if liteEntry, ok := cfg.EntryByModel(cfg.Lite); ok && liteEntry.Model != "" {
		if liteSender, lerr := buildSender(liteEntry.Provider, liteEntry, io.Discard, senderTuning{}); lerr == nil {
			a.LiteSender = liteSender
			a.LiteModel = liteEntry.Model
		}
	}
	// Images become text for a text-only model when a vision helper is
	// configured. Nil (unconfigured) leaves every image path unchanged.
	a.SetImageDescriber(app.NewVisionDescriber(a, cfg))
	if a.LiteSender == nil {
		// No explicit lite entry — fall back to the vendor's registry lite
		// model on the SAME sender, so compaction stays on the endpoint, key,
		// and prompt cache the conversation is already using.
		if lm := app.ImplicitLiteModel(provName, resolvedModel, resolveBaseURL(provName, entry)); lm != "" {
			a.LiteSender = llmSender
			a.LiteModel = lm
		}
	}

	// Build the tool executor up-front (REPL mode only — single-turn mode
	// doesn't dispatch tools) and register the sub-agent dispatcher BEFORE the
	// startup memory pass runs. This lets maybeProcessMemory's consolidation
	// step use a sub-agent (M10, #6) with read-only filesystem tools, falling
	// back to the side-call path when no Spawner is wired.
	//
	// The line reader is built here too so the same instance is shared with
	// the REPL loop, the permission gate, and the asker (no double-buffering
	// of stdin). On an interactive terminal we use chzyer/readline for
	// history + line editing; otherwise (tests, pipes) we fall back to a
	// scanner over stdin. Registering the asker NOW also matters for
	// DefaultTools() gating: the ask_user_question tool only appears when
	// an asker is registered, and DefaultTools is computed below before
	// runREPL starts.
	var (
		toolExecutor tools.DefaultRegistry
		replReader   lineReader
		replView     ViewSink
		subAgentMgr  *tools.SubAgentManager
	)
	// Two surfaces only. An interactive terminal drives the bubbletea TUI;
	// everything else is a headless one-shot — octo's claude -p mode. A
	// positional message, --prompt-file, piped/redirected stdin, --no-tui, or
	// OCTO_TUI=0 all take the one-shot path (tests, octo-eval, CI included).
	useTUI := isREPL && stdinIsTTY(stdin) && !*noTUI && !tuiDisabledByEnv() && seedPrompt == ""

	// Resuming a session (-c) is an interactive affordance — it only makes sense
	// in the TUI. A headless one-shot starts fresh.
	if resumeID != "" && !useTUI {
		fmt.Fprintln(stderr, "octo: -c/--continue needs an interactive terminal (run it without piping/--no-tui)")
		return 2
	}

	// Resolve the single prompt for the one-shot path: positional message →
	// --prompt-file → all of piped stdin. The TUI reads its own input.
	var oneShotPrompt string
	if !useTUI {
		oneShotPrompt = userInput
		if oneShotPrompt == "" {
			oneShotPrompt = seedPrompt
		}
		if oneShotPrompt == "" && !stdinIsTTY(stdin) {
			b, rerr := io.ReadAll(stdin)
			if rerr != nil {
				fmt.Fprintf(stderr, "octo: read stdin: %v\n", rerr)
				return 1
			}
			oneShotPrompt = strings.TrimSpace(string(b))
		}
		if oneShotPrompt == "" {
			fmt.Fprintln(stderr, "octo: no prompt — pass a message, use --prompt-file, or pipe input on stdin")
			return 2
		}
	}

	// Agentic setup — shared by both paths, which each run the full tool loop.
	// WireTools builds the executor, registers the sub-agent spawner, creates the
	// sub-agent manager, and installs the session task store; its cleanup resets
	// those process-global registrations at session end.
	toolEnv, toolCleanup := app.WireTools(a, true)
	defer toolCleanup()
	toolExecutor = toolEnv.Executor
	subAgentMgr = toolEnv.SubAgentMgr

	// The headless one-shot exits when its single turn ends, so a background
	// sub-agent's completion notification has no follow-up turn to land in —
	// children spawned with run_in_background=true would be orphaned and their
	// results silently lost. Run sub-agents inline instead; this is the only
	// transport that still forces sync. (Web session and IM turns keep async:
	// they kick an idle follow-up turn on completion.) Parallel fan-out is
	// unaffected: sync sub_agent calls issued in one assistant message still
	// dispatch concurrently. The TUI keeps async too — it re-injects
	// completions as follow-up turns.
	if !useTUI {
		subAgentMgr.SetSynchronous(true)
	}

	// send_message / send_file for the CLI/TUI: delegate to a local `octo serve`
	// (live adapters — needed for WeChat) or fall back to a one-shot send from
	// config. Only wired when the user has configured at least one channel, so
	// non-IM users don't get the extra tools advertised.
	if chCfg, err := channel.LoadConfig(); err == nil && len(chCfg.Channels) > 0 {
		tools.SetMessenger(cliMessenger{})
		defer tools.SetMessenger(nil)
	}
	if useTUI {
		// bubbletea owns stdin and renders its own input; the asker and gate
		// are wired to the TUI sink inside runTUI.
		defer tools.SetAsker(nil)
		// The interactive TUI can re-enter a live session, so advertise the
		// schedule_wakeup tool (the in-session loop mechanism). The headless
		// one-shot below leaves it off — a process that exits after one turn
		// has no session to wake.
		tools.SetWakerSupported(true)
	} else {
		// A TTY reader (a positional message typed at a terminal) makes
		// permission / Ask prompts interactive. Over a pipe stdin is already
		// drained into the prompt, so the reader hits EOF and plainView
		// auto-denies — the headless posture.
		if stdinIsTTY(stdin) {
			rl, err := newReadlineReader(defaultHistoryFile())
			switch {
			case err == nil:
				replReader = rl
			case errors.Is(err, errReadlineUnsupported):
				// Expected on Windows: cooked-mode conhost handles paste and
				// basic editing natively, so degrade silently — this is the
				// designed posture, not an error worth announcing every run.
				replReader = newScannerLineReader(stdin, stdout)
			default:
				fmt.Fprintf(stderr, "octo: line editor unavailable (%v); falling back to plain input\n", err)
				replReader = newScannerLineReader(stdin, stdout)
			}
		} else {
			replReader = newScannerLineReader(stdin, stdout)
		}
		pv := newPlainView(replReader, stdout, stderr, resolveVerbosity(*quietFlag, *verboseFlag), *plain)
		// Secrets read with no echo, straight from the tty — never through the
		// line editor (the value would echo and land in readline history).
		if f, ok := stdin.(*os.File); ok && stdinIsTTY(stdin) {
			pv.secretReader = func(prompt string) (string, bool) {
				fmt.Fprint(pv.out, prompt)
				b, err := term.ReadPassword(int(f.Fd()))
				if err != nil {
					return "", false
				}
				return string(b), true
			}
		}
		replView = pv
		tools.SetAsker(newREPLAsker(replView))
		defer tools.SetAsker(nil)
	}

	// MCP servers: load config, connect, register so DefaultTools and
	// DefaultRegistry pick them up. Best-effort — a misconfigured or missing
	// server is logged on stderr and the session keeps going. Skipped when
	// tools are off (the agent would never invoke them).
	//
	// Connecting is synchronous for the headless one-shot — its single turn
	// needs the full tool surface before it runs. The interactive TUI instead
	// defers connection to a background tea.Cmd (mcpBoot, handled in runTUI) so
	// the banner + input paint immediately; tools register when the registry
	// arrives a moment later.
	var mcpBoot *mcpBootstrap
	if toolsOn {
		mcpCfg, err := mcp.LoadConfig()
		if err != nil {
			fmt.Fprintf(stderr, "octo: mcp config: %v\n", err)
		} else if len(mcpCfg.Servers) > 0 {
			info := mcp.Implementation{Name: "octo", Version: version.Version}
			// A stdio server's subprocess writes diagnostics to its stderr at
			// arbitrary times, from an exec copy goroutine. Under the bubbletea
			// TUI a direct terminal write corrupts the frame; in the headless
			// one-shot it interleaves with the turn's own output (e.g. a
			// file-watcher banner mid-turn). Route it to a log file in both
			// modes (recoverable, never the screen). Connection warnings still
			// reach the terminal via the warn writer. The file is closed when
			// runChat returns.
			var childStderr io.Writer
			if f := openMCPLogFile(); f != nil {
				childStderr = f
				defer f.Close()
			} else {
				childStderr = io.Discard
			}
			if useTUI {
				mcpBoot = &mcpBootstrap{cfg: mcpCfg, info: info, childErr: childStderr}
			} else {
				// Headless: connect now and register before the one-shot turn.
				mcpReg := mcp.ConnectAll(
					context.Background(),
					mcpCfg,
					info,
					func(serverName string) mcp.OAuthPrompt {
						return newCLIOAuthPrompt(stdout, serverName)
					},
					stderr,
					childStderr,
				)
				if mcpReg.Len() > 0 {
					tools.SetMCPRegistry(mcpReg)
					defer func() {
						tools.SetMCPRegistry(nil)
						mcpReg.Close()
					}()
				}
			}
		}
	}

	// MCP tools manifest: name + one-line description for every deferred MCP
	// tool, rendered into the system prompt when Tool Search is active for
	// this model (empty string otherwise — see tools.MCPManifestFor). Computed
	// here, after the block above, so the headless one-shot (which connects
	// MCP synchronously just before this point) sees a populated registry.
	// The interactive TUI defers MCP connection to a background tea.Cmd
	// (mcpBoot below), so this still evaluates to "" at first paint for TUI —
	// cfg.recomposeMCPManifest (wired below) redoes this once mcpReadyMsg
	// fires and the registry is actually live.
	mcpManifest := tools.MCPManifestFor(resolvedModel, agentProfile)

	// Inject the project's MEMORY.md (plus the manage-it-yourself instruction)
	// into the system prompt. The agent reads/writes the rest of the memory
	// directory on demand with its file tools — no consolidation pass.
	var memInjection string
	if memDir != "" {
		memInjection = memory.RenderInjection(memDir, homeMemDir)
	}
	if g := tools.MemoryBackendGuidance(); g != "" {
		memInjection = strings.TrimSpace(memInjection + "\n\n" + g)
	}
	a.System, a.LeanSystem = prompt.ComposePair(*system, cwd, env, skillsManifest, mcpManifest, memInjection, coauthor, false)

	// Attention layer: re-surface MEMORY.md's structured rules (## 必须遵守 /
	// ## 触发提醒) on the message stream at the point of action. This rides each
	// user turn rather than the cached system prompt, so the prompt prefix stays
	// byte-stable. The same injector carries the save-nudge, appended to a tool
	// result when a milestone-shaped command (gh pr create/merge) lands, so the
	// injector is wired even when MEMORY.md has no structured rules — Reminder
	// is silent then.
	// Hook engine: shell hooks (from the OCTO_HOOK_* env shim) plus the memory
	// injector's reminder & save-nudge as in-process hooks, unified on one
	// dispatch path that the agent core drives for every transport. Shares the
	// process seen-set so SessionStart resume fires once per OS process.
	projectHooksTrusted := resolveProjectHooksTrust(cwd, stdin, stdinIsTTY(stdin), stdout, stderr)
	hookEngine := hooks.EngineFromEnvAndFiles(hooks.SharedSeen(), cwd, projectHooksTrusted)
	hookEngine.Notify = func(m string) { fmt.Fprintln(stderr, "↳ hook: "+m) }
	hooks.SetSpillNotify(func(m string) { fmt.Fprintln(stderr, "↳ hook: "+m) })
	if memDir != "" {
		rules := memory.ParseRules(memDir)
		// Skip the merge when both resolve to the same directory (a non-repo
		// cwd shares the home tier) — parsing it twice only to dedupe by rule
		// text is wasted work. Matches the server's injectorFor.
		if homeMemDir != "" && homeMemDir != memDir {
			rules.Merge(memory.ParseRules(homeMemDir))
		}
		memory.NewInjector(rules).RegisterHooks(hookEngine)
	}
	// Suggest saving a workflow once the model chains >=2 skills by hand in a
	// turn — independent of memory, so wired unconditionally.
	tools.NewWorkflowNudger().RegisterHooks(hookEngine)
	// Validate ~/.octo/config.yml right after the agent edits it.
	tools.NewConfigGuard().RegisterHooks(hookEngine)
	// Auto-store into the external memory backend (if configured) after each
	// turn — independent of memDir/MEMORY.md, so wired unconditionally; a
	// no-op when no backend is set.
	tools.RegisterMemoryBackendHooks(hookEngine)
	a.Hooks = hookEngine
	a.HookMeta = hooks.Meta{Cwd: cwd}

	// Permission engine — gates every tool call; shared by both paths. The
	// memory tree (outside CWD) is whitelisted for writes so the agent can
	// manage its memory files without a prompt on every save.
	var permEngine *permission.Engine
	if toolsOn {
		eng, perr := permission.New(permissionConfigPath(), cwd, resolvePermissionMode(resolvedPermMode), memWriteRoots...)
		if perr != nil {
			fmt.Fprintf(stderr, "octo: permission config: %v\n", perr)
			return 1
		}
		permEngine = eng
	}

	// ── Interactive TUI ───────────────────────────────────────────────────────
	if useTUI {
		var sess *agent.Session
		if resumeID != "" {
			startProvider, startModel, startEntry := provName, resolvedModel, entry
			sess, err = agent.LoadSession(resumeID)
			if err != nil {
				fmt.Fprintf(stderr, "octo: %v\n", err)
				return 1
			}
			if ok, msg, berr := sess.Bind(agent.EntryTUI, *takeOver); ok == agent.Rejected {
				fmt.Fprintf(stderr, "octo: %v\n", berr)
				return 1
			} else if msg != "" && !*quietFlag {
				fmt.Fprintln(stderr, "octo:", msg)
			}
			// Restore history and override model/system from saved session.
			a.History = sess.ToHistory()
			if sess.Model != "" {
				a.Model = sess.Model
				// The sender was built for the current config default, but a
				// saved session carries its own model — often from a different
				// endpoint (created on the kimi endpoint, resumed after the
				// default moved to deepseek). Sending a.Model through a sender
				// built for another endpoint misroutes the request (deepseek
				// endpoint + k3-256k → HTTP 400). Mirror the server's
				// senderForSession: re-resolve the session's model to its
				// config entry, rebuild the sender when the endpoint differs,
				// and keep the status bar (modelName) on the model actually in
				// use. A session model that left the config falls back to the
				// current default so the session stays usable.
				// Prefer the session's binding (ModelConfig, set by a mid-session
				// /model switch) over its bare wire model — the binding keeps the
				// exact endpoint when the same model exists on several ones,
				// mirroring the server's senderForSession.
				p, m, e, rebuild, ok := resolveResumedModel(resumeModelRef(sess), provName, entry, cfg)
				if !ok {
					fmt.Fprintf(stderr, "octo: warning: session model %q is no longer configured; resuming on %q\n", sess.Model, resolvedModel)
					a.Model = resolvedModel
				} else {
					prevProvider, prevModel, prevEntry := provName, resolvedModel, entry
					prevSender := llmSender
					provName, resolvedModel, entry = p, m, e
					if rebuild {
						newSender, serr := buildSender(p, e, stderr, senderTuning{
							thinkingBudget:  anthropicThinkingBudget(resolvedEffort),
							reasoningEffort: resolvedEffort,
							showReasoning:   resolvedShowReasoning,
						})
						if serr != nil {
							// Rebuild failed (missing key, …) — revert to the
							// default rather than sending the session's model
							// to the wrong endpoint.
							provName, resolvedModel, entry = prevProvider, prevModel, prevEntry
							a.Model = prevModel
							fmt.Fprintf(stderr, "octo: warning: could not rebuild the sender for the resumed session's model %q (%v); resuming on %q\n", sess.Model, serr, resolvedModel)
						} else {
							llmSender = newSender
							a.SetSender(llmSender)
							a.Model = m
							// Compaction's lite model was inferred for the
							// config default above; re-infer it on the session's
							// own sender (resumeLite no-ops when the lite is an
							// explicit cfg.Lite entry).
							resumeLite(a, prevSender, p, m, e)
						}
					} else {
						a.Model = m
						resumeLite(a, prevSender, p, m, e)
					}
				}
				// The startup banner (above) printed the config-default
				// resolution; when a resumed session overrode it, say so under
				// --verbose so the line matches the wire.
				if resolveVerbosity(*quietFlag, *verboseFlag).verbose() &&
					(provName != startProvider || resolvedModel != startModel || entry.BaseURL != startEntry.BaseURL) {
					fmt.Fprintf(stderr, "octo: resumed session: provider=%s model=%s endpoint=%s\n",
						provName, resolvedModel, effectiveEndpoint(provName, entry))
				}
			}
			// Recompose from the session's raw user layer so base/project/env
			// pick up any changes since the session was created. Rerender the
			// MCP manifest against a.Model (may differ from resolvedModel — a
			// saved session can override it above) so the Tool Search
			// activation gate matches the model actually in use.
			a.System, a.LeanSystem = prompt.ComposePair(sess.System, cwd, env, skillsManifest, tools.MCPManifestFor(a.Model, agentProfile), memInjection, coauthor, agentProfile != nil && agentProfile.SystemPrompt != "")
		} else {
			sess = agent.NewSession(resolvedModel, *system)
			sess.Bind(agent.EntryTUI, false)
			// The directory a TUI session was started in is part of its
			// identity: `octo -c` only offers sessions belonging to the
			// current directory (see sessionsForDir), and every other surface
			// resolves the session's tool cwd from this field
			// (Server.resolveSessionDir) instead of falling back to wherever
			// `octo serve` happened to be launched. Without it, continuing
			// this session on the web ran its tools somewhere else entirely.
			//
			// Only on a fresh session: a resumed one already carries whatever
			// identity it was created with, and the resume path above has
			// already relocated cwd to its project's directory if it has one.
			if err := sess.SetWorkingDir(cwd); err != nil {
				slog.Debug("could not record the session's working directory", "err", err)
			}
			if agentProfileID != "" {
				sess.AgentID = agentProfileID
				// An expert agent profile with its own system prompt
				// replaces the server base prompt. Persist in the session
				// so resume recomposes with the right persona.
				if agentProfile != nil && agentProfile.SystemPrompt != "" {
					sess.System = agentProfile.SystemPrompt

					// a.System was set at startup from the server base
					// prompt (line ~946). Re-compose with the expert
					// prompt and skip identity layers so the expert's
					// persona isn't polluted by soul.md/user.md.
					a.System, a.LeanSystem = prompt.ComposePair(sess.System, cwd, env, skillsManifest, tools.MCPManifestFor(a.Model, agentProfile), memInjection, coauthor, true)
				}
			}
		}
		wireSessionHooks(a, sess, agent.EntryTUI)
		// Session goals: the session is the durable goal record and the
		// accountant; the goal tools reach it through the process-global store
		// (one session per CLI process). The headless one-shot below stays
		// unwired — its session is never persisted, so a goal could not
		// outlive the single turn.
		if gcfg, gerr := config.Load(); gerr == nil && gcfg.GoalEnabled() {
			tools.SetGoalStore(sess)
			defer tools.SetGoalStore(nil)
			a.GoalAcct = sess
		}
		// Persisted (non-ephemeral) sessions archive folded turns so the model
		// can recall them with the read tool after a compaction.
		if !*noSave {
			if dir, err := sess.ChunkDir(); err == nil {
				a.ArchiveDir = dir
			}
		}

		cfg := replConfig{
			a:               a,
			session:         sess,
			noSave:          *noSave,
			suggest:         suggestEnabled(*noSuggest),
			plain:           *plain,
			verbosity:       resolveVerbosity(*quietFlag, *verboseFlag),
			stdin:           stdin,
			stdout:          stdout,
			stderr:          stderr,
			skillReg:        skillReg,
			memDir:          memDir,
			cwd:             cwd,
			reader:          replReader, // shared with the asker / permission gate
			view:            replView,   // same surface for turn render + Ask prompts
			permEngine:      permEngine,
			mcpBoot:         mcpBoot, // nil unless tools on with servers configured
			modelName:       resolvedModel,
			reasoningEffort: resolvedEffort,
			providerName:    provName,
			configEntry:     entry,
			showReasoning:   resolvedShowReasoning,
			notify:          cfg.NotifyEnabled(),
			terminalTitle:   cfg.TerminalTitleEnabled(),
		}
		// A fresh TUI session joins a project for the directory it works in,
		// which is what scopes its memory to that directory on every surface
		// (Server.sessionMemDir) rather than only in this process. Waits for
		// the first save so no project is created for a session that never
		// said anything; a failure here is left to the reconciliation pass the
		// next `octo serve` start runs (adoptTaskWorkingDirs), which is why it
		// only whispers into the debug log — the TUI owns the screen.
		if !*noSave && resumeID == "" {
			sid, projectDir := sess.ID, cwd
			cfg.afterFirstSave = func() {
				if err := server.EnsureProjectForDir(projectDir, sid); err != nil {
					slog.Debug("could not file the session under a project", "dir", projectDir, "err", err)
				}
			}
		}
		// Redo the "# Available MCP tools" system-prompt layer once the
		// background MCP connect (mcpBoot, see mcpReadyMsg in tuirepl.go)
		// completes — at the compose calls above, no server has connected yet
		// so tools.MCPManifestFor always returned "". a.Model is read at call
		// time (not resolvedModel) so a mid-session model switch still gets
		// the right activation threshold. sess.System is the raw user layer
		// for both a fresh session (NewSession stores *system into it) and a
		// resumed one (loaded from disk), so it's correct in either case.
		cfg.recomposeMCPManifest = func() {
			a.System, a.LeanSystem = prompt.ComposePair(sess.System, cwd, env, skillsManifest, tools.MCPManifestFor(a.Model, agentProfile), memInjection, coauthor, agentProfile != nil && agentProfile.SystemPrompt != "")
		}
		// Backs /reload: re-renders every layer that can go stale mid-session
		// (skills manifest, MCP manifest, memory injection) and re-composes,
		// rather than just the MCP layer above. skillReg.Reload() rescans
		// ~/.octo/skills and ./.octo/skills so a skill installed after this
		// session started is picked up.
		cfg.recomposeSystemPrompt = func() {
			skillReg.Reload()
			skillsManifest = tools.SkillsManifest(skillReg)
			if memDir != "" {
				memInjection = memory.RenderInjection(memDir, homeMemDir)
			}
			if g := tools.MemoryBackendGuidance(); g != "" {
				memInjection = strings.TrimSpace(memInjection + "\n\n" + g)
			}
			a.System, a.LeanSystem = prompt.ComposePair(sess.System, cwd, env, skillsManifest, tools.MCPManifestFor(a.Model, agentProfile), memInjection, coauthor, agentProfile != nil && agentProfile.SystemPrompt != "")
		}
		if toolsOn {
			// Built-ins only at first paint — the MCP registry is still nil
			// (mcpBoot connects it in the background). mcpReadyMsg recomputes
			// this list once the servers are live.
			cfg.tools = tools.DefaultToolsFor(resolvedModel)
			cfg.executor = toolExecutor
			cfg.subAgentMgr = subAgentMgr
		}
		// First run with a working model but no identity yet: automatically
		// start the onboarding ceremony so a new CLI user isn't left with an
		// impersonal agent — mirroring the web's soul_setup nudge. The config
		// wizard above only sets the key/model; onboard is who-it-is/who-you-are.
		// shouldAutoOnboard writes the one-shot marker as it decides, so an
		// interrupted first run can't retrigger /onboard on the next startup
		// (#1660) — the marker lands before the TUI even takes over.
		if shouldAutoOnboard() {
			cfg.autoFirstInput = "/onboard"
		}
		return runTUI(cfg)
	}

	// ── Headless one-shot (claude -p) ─────────────────────────────────────────
	// One agentic turn, then exit. The session is ephemeral — one-shot runs are
	// not persisted (resuming with -c stays a TUI affordance).
	//
	// Workflows run foreground-blocking here: the process exits when the turn
	// ends (killing background work), so a detached run could never deliver its
	// result. Set before DefaultToolsFor below — the workflow tool's description
	// advertises which contract the model gets.
	tools.SetWorkflowForeground(true)
	oneShotSess := agent.NewSession(resolvedModel, *system)
	if agentProfileID != "" {
		if agentProfile != nil && agentProfile.SystemPrompt != "" {
			oneShotSess.System = agentProfile.SystemPrompt
			// Re-compose a.System with the expert prompt and skip
			// identity layers (soul.md/user.md don't belong here).
			a.System, a.LeanSystem = prompt.ComposePair(oneShotSess.System, cwd, env, skillsManifest, tools.MCPManifestFor(a.Model, agentProfile), memInjection, coauthor, true)
		}
		oneShotSess.AgentID = agentProfileID
	}
	wireSessionHooks(a, oneShotSess, agent.EntryCLI)
	replCfg := replConfig{
		a:               a,
		session:         oneShotSess,
		noSave:          true,
		plain:           *plain,
		verbosity:       resolveVerbosity(*quietFlag, *verboseFlag),
		stdin:           stdin,
		stdout:          stdout,
		stderr:          stderr,
		skillReg:        skillReg,
		memDir:          memDir,
		reader:          replReader,
		view:            replView,
		permEngine:      permEngine,
		modelName:       resolvedModel,
		reasoningEffort: resolvedEffort,
		providerName:    provName,
		configEntry:     entry,
	}
	if toolsOn {
		// Build a context with the profile store + agent ID so
		// DefaultToolsForProfile filters the tool allowlist. The CLI is
		// single-session, so this static filtering at startup is sufficient.
		toolCtx := context.Background()
		if agentProfileID != "" && agentStore != nil {
			toolCtx = tools.WithSessionAgentID(toolCtx, agentProfileID)
			toolCtx = tools.WithProfileStore(toolCtx, agentStore)
		}
		replCfg.tools = tools.DefaultToolsForProfile(toolCtx, resolvedModel)
		replCfg.executor = toolExecutor
		replCfg.subAgentMgr = subAgentMgr
	}
	return runOnce(replCfg, oneShotPrompt, *stream)
}

// printUsageLine writes a one-line token/cache summary to w when the backend
// reported any usage. Goes to stderr so it doesn't pollute piped stdout.
func printUsageLine(w io.Writer, reply agent.Reply) {
	if reply.InputTokens == 0 && reply.OutputTokens == 0 && reply.CacheReadTokens == 0 {
		return
	}
	fmt.Fprintf(w, "[usage] in %d / out %d / cache %d read, %d write\n",
		reply.InputTokens, reply.OutputTokens, reply.CacheReadTokens, reply.CacheWriteTokens)
}

// senderTuning carries the optional reasoning/thinking knobs a caller wants on
// the sender. The zero value (no extended reasoning) is what the IM bridge and
// server use.
type senderTuning struct {
	thinkingBudget  int
	reasoningEffort string
	showReasoning   bool
}

// buildSender resolves credentials/endpoint (env-first, then the resolved
// config entry) and returns an agent.Sender built through internal/app — the
// single place that constructs provider clients. On a configuration error it
// writes a user-facing message to stderr and returns a non-nil error. A fresh
// prompt-cache key is generated per call.
func buildSender(name string, entry config.ModelEntry, stderr io.Writer, tuning senderTuning) (agent.Sender, error) {
	apiKey, err := resolveAPIKey(name, entry, stderr)
	if err != nil {
		return nil, err
	}
	// Protocol and Headers matter only for the Custom vendor; they come from
	// the entry when the resolved provider is that entry's provider — see
	// app.EntryConnectionOverrides.
	protocol, headers := app.EntryConnectionOverrides(name, entry)
	s, err := app.NewSender(app.SenderOptions{
		Provider:        name,
		APIKey:          apiKey,
		BaseURL:         resolveBaseURL(name, entry),
		Protocol:        protocol,
		Headers:         headers,
		CacheKey:        newCacheKey(),
		ThinkingBudget:  tuning.thinkingBudget,
		ReasoningEffort: tuning.reasoningEffort,
		ShowReasoning:   tuning.showReasoning,
	})
	if err != nil {
		fmt.Fprintf(stderr, "octo: %v\n", err)
		return nil, err
	}
	return s, nil
}

// resolveAPIKey returns the API key for the requested vendor: the provider's
// env var, else the resolved entry's stored key when it targets this same
// provider. On a missing key it prints provider-specific setup help to stderr
// and returns a non-nil error.
func resolveAPIKey(name string, entry config.ModelEntry, stderr io.Writer) (string, error) {
	if !app.IsKnownVendor(name) {
		fmt.Fprintf(stderr, "octo: unknown provider %q\n", name)
		return "", fmt.Errorf("unknown provider %q", name)
	}

	envVar := app.VendorAPIKeyEnvVar(name)
	apiKey := os.Getenv(envVar)
	if apiKey == "" && entry.Provider == name {
		apiKey = entry.APIKey
	}
	if apiKey == "" {
		fmt.Fprintf(stderr, "octo: %s is not set.\n", envVar)
		fmt.Fprintln(stderr, "")
		fmt.Fprintf(stderr, "To use %s:\n", app.VendorDisplayName(name))
		step := 1
		if url := app.VendorWebsiteURL(name); url != "" {
			fmt.Fprintf(stderr, "  %d. Get a key at %s\n", step, url)
			step++
		}
		fmt.Fprintf(stderr, "  %d. export %s=sk-...\n", step, envVar)
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Or run `octo config` to save a default provider/key.")
		fmt.Fprintln(stderr, "")
		return "", fmt.Errorf("%w: %s", errMissingAPIKey, envVar)
	}
	return apiKey, nil
}

// newCacheKey returns a random hex token used as the prompt-cache key for one
// conversation/process. Falls back to a timestamp if the system RNG is
// unavailable (still stable for the process).
func newCacheKey() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("octo-%d", time.Now().UnixNano())
	}
	return "octo-" + hex.EncodeToString(b[:])
}

// agentUserDir is the user-level profile directory (~/.octo/agents).
func agentUserDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "agents")
}

// profileIDs returns the IDs of all non-builtin profiles in the store.
func profileIDs(store *agentprofile.Store) []string {
	ids := make([]string, 0, len(store.List()))
	for _, p := range store.List() {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}
