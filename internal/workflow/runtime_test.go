package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// echoAgent replies "reply<prompt>" with a fixed token cost.
func echoAgent(_ context.Context, prompt string, _ AgentOptions) AgentResult {
	return AgentResult{Reply: "reply<" + prompt + ">", InputTokens: 5, OutputTokens: 7}
}

// echoSkill returns outputs echoing the call, so a test can assert dispatch,
// params, and schema flowed through.
func echoSkill(_ context.Context, name, paramsJSON, schema string) AgentResult {
	return AgentResult{
		Reply:        fmt.Sprintf(`{"name":%q,"params":%s,"schema":%q}`, name, paramsJSON, schema),
		OutputTokens: 3,
	}
}

func TestRun_AgentRoundTrip(t *testing.T) {
	got, err := Run(context.Background(),
		`a = agent("hi"); "got: #{a}"`,
		Options{Agent: echoAgent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "got: reply<hi>" {
		t.Errorf("Output = %q", got.Output)
	}
	if got.OutputTokens != 7 || got.InputTokens != 5 {
		t.Errorf("usage = in %d out %d, want 5/7", got.InputTokens, got.OutputTokens)
	}
}

// TestRun_ParallelConcurrent proves real concurrency by counting how many
// agents are in flight at once — robust against wazero/race startup overhead
// (which a wall-clock ceiling is not). With no MaxConcurrent, all n branches
// should overlap, and results must come back in input order.
func TestRun_ParallelConcurrent(t *testing.T) {
	const n = 5
	var inFlight, peak int64
	track := func(_ context.Context, p string, _ AgentOptions) AgentResult {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond) // hold so siblings pile up
		atomic.AddInt64(&inFlight, -1)
		return AgentResult{Reply: "r<" + p + ">"}
	}
	got, err := Run(context.Background(),
		`parallel([1,2,3,4,5]) { |i| agent("t#{i}") }.join(",")`,
		Options{Agent: track})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "r<t1>,r<t2>,r<t3>,r<t4>,r<t5>" {
		t.Errorf("Output = %q", got.Output)
	}
	if peak != n {
		t.Errorf("peak in-flight = %d, want %d (all branches concurrent)", peak, n)
	}
}

func TestRun_MaxConcurrent(t *testing.T) {
	var inFlight, peak int64
	track := func(_ context.Context, p string, _ AgentOptions) AgentResult {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)
		return AgentResult{Reply: p}
	}
	_, err := Run(context.Background(),
		`parallel([1,2,3,4,5,6,7,8]) { |i| agent("x") }.size.to_s`,
		Options{Agent: track, MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want <= 2 (MaxConcurrent)", peak)
	}
}

func TestRun_Pipeline(t *testing.T) {
	// stage1 echoes; stage2 appends the stage1 result length.
	agent := func(_ context.Context, p string, _ AgentOptions) AgentResult {
		return AgentResult{Reply: p + "!"}
	}
	script := `
		s1 = ->(x) { agent("a#{x}") }
		s2 = ->(prev) { "#{prev}/len#{prev.length}" }
		pipeline([1,2], s1, s2).join(" | ")
	`
	got, err := Run(context.Background(), script, Options{Agent: agent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// s1(1) -> "aa1!"? no: agent("a1") -> "a1!" (len 3); s2 -> "a1!/len3"
	want := "a1!/len3 | a2!/len3"
	if got.Output != want {
		t.Errorf("Output = %q, want %q", got.Output, want)
	}
}

// TestRun_NestedParallelDeadlock is the regression guard for the re-entrant
// scheduler. One outer branch keeps a top-level agent in flight (fast) while a
// sibling branch sits inside a nested parallel (slow). The nested level's
// wait_any therefore receives the OUTER branch's token first. Before the
// $__wf_ready buffer, the nested loop mis-routed that foreign token —
// __agent_take'ing the outer result and resuming a nil fiber — which crashed
// the script and deadlocked the outer loop (its token never came back). The
// slow/fast skew makes the cross-level arrival deterministic; the watchdog
// turns a regression into a fast failure instead of a hung test.
func TestRun_NestedParallelDeadlock(t *testing.T) {
	agent := func(_ context.Context, p string, _ AgentOptions) AgentResult {
		if strings.Contains(p, "outer") {
			time.Sleep(10 * time.Millisecond) // completes first → lands in the nested level's wait
		} else {
			time.Sleep(80 * time.Millisecond)
		}
		return AgentResult{Reply: "r<" + p + ">"}
	}
	script := `
		parallel([1, 2]) do |i|
			if i == 1
				agent("outer")
			else
				parallel([1, 2]) { |j| agent("inner#{j}") }.join("+")
			end
		end.join(",")
	`
	type out struct {
		res Result
		err error
	}
	ch := make(chan out, 1)
	go func() {
		r, e := Run(context.Background(), script, Options{Agent: agent})
		ch <- out{r, e}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("Run: %v", got.err)
		}
		want := "r<outer>,r<inner1>+r<inner2>"
		if got.res.Output != want {
			t.Errorf("Output = %q, want %q", got.res.Output, want)
		}
	case <-time.After(30 * time.Second):
		// A real re-entrancy regression hangs forever, so this watchdog only
		// needs to fail faster than the package-level `go test` timeout — not
		// race the work. The job is ~80ms of agent sleeps; 5s tripped as a
		// false positive on a heavily-loaded CI core (goroutine wakeups delayed
		// under contention). 30s keeps the guard effective without flaking.
		t.Fatal("workflow deadlocked on nested parallel (re-entrant scheduler regression)")
	}
}

// TestRun_NestedPipelineInParallel exercises a pipeline nested inside parallel
// — the "judge panel" shape from the docs — and checks every result threads
// through correctly across the two scheduler levels.
func TestRun_NestedPipelineInParallel(t *testing.T) {
	agent := func(_ context.Context, p string, _ AgentOptions) AgentResult {
		return AgentResult{Reply: p}
	}
	script := `
		parallel([1, 2]) do |i|
			s1 = ->(x) { agent("p#{x}") }
			s2 = ->(prev) { prev + "!" }
			pipeline([i, i + 10], s1, s2).join(",")
		end.join(" | ")
	`
	got, err := Run(context.Background(), script, Options{Agent: agent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "p1!,p11! | p2!,p12!"
	if got.Output != want {
		t.Errorf("Output = %q, want %q", got.Output, want)
	}
}

func TestRun_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocked := func(c context.Context, _ string, _ AgentOptions) AgentResult {
		<-c.Done() // never returns until canceled
		return AgentResult{Err: c.Err()}
	}
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	res, err := Run(ctx,
		`parallel([1,2,3]) { |i| agent("x") }.join`,
		Options{Agent: blocked})
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if !res.Canceled {
		t.Error("Result.Canceled should be true")
	}
}

// TestRun_ParallelBranchErrorNamesItem verifies a branch that raises before its
// first agent() call surfaces the failing item's index and value, not a bare
// contextless script error — so the author knows which of N branches blew up.
func TestRun_ParallelBranchErrorNamesItem(t *testing.T) {
	_, err := Run(context.Background(),
		`parallel([10, 20, 30]) { |x| raise "boom" if x == 20; agent("x") }`,
		Options{Agent: echoAgent})
	if err == nil {
		t.Fatal("expected a script error from the raising branch")
	}
	for _, want := range []string{"item #1", "20", "boom"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the failing item (%q); got: %v", want, err)
		}
	}
}

// TestRun_ParallelBranchErrorAfterAgentNamesItem covers the second resume path:
// a branch that raises AFTER its agent() returns (the fiber is resumed with the
// result, then throws) must still be localized to its item.
func TestRun_ParallelBranchErrorAfterAgentNamesItem(t *testing.T) {
	_, err := Run(context.Background(),
		`parallel([1, 2]) { |x| r = agent("x"); raise "bad" if x == 2; r }`,
		Options{Agent: echoAgent})
	if err == nil {
		t.Fatal("expected a script error from the raising branch")
	}
	for _, want := range []string{"item #1", "bad"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the failing item (%q); got: %v", want, err)
		}
	}
}

// TestRun_NestedParallelBranchErrorKeepsInnerLocus exercises the passthrough
// branch of __resume_branch: an inner level localizes the failure ("item
// #0 …"), and the OUTER level must re-raise that already-localized message
// unchanged rather than swallowing it or double-prefixing. Guards the
// `raise e` (not bare `raise`) fix — a bare re-raise drops the message in this
// mruby build, collapsing the error to a bare class name.
func TestRun_NestedParallelBranchErrorKeepsInnerLocus(t *testing.T) {
	_, err := Run(context.Background(),
		`parallel([1]) { |x| parallel([1]) { |y| raise "deep"; agent("z") } }`,
		Options{Agent: echoAgent})
	if err == nil {
		t.Fatal("expected a script error from the nested raising branch")
	}
	// The inner locus + original message must survive the outer re-raise.
	for _, want := range []string{"item #0", "deep"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("nested error should preserve inner locus/message (%q); got: %v", want, err)
		}
	}
}

// TestRun_BudgetExhaustedInParallelKeepsMessage guards that a scheduler signal
// raised inside a branch ("workflow: token budget exhausted") passes through
// __resume_branch with its message intact rather than being localized or
// reduced to a bare class name.
func TestRun_BudgetExhaustedInParallelKeepsMessage(t *testing.T) {
	costly := func(_ context.Context, _ string, _ AgentOptions) AgentResult {
		return AgentResult{Reply: "ok", OutputTokens: 100}
	}
	// Spend the budget with a top-level agent() first — it blocks to completion,
	// so b.outTok is settled at 100 (> Budget 50) before parallel runs. Every
	// branch's pre-call budget check then raises deterministically. Without the
	// warmup, __run_fibers advances all branches to their first agent() before
	// any completion is delivered (which is what increments outTok), so whether a
	// branch observes exhaustion is a scheduling race — the source of the flake.
	_, err := Run(context.Background(),
		`agent("warmup")
		 parallel([1, 2, 3]) { |i| agent("x") }`,
		Options{Agent: costly, Budget: 50})
	if err == nil {
		t.Fatal("expected a budget-exhausted error")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("budget message should survive the branch re-raise; got: %v", err)
	}
}

// TestRun_CancellationComputeOnly guards the close-on-context-done path: a
// runaway script that never re-enters a host call (no agent() in flight) must
// still observe ctx cancellation, or workflow_kill — and the foreground
// one-shot's SIGINT — could never stop it.
func TestRun_CancellationComputeOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, `i = 0; loop { i += 1 }`, Options{Agent: echoAgent})
		done <- err
	}()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("compute-only loop did not observe cancellation — Run is uninterruptible")
	}
}

func TestRun_BudgetExhausted(t *testing.T) {
	// First agent spends 100 output tokens; Budget is 50, so the budget is
	// already over after one call and the next agent() raises.
	costly := func(_ context.Context, _ string, _ AgentOptions) AgentResult {
		return AgentResult{Reply: "ok", OutputTokens: 100}
	}
	script := `
		agent("first")
		begin
			agent("second")
			"no-raise"
		rescue => e
			"raised: #{e.message}"
		end
	`
	got, err := Run(context.Background(), script, Options{Agent: costly, Budget: 50})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got.Output, "budget exhausted") {
		t.Errorf("Output = %q, want budget-exhausted raise", got.Output)
	}
}

func TestRun_ProgressLifecycle(t *testing.T) {
	var prog []string
	_, err := Run(context.Background(),
		`parallel([1,2]) { |i| agent("task-#{i}") }.join`,
		Options{
			Agent:    echoAgent,
			Progress: func(s string) { prog = append(prog, s) },
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Two starts (→) and two finishes (✓), labels carrying the prompt.
	var starts, dones int
	for _, p := range prog {
		switch {
		case strings.HasPrefix(p, "→ "):
			starts++
		case strings.HasPrefix(p, "✓ "):
			dones++
		}
	}
	if starts != 2 || dones != 2 {
		t.Errorf("progress = %v; want 2 starts + 2 dones", prog)
	}
}

func TestRun_Log(t *testing.T) {
	var lines []string
	got, err := Run(context.Background(),
		`log("starting"); log("done"); "ok"`,
		Options{Agent: echoAgent, Log: func(s string) { lines = append(lines, s) }})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "ok" {
		t.Errorf("Output = %q", got.Output)
	}
	if len(lines) != 2 || lines[0] != "starting" || lines[1] != "done" {
		t.Errorf("log lines = %v", lines)
	}
}

// TestRun_AgentOptions verifies agent(prompt, opts) forwards model / tools /
// read_only to the AgentFunc, and that a bare agent(prompt) yields zero opts.
func TestRun_AgentOptions(t *testing.T) {
	var got []AgentOptions
	var mu sync.Mutex
	rec := func(_ context.Context, _ string, o AgentOptions) AgentResult {
		mu.Lock()
		got = append(got, o)
		mu.Unlock()
		return AgentResult{Reply: "ok"}
	}
	script := `
		agent("a", model: "haiku", tools: ["read_file", "grep"], read_only: true)
		agent("b")
		"done"
	`
	if _, err := Run(context.Background(), script, Options{Agent: rec}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d calls, want 2", len(got))
	}
	// Ordering across two sequential top-level agent() calls is deterministic.
	first, second := got[0], got[1]
	if first.Model != "haiku" {
		t.Errorf("opts.Model = %q, want haiku", first.Model)
	}
	if len(first.Tools) != 2 || first.Tools[0] != "read_file" || first.Tools[1] != "grep" {
		t.Errorf("opts.Tools = %v, want [read_file grep]", first.Tools)
	}
	if !first.ReadOnly {
		t.Error("opts.ReadOnly = false, want true")
	}
	if second.Model != "" || len(second.Tools) != 0 || second.ReadOnly {
		t.Errorf("bare agent() should yield zero opts, got %+v", second)
	}
}

// TestRun_AgentSchema verifies agent(prompt, schema: "...") forwards the schema
// string to the AgentFunc, and a bare call yields an empty schema.
func TestRun_AgentSchema(t *testing.T) {
	var got []AgentOptions
	var mu sync.Mutex
	rec := func(_ context.Context, _ string, o AgentOptions) AgentResult {
		mu.Lock()
		got = append(got, o)
		mu.Unlock()
		return AgentResult{Reply: `{"ok":true}`}
	}
	script := `
		agent("a", schema: '{"type":"object"}')
		agent("b")
		"done"
	`
	if _, err := Run(context.Background(), script, Options{Agent: rec}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d calls, want 2", len(got))
	}
	if got[0].Schema != `{"type":"object"}` {
		t.Errorf("opts.Schema = %q, want the JSON schema string", got[0].Schema)
	}
	if got[1].Schema != "" {
		t.Errorf("bare agent() should yield empty schema, got %q", got[1].Schema)
	}
}

// TestRun_AgentIsolation verifies agent(prompt, isolation: "...") forwards the
// isolation mode to the AgentFunc.
func TestRun_AgentIsolation(t *testing.T) {
	var got []AgentOptions
	var mu sync.Mutex
	rec := func(_ context.Context, _ string, o AgentOptions) AgentResult {
		mu.Lock()
		got = append(got, o)
		mu.Unlock()
		return AgentResult{Reply: "ok"}
	}
	script := `
		agent("a", isolation: "worktree")
		agent("b")
		"done"
	`
	if _, err := Run(context.Background(), script, Options{Agent: rec}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d calls, want 2", len(got))
	}
	if got[0].Isolation != "worktree" {
		t.Errorf("opts.Isolation = %q, want worktree", got[0].Isolation)
	}
	if got[1].Isolation != "" {
		t.Errorf("bare agent() should yield empty isolation, got %q", got[1].Isolation)
	}
}

func TestRun_Phase(t *testing.T) {
	var lines []string
	got, err := Run(context.Background(),
		`phase("Review"); log("x"); phase("Verify"); "ok"`,
		Options{Agent: echoAgent, Log: func(s string) { lines = append(lines, s) }})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "ok" {
		t.Errorf("Output = %q", got.Output)
	}
	want := []string{"== phase: Review", "x", "== phase: Verify"}
	if len(lines) != len(want) {
		t.Fatalf("log lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestRun_ExceptionHandling(t *testing.T) {
	// Exercises mruby begin/rescue (setjmp/longjmp via the wasm EH proposal).
	got, err := Run(context.Background(),
		`begin; raise "boom"; rescue => e; "caught: #{e.message}"; end`,
		Options{Agent: echoAgent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "caught: boom" {
		t.Errorf("Output = %q", got.Output)
	}
}

func TestRun_SkillRoundTrip(t *testing.T) {
	// skill() returns a native Ruby Hash (parsed from the outputs JSON); params
	// and name flow through and are readable.
	got, err := Run(context.Background(),
		`s = skill("download-excels", {"month" => "2026-06"}); "name=#{s["name"]} month=#{s["params"]["month"]}"`,
		Options{Agent: echoAgent, Skill: echoSkill})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "name=download-excels month=2026-06" {
		t.Errorf("Output = %q", got.Output)
	}
	if got.OutputTokens != 3 {
		t.Errorf("skill usage not counted: out=%d, want 3", got.OutputTokens)
	}
}

func TestRun_SkillInPipeline(t *testing.T) {
	// skill() composes in a pipeline: stage 1 replays a skill returning a file
	// list, stage 2 consumes the parsed Hash.
	dl := func(_ context.Context, _, _, _ string) AgentResult {
		return AgentResult{Reply: `{"files":["a","b","c"]}`}
	}
	script := `
		s1 = ->(item) { skill("download") }
		s2 = ->(prev) { prev["files"].length }
		pipeline(["x"], s1, s2)[0].to_s
	`
	got, err := Run(context.Background(), script, Options{Agent: echoAgent, Skill: dl})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "3" {
		t.Errorf("Output = %q, want 3", got.Output)
	}
}

func TestRun_SkillUnavailable(t *testing.T) {
	// No Skill wired: skill() fails with a clear error rather than a JSON crash.
	_, err := Run(context.Background(), `skill("x")`, Options{Agent: echoAgent})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("want skill-unavailable error, got %v", err)
	}
}

func TestRun_SkillFailureRaises(t *testing.T) {
	// A skill that errors surfaces as a raised, message-carrying failure (halting
	// the run) — not a swallowed error string fed downstream.
	fail := func(_ context.Context, name, _, _ string) AgentResult {
		return AgentResult{Err: fmt.Errorf("boom in %s", name)}
	}
	_, err := Run(context.Background(), `skill("x")`, Options{Agent: echoAgent, Skill: fail})
	if err == nil || !strings.Contains(err.Error(), "boom in x") {
		t.Fatalf("want the skill failure surfaced, got %v", err)
	}
}

func TestRun_ScriptError(t *testing.T) {
	_, err := Run(context.Background(),
		`this_method_does_not_exist(42)`,
		Options{Agent: echoAgent})
	if err == nil || !strings.Contains(err.Error(), "script error") {
		t.Errorf("err = %v, want script error", err)
	}
	// The misleading mruby position prefix ("(unknown):0:") must be stripped,
	// while the method name and error class survive so the model can self-fix.
	if strings.Contains(err.Error(), "(unknown)") {
		t.Errorf("err leaks mruby position noise: %v", err)
	}
	for _, want := range []string{"this_method_does_not_exist", "NoMethodError"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestCleanScriptError(t *testing.T) {
	cases := []struct {
		name, in string
		want     string // substring that must be present
		absent   string // substring that must be gone
	}{
		{"nomethod", "(unknown):0: undefined method 'nope' for Object (NoMethodError)",
			"undefined method 'nope'", "(unknown)"},
		{"syntax", "line 90:0: syntax error, unexpected end of file\n(unknown):0: syntax error (SyntaxError)",
			"unexpected end of file", "line 90"},
		{"trace", "trace (most recent call last):\n(unknown):0:in +: String cannot be converted to Float (TypeError)",
			"cannot be converted to Float", "trace (most recent"},
	}
	for _, c := range cases {
		got := cleanScriptError(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: cleanScriptError(%q) = %q, want substring %q", c.name, c.in, got, c.want)
		}
		if c.absent != "" && strings.Contains(got, c.absent) {
			t.Errorf("%s: cleanScriptError(%q) = %q, must not contain %q", c.name, c.in, got, c.absent)
		}
	}
	// A non-blank input that is entirely position-noise must not vanish — it
	// falls back to the raw stderr so the failure is never silently hidden.
	if got := cleanScriptError("(unknown):0:"); got == "" {
		t.Error("cleanScriptError of noise-only input returned empty string")
	}
}

func TestRun_RequiresAgent(t *testing.T) {
	_, err := Run(context.Background(), `"x"`, Options{})
	if err == nil || !strings.Contains(err.Error(), "Agent is required") {
		t.Errorf("err = %v, want Agent-required", err)
	}
}

// Ensure distinct prompts flow through correctly under concurrency.
func TestRun_ParallelResultsMatchOrder(t *testing.T) {
	got, err := Run(context.Background(),
		`parallel((1..20).to_a) { |i| agent(i.to_s) }.join(",")`,
		Options{Agent: echoAgent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var want []string
	for i := 1; i <= 20; i++ {
		want = append(want, fmt.Sprintf("reply<%d>", i))
	}
	if got.Output != strings.Join(want, ",") {
		t.Errorf("Output = %q", got.Output)
	}
}

// TestRun_JSONAvailable guards that the embedded mruby.wasm carries the JSON
// gem: a script can round-trip JSON.parse / JSON.generate. This is what lets a
// workflow decode a schema-constrained agent() reply and re-encode structured
// data into a prompt.
func TestRun_JSONAvailable(t *testing.T) {
	got, err := Run(context.Background(),
		`h = JSON.parse('{"bugs":[{"id":1},{"id":2}]}'); JSON.generate({"n" => h["bugs"].size})`,
		Options{Agent: echoAgent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != `{"n":2}` {
		t.Errorf("Output = %q, want {\"n\":2}", got.Output)
	}
}

// TestRun_Args proves the args primitive surfaces the run's input JSON as
// native Ruby (Hash/Array/scalar).
func TestRun_Args(t *testing.T) {
	got, err := Run(context.Background(),
		`"#{args["target"]}:#{args["lenses"].size}"`,
		Options{Agent: echoAgent, Args: `{"target":"internal/agent","lenses":["a","b","c"]}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "internal/agent:3" {
		t.Errorf("Output = %q, want internal/agent:3", got.Output)
	}
}

// TestRun_ArgsNilWhenAbsent: with no Args, the args primitive returns nil.
func TestRun_ArgsNilWhenAbsent(t *testing.T) {
	got, err := Run(context.Background(),
		`args.nil? ? "none" : "some"`,
		Options{Agent: echoAgent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "none" {
		t.Errorf("Output = %q, want none", got.Output)
	}
}

// TestRun_ResumeArgsMismatch: resuming the same script with different args is
// rejected, since args drives control flow and invalidates cached results.
func TestRun_ResumeArgsMismatch(t *testing.T) {
	dir := t.TempDir()
	script := `agent(args["q"])`
	j, _ := CreateJournal(dir, "wf-args", runIdentityHash(script, `{"q":"first"}`))
	_ = j.Close()

	_, err := Run(newTestCtx(t), script, Options{
		Agent:      echoAgent,
		JournalDir: dir,
		ResumeFrom: "wf-args",
		Args:       `{"q":"second"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "different script") {
		t.Errorf("err = %v, want different-script error on args mismatch", err)
	}
}

// TestRun_Regexp exercises the RE2-backed Regexp layer (host __regex_* +
// prelude Regexp/MatchData/String). Requires the rebuilt mruby.wasm that
// imports regex_compile_check/regex_scan.
func TestRun_Regexp(t *testing.T) {
	cases := []struct{ name, script, want string }{
		{"match index (=~)", `("order-1234" =~ /\d+/).to_s`, "6"},
		{"capture group", `"order-1234".match(/(\d+)/)[1]`, "1234"},
		{"scan flat", `"a1b2c3".scan(/\d/).join(",")`, "1,2,3"},
		{"scan groups", `"k1=v1;k2=v2".scan(/(\w+)=(\w+)/).map { |a| a.join(":") }.join(",")`, "k1:v1,k2:v2"},
		{"gsub backref", `"John Smith".gsub(/(\w+) (\w+)/, '\2 \1')`, "Smith John"},
		{"gsub block", `"abc".gsub(/./) { |c| c + "!" }`, "a!b!c!"},
		{"sub once", `"aaa".sub(/a/, "b")`, "baa"},
		{"ignorecase flag", `("HELLO" =~ /hello/i).to_s`, "0"},
		{"match? false", `"x".match?(/y/) ? "yes" : "no"`, "no"},
		{"bracket regex", `"foo123"[/\d+/]`, "123"},
		{"split regex", `"a, b,c".split(/,\s*/).join("|")`, "a|b|c"},
		{"named capture", `"2026-07".match(/(?P<y>\d+)-(?P<m>\d+)/)["y"]`, "2026"},
		{"gsub named backref", `"2026-07".gsub(/(?P<y>\d+)-(?P<m>\d+)/, '\k<m>/\k<y>')`, "07/2026"},
		{"line-anchored ^ (Ruby default)", `"a\nb\nc".scan(/^\w/).join`, "abc"},
		{"Regexp.escape", `Regexp.escape("1+1")`, `1\+1`},
		{"plain string split still works", `"a,b,c".split(",").join("|")`, "a|b|c"},
		{"default whitespace split still works", `"a  b c".split.join("|")`, "a|b|c"},
		// Multibyte: offsets are byte offsets and mruby strings are byte-indexed,
		// so slicing-based ops (gsub/split) must not corrupt UTF-8.
		{"utf8 gsub", `"你好x好".gsub(/x/, "-")`, "你好-好"},
		{"utf8 split", `"a→b→c".split(/→/).join("|")`, "a|b|c"},
		{"utf8 sub before multibyte", `"café x".sub(/x/, "y")`, "café y"},
		// Ruby includes the separator's capture groups in split output.
		{"split keeps captures", `"a1b2c".split(/(\d)/).join("|")`, "a|1|b|2|c"},
	}
	for _, tc := range cases {
		got, err := Run(context.Background(), tc.script, Options{Agent: echoAgent})
		if err != nil {
			t.Errorf("%s: Run: %v", tc.name, err)
			continue
		}
		if got.Output != tc.want {
			t.Errorf("%s: Output = %q, want %q", tc.name, got.Output, tc.want)
		}
	}
}

// TestRun_RegexpInvalidRaises verifies an un-compilable pattern raises
// RegexpError (via the host compile check) rather than silently misbehaving.
func TestRun_RegexpInvalidRaises(t *testing.T) {
	got, err := Run(context.Background(),
		`begin; Regexp.new("("); "no-raise"; rescue RegexpError; "raised"; end`,
		Options{Agent: echoAgent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Output != "raised" {
		t.Errorf("Output = %q, want raised", got.Output)
	}
}
