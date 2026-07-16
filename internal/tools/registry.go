package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
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
	if out, ok, err := executeMCP(ctx, name, input); ok {
		return agent.ToolResult{Text: out}, err
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
				case "terminal":
					if cmd, ok := input["command"].(string); ok {
						r.recordTerminalReads(cmd)
						r.recordTerminalWrites(cmd)
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
// The parser is intentionally lightweight: it tokenises the command string,
// skips flags and shell metacharacters, and treats any remaining token that
// resolves to an existing file as a read. It does NOT understand subshells,
// variable expansion, or complex pipelines — those still require an explicit
// read_file if the model wants to edit afterwards.
func (r DefaultRegistry) recordTerminalReads(command string) {
	// Commands that are known to read files as positional arguments.
	readCmds := map[string]bool{
		"cat": true, "head": true, "tail": true, "less": true, "more": true,
		"grep": true, "rg": true, "awk": true, "sed": true, "wc": true,
		"sort": true, "uniq": true, "diff": true, "comm": true, "cmp": true,
		"md5sum": true, "sha256sum": true, "shasum": true, "file": true,
		"stat": true, "ls": true, "find": true, "readlink": true,
	}

	tokens := tokenizeCommand(command)
	if len(tokens) == 0 {
		return
	}
	if !readCmds[tokens[0]] {
		return
	}

	for _, tok := range tokens[1:] {
		if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, ">") || strings.HasPrefix(tok, "<") {
			continue // skip flags and redirection operators
		}
		if strings.ContainsAny(tok, "|;&$()`") {
			continue // skip tokens with shell metacharacters
		}
		// Try to resolve as a path.  If the file exists, record it.
		abs, err := resolvePath(tok)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			r.tracker.RecordRead(abs)
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
// for files the command never named. Detection is deliberately conservative —
// RefreshTarget only ever touches an exact path the tracker already knows.
// Writers that name a directory or the whole tree rather than specific files
// (`gofmt -w .`, `go fmt ./...`, `make fmt`) are intentionally not followed
// inside: attributing a subtree's mtime bumps to the command would let an
// unrelated out-of-band edit slip through. The model re-reads in that case.
func (r DefaultRegistry) recordTerminalWrites(command string) {
	tokens := tokenizeCommand(command)
	if len(tokens) == 0 {
		return
	}
	for _, target := range writeTargets(tokens) {
		if abs, err := resolvePath(target); err == nil {
			r.tracker.RefreshTarget(abs)
		}
	}
}

// writeTargets returns the file paths a terminal command writes to, derived
// from shell redirections plus a curated set of in-place editors. Only paths
// the command names explicitly are returned — a bare directory (`gofmt -w .`)
// yields its literal token, which won't match any tracked file, so a
// whole-subtree format falls through to a re-read rather than over-attributing.
func writeTargets(tokens []string) []string {
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
	if hasInPlaceFlag(filepath.Base(tokens[0]), tokens[1:]) {
		targets = append(targets, positionals(tokens[1:])...)
	}
	return targets
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
func positionals(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
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
func DefaultToolsForCtx(ctx context.Context, model string) []agent.ToolDefinition {
	return defaultToolsFor(ctx, model)
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
		if _, isAgent := t.(AgentTool); isAgent && !mgrOn {
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
