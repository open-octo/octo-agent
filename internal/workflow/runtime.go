// Package workflow runs a Ruby (mruby) DSL for deterministic agent
// orchestration. The interpreter is mruby compiled to wasm32-wasi (embedded as
// mruby.wasm) executed by the pure-Go wazero runtime — no cgo, so the binary
// stays CGO_ENABLED=0 and cross-compiles unchanged.
//
// The DSL surface (agent / parallel / pipeline / log) is a Ruby prelude
// (prelude.rb) prepended to the user's script. Its primitives call host
// functions implemented here, which back onto an AgentFunc — in production the
// sub-agent Spawner, in tests a fake. parallel/pipeline get real concurrency
// from mruby Fibers cooperating with goroutines: agent() starts work in a
// goroutine and yields its fiber; the Ruby event loop starts every branch
// before awaiting any.
package workflow

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed mruby.wasm
var mrubyWasm []byte

//go:embed prelude.rb
var preludeRB string

// AgentFunc runs one sub-agent task to completion. It backs the DSL's agent()
// primitive. Implementations must be safe for concurrent calls on distinct
// prompts (parallel() invokes it from multiple goroutines at once).
type AgentFunc func(ctx context.Context, prompt string, opts AgentOptions) AgentResult

// AgentOptions carries the optional per-call settings from agent(prompt, …).
// All fields are zero-valued ("inherit the parent's default") when the script
// passes no options.
type AgentOptions struct {
	// Model overrides the model for this one sub-agent (empty = inherit).
	Model string
	// Tools restricts the child to this subset of tools (empty = inherit all).
	Tools []string
	// ReadOnly strips the mutating tools (write_file, edit_file) from the child.
	ReadOnly bool
	// Schema is a JSON Schema (as a JSON string) the sub-agent's reply must
	// satisfy; empty means free-form text.
	Schema string
	// Isolation, when "worktree", runs the sub-agent in a fresh git worktree so
	// its file/terminal changes are isolated from the main checkout.
	Isolation string
}

// AgentResult is one agent() call's outcome. It also carries a skill() call's
// outcome, where Reply holds the skill's outputs as a JSON string.
type AgentResult struct {
	Reply        string
	InputTokens  int
	OutputTokens int
	Err          error
}

// SkillFunc runs one skill() call to completion: a recorded browser skill or a
// SKILL.md sub-agent, dispatched by name. paramsJSON is the call's params object
// (JSON), schema an optional JSON Schema the result must satisfy (SKILL.md path).
// The returned AgentResult's Reply is the skill's outputs as a JSON string —
// what skill() returns to the script (parsed to native Ruby). Must be safe for
// concurrent calls (parallel()/pipeline() invoke it from multiple goroutines).
type SkillFunc func(ctx context.Context, name, paramsJSON, schema string) AgentResult

// Options configures one workflow run.
type Options struct {
	// Agent backs agent(). Required.
	Agent AgentFunc
	// Skill backs skill(). Optional: when nil, skill() raises in the script.
	Skill SkillFunc
	// Log backs the script's log() calls; nil discards.
	Log func(string)
	// Progress, when set, receives human-readable lifecycle lines as the
	// workflow runs ("→ <label>" when an agent starts, "✓ <label>" when it
	// finishes). Lets a streaming caller surface live progress. Distinct from
	// Log, which is the script's own output. Invoked only from the single VM
	// goroutine, so it need not be concurrency-safe.
	Progress func(string)
	// Budget caps total output tokens across all agent() calls; <= 0 = unlimited.
	// When the budget is already spent, agent() raises in the script.
	Budget int64
	// MaxConcurrent caps in-flight agent() calls; <= 0 = unlimited.
	MaxConcurrent int
	// Args is the workflow's input value as a JSON string, surfaced to the script
	// as the args primitive (parsed to native Ruby). Empty means the script's
	// args returns nil. Part of the run identity: resuming with different Args is
	// rejected, since args drives control flow and would invalidate cached results.
	Args string

	// JournalDir is the directory for workflow journal files. When empty,
	// Run uses ~/.octo/workflow-journals/. Pass a temp dir in tests.
	JournalDir string
	// ResumeFrom, when non-empty, is the RunID of a prior run whose journal
	// provides cached results for already-completed agent() calls. The script
	// must match the original run's script exactly; a mismatch returns an error.
	ResumeFrom string
}

// Result is a finished workflow run.
type Result struct {
	Output       string // the script's final value, as a string
	InputTokens  int
	OutputTokens int
	Canceled     bool
	// RunID identifies the journal written for this run. Pass it as
	// Options.ResumeFrom to replay completed calls on a subsequent run.
	// Empty when journaling is disabled or the journal directory is unavailable.
	RunID string
}

// cancelToken is the sentinel agent_wait_any returns when ctx is canceled; the
// prelude turns it into a raised "workflow: canceled".
const cancelToken = 0

// Run executes script (the prelude is prepended) and returns its final value.
// A Ruby-level error surfaces as a non-nil error carrying the mruby backtrace.
func Run(ctx context.Context, script string, opt Options) (Result, error) {
	if opt.Agent == nil {
		return Result{}, fmt.Errorf("workflow: Options.Agent is required")
	}

	hash := runIdentityHash(script, opt.Args)

	// Resolve the journal directory (best-effort; journaling is non-fatal).
	jDir := opt.JournalDir
	if jDir == "" {
		if d, err := journalsDir(); err == nil {
			jDir = d
		}
	}

	// Load cached entries from a prior run when resuming.
	var cached []JournalEntry
	if opt.ResumeFrom != "" {
		if jDir == "" {
			return Result{}, fmt.Errorf("workflow: resume_from requires a writable journal directory")
		}
		entries, prevHash, err := LoadJournal(jDir, opt.ResumeFrom)
		if err != nil {
			return Result{}, fmt.Errorf("workflow: load resume journal: %w", err)
		}
		if prevHash != hash {
			return Result{}, fmt.Errorf("workflow: resume_from %q was created with a different script; omit resume_from to start fresh", opt.ResumeFrom)
		}
		cached = entries
	}

	// Create a fresh journal for this run. Pre-populate with the replayed
	// entries so the new run ID is self-contained.
	runID := NewRunID()
	var j *Journal
	if jDir != "" {
		var err error
		j, err = CreateJournal(jDir, runID, hash)
		if err == nil && len(cached) > 0 {
			for _, e := range cached {
				_ = j.Append(e) // copy replayed entries; best-effort
			}
		}
		if err != nil {
			j = nil // journaling unavailable; run without it
		}
	}

	be := newBackend(ctx, opt, cached, j)

	// WithCloseOnContextDone makes in-flight wasm execution observe ctx
	// cancellation even when the script never re-enters a host call — a
	// compute-only runaway loop (`loop { }`) is otherwise uninterruptible,
	// which would hang workflow_kill and, worse, a foreground (one-shot) run
	// where Wait blocks the whole process with SIGINT already trapped.
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCoreFeatures(api.CoreFeaturesV2|experimental.CoreFeaturesExceptionHandling).
		WithCloseOnContextDone(true))
	defer r.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	if _, err := be.register(ctx, r); err != nil {
		return Result{}, fmt.Errorf("workflow: host module: %w", err)
	}

	var stdout, stderr bytes.Buffer
	full := preludeRB + "\n" + script + "\n"
	mc := wazero.NewModuleConfig().
		WithStdin(strings.NewReader(full)).
		WithStdout(&stdout).
		WithStderr(&stderr)

	_, err := r.InstantiateWithConfig(ctx, mrubyWasm, mc)
	if j != nil {
		_ = j.Close()
	}

	in, out := be.usage()
	// Canceled covers both cancellation avenues: the backend observing it in a
	// host call, and wazero's close-on-context-done tearing the module down
	// mid-compute (where no host call was in flight to notice).
	res := Result{InputTokens: in, OutputTokens: out, Canceled: be.canceled() || ctx.Err() != nil, RunID: runID}

	if err != nil {
		var ee *sys.ExitError
		if errors.As(err, &ee) {
			if ee.ExitCode() == 0 {
				res.Output = strings.TrimRight(stdout.String(), "\n")
				return res, nil
			}
			// Non-zero exit: a Ruby error (backtrace on stderr) or cancellation.
			if res.Canceled {
				return res, context.Canceled
			}
			return res, fmt.Errorf("workflow: script error: %s", cleanScriptError(stderr.String()))
		}
		if res.Canceled {
			return res, context.Canceled
		}
		return res, fmt.Errorf("workflow: run: %w", err)
	}
	res.Output = strings.TrimRight(stdout.String(), "\n")
	return res, nil
}

// mruby backtrace noise. The interpreter prepends a position prefix to every
// error line, but the position is meaningless to the script author: the
// filename is always "(unknown)", runtime errors report line/column "0", and
// syntax-error line numbers count from the top of the Go-prepended prelude
// (~86 lines) rather than the user's script — so surfacing them just misleads
// the model into editing a line that doesn't exist. We strip the prefixes and
// keep the human-readable message + error class.
var (
	mrubyPosPrefix = regexp.MustCompile(`(?m)^\s*(?:\(unknown\)|line \d+):\d+:(?:in [^:]*:)?\s*`)
	mrubyTraceLine = regexp.MustCompile(`(?m)^\s*(?:trace \(most recent call last\):|\[\d+\] .*)\s*$`)
)

// cleanScriptError turns a raw mruby backtrace into a concise, author-facing
// message by stripping the misleading position prefixes (see mrubyPosPrefix)
// and the bare trace scaffolding, then collapsing blank/duplicate lines. The
// error class (NoMethodError, SyntaxError, …) and message survive intact.
func cleanScriptError(stderr string) string {
	s := mrubyTraceLine.ReplaceAllString(stderr, "")
	s = mrubyPosPrefix.ReplaceAllString(s, "")
	// Collapse to non-empty, de-duplicated lines (mruby often repeats the
	// SyntaxError summary on a second line).
	seen := make(map[string]bool)
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || seen[ln] {
			continue
		}
		seen[ln] = true
		out = append(out, ln)
	}
	if len(out) == 0 {
		return strings.TrimSpace(stderr) // never return empty — fall back to raw
	}
	return strings.Join(out, "; ")
}

// backend holds the host-side scheduler state for one Run.
type backend struct {
	ctx context.Context
	opt Options
	sem chan struct{} // concurrency limiter; nil when unlimited

	done chan uint32 // tokens of finished agents

	args string // the run's input JSON, served verbatim to the __args host call

	mu        sync.Mutex
	next      uint32
	results   map[uint32]AgentResult
	prompts   map[uint32]string // token => prompt, for progress labels
	inTok     int
	outTok    int
	wasCancel bool

	// cached holds pre-loaded results from a resume_from journal. Tokens
	// 1..len(cached) map to cached[tok-1]; tokens beyond that are fresh.
	cached []JournalEntry
	// journal, when non-nil, receives a record for every fresh (non-replayed)
	// agent() call as it completes.
	journal *Journal

	// reMu/reCache memoize compiled Go regexps across __regex_* host calls, keyed
	// by flags+pattern, so a Regexp reused in a scan/gsub loop compiles once.
	reMu    sync.Mutex
	reCache map[string]*regexp.Regexp
}

func newBackend(ctx context.Context, opt Options, cached []JournalEntry, j *Journal) *backend {
	b := &backend{
		ctx:     ctx,
		opt:     opt,
		args:    opt.Args,
		done:    make(chan uint32, 1024),
		results: map[uint32]AgentResult{},
		prompts: map[uint32]string{},
		cached:  cached,
		journal: j,
	}
	if opt.MaxConcurrent > 0 {
		b.sem = make(chan struct{}, opt.MaxConcurrent)
	}
	return b
}

func (b *backend) usage() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inTok, b.outTok
}
func (b *backend) canceled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wasCancel
}

func (b *backend) register(ctx context.Context, r wazero.Runtime) (api.Module, error) {
	return r.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(b.agentStart).Export("agent_start").
		NewFunctionBuilder().WithFunc(b.agentWaitAny).Export("agent_wait_any").
		NewFunctionBuilder().WithFunc(b.agentTake).Export("agent_take").
		NewFunctionBuilder().WithFunc(b.skillStart).Export("skill_start").
		NewFunctionBuilder().WithFunc(b.log).Export("log").
		NewFunctionBuilder().WithFunc(b.budgetRemaining).Export("budget_remaining").
		NewFunctionBuilder().WithFunc(b.args_).Export("args").
		NewFunctionBuilder().WithFunc(b.regexCompileCheck).Export("regex_compile_check").
		NewFunctionBuilder().WithFunc(b.regexScan).Export("regex_scan").
		Instantiate(ctx)
}

// agentStart kicks off one agent() call and returns its token immediately
// (non-blocking). When the call maps to a cached journal entry it delivers the
// stored result on a goroutine without invoking Agent. Returns -1 (cast) when
// the budget is exhausted.
func (b *backend) agentStart(_ context.Context, mod api.Module, ptr, length, mptr, mlen, tptr, tlen, readOnly, sptr, slen, iptr, ilen uint32) uint32 {
	promptBytes, _ := mod.Memory().Read(ptr, length)
	prompt := string(promptBytes)

	// Per-call options (model / tools / read_only / schema / isolation). The
	// prelude always passes every slot, empty when unset. Read synchronously —
	// the wasm linear memory may be reused once we return the token.
	var opts AgentOptions
	if mlen > 0 {
		if mb, ok := mod.Memory().Read(mptr, mlen); ok {
			opts.Model = string(mb)
		}
	}
	if tlen > 0 {
		if tb, ok := mod.Memory().Read(tptr, tlen); ok {
			for _, t := range strings.Split(string(tb), ",") {
				if t = strings.TrimSpace(t); t != "" {
					opts.Tools = append(opts.Tools, t)
				}
			}
		}
	}
	opts.ReadOnly = readOnly != 0
	if slen > 0 {
		if sb, ok := mod.Memory().Read(sptr, slen); ok {
			opts.Schema = string(sb)
		}
	}
	if ilen > 0 {
		if ib, ok := mod.Memory().Read(iptr, ilen); ok {
			opts.Isolation = string(ib)
		}
	}

	b.mu.Lock()
	if b.opt.Budget > 0 && int64(b.outTok) >= b.opt.Budget {
		b.mu.Unlock()
		return ^uint32(0) // -1: prelude raises "budget exhausted"
	}
	b.next++
	tok := b.next
	seq := int(tok) - 1
	b.prompts[tok] = prompt
	b.mu.Unlock()

	if b.opt.Progress != nil {
		b.opt.Progress("→ " + label(prompt))
	}

	if seq < len(b.cached) {
		// Replayed from journal: deliver without calling Agent.
		ce := b.cached[seq]
		go func() {
			var res AgentResult
			if ce.ErrMsg != "" {
				res.Err = errors.New(ce.ErrMsg)
			} else {
				res.Reply = ce.Reply
				res.InputTokens = ce.InputTokens
				res.OutputTokens = ce.OutputTokens
			}
			b.deliver(tok, res)
		}()
		return tok
	}

	go func() {
		if b.sem != nil {
			select {
			case b.sem <- struct{}{}:
				defer func() { <-b.sem }()
			case <-b.ctx.Done():
				b.deliver(tok, AgentResult{Err: b.ctx.Err()})
				return
			}
		}
		res := b.opt.Agent(b.ctx, prompt, opts)
		b.deliver(tok, res)
	}()
	return tok
}

// skillStart kicks off one skill() call and returns its token immediately,
// mirroring agentStart: same token space, budget gate, journal replay, and
// delivery queue, so skill() composes with parallel()/pipeline() and resumes
// like agent(). It calls Options.Skill instead of Agent; a nil Skill delivers an
// error the script raises.
func (b *backend) skillStart(_ context.Context, mod api.Module, nptr, nlen, pptr, plen, sptr, slen uint32) uint32 {
	name := readGuestString(mod, nptr, nlen)
	params := readGuestString(mod, pptr, plen)
	schema := readGuestString(mod, sptr, slen)

	b.mu.Lock()
	if b.opt.Budget > 0 && int64(b.outTok) >= b.opt.Budget {
		b.mu.Unlock()
		return ^uint32(0) // -1: prelude raises "budget exhausted"
	}
	b.next++
	tok := b.next
	seq := int(tok) - 1
	b.prompts[tok] = "skill: " + name
	b.mu.Unlock()

	if b.opt.Progress != nil {
		b.opt.Progress("→ skill: " + name)
	}

	if seq < len(b.cached) {
		// Replayed from journal: deliver the stored outputs without re-running.
		ce := b.cached[seq]
		go func() {
			var res AgentResult
			if ce.ErrMsg != "" {
				res.Err = errors.New(ce.ErrMsg)
			} else {
				res.Reply = ce.Reply
				res.InputTokens = ce.InputTokens
				res.OutputTokens = ce.OutputTokens
			}
			b.deliver(tok, res)
		}()
		return tok
	}

	go func() {
		if b.sem != nil {
			select {
			case b.sem <- struct{}{}:
				defer func() { <-b.sem }()
			case <-b.ctx.Done():
				b.deliver(tok, AgentResult{Err: b.ctx.Err()})
				return
			}
		}
		if b.opt.Skill == nil {
			b.deliver(tok, AgentResult{Err: errors.New("skill() is not available in this run")})
			return
		}
		b.deliver(tok, b.opt.Skill(b.ctx, name, params, schema))
	}()
	return tok
}

// readGuestString reads a (ptr,len) string from guest linear memory, returning
// "" for a zero-length or unreadable span.
func readGuestString(mod api.Module, ptr, length uint32) string {
	if length == 0 {
		return ""
	}
	if bs, ok := mod.Memory().Read(ptr, length); ok {
		return string(bs)
	}
	return ""
}

func (b *backend) deliver(tok uint32, res AgentResult) {
	b.mu.Lock()
	b.results[tok] = res
	b.inTok += res.InputTokens
	b.outTok += res.OutputTokens
	prompt := b.prompts[tok]
	b.mu.Unlock()

	// Journal fresh (non-replayed) calls so they can be resumed later.
	if b.journal != nil && int(tok) > len(b.cached) {
		e := JournalEntry{
			Seq:          int(tok) - 1,
			Prompt:       prompt,
			Reply:        res.Reply,
			InputTokens:  res.InputTokens,
			OutputTokens: res.OutputTokens,
		}
		if res.Err != nil {
			e.ErrMsg = res.Err.Error()
		}
		_ = b.journal.Append(e) // best-effort; a write failure doesn't break the run
	}

	b.done <- tok
}

// agentWaitAny blocks until some agent finishes, returning its token; returns
// cancelToken (0) when ctx is canceled so the prelude can unwind.
func (b *backend) agentWaitAny(_ context.Context, _ api.Module) uint32 {
	if b.ctx.Err() != nil {
		return b.markCanceled()
	}
	select {
	case tok := <-b.done:
		// A cancel that landed at the same time as a completion still cancels —
		// the workflow shouldn't half-finish on a canceled context.
		if b.ctx.Err() != nil {
			return b.markCanceled()
		}
		if b.opt.Progress != nil {
			b.mu.Lock()
			p := b.prompts[tok]
			b.mu.Unlock()
			b.opt.Progress("✓ " + label(p))
		}
		return tok
	case <-b.ctx.Done():
		return b.markCanceled()
	}
}

func (b *backend) markCanceled() uint32 {
	b.mu.Lock()
	b.wasCancel = true
	b.mu.Unlock()
	return cancelToken
}

// label trims a prompt to a short single-line progress label.
func label(prompt string) string {
	s := prompt
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	const max = 60
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// agentTake writes the result for token into the guest buffer, returning its
// length. A failed agent surfaces its error text as the result string.
func (b *backend) agentTake(_ context.Context, mod api.Module, token, outPtr, outCap uint32) uint32 {
	b.mu.Lock()
	res, ok := b.results[token]
	delete(b.results, token)
	delete(b.prompts, token)
	b.mu.Unlock()

	var s string
	switch {
	case !ok:
		s = "[workflow: unknown token]"
	case res.Err != nil:
		s = "[agent error] " + res.Err.Error()
	default:
		s = res.Reply
	}
	out := []byte(s)
	if uint32(len(out)) > outCap {
		out = out[:outCap]
	}
	mod.Memory().Write(outPtr, out)
	return uint32(len(out))
}

func (b *backend) log(_ context.Context, mod api.Module, ptr, length uint32) {
	msg, _ := mod.Memory().Read(ptr, length)
	if b.opt.Log != nil {
		b.opt.Log(string(msg))
	}
}

// args_ writes the run's input JSON into the guest buffer, returning its length
// (0 = no args). Backs the __args host call; the prelude's args() parses it.
func (b *backend) args_(_ context.Context, mod api.Module, outPtr, outCap uint32) uint32 {
	out := []byte(b.args)
	if uint32(len(out)) > outCap {
		out = out[:outCap]
	}
	mod.Memory().Write(outPtr, out)
	return uint32(len(out))
}

// ─── Regexp host functions (Go RE2, backing the Regexp class in prelude.rb) ──
//
// mruby core has no Regexp and the usual C engine won't cross-compile to wasi
// (see mruby/build_config.rb), so Regexp is implemented against Go's regexp.
// RE2 is linear-time — a model-authored pattern can't ReDoS the sandbox — at
// the cost of no backreferences or lookaround in the pattern itself.

// compileRE compiles a pattern under Ruby-ish flag semantics, memoized. Ruby's
// ^/$ are line-anchored by default (Go's match text start/end) so Go's "m" is
// always on; Ruby's /m (dot matches newline) maps to Go's "s"; /i maps to "i".
// Ruby's /x (extended) has no Go equivalent and is ignored.
func (b *backend) compileRE(pattern, flags string) (*regexp.Regexp, error) {
	goFlags := "m"
	if strings.Contains(flags, "i") {
		goFlags += "i"
	}
	if strings.Contains(flags, "m") {
		goFlags += "s"
	}
	key := goFlags + "\x00" + pattern
	b.reMu.Lock()
	defer b.reMu.Unlock()
	if b.reCache == nil {
		b.reCache = map[string]*regexp.Regexp{}
	}
	if re, ok := b.reCache[key]; ok {
		return re, nil
	}
	re, err := regexp.Compile("(?" + goFlags + ")" + pattern)
	if err != nil {
		return nil, err
	}
	b.reCache[key] = re
	return re, nil
}

// writeGuest writes s into the guest buffer, truncated to cap, returning its len.
func writeGuest(mod api.Module, outPtr, outCap uint32, s string) uint32 {
	out := []byte(s)
	if uint32(len(out)) > outCap {
		out = out[:outCap]
	}
	mod.Memory().Write(outPtr, out)
	return uint32(len(out))
}

// regexCompileCheck returns "" if the pattern compiles, else a cleaned error
// message, so the prelude's Regexp.new can raise RegexpError with it.
func (b *backend) regexCompileCheck(_ context.Context, mod api.Module, pptr, plen, fptr, flen, outPtr, outCap uint32) uint32 {
	pattern := readGuestString(mod, pptr, plen)
	flags := readGuestString(mod, fptr, flen)
	if _, err := b.compileRE(pattern, flags); err != nil {
		return writeGuest(mod, outPtr, outCap, strings.TrimPrefix(err.Error(), "error parsing regexp: "))
	}
	return writeGuest(mod, outPtr, outCap, "")
}

// regexScan returns ALL matches of the pattern in text, as JSON. Scanning the
// whole string in one call (rather than a from-offset find) keeps Ruby's
// line-anchored ^/$ semantics correct; the prelude's match/=~/scan/gsub all
// derive from this array. Offsets are CHARACTER offsets (Ruby semantics). JSON:
// {"m":[ <match>, ... ], "names":{"n":idx}} where each <match> is
// [[start,end,"str"], <group1>, ...] (g0 = whole match; non-participating group
// = null). "" means no match.
func (b *backend) regexScan(_ context.Context, mod api.Module, pptr, plen, fptr, flen, tptr, tlen, outPtr, outCap uint32) uint32 {
	pattern := readGuestString(mod, pptr, plen)
	flags := readGuestString(mod, fptr, flen)
	text := readGuestString(mod, tptr, tlen)
	re, err := b.compileRE(pattern, flags)
	if err != nil {
		return writeGuest(mod, outPtr, outCap, "")
	}
	all := re.FindAllStringSubmatchIndex(text, -1)
	if all == nil {
		return writeGuest(mod, outPtr, outCap, "")
	}
	matches := make([]interface{}, 0, len(all))
	for _, loc := range all {
		groups := make([][]interface{}, len(loc)/2)
		for i := range groups {
			bs, be := loc[2*i], loc[2*i+1]
			if bs < 0 {
				groups[i] = nil // non-participating group -> JSON null
				continue
			}
			groups[i] = []interface{}{
				utf8.RuneCountInString(text[:bs]),
				utf8.RuneCountInString(text[:be]),
				text[bs:be],
			}
		}
		matches = append(matches, groups)
	}
	names := map[string]int{}
	for i, n := range re.SubexpNames() {
		if n != "" {
			names[n] = i
		}
	}
	payload := struct {
		M     []interface{}  `json:"m"`
		Names map[string]int `json:"names"`
	}{matches, names}
	out, err := json.Marshal(payload)
	if err != nil {
		return writeGuest(mod, outPtr, outCap, "")
	}
	return writeGuest(mod, outPtr, outCap, string(out))
}

func (b *backend) budgetRemaining(_ context.Context, _ api.Module) uint64 {
	if b.opt.Budget <= 0 {
		return uint64(int64(^uint64(0) >> 1)) // max int64: "unlimited"
	}
	b.mu.Lock()
	rem := b.opt.Budget - int64(b.outTok)
	b.mu.Unlock()
	if rem < 0 {
		rem = 0
	}
	return uint64(rem)
}
