// Package permission gates tool calls through a rule-driven decision
// engine. Each tool invocation is evaluated against the tool's rule list;
// matches are resolved by tier — deny beats ask beats allow, regardless of
// declaration order. If no rule matches in any tier, the implicit default
// is ask.
//
// The engine is the centerpiece of M6.5 — without it, any caller that
// reaches the agent (CLI, future M8 HTTP server, future M9 IM bridge)
// can drive arbitrary tools, including `terminal: rm -rf`. With it,
// CLI users get an interactive prompt for risky operations and remote
// callers are denied unless explicitly whitelisted in ~/.octo/permissions.yml.
//
// Embedding rules:
//
//	The package ships an embedded defaults.yml that's loaded when no user
//	config is present. The defaults aim for "safe and usable" — common
//	tools like ls / git status auto-allow, dangerous operations like
//	rm -rf / sudo trigger ask, and credential-adjacent paths (~/.ssh/*,
//	.env*, /etc/*) always deny.
package permission

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/config"
	"gopkg.in/yaml.v3"
)

// Decision is the outcome of a permission check.
type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
	Ask   Decision = "ask"
)

// Mode selects how the engine resolves Ask decisions.
//
// Interactive (CLI default) returns Ask as-is so the caller can prompt the
// user. AutoApprove converts Ask → Allow, so no interactive prompt is shown.
// Strict converts Ask → Deny — the unattended posture (evals, the IM channel
// bridge, scripted runs) where nobody can answer a prompt and blanket
// approval is unacceptable. Allow / Deny pass through unchanged in all modes.
type Mode string

const (
	ModeInteractive Mode = "interactive"
	ModeAutoApprove Mode = "auto"
	ModeStrict      Mode = "strict"
)

// ResolveDefaultMode reads the global default permission mode from
// ~/.octo/config.yml. This is the value a brand-new session snapshots at
// creation time (see agent.Session.PermissionMode) — once a session has its
// own mode, it never calls back into this. An unset or unrecognized value
// falls back to ModeInteractive, matching New's own zero-value default.
func ResolveDefaultMode() Mode {
	cfg, _ := config.Load()
	switch cfg.PermissionMode {
	case string(ModeAutoApprove):
		return ModeAutoApprove
	case string(ModeStrict):
		return ModeStrict
	default:
		return ModeInteractive
	}
}

// ResolveUnattendedDefaultMode is ResolveDefaultMode for sessions that run
// with nobody present to answer an ask prompt — currently cron task
// sessions (see tasks_handlers.go's CreateSession). An explicit
// ~/.octo/config.yml `permission_mode` is honored exactly like
// ResolveDefaultMode; only the "nothing configured" fallback differs. Since
// write_file/edit_file no longer blanket-allow $CWD (see defaults.yml),
// ModeInteractive's implicit ask has no one to answer it — every write
// would time out to deny — so an unset global mode resolves to
// ModeAutoApprove here instead.
func ResolveUnattendedDefaultMode() Mode {
	cfg, _ := config.Load()
	switch cfg.PermissionMode {
	case string(ModeAutoApprove):
		return ModeAutoApprove
	case string(ModeStrict):
		return ModeStrict
	case string(ModeInteractive):
		return ModeInteractive
	default:
		return ModeAutoApprove
	}
}

// Rule is one entry in the rule list for a tool. Exactly one of Pattern /
// Hostname / Path is populated, depending on the tool's natural axis.
type Rule struct {
	Decision Decision

	// Pattern is a case-sensitive substring match against the relevant
	// input field. For `terminal` that's input["command"]; for the
	// pattern-free tools (glob / grep / web_search) Pattern is empty and
	// the rule matches unconditionally. A leading `^` makes the match
	// command-position anchored — see patternMatches.
	Pattern string

	// Hostname is a list of glob patterns matched against URL hosts.
	// Used by web_fetch. `*` matches a single DNS label, so `*.dev`
	// matches `foo.dev` but not `foo.bar.dev`.
	Hostname []string

	// Path is a list of glob patterns matched against file paths.
	// Used by write_file / edit_file / read_file. `**` matches any
	// number of directory components; `$CWD` expands to the engine's
	// configured working directory at construction time.
	Path []string
}

// RuleSet maps tool name → ordered list of rules.
type RuleSet map[string][]Rule

// lastGoodRules caches, per configPath, the most recently successfully
// parsed *user* rule overrides (never the merged-with-defaults RuleSet New
// builds) — see New's fallback logic.
var lastGoodRules struct {
	mu    sync.Mutex
	rules map[string]RuleSet
}

func cachedUserRules(configPath string) (RuleSet, bool) {
	lastGoodRules.mu.Lock()
	defer lastGoodRules.mu.Unlock()
	rules, ok := lastGoodRules.rules[configPath]
	return rules, ok
}

func setCachedUserRules(configPath string, rules RuleSet) {
	lastGoodRules.mu.Lock()
	defer lastGoodRules.mu.Unlock()
	if lastGoodRules.rules == nil {
		lastGoodRules.rules = make(map[string]RuleSet)
	}
	lastGoodRules.rules[configPath] = rules
}

func clearCachedUserRules(configPath string) {
	lastGoodRules.mu.Lock()
	defer lastGoodRules.mu.Unlock()
	delete(lastGoodRules.rules, configPath)
}

// Engine evaluates rules against tool calls and remembers session-level
// decisions for "always allow this" style answers.
type Engine struct {
	rules RuleSet
	cwd   string

	modeMu sync.RWMutex
	mode   Mode

	mu       sync.Mutex
	remember *Remembered // session decisions; swappable via AttachRemembered
}

//go:embed defaults.yml
var defaultsYAML []byte

// New builds an Engine. configPath may be empty (use embedded defaults)
// or point to a YAML file overriding them. cwd is the working directory
// used to expand `$CWD` in path rules — pass os.Getwd() unless overriding
// for tests. allowWriteRoots are extra directories (e.g. the per-repo memory
// directory, which lives outside CWD) whose contents the agent may write/edit
// without a prompt; each gets a prepended allow rule on write_file / edit_file.
//
// If configPath fails to read or parse but a previous call for the same path
// already loaded successfully, New falls back to those last known good user
// rules instead of erroring — see the fallback logic below. Only the very
// first call for a given path, before anything has loaded successfully,
// returns the raw error.
func New(configPath string, cwd string, mode Mode, allowWriteRoots ...string) (*Engine, error) {
	rules, err := loadRules(defaultsYAML)
	if err != nil {
		return nil, fmt.Errorf("permission: parse default rules: %w", err)
	}

	if configPath != "" {
		raw, readErr := os.ReadFile(configPath)
		switch {
		case readErr == nil:
			user, parseErr := loadRules(raw)
			if parseErr != nil {
				// New is called fresh on every turn (octo serve has no
				// restart hook for a config change to take effect), so with
				// no fallback a single typo saved mid-edit would deny/ask
				// every tool call on every live session the instant the
				// file hits disk — permission.New callers treat an error
				// here as "abort the turn", never "run ungated" (see
				// server.go's prepareToolTurn/runChannelTurns). Falling
				// back to the last rules that DID parse keeps serve
				// running on the previous good policy instead.
				if cached, ok := cachedUserRules(configPath); ok {
					slog.Warn("permission: permissions.yml has a parse error, using last known good rules until it's fixed", "path", configPath, "err", parseErr)
					user = cached
				} else {
					return nil, fmt.Errorf("permission: parse %s: %w", configPath, parseErr)
				}
			} else {
				setCachedUserRules(configPath, user)
			}
			// User rules override defaults per-tool. (Full-replace per
			// tool, not append — keeps "I want to allow rm -rf in this
			// environment" simple to express.)
			for tool, rs := range user {
				rules[tool] = rs
			}
		case os.IsNotExist(readErr):
			// Deleted → no override is the correct state, not "temporarily
			// broken"; drop any cached rules so a later parse error against
			// a recreated file doesn't resurrect a deleted config.
			clearCachedUserRules(configPath)
		default:
			if cached, ok := cachedUserRules(configPath); ok {
				slog.Warn("permission: failed to read permissions.yml, using last known good rules until it's fixed", "path", configPath, "err", readErr)
				for tool, rs := range cached {
					rules[tool] = rs
				}
			} else {
				return nil, fmt.Errorf("permission: read %s: %w", configPath, readErr)
			}
		}
	}

	if cwd == "" {
		// Reasonable fallback; rules using $CWD against a missing cwd
		// will just not match anything.
		cwd, _ = os.Getwd()
	}
	if mode == "" {
		mode = ModeInteractive
	}

	// Whitelist extra writable roots (e.g. the memory directory, or `octo
	// init`'s own cwd) with an allow rule so it wins over the implicit
	// ask-fallthrough. List position no longer matters — classify() resolves
	// deny > ask > allow by tier — so a secret-path deny elsewhere in the
	// list still wins over this even though it's prepended; these roots are
	// octo-managed dirs (or, for init, the analyzed repo), not places
	// secrets live, so that never comes up in practice.
	for _, root := range allowWriteRoots {
		if root == "" {
			continue
		}
		allow := Rule{Decision: Allow, Path: []string{root, root + "/**"}}
		rules["write_file"] = append([]Rule{allow}, rules["write_file"]...)
		rules["edit_file"] = append([]Rule{allow}, rules["edit_file"]...)
	}

	// Apply non-overrideable hardcoded guards last. These protect the host OS
	// from catastrophic data loss even if a user (or a malicious LLM acting
	// through a user's permissions.yml) tries to relax the defaults. They are
	// appended after the user rules and after allowWriteRoots, but because
	// classify() resolves deny > ask > allow the hardcoded denies still win.
	applyHardcodedDenyRules(rules)

	return &Engine{
		rules:    rules,
		mode:     mode,
		cwd:      cwd,
		remember: NewRemembered(),
	}, nil
}

// Mode returns the engine's current mode (for callers that need to branch
// on it, e.g. to decide whether to prompt the user).
func (e *Engine) GetMode() Mode {
	e.modeMu.RLock()
	defer e.modeMu.RUnlock()
	return e.mode
}

// SetMode changes the engine's mode at runtime (e.g. TUI shortcut).
func (e *Engine) SetMode(mode Mode) {
	e.modeMu.Lock()
	defer e.modeMu.Unlock()
	e.mode = mode
}

// Check evaluates the rules for one tool invocation. Returns Allow,
// Deny, or Ask.
//
// Resolution order:
//  1. Rules for the tool are scanned; matches are grouped into deny/ask/allow
//     tiers and the tier wins regardless of declaration order — deny beats
//     ask beats allow, matching what a rule author expects rather than
//     depending on list position.
//  2. No match in any tier → implicit Ask.
//  3. Session-remembered decision (set by Remember) short-circuits an Ask.
//  4. Mode adjustment: ModeAutoApprove turns Ask into Allow; ModeStrict
//     turns Ask into Deny.
func (e *Engine) Check(toolName string, input map[string]any) Decision {
	sig := signature(toolName, e.cwd, input)

	deny, ask, allow := e.classify(toolName, input)
	d := Ask // implicit default
	switch {
	case deny != nil:
		d = Deny
	case ask != nil:
		d = Ask
	case allow != nil:
		d = Allow
	}
	// A matched deny always wins. The remembered store only short-circuits
	// Ask: engines are rebuilt per turn precisely so policy edits take
	// effect mid-session, and a deny rule added after the user said
	// "always allow" must not be bypassed by the cache.
	if d == Deny {
		return e.applyMode(d)
	}

	e.mu.Lock()
	store := e.remember
	e.mu.Unlock()
	if cached, ok := store.get(sig); ok {
		return e.applyMode(cached)
	}
	return e.applyMode(d)
}

// classify scans the tool's rule list once and returns the first matching
// rule in each decision tier (deny/ask/allow) — nil where a tier had no
// match. Callers resolve deny > ask > allow priority from the result,
// independent of the order rules were declared in.
func (e *Engine) classify(toolName string, input map[string]any) (deny, ask, allow *Rule) {
	rules := e.rules[toolName]
	for i := range rules {
		r := &rules[i]
		if !e.matches(toolName, *r, input) {
			continue
		}
		switch r.Decision {
		case Deny:
			if deny == nil {
				deny = r
			}
		case Ask:
			if ask == nil {
				ask = r
			}
		case Allow:
			if allow == nil {
				allow = r
			}
		}
	}
	return deny, ask, allow
}

// Remember stores a session-level decision so a subsequent Check for the
// same (toolName, input-signature) pair short-circuits. Use this when the
// user answers "always allow this turn / this session".
func (e *Engine) Remember(toolName string, input map[string]any, decision Decision) {
	e.mu.Lock()
	store := e.remember
	e.mu.Unlock()
	store.set(signature(toolName, e.cwd, input), decision)
}

// DenialReason returns a human-readable explanation for why a tool call
// was denied. Useful for surfacing to the LLM so it knows the failure
// was a policy denial, not a tool malfunction.
func (e *Engine) DenialReason(toolName string, input map[string]any) string {
	deny, ask, _ := e.classify(toolName, input)
	switch {
	case deny != nil:
		return formatRuleReason(toolName, *deny)
	case ask != nil:
		return formatRuleReason(toolName, *ask)
	}
	if e.mode == ModeStrict {
		return fmt.Sprintf("permission_denied: %s — requires confirmation, and strict mode denies without prompting.", toolName)
	}
	return fmt.Sprintf("permission_denied: %s — user declined the interactive prompt.", toolName)
}

// applyMode adjusts Ask decisions based on the engine mode:
//   - ModeAutoApprove: Ask → Allow
//   - ModeStrict: Ask → Deny
//   - ModeInteractive: Ask passes through unchanged
func (e *Engine) applyMode(d Decision) Decision {
	if d != Ask {
		return d
	}
	e.modeMu.RLock()
	defer e.modeMu.RUnlock()
	switch e.mode {
	case ModeAutoApprove:
		return Allow
	case ModeStrict:
		return Deny
	default:
		return d
	}
}

// matches reports whether rule r applies to the tool invocation. The
// matching axis (pattern / hostname / path) is chosen by which field of r
// is populated.
func (e *Engine) matches(toolName string, r Rule, input map[string]any) bool {
	switch {
	case len(r.Hostname) > 0:
		// web_fetch input["url"]
		raw, _ := input["url"].(string)
		host := parseHost(raw)
		if host == "" {
			return false
		}
		for _, pat := range r.Hostname {
			if globMatch(pat, host) {
				return true
			}
		}
		return false

	case len(r.Path) > 0:
		// write_file / edit_file / read_file input["path"]
		raw, _ := input["path"].(string)
		if raw == "" {
			return false
		}
		abs := absPath(raw, e.cwd)
		for _, pat := range r.Path {
			expanded := expandCWD(pat, e.cwd)
			if pathMatch(expanded, abs) {
				return true
			}
		}
		return false

	default:
		// Empty pattern matches unconditionally (used for "default
		// allow / default deny" entries on pattern-free tools).
		if r.Pattern == "" {
			return true
		}
		// terminal input["command"], otherwise stringified input.
		cmd, _ := input["command"].(string)
		if cmd == "" {
			// Fall back to substring search across all string fields,
			// so a rule like { pattern: ".env" } on a hypothetical tool
			// that exposes the file via input["file"] still works.
			cmd = stringifyInput(input)
		}
		// Allow rules on terminal must only match simple commands. A
		// substring match on "ls" would otherwise allow "ls && ./pwn".
		if r.Decision == Allow {
			return allowPatternMatches(cmd, r.Pattern)
		}
		return patternMatches(cmd, r.Pattern)
	}
}

// patternMatches reports whether cmd triggers a terminal pattern rule. The
// documented semantics are a case-sensitive substring match, with two
// refinements:
//
//   - A pattern ending in a filesystem-root marker (`/` or `~`) only matches
//     when that marker sits at an argument boundary — i.e. the command
//     targets root/home itself, not a path beneath it. Without this,
//     `deny: rm -rf /` (intended to block a root wipe) is a literal substring
//     of every absolute-path delete like `rm -rf /Users/me/project`,
//     hard-denying legitimate operations that should instead fall through to
//     the `ask: rm -rf` rule.
//
//   - A pattern starting with `^` is command-position anchored: it only
//     matches where a command word can begin (see anchoredPatternMatches).
//     Plain substring matching is wrong for command-name rules — a
//     `deny: "format "` substring would hit `docker ps --format json`, and
//     `deny: "shutdown "` would hit `git commit -m "fix shutdown handling"`.
func patternMatches(cmd, pattern string) bool {
	if pattern == "" {
		return true
	}
	if pattern[0] == '^' {
		return anchoredPatternMatches(cmd, pattern[1:])
	}
	if last := pattern[len(pattern)-1]; last != '/' && last != '~' {
		return strings.Contains(cmd, pattern)
	}
	// Boundary-anchored: the character after a match must terminate the
	// argument (end of command, whitespace, or a `*` glob), otherwise the
	// match is a longer path under root/home and the rule does not apply.
	from := 0
	for {
		i := strings.Index(cmd[from:], pattern)
		if i < 0 {
			return false
		}
		end := from + i + len(pattern)
		if end >= len(cmd) || isArgBoundary(cmd[end]) {
			return true
		}
		from += i + 1
	}
}

// anchoredPatternMatches implements the `^` pattern form. The match must
// start at a command position (see isCommandPosition) and end at a word
// boundary, so `^shutdown` matches `shutdown -h now`, `sudo shutdown`, and
// `echo hi; shutdown`, but not `git commit -m "fix shutdown handling"`
// (argument text), `docker ps --format json` (a flag, for `^format`), or
// `kill -9 -1234` (for `^kill -9 -1` — the word continues).
//
// A pattern ending in `/` or `~` keeps the root-marker end-anchoring of the
// substring form, so `^rm -rf /` still refuses only a root wipe while
// `rm -rf /path/under` falls through.
func anchoredPatternMatches(cmd, pattern string) bool {
	pattern = strings.TrimRight(pattern, " \t")
	if pattern == "" {
		return false
	}
	rootAnchored := pattern[len(pattern)-1] == '/' || pattern[len(pattern)-1] == '~'
	from := 0
	for {
		i := strings.Index(cmd[from:], pattern)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(pattern)
		endOK := true
		switch {
		case rootAnchored:
			endOK = end >= len(cmd) || isArgBoundary(cmd[end])
		case isWordChar(pattern[len(pattern)-1]):
			endOK = end >= len(cmd) || !isWordChar(cmd[end])
		}
		if endOK && isCommandPosition(cmd, start) {
			return true
		}
		from = start + 1
	}
}

// isCommandPosition reports whether a token starting at cmd[i] sits where a
// command word can begin: at the start of the command, right after a shell
// chain operator, at the basename of a path invocation (`/sbin/reboot`), or
// after transparent prefix tokens — command wrappers like sudo/env/xargs,
// flags, and VAR=value assignments — that themselves sit at a command
// position. Everything else (ordinary arguments, quoted strings, flag
// values) is not a command position.
func isCommandPosition(cmd string, i int) bool {
	j := i
	for j > 0 && (cmd[j-1] == ' ' || cmd[j-1] == '\t') {
		j--
	}
	if j == 0 {
		return true
	}
	c := cmd[j-1]
	if isChainChar(c) {
		return true
	}
	if j == i && c == '/' {
		// Path invocation: the match is the basename of a path token
		// (`/sbin/reboot`, `sudo /bin/rm …`). Accept when the path token
		// itself starts at a command position.
		k := j - 1
		for k > 0 && !isTokenBreak(cmd[k-1]) {
			k--
		}
		return isCommandPosition(cmd, k)
	}
	if j != i {
		// Whitespace-separated: the preceding token may be a transparent
		// prefix (sudo, a flag, VAR=value, …) that keeps this a command
		// position.
		k := j
		for k > 0 && !isTokenBreak(cmd[k-1]) {
			k--
		}
		if isTransparentPrefix(cmd[k:j]) {
			return isCommandPosition(cmd, k)
		}
	}
	return false
}

// isTransparentPrefix reports whether a token between a command position and
// a candidate match keeps the match at a command position: command wrappers
// that execute their argument, flags (Unix `-x` and cmd.exe `/c` styles),
// and environment assignments.
func isTransparentPrefix(tok string) bool {
	if tok == "" {
		return false
	}
	if tok[0] == '-' || tok[0] == '/' {
		return true
	}
	if strings.Contains(tok, "=") {
		return true
	}
	switch strings.ToLower(tok) {
	case "sudo", "doas", "env", "nohup", "exec", "xargs", "setsid", "command",
		"eval", "builtin", "nice", "time", "stdbuf", "timeout",
		"cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "sh", "bash", "zsh", "busybox":
		return true
	}
	return false
}

// isChainChar reports whether c starts a new shell command (chain operator,
// subshell, command substitution, or newline).
func isChainChar(c byte) bool {
	return c == ';' || c == '|' || c == '&' || c == '(' || c == '`' || c == '\n'
}

// isTokenBreak reports whether c separates shell tokens for the purposes of
// isCommandPosition's walk-back.
func isTokenBreak(c byte) bool {
	return c == ' ' || c == '\t' || isChainChar(c)
}

// isWordChar reports whether c can continue a command word; a `^` pattern
// ending in a word char must be followed by a non-word char (or the end of
// the command), so `^reboot` does not match `rebooter`.
func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// isArgBoundary reports whether c ends a shell argument for the purposes of
// patternMatches' root/home anchoring.
func isArgBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '*' ||
		c == ';' || c == '&' || c == '|' || c == '(' || c == ')'
}

// shellControlChars lists characters that start a new command or shell
// construct. Allow rules must never match a command containing any of them,
// otherwise "ls && ./untrusted" would be auto-approved by the "ls" rule.
var shellControlChars = []byte(";|&$()<>" + "`" + "\n")

// containsShellControl reports whether cmd contains a shell metacharacter
// that would let one command chain into another.
func containsShellControl(cmd string) bool {
	for i := 0; i < len(cmd); i++ {
		for _, c := range shellControlChars {
			if cmd[i] == c {
				return true
			}
		}
	}
	return false
}

// allowPatternMatches is the stricter matcher used for terminal allow rules.
// It requires the command to start with the pattern (after optional leading
// whitespace), the pattern to end at a command boundary, and the whole
// command to contain no shell chaining metacharacters.
func allowPatternMatches(cmd, pattern string) bool {
	cmd = strings.TrimLeft(cmd, " \t\n")
	// Allow rules are already prefix-anchored; tolerate the `^` marker so a
	// user who writes it out of habit doesn't create a dead rule.
	pattern = strings.TrimPrefix(pattern, "^")
	if pattern == "" {
		return true
	}
	// Trailing whitespace in the pattern is a stylistic way to avoid prefix
	// matches (e.g. "cat " vs "catapult"). For boundary checking we ignore it.
	pattern = strings.TrimRight(pattern, " \t\n")
	if !strings.HasPrefix(cmd, pattern) {
		return false
	}
	if containsShellControl(cmd) {
		return false
	}
	// The character after the pattern must be a boundary (end of command or
	// start of arguments), not a continuation of the command word.
	if end := len(pattern); end < len(cmd) && !isArgBoundary(cmd[end]) {
		return false
	}
	return true
}

// signature returns a stable hash of (tool, input) for the remember
// cache. Two calls with the same logical input hit the same cache slot
// regardless of map iteration order, since json.Marshal sorts keys.
//
// write_file/edit_file are keyed on the path alone, not the full input:
// their input also carries the new content/diff, which differs on every
// call, so hashing it would mean "always allow" never hits the cache a
// second time. Remembering per-path instead matches an editor's "don't ask
// again for this file" — one confirmation per file per session, not one per
// edit. The path is resolved against cwd the same way matches() does (via
// absPath), so "src/main.go" and "/work/src/main.go" — two spellings of the
// same file — share one cache slot instead of prompting twice.
func signature(toolName string, cwd string, input map[string]any) string {
	keyed := input
	if toolName == "write_file" || toolName == "edit_file" {
		if p, ok := input["path"].(string); ok && p != "" {
			keyed = map[string]any{"path": absPath(p, cwd)}
		}
	}
	b, _ := json.Marshal(keyed)
	h := sha256.Sum256(append([]byte(toolName+"|"), b...))
	return hex.EncodeToString(h[:16])
}

// parseHost extracts the lowercased host from a URL. Returns "" for
// unparseable inputs.
func parseHost(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	return strings.ToLower(host)
}

// globMatch tests pattern against name. `*` matches any sequence of
// characters (including dots) and `?` matches a single character. Same
// semantics filesystem globs use, so users don't have to learn a new
// dialect for permissions.yml.
//
// Note that `*` in a hostname rule like `*.github.com` therefore matches
// both `raw.github.com` and `a.b.github.com`. That's acceptable for our
// allow-list use case: the suffix anchor (`.github.com`) is still
// enforced, so `foo.github.com.attacker.example` does NOT match.
func globMatch(pattern, name string) bool {
	if pattern == name {
		return true
	}
	return globMatchStateMachine(pattern, name)
}

func globMatchStateMachine(pattern, name string) bool {
	pi, ni := 0, 0
	starPi, starNi := -1, 0
	for ni < len(name) {
		switch {
		case pi < len(pattern) && (pattern[pi] == name[ni] || pattern[pi] == '?'):
			pi++
			ni++
		case pi < len(pattern) && pattern[pi] == '*':
			starPi = pi
			starNi = ni
			pi++
		case starPi != -1:
			pi = starPi + 1
			starNi++
			ni = starNi
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// pathMatch reports whether path is selected by glob. Supports `**` for
// recursive directory matching (so `$CWD/**` matches any file under the
// working dir). Single `*` matches within a single path component.
func pathMatch(glob, path string) bool {
	// Fast path: exact equality.
	if glob == path {
		return true
	}
	// Translate `**` to a sentinel, then split on `/`, then re-substitute.
	parts := strings.Split(glob, "/")
	pathParts := strings.Split(path, "/")
	return matchParts(parts, pathParts)
}

func matchParts(pattern, path []string) bool {
	switch {
	case len(pattern) == 0:
		return len(path) == 0
	case pattern[0] == "**":
		// `**` consumes zero or more path components.
		for i := 0; i <= len(path); i++ {
			if matchParts(pattern[1:], path[i:]) {
				return true
			}
		}
		return false
	case len(path) == 0:
		// Pattern still has parts (and the first isn't **) — no match.
		return false
	default:
		if !globMatchStateMachine(pattern[0], path[0]) {
			return false
		}
		return matchParts(pattern[1:], path[1:])
	}
}

// expandCWD replaces a leading `$CWD` with the engine's working directory.
func expandCWD(pat, cwd string) string {
	if strings.HasPrefix(pat, "$CWD") {
		return cwd + pat[len("$CWD"):]
	}
	return pat
}

// absPath resolves rel against cwd if it isn't already absolute. Output
// is always slash-separated regardless of host OS, so rule patterns
// (which are always written with `/`) match consistently on Windows.
//
// Symlink expansion is intentionally NOT performed — we want rules to
// match the path the user/LLM typed, not its canonical form, so a rule
// like `deny: /etc/**` blocks an attempt to write `/etc/passwd` via a
// symlink-traversal trick (the deny still applies on the literal path,
// even though the underlying file is elsewhere).
func absPath(rel, cwd string) string {
	rel = filepath.ToSlash(rel)
	cwd = filepath.ToSlash(cwd)
	// Windows drive-letter absolute (`C:/...`).
	if len(rel) >= 3 && rel[1] == ':' && rel[2] == '/' {
		return path.Clean(rel)
	}
	if path.IsAbs(rel) {
		return path.Clean(rel)
	}
	return path.Clean(cwd + "/" + rel)
}

// stringifyInput renders input map values as a single space-joined
// string. Used as a fallback for tools without a single canonical input
// field.
func stringifyInput(input map[string]any) string {
	var parts []string
	for _, v := range input {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, " ")
}

// formatRuleReason produces a short, structured-enough message that an
// LLM can read it and explain to its user why the call was rejected.
func formatRuleReason(toolName string, r Rule) string {
	switch r.Decision {
	case Deny:
		switch {
		case r.Pattern != "":
			return fmt.Sprintf("permission_denied: %s matched deny rule (pattern: %q). "+
				"This operation is blocked by policy.", toolName, r.Pattern)
		case len(r.Hostname) > 0:
			return fmt.Sprintf("permission_denied: %s matched deny rule (hostname: %v). "+
				"This host is blocked by policy.", toolName, r.Hostname)
		case len(r.Path) > 0:
			return fmt.Sprintf("permission_denied: %s matched deny rule (path: %v). "+
				"This path is blocked by policy.", toolName, r.Path)
		}
	case Ask:
		return fmt.Sprintf("permission_denied: %s matched ask rule but caller is non-interactive. "+
			"Add an explicit allow rule to ~/.octo/permissions.yml.", toolName)
	}
	return fmt.Sprintf("permission_denied: %s rejected.", toolName)
}

// ─── YAML loading ─────────────────────────────────────────────────────────

type rawClause struct {
	Pattern  string   `yaml:"pattern"`
	Hostname []string `yaml:"hostname"`
	Path     []string `yaml:"path"`
}

type rawRule struct {
	Allow *rawClause `yaml:"allow"`
	Deny  *rawClause `yaml:"deny"`
	Ask   *rawClause `yaml:"ask"`
}

// applyHardcodedDenyRules appends engine-level deny rules that cannot be
// overridden by user permissions.yml. They are the last line of defense for
// operations that can destroy the host OS, filesystem, or session state.
func applyHardcodedDenyRules(rules RuleSet) {
	// System directories that should never be written or edited by an agent.
	systemPaths := []string{
		"/bin/**", "/sbin/**",
		"/usr/bin/**", "/usr/sbin/**", "/usr/lib/**", "/usr/lib64/**",
		"/System/**", "/boot/**", "/lib/**", "/lib64/**",
		"/Windows/**", "/Program Files/**", "/Program Files (x86)/**",
		"C:/Windows/**", "C:/Windows/System32/**", "C:/Windows/SysWOW64/**",
		"C:/Program Files/**", "C:/Program Files (x86)/**", "C:/ProgramData/**",
		"D:/Windows/**", "D:/Windows/System32/**", "D:/Windows/SysWOW64/**",
		"D:/Program Files/**", "D:/Program Files (x86)/**", "D:/ProgramData/**",
	}
	deny := Rule{Decision: Deny, Path: systemPaths}
	rules["write_file"] = append(rules["write_file"], deny)
	rules["edit_file"] = append(rules["edit_file"], deny)

	// Terminal commands that can brick the machine or destroy data
	// irrecoverably. Hardcoded so that a user override of terminal rules
	// cannot silently permit them; kept strictly to catastrophes — risky but
	// legitimate tools (dism, fsutil, untargeted recursive deletes, …) live
	// in defaults.yml as ask rules instead.
	//
	// Every pattern is command-position anchored (`^`, see patternMatches):
	// it matches the command word itself — including after sudo/xargs/shell
	// wrappers, chain operators, and path invocations — but not incidental
	// occurrences inside arguments, so `git commit -m "fix shutdown handling"`
	// and `docker ps --format json` are unaffected. The no-trailing-space
	// forms also catch bare invocations (`reboot`, `halt`) that a trailing
	// space would miss.
	hardTerminalDenies := []string{
		// Raw disk / filesystem destruction.
		"^dd if=",                                      // raw disk write
		"^mkfs",                                        // make filesystem (mkfs, mkfs.ext4, …)
		"^fdisk",                                       // partition manipulation
		"^parted",                                      // partition manipulation
		"^format",                                      // Windows filesystem format
		"^diskpart",                                    // Windows partition / volume management
		"^diskutil eraseDisk", "^diskutil eraseVolume", // macOS disk erase
		"^diskutil secureErase", "^diskutil partitionDisk", // macOS wipe / partition
		// System-wide power / process catastrophes.
		"^shutdown", "^poweroff", "^halt", "^init 0", "^reboot",
		"^systemctl poweroff", "^systemctl reboot", "^systemctl halt",
		"^kill -9 -1", "^kill -SIGKILL -1", // kill all user processes
		// Root / home wipe (root-marker end-anchoring keeps subpaths on ask).
		"^rm -rf /", "^rm -rf ~",
		// Windows OS-configuration destruction.
		"^reg delete",      // registry deletion
		"^bcdedit",         // boot configuration
		"^wbadmin delete",  // backup catalog deletion
		"^vssadmin delete", // shadow copy deletion
		"^wevtutil cl",     // event log clear
	}
	// Unix system directory removal — deleting /usr, /bin, /System, … bricks
	// the OS. The pattern is a prefix, so subpaths (`rm -rf /usr/local`) are
	// covered too.
	for _, dir := range []string{
		"/usr", "/bin", "/sbin", "/boot", "/lib", "/lib64",
		"/System", "/Windows", "/Program Files",
	} {
		hardTerminalDenies = append(hardTerminalDenies, "^rm -rf "+dir)
	}
	// Windows system directory removal, across the spellings an LLM emits:
	// rm (the PowerShell Remove-Item alias), cmd.exe builtins, and the other
	// Remove-Item aliases. Each pattern is generated bare and with an opening
	// quote; both are prefixes, so quoted forms, System32/SysWOW64 subpaths,
	// and "Program Files (x86)" are all covered.
	winDirs := []string{`C:\Windows`, `C:\ProgramData`, `C:\Program Files`}
	winDeleteOps := []string{
		"rm -rf", "rmdir /s /q", "rd /s /q", "del /s /q", "erase /s /q",
		"ri -r -fo", "del -r -fo", "erase -r -fo",
	}
	for _, op := range winDeleteOps {
		for _, dir := range winDirs {
			hardTerminalDenies = append(hardTerminalDenies,
				"^"+op+" "+dir,
				"^"+op+` "`+dir)
		}
	}
	for _, pat := range hardTerminalDenies {
		rules["terminal"] = append(rules["terminal"], Rule{Decision: Deny, Pattern: pat})
	}
}

// loadRules parses YAML bytes into a RuleSet. The YAML schema is the one
// used in defaults.yml: top-level keys are tool names, values are lists of
// { allow|deny|ask: { pattern|hostname|path: ... } } entries.
func loadRules(data []byte) (RuleSet, error) {
	var raw map[string][]rawRule
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}

	out := RuleSet{}
	for tool, rules := range raw {
		for i, rr := range rules {
			rule, ok := convertRawRule(rr)
			if !ok {
				return nil, fmt.Errorf("tool %q rule %d: must have exactly one of allow/deny/ask",
					tool, i)
			}
			out[tool] = append(out[tool], rule)
		}
	}
	return out, nil
}

func convertRawRule(rr rawRule) (Rule, bool) {
	set := 0
	out := Rule{}
	if rr.Allow != nil {
		set++
		out.Decision = Allow
		out.Pattern, out.Hostname, out.Path = rr.Allow.Pattern, rr.Allow.Hostname, rr.Allow.Path
	}
	if rr.Deny != nil {
		set++
		out.Decision = Deny
		out.Pattern, out.Hostname, out.Path = rr.Deny.Pattern, rr.Deny.Hostname, rr.Deny.Path
	}
	if rr.Ask != nil {
		set++
		out.Decision = Ask
		out.Pattern, out.Hostname, out.Path = rr.Ask.Pattern, rr.Ask.Hostname, rr.Ask.Path
	}
	return out, set == 1
}
