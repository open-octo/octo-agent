package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// tool is the internal interface every built-in tool implements — both a
// Definition (what the LLM sees) and an Execute (what the agent loop calls).
// External callers of the tools package only need agent.ToolExecutor; this
// interface is private so adding methods later doesn't break consumers.
type tool interface {
	Definition() agent.ToolDefinition
	Execute(ctx context.Context, name string, input map[string]any) (agent.ToolResult, error)
}

// allTools is the canonical, ordered list of built-in tools shipped with
// octo-agent. Adding a tool means a single new entry here — the registry
// scan and the DefaultTools() listing both pick it up automatically.
var allTools = []tool{
	TerminalTool{},
	TerminalOutputTool{},
	TerminalInputTool{},
	KillShellTool{},
	ReadFileTool{},
	WriteFileTool{},
	EditFileTool{},
	ShowArtifactTool{},
	RenderUITool{},
	SendFileTool{},
	SendMessageTool{},
	GlobTool{},
	GrepTool{},
	WebFetchTool{},
	WebSearchTool{},
	SkillTool{},
	AgentTool{},
	AgentSendTool{},
	AgentStatusTool{},
	AgentKillTool{},
	WorkflowTool{},
	WorkflowSaveTool{},
	WorkflowStatusTool{},
	WorkflowKillTool{},
	AskUserQuestionTool{},
	TaskCreateTool{},
	TaskUpdateTool{},
	TaskListTool{},
	GetGoalTool{},
	CreateGoalTool{},
	UpdateGoalTool{},
	RestartServerTool{},
	ScheduleWakeupTool{},
	BrowserTool{},
	ComputerTool{},
	MemoryRecallTool{},
}

// DefaultRegistry is the agent.ToolExecutor used when `octo --tools` is
// enabled. It dispatches each tool call by name to the matching entry in
// allTools, returning a clean error for unknown names.
//
// When tracker is non-nil it enforces read-before-write: write_file /
// edit_file calls to an existing file are refused unless the file was read
// (and is unchanged) this session. The zero value (DefaultRegistry{}) has a
// nil tracker and so enforces nothing — preserved for tests and callers that
// don't want the discipline. Use NewDefaultRegistry (fresh tracker) or
// NewDefaultRegistryWithTracker (caller-supplied, e.g. session-scoped) for
// the enforced variant.
type DefaultRegistry struct {
	tracker *ReadTracker
}

// NewDefaultRegistry returns a registry with read-before-write enforcement
// backed by a fresh ReadTracker. Callers that need the tracker to survive
// across turns (web/IM, keyed by session id) should use
// NewDefaultRegistryWithTracker(tools.SessionReadTracker(id)) instead.
func NewDefaultRegistry() DefaultRegistry {
	return DefaultRegistry{tracker: NewReadTracker()}
}

// NewDefaultRegistryWithTracker returns a registry with read-before-write
// enforcement backed by the given tracker, e.g. a session-scoped tracker
// obtained from SessionReadTracker so reads recorded in one turn are still
// honored in a later turn of the same conversation. A nil tracker behaves
// like the zero value DefaultRegistry{} — enforcement disabled.
func NewDefaultRegistryWithTracker(tracker *ReadTracker) DefaultRegistry {
	return DefaultRegistry{tracker: tracker}
}

// Execute implements agent.ToolExecutor.
func (r DefaultRegistry) Execute(ctx context.Context, name string, input map[string]any) (agent.ToolResult, error) {
	return r.ExecuteStream(ctx, name, input, nil)
}

// ExecuteStream implements agent.StreamingToolExecutor. It dispatches by name
// exactly like Execute, but when progress is non-nil and the target tool
// implements agent.StreamingToolExecutor (currently just TerminalTool), it
// calls the tool's ExecuteStream instead of Execute so callers get
// incremental chunks.
//
// This method is what makes agent.go's executor.(StreamingToolExecutor) type
// assertion succeed for the registry as a whole — that assertion is on the
// single ToolExecutor passed to the agent loop, not on individual tools, so
// without it EventToolProgress never fires for ANY tool even though
// TerminalTool has implemented ExecuteStream since #49 (issue #1094).
func (r DefaultRegistry) ExecuteStream(ctx context.Context, name string, input map[string]any, progress func(chunk string)) (agent.ToolResult, error) {
	// MCP tools land here too — route them first so an "mcp__…" name
	// never falls through to the unknown-tool path. executeMCP returns
	// ok=false when the name isn't ours, then dispatch continues below.
	if res, ok, err := executeMCP(ctx, name, input); ok {
		return res, err
	}

	// Tool Search bridge: describe the deferred MCP catalog, or invoke a tool
	// through mcp_call (which routes back into executeMCP on the real name).
	// Handled here because the bridge tools aren't in allTools. Tool names +
	// one-line descriptions are always visible in the system prompt (see
	// MCPManifestFor) so there's no separate search step.
	switch name {
	case toolDescribeName:
		return execToolDescribe(input)
	case toolCallName:
		return execToolCall(ctx, input)
	}

	// Read-before-write pre-check (skipped when no tracker is configured).
	if r.tracker != nil && (name == "write_file" || name == "edit_file") {
		if path, ok := input["path"].(string); ok {
			if abs, err := resolvePath(path); err == nil {
				if cerr := r.tracker.CheckWritable(abs); cerr != nil {
					return agent.ToolResult{}, cerr
				}
			}
		}
	}

	for _, t := range allTools {
		if t.Definition().Name == name {
			out, err := callTool(ctx, t, name, input, progress)
			// On a successful read OR write, (re)stamp the tracker so the
			// file is considered "read at its current mtime" — this lets a
			// write be followed by an edit without a redundant re-read.
			if err == nil && r.tracker != nil {
				switch name {
				case "read_file", "write_file", "edit_file":
					if path, ok := input["path"].(string); ok {
						if abs, rerr := resolvePath(path); rerr == nil {
							r.tracker.RecordRead(abs)
						}
					}
				case "grep":
					r.recordGrepReads(ctx, input, out.Text)
				case "terminal":
					if cmd, ok := input["command"].(string); ok {
						r.recordTerminalReads(ctx, cmd)
						r.recordTerminalWrites(ctx, cmd)
					}
				}
			}
			return out, err
		}
	}
	return agent.ToolResult{Text: ""}, fmt.Errorf("unknown tool %q", name)
}

// callTool runs a single tool, using its ExecuteStream when the tool
// implements agent.StreamingToolExecutor and a progress callback was given;
// otherwise (nil progress, or a non-streaming tool) it calls plain Execute.
func callTool(ctx context.Context, t tool, name string, input map[string]any, progress func(chunk string)) (agent.ToolResult, error) {
	if progress != nil {
		if streaming, ok := t.(agent.StreamingToolExecutor); ok {
			return streaming.ExecuteStream(ctx, name, input, progress)
		}
	}
	return t.Execute(ctx, name, input)
}

// recordTerminalReads parses a successful terminal command for common file-
// reading operations (cat, head, tail, grep, etc.) and records the referenced
// paths in the read tracker. This closes the gap where `cat file` followed by
// `write_file file` would wrongly fail with "File has not been read yet".
//
// The command line is split into its simple commands first (`&&`, `||`, `|`,
// `;`, newline) so a read behind a `cd … &&` prefix is still seen, and a
// relative path is resolved against the directory that `cd` established —
// the same directory the shell resolved it in.
//
// The parser is intentionally lightweight: it tokenises each segment, skips
// flags and shell metacharacters, and treats any remaining token that
// resolves to an existing file as a read. It does NOT understand subshells,
// variable expansion, or complex pipelines — those still require an explicit
// read_file if the model wants to edit afterwards.
func (r DefaultRegistry) recordTerminalReads(ctx context.Context, command string) {
	// Commands that are known to read files as positional arguments.
	readCmds := map[string]bool{
		"cat": true, "head": true, "tail": true, "less": true, "more": true,
		"grep": true, "rg": true, "awk": true, "sed": true, "wc": true,
		"sort": true, "uniq": true, "diff": true, "comm": true, "cmp": true,
		"md5sum": true, "sha256sum": true, "shasum": true, "file": true,
		"stat": true, "ls": true, "find": true, "readlink": true,
	}

	for _, seg := range shellSegments(command, WorkingDir(ctx)) {
		if !readCmds[seg.tokens[0]] {
			continue
		}
		for _, tok := range seg.tokens[1:] {
			if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, ">") || strings.HasPrefix(tok, "<") {
				continue // skip flags and redirection operators
			}
			if strings.ContainsAny(tok, "|;&$()`") {
				continue // skip tokens with shell metacharacters
			}
			// Try to resolve as a path.  If the file exists, record it.
			abs, ok := seg.resolve(tok)
			if !ok {
				continue
			}
			if _, err := os.Stat(abs); err == nil {
				r.tracker.RecordRead(abs)
			}
		}
	}
}

// sepDigitsSep matches a separator (hyphen or colon) followed by digits
// and another separator — the boundary between a file path and the
// line-number region of an rg content/context output line
// ("path:12:text", "path-12-text"). A filename itself may contain
// separator+digit runs (e.g. "report-2024-01-01.txt:5:hit" has them at
// "-2024-" and ":5-"), so recordGrepReads collects EVERY such boundary
// and tries each prefix; the rightmost one that stats to a real file
// wins. Windows drive prefixes survive because the regex requires a
// leading separator before the digits.
var sepDigitsSep = regexp.MustCompile(`[-:]\d+[-:]`)

// grepCountRe matches count-mode lines "path:3", which have no trailing
// separator after the number and so escape sepDigitsSep.
var grepCountRe = regexp.MustCompile(`^(.+):\d+$`)

// recordGrepReads stamps the read tracker with every file a successful grep
// call surfaced, so a grep-then-edit flow doesn't hit "File has not been read
// yet" — the model HAS seen the lines it is about to edit. Recording also
// pins the current mtime, so the "modified since read" guard still fires if
// the file changes out-of-band afterwards.
//
// The parser mirrors recordTerminalReads' lightweight philosophy: it tries
// each output line as a whole path (files_with_matches mode), as "path:N:…"
// or "path-N-…" (content mode), and as "path:N" (count mode), and records
// whatever resolves to an existing regular file. Lines that aren't paths —
// "(no matches)", "--" group separators, the truncation marker — simply fail
// to stat and are skipped.
func (r DefaultRegistry) recordGrepReads(ctx context.Context, input map[string]any, output string) {
	base := WorkingDir(ctx)
	record := func(candidate string) {
		abs, err := resolvePathIn(base, candidate)
		if err != nil {
			return
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			r.tracker.RecordRead(abs)
		}
	}

	// Single-file search: rg omits the path prefix from its output, so the
	// only place the file appears is the input's own `path` argument. A
	// zero-match grep returns "(no matches)" with a nil error — in that case
	// the model saw nothing, so don't stamp (it would wrongly unlock a blind
	// edit of a file the search didn't actually surface).
	if p, _ := input["path"].(string); p != "" && output != "(no matches)" {
		record(p)
	}

	for _, line := range strings.Split(output, "\n") {
		record(line) // files_with_matches mode: the whole line is a path
		if idx := sepDigitsSep.FindAllStringIndex(line, -1); idx != nil {
			// Content/context mode. The line may hold multiple
			// "separator+digits+separator" boundaries when the filename
			// itself contains them — try every prefix so the longest one
			// that resolves to a real file wins.
			for _, pos := range idx {
				record(line[:pos[0]])
			}
		} else if m := grepCountRe.FindStringSubmatch(line); m != nil {
			record(m[1])
		}
	}
}

// recordTerminalWrites is the write-side counterpart to recordTerminalReads.
// A successful terminal command may have modified a file the session already
// read — a formatter (`gofmt -w`, `sed -i`), a redirect (`printf … > f`), or a
// copy/move. That bumps the file's mtime, which would otherwise trip
// CheckWritable's "modified since read" guard on the very next edit_file even
// though the change was the session's OWN doing, not an out-of-band editor's.
//
// It refreshes the recorded mtime of every tracked file the command names as an
// exact write target, leaving the guard intact for changes that did NOT come
// through the terminal tool (a human editing in their IDE never lands here) and
// for files the command never named. The command line is split into its simple
// commands so a writer behind a `cd … &&` prefix is found, and a relative
// target resolves against the directory that `cd` established — otherwise the
// refresh would look up a path the tracker never recorded and silently no-op.
// Detection is deliberately conservative — RefreshTarget only ever touches an
// exact path the tracker already knows. Writers that name a directory or the
// whole tree rather than specific files (`gofmt -w .`, `go fmt ./...`, `make
// fmt`) are intentionally not followed inside: attributing a subtree's mtime
// bumps to the command would let an unrelated out-of-band edit slip through.
// The model re-reads in that case.
//
// Known gap, accepted: attribution is static. A writer behind a failed `&&`
// (`go build ./... && gofmt -w f` with the build broken) never ran, yet f is
// refreshed as if it had. Gating on the command's exit status would close it
// but would also un-attribute `gofmt -w f && go test` every time the tests
// fail — the most common shape this tracking exists for. The gap needs an
// out-of-band edit AND a writer conditional on a failing command at once, so
// it stays open in favour of the common case.
func (r DefaultRegistry) recordTerminalWrites(ctx context.Context, command string) {
	for _, seg := range shellSegments(command, WorkingDir(ctx)) {
		for _, target := range writeTargets(seg.tokens) {
			if abs, ok := seg.resolve(target); ok {
				r.tracker.RefreshTarget(abs)
			}
		}
	}
}

// shellSegment is one simple command of a command line together with the
// directory it ran in, as established by any `cd` segment before it.
type shellSegment struct {
	tokens []string
	// dir is the working directory for this segment: the session's working
	// directory (or the process CWD when that is empty) until a `cd` changes
	// it. lost is set once a `cd` went somewhere we can't follow (`cd -`,
	// `cd "$X"`), after which relative paths are unresolvable — the guard
	// then errs towards a re-read rather than guessing a directory.
	dir  string
	lost bool
}

// resolve turns a path named by this segment into an absolute path, or
// reports false when the segment's directory is unknown and the path is
// relative.
func (seg shellSegment) resolve(path string) (string, bool) {
	if seg.lost && !filepath.IsAbs(path) && !isHomeRelative(path) {
		return "", false
	}
	abs, err := resolvePathIn(seg.dir, path)
	if err != nil {
		return "", false
	}
	return abs, true
}

// shellSegments splits a command line into its simple commands and threads
// the working directory through them: a segment that is a plain `cd <dir>`
// changes the directory for every segment after it. base is the directory
// the command line started in ("" ⇒ process CWD). Segments with no tokens
// are dropped, so every returned segment has a command head at tokens[0].
//
// `cd` is followed only in its plain forms — `cd`, `cd <literal>`, `cd ~/x`
// — and only as the head of a segment. Anything else (`cd -`, an unexpanded
// variable, extra flags) marks the directory as lost for the remainder of the
// line. So does any directory change we can see but not follow: a `cd` that
// isn't the head (`{ cd x; …`, `do cd x`, `$(cd x …`), a builtin that runs
// another command (`eval "cd x"`, `command cd x`), the directory stack
// (`pushd`/`popd`), PowerShell's equivalents, and a bare `(` subshell whose
// end we can't see.
//
// A shell-wrapped command (`bash -c "cd x && sed -i … f"`) is expanded into
// its inner segments with the current directory as their base; the inner
// `cd` is followed there and cannot change the outer directory.
func shellSegments(command, base string) []shellSegment {
	return appendShellSegments(nil, command, base, false)
}

func appendShellSegments(segs []shellSegment, command, dir string, lost bool) []shellSegment {
	for _, raw := range splitShellSegments(command) {
		tokens := tokenizeCommand(raw)
		if len(tokens) == 0 {
			continue
		}
		if hasBareSubshell(raw) || hidesDirectoryChange(tokens) {
			lost = true
		}
		if tokens[0] == "cd" {
			dir, lost = followCd(dir, lost, tokens[1:])
			continue
		}
		if payload := wrappedCommand(filepath.Base(tokens[0]), tokens[1:]); payload != "" {
			segs = appendShellSegments(segs, payload, dir, lost)
			continue
		}
		segs = append(segs, shellSegment{tokens: tokens, dir: dir, lost: lost})
	}
	return segs
}

// cdNames are the commands that change the shell's directory, in the Unix
// and PowerShell spellings the terminal tool may run. Compared
// case-insensitively because PowerShell is.
var cdNames = map[string]bool{
	"cd": true, "chdir": true, "pushd": true, "popd": true,
	"set-location": true, "sl": true, "push-location": true, "pop-location": true,
}

// opaqueHeads are builtins that run whatever follows them, so a directory
// change can hide in their arguments (`eval "cd x"`, `command cd x`, `. env.sh`).
var opaqueHeads = map[string]bool{
	"eval": true, "source": true, ".": true, "command": true, "builtin": true, "exec": true,
}

// hidesDirectoryChange reports whether a segment may change the directory in
// a way followCd can't reproduce — every case here is resolved by marking the
// directory lost rather than guessing. A plain `cd` at the head is excluded:
// followCd handles that one.
func hidesDirectoryChange(tokens []string) bool {
	if opaqueHeads[strings.ToLower(tokens[0])] {
		return true
	}
	for i, tok := range tokens {
		if i == 0 && tok == "cd" {
			continue
		}
		// `{cd`, `(cd`, `$(cd` and `\cd` all run cd; strip the prefix that
		// glued to it.
		if cdNames[strings.ToLower(strings.TrimLeft(tok, `({$\`))] {
			return true
		}
	}
	return false
}

// hasBareSubshell reports whether raw opens a `( … )` subshell outside
// quotes. `$(…)` substitution is not treated as one: its exit is where its
// directory change ends, and a `cd` inside it is caught by
// hidesDirectoryChange instead.
func hasBareSubshell(raw string) bool {
	var inSingle, inDouble bool
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
		case c == '(' && (i == 0 || raw[i-1] != '$'):
			return true
		}
	}
	return false
}

// isHomeRelative reports whether path is one resolvePathIn expands from the
// home directory (`~` or `~/…`), so it stays resolvable after a lost cd.
func isHomeRelative(path string) bool {
	return path == "~" || strings.HasPrefix(path, "~/")
}

// followCd returns the directory after `cd args...` runs in dir.
func followCd(dir string, lost bool, args []string) (string, bool) {
	switch {
	case len(args) == 0:
		if home, err := os.UserHomeDir(); err == nil {
			return home, false
		}
		return "", true
	case len(args) > 1, strings.HasPrefix(args[0], "-"), strings.ContainsAny(args[0], "$`"):
		return "", true
	case strings.HasPrefix(args[0], "~") && !isHomeRelative(args[0]):
		return "", true // `cd ~user`: resolvePathIn would join it as a relative path
	}
	if lost && !filepath.IsAbs(args[0]) && !isHomeRelative(args[0]) {
		return "", true
	}
	abs, err := resolvePathIn(dir, args[0])
	if err != nil {
		return "", true
	}
	return abs, false
}

// splitShellSegments splits a command line on the shell's command separators
// — `&&`, `||`, `|`, `;` and newline — outside single/double quotes. A lone
// `&` is NOT a separator so `2>&1` stays inside its segment. Redirection
// operators are left for the tokenizer; only the boundaries between simple
// commands are cut here.
//
// A heredoc (`<<`) ends the split at the next newline: everything after it is
// the document body, which the shell writes as data rather than runs. Treating
// those lines as commands would credit a script's `gofmt -w f` line as a write
// of f that never happened. Commands after the terminator are dropped too —
// under-attribution costs a re-read, over-attribution defeats the guard.
func splitShellSegments(s string) []string {
	var segs []string
	var cur strings.Builder
	var inSingle, inDouble, heredoc bool
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			cur.WriteByte(c)
		case c == '"' && !inSingle:
			inDouble = !inDouble
			cur.WriteByte(c)
		case inSingle || inDouble:
			cur.WriteByte(c)
		case c == '\n' && heredoc:
			flush()
			return segs
		case c == '<' && i+1 < len(s) && s[i+1] == '<':
			heredoc = true
			cur.WriteByte(c)
		case (c == '&' || c == '|') && i+1 < len(s) && s[i+1] == c:
			flush()
			i++ // consume the second character of && / ||
		case c == '|' || c == ';' || c == '\n':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return segs
}

// writeTargets returns the file paths a terminal command writes to, derived
// from shell redirections plus a curated set of in-place editors. Only paths
// the command names explicitly are returned — a bare directory (`gofmt -w .`)
// yields its literal token, which won't match any tracked file, so a
// whole-subtree format falls through to a re-read rather than over-attributing.
func writeTargets(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	var targets []string

	// Pass 1: redirections (`> f`, `>>f`, `2>f`, `&>f`) and `tee`, which can
	// appear anywhere in a pipeline.
	for i, tok := range tokens {
		if tok == "tee" {
			if p := nextPositional(tokens, i+1); p != "" {
				targets = append(targets, p)
			}
			continue
		}
		switch op := redirectTarget(tok); op {
		case "":
			// not a redirect
		case ">next":
			if p := nextPositional(tokens, i+1); p != "" {
				targets = append(targets, p)
			}
		default:
			targets = append(targets, op)
		}
	}

	// Pass 2: in-place editors (`sed -i`, `gofmt -w`, `… --write`) — their
	// non-flag args are the files rewritten, keyed on the head of the command.
	head := filepath.Base(tokens[0])
	if hasInPlaceFlag(head, tokens[1:]) {
		targets = append(targets, positionals(tokens[1:])...)
	}

	// Shell-wrapped commands (`bash -c "sed -i ... f"`) are expanded by
	// shellSegments before they reach here, so the payload's own `cd` is
	// followed like any other segment's.
	return targets
}

// wrappedCommand returns the inner payload of a shell-wrapped invocation, or
// "" if the command isn't one. Recognizes `bash|sh|zsh -c "..."` (and
// `--posix -c`). The payload is the single string argument to `-c`, verbatim —
// shellSegments re-parses it as its own command line.
func wrappedCommand(head string, args []string) string {
	switch head {
	case "bash", "sh", "zsh":
	default:
		return ""
	}
	for i, a := range args {
		if a == "-c" {
			// The payload is the next positional token (the quoted script).
			return nextPositional(args, i+1)
		}
	}
	return ""
}

// redirectTarget classifies a single token as an output redirection:
//   - ">next" when it is a bare operator (`>`, `>>`, `2>`, `&>`) whose target
//     is the following token;
//   - the fused path when the operator carries its target (`>f`, `2>>f`);
//   - "" when the token is not an output redirect (input `<` included).
func redirectTarget(tok string) string {
	rest := tok
	if len(rest) > 0 && (rest[0] == '&' || (rest[0] >= '0' && rest[0] <= '9')) {
		rest = rest[1:] // optional fd / & prefix: 2> &>
	}
	if !strings.HasPrefix(rest, ">") {
		return ""
	}
	rest = strings.TrimPrefix(rest, ">")
	rest = strings.TrimPrefix(rest, ">") // second '>' of an append (>>)
	if rest == "" {
		return ">next"
	}
	if strings.HasPrefix(rest, "&") {
		return "" // fd duplication (2>&1, >&2), not a file target
	}
	return rest
}

// hasInPlaceFlag reports whether the command edits its file arguments in place.
// The long forms `--write` / `--in-place` are unambiguous across tools; the
// short `-i` / `-w` forms are only honored for the specific editors/formatters
// that use them that way, so `grep -w` or `cp -i` aren't mistaken for writers.
func hasInPlaceFlag(cmd string, args []string) bool {
	for _, a := range args {
		if a == "--write" || a == "--in-place" {
			return true
		}
	}
	switch cmd {
	case "sed", "perl":
		for _, a := range args {
			if a == "-i" || strings.HasPrefix(a, "-i") { // sed's -i.bak / -i''
				return true
			}
		}
	case "gofmt", "gofumpt", "goimports":
		for _, a := range args {
			if a == "-w" {
				return true
			}
		}
	}
	return false
}

// positionals returns the non-flag tokens (a flag is any token starting with
// "-"). A stray positional that isn't a real path — a sed script, say — is
// harmless: RefreshTarget ignores paths the tracker never recorded.
//
// Trailing shell metacharacters that tokenizeCommand can't split off (it only
// splits on whitespace and honors quotes) are stripped here so that a filename
// written as `file.go;` (`sed -i '...' file.go; echo done`) or `file.go|`
// resolves to the real path. Without this, the glued `;` makes the path
// unresolvable and the tracker is never refreshed for that file.
func positionals(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, strings.TrimRight(a, ";|&"))
		}
	}
	return out
}

// nextPositional returns the first non-flag token at or after index i, or "".
func nextPositional(tokens []string, i int) string {
	for ; i < len(tokens); i++ {
		if !strings.HasPrefix(tokens[i], "-") {
			return tokens[i]
		}
	}
	return ""
}

// tokenizeCommand splits a shell command line into whitespace-separated tokens,
// respecting single and double quotes (no nesting, no escape sequences).
func tokenizeCommand(s string) []string {
	var tokens []string
	var cur strings.Builder
	var inSingle, inDouble bool

	for _, r := range s {
		switch r {
		case '\'':
			if inDouble {
				cur.WriteRune(r)
			} else {
				inSingle = !inSingle
			}
		case '"':
			if inSingle {
				cur.WriteRune(r)
			} else {
				inDouble = !inDouble
			}
		case ' ', '\t':
			if inSingle || inDouble {
				cur.WriteRune(r)
			} else {
				if cur.Len() > 0 {
					tokens = append(tokens, cur.String())
					cur.Reset()
				}
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// DefaultTools returns the tool list with no model context — equivalent to
// DefaultToolsFor(""). MCP schemas are always uploaded in full (the Tool Search
// bridge needs the model to evaluate its auto threshold), so unmigrated callers
// keep the original behaviour.
func DefaultTools() []agent.ToolDefinition { return DefaultToolsFor("") }

// DefaultToolsFor returns the slice of ToolDefinitions sent to the LLM when
// `--tools` is on, for the given model. Order matches allTools. Each
// capability-gated tool is withheld unless the corresponding registration call
// has been made — SkillTool needs SetSkills, sub-agent tools need a
// SubAgentManager. Advertising a tool that can only error wastes a slot and
// confuses the model.
//
// This is the process-global-only gate: it sees a session/turn's sub-agent
// manager only if something wrote it into the CLI/TUI's process-wide slot
// (tools.SetSpawner / tools.SetDefaultSubAgentManager), which is only correct
// for a single-session process. Server/cron turns — inherently multi-session —
// must use DefaultToolsForCtx instead so per-turn advertisement doesn't depend
// on process-global state (#1133).
//
// MCP surfaces ride alongside the built-ins. When Tool Search is active for
// this model (see toolSearchActive) the full per-tool catalog is replaced by
// the two describe/call bridge tools, so the model's tools array carries two
// small schemas instead of every MCP tool's schema every turn. The tool
// names + one-line descriptions aren't lost — MCPManifestFor renders them
// into the system prompt separately (see dev-docs/tool-search-mcp.md).
func DefaultToolsFor(model string) []agent.ToolDefinition {
	return defaultToolsFor(context.Background(), model)
}

// DefaultToolsForCtx is DefaultToolsFor, but also advertises sub_agent* and
// workflow* when THIS turn has a ctx-scoped SubAgentManager (tools.
// WithSubAgentManager) — even if no process-global spawner/manager is
// registered. Server/cron turns each carry their own per-turn manager in ctx
// (see prepareToolTurn); this lets them advertise correctly without the
// per-turn tools.SetSpawner/tools.SetDefaultSubAgentManager swap-and-restore
// that used to be required, which mutated process-global state on every turn
// of an inherently multi-session process (#1133). CLI/TUI callers, which have
// no ctx-scoped manager, see identical behavior to DefaultToolsFor.
//
// When the context also carries a profile store (WithProfileStore) and session
// agent ID (WithSessionAgentID), tools are filtered to the profile's allowlist
// — see DefaultToolsForProfile.
func DefaultToolsForCtx(ctx context.Context, model string) []agent.ToolDefinition {
	return DefaultToolsForProfile(ctx, model)
}

// ctxKeySessionAgentID is the context key for the per-turn session AgentID.
type ctxKeySessionAgentID struct{}

// WithSessionAgentID attaches a session's agent ID to the context so
// DefaultToolsForProfile can resolve the profile for tool filtering. Stamped
// per turn by the server's IM/web/cron handlers.
func WithSessionAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, ctxKeySessionAgentID{}, agentID)
}

// sessionAgentIDFromContext returns the context's session AgentID, or "".
func sessionAgentIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeySessionAgentID{}).(string); ok {
		return id
	}
	return ""
}

// DefaultToolsForProfile is DefaultToolsForCtx with a per-profile allowlist.
// When the context carries a profile store (WithProfileStore) and a session
// agent ID (WithSessionAgentID), it resolves the profile and filters tools to
// its allowlist. Otherwise it behaves like DefaultToolsForCtx — so callers
// that don't wire a store see unchanged behavior. Resolved fresh per turn so
// profile edits land on the next message without rebuilding anything.
//
// Empty Tools means different things depending on the agent source:
//   - builtin (default, explore, general, code-review): empty = all tools
//   - user-created: empty = no tools (explicitly restricted)
func DefaultToolsForProfile(ctx context.Context, model string) []agent.ToolDefinition {
	all := defaultToolsFor(ctx, model)
	store := profileStoreFromContext(ctx)
	agentID := sessionAgentIDFromContext(ctx)
	if store == nil || agentID == "" {
		return all
	}
	profile, ok := store.Get(agentID)
	if !ok {
		return all
	}
	if len(profile.Tools) == 0 {
		if profile.Source == agentprofile.SourceBuiltin {
			return all
		}
		return nil
	}
	allowed := make(map[string]bool, len(profile.Tools))
	for _, t := range profile.Tools {
		allowed[t] = true
	}
	filtered := make([]agent.ToolDefinition, 0, len(all))
	for _, t := range all {
		if allowed[t.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// KnownToolNames returns the names of all built-in tools. Used by the server
// to validate profile tool allowlists against the canonical tool set.
func KnownToolNames() []string {
	names := make([]string, len(allTools))
	for i, t := range allTools {
		names[i] = t.Definition().Name
	}
	return names
}

func defaultToolsFor(ctx context.Context, model string) []agent.ToolDefinition {
	skillsOn := skillsEnabled()
	mgrOn := subAgentManagerEnabled()
	askerOn := askerEnabled()
	tasksOn := tasksEnabled()
	goalsOn := goalsEnabled()
	restarterOn := restarterEnabled()
	messengerOn := messengerEnabled()
	wakerOn := wakerEnabled()
	browserOn := browserEnabled()
	computerOn := computerEnabled()
	spawnerOn := spawnerEnabled()
	memoryBackendOn := memoryBackendEnabled()
	// A ctx-scoped manager (per-turn, server/IM — see WithSubAgentManager)
	// makes both gates true for this turn even when the process-global
	// spawner/manager slots are untouched.
	if ctxMgr := subAgentManagerFromContext(ctx); ctxMgr != nil {
		mgrOn = true
		if ctxMgr.Spawner() != nil {
			spawnerOn = true
		}
	}
	defs := make([]agent.ToolDefinition, 0, len(allTools))
	for _, t := range allTools {
		if _, isSendFile := t.(SendFileTool); isSendFile && !messengerOn {
			// Needs a chat to push to, which only the server has (live adapters).
			// Advertised alongside send_message when a messenger is registered
			// (covers both web and IM turns); hidden in CLI/TUI.
			continue
		}
		if _, isSkill := t.(SkillTool); isSkill && !skillsOn {
			continue
		}
		if at, isAgent := t.(AgentTool); isAgent {
			if !mgrOn {
				continue
			}
			// Model-aware schema: the model-override parameter lists the
			// sibling models reachable on the session model's endpoint.
			defs = append(defs, at.DefinitionFor(model))
			continue
		}
		if _, isSend := t.(AgentSendTool); isSend && !mgrOn {
			continue
		}
		if _, isStatus := t.(AgentStatusTool); isStatus && !mgrOn {
			continue
		}
		if _, isKill := t.(AgentKillTool); isKill && !mgrOn {
			continue
		}
		if _, isWorkflow := t.(WorkflowTool); isWorkflow && !spawnerOn {
			continue
		}
		if _, isWfSave := t.(WorkflowSaveTool); isWfSave && !spawnerOn {
			continue
		}
		if _, isWfStatus := t.(WorkflowStatusTool); isWfStatus && !spawnerOn {
			continue
		}
		if _, isWfKill := t.(WorkflowKillTool); isWfKill && !spawnerOn {
			continue
		}
		if _, isAsk := t.(AskUserQuestionTool); isAsk && !askerOn {
			continue
		}
		if _, isRestart := t.(RestartServerTool); isRestart && !restarterOn {
			continue
		}
		if _, isSendMsg := t.(SendMessageTool); isSendMsg && !messengerOn {
			continue
		}
		if _, isWakeup := t.(ScheduleWakeupTool); isWakeup && !wakerOn {
			continue
		}
		if _, isBrowser := t.(BrowserTool); isBrowser && !browserOn {
			continue
		}
		if _, isComputer := t.(ComputerTool); isComputer && !computerOn {
			continue
		}
		if _, isMemoryRecall := t.(MemoryRecallTool); isMemoryRecall && !memoryBackendOn {
			continue
		}
		if _, isTaskCreate := t.(TaskCreateTool); isTaskCreate && !tasksOn {
			continue
		}
		if _, isTaskUpdate := t.(TaskUpdateTool); isTaskUpdate && !tasksOn {
			continue
		}
		if _, isTaskList := t.(TaskListTool); isTaskList && !tasksOn {
			continue
		}
		if _, isGetGoal := t.(GetGoalTool); isGetGoal && !goalsOn {
			continue
		}
		if _, isCreateGoal := t.(CreateGoalTool); isCreateGoal && !goalsOn {
			continue
		}
		if _, isUpdateGoal := t.(UpdateGoalTool); isUpdateGoal && !goalsOn {
			continue
		}
		defs = append(defs, t.Definition())
	}
	// MCP-advertised surfaces. Synthesised per-connection (one def per
	// tool/resource/prompt) when uploaded in full; collapsed to the two
	// bridge tools when Tool Search is active for this model.
	mcpDefs := mcpCatalog()
	if toolSearchActive(model, mcpDefs) {
		defs = append(defs, toolSearchBridgeDefs()...)
	} else {
		defs = append(defs, mcpDefs...)
	}
	return defs
}
