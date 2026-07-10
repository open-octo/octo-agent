package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestTerminalTool_Definition(t *testing.T) {
	def := TerminalTool{}.Definition()
	if def.Name != "terminal" {
		t.Errorf("Name = %q, want terminal", def.Name)
	}
	if def.Description == "" {
		t.Error("Description should not be empty")
	}
	if def.Parameters["type"] != "object" {
		t.Errorf("Parameters.type = %v", def.Parameters["type"])
	}
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters.properties is not a map")
	}
	if _, ok := props["command"]; !ok {
		t.Error("Parameters.properties.command should exist")
	}
	if _, ok := props["stdin"]; !ok {
		t.Error("Parameters.properties.stdin should exist — the executor reads it, so the schema must declare it or the model never sends it")
	}
	rib, ok := props["run_in_background"].(map[string]any)
	if !ok {
		t.Fatal("Parameters.properties.run_in_background should be a map")
	}
	if rib["type"] != "string" {
		t.Errorf("run_in_background.type = %v, want string", rib["type"])
	}
	wantEnum := []string{"async", "interactive"}
	if got, ok := rib["enum"].([]string); !ok || len(got) != len(wantEnum) {
		t.Errorf("run_in_background.enum = %v, want %v", rib["enum"], wantEnum)
	}
	// The description must tell a sub-agent that backgrounding is unavailable.
	if desc, _ := rib["description"].(string); !strings.Contains(desc, "sub-agent") {
		t.Errorf("run_in_background.description should explain the sub-agent restriction; got: %q", desc)
	}
	// The explicit sync timeout must be advertised.
	if to, ok := props["timeout"].(map[string]any); !ok {
		t.Error("Parameters.properties.timeout should exist — the executor reads it, so the schema must declare it")
	} else if to["type"] != "integer" {
		t.Errorf("timeout.type = %v, want integer", to["type"])
	}
	req, ok := def.Parameters["required"].([]string)
	if !ok {
		t.Fatal("Parameters.required is not []string")
	}
	if len(req) != 1 || req[0] != "command" {
		t.Errorf("required = %v, want [command]", req)
	}
}

func TestTerminalTool_Execute_Echo(t *testing.T) {
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(result.Text) != "hello" {
		t.Errorf("result = %q, want 'hello'", result.Text)
	}
}

func TestTerminalTool_Execute_Multiline(t *testing.T) {
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo line1 && echo line2",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Text, "line1") || !strings.Contains(result.Text, "line2") {
		t.Errorf("result = %q, want line1 and line2", result.Text)
	}
}

func TestTerminalTool_Execute_NonZeroExit(t *testing.T) {
	// A failing command should return output + exit info as result text,
	// NOT as a Go error, so the LLM can read it. `echo …; exit 1` is valid in
	// both POSIX sh and PowerShell, so this stays platform-agnostic.
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo oops; exit 1",
	})
	if err != nil {
		t.Fatalf("Execute should not return a Go error for non-zero exit: %v", err)
	}
	if !strings.Contains(result.Text, "oops") {
		t.Errorf("result should contain stdout: %q", result.Text)
	}
	if !strings.Contains(result.Text, "[exit:") {
		t.Errorf("result should contain [exit:...]: %q", result.Text)
	}
}

func TestTerminalTool_Execute_EmptyCommand(t *testing.T) {
	_, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "",
	})
	if err == nil {
		t.Fatal("empty command should return error")
	}
	// The error is fed back to the model verbatim, so it must explain how to
	// retry — naming the "command" argument — rather than just stating a fact.
	if !strings.Contains(err.Error(), `"command"`) {
		t.Errorf("error should name the \"command\" argument to guide retry, got %q", err.Error())
	}
}

func TestTerminalTool_Execute_NoCommandKey(t *testing.T) {
	_, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{})
	if err == nil {
		t.Error("missing command key should return error")
	}
}

func TestDefaultRegistry_Execute(t *testing.T) {
	r := DefaultRegistry{}

	result, err := r.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo registry",
	})
	if err != nil {
		t.Fatalf("Execute terminal: %v", err)
	}
	if strings.TrimSpace(result.Text) != "registry" {
		t.Errorf("result = %q", result.Text)
	}
}

// Broader DefaultRegistry / DefaultTools assertions live in registry_test.go
// now that the registry hosts multiple tools.

// ─── ExecuteStream tests ────────────────────────────────────────────────────

func TestTerminalTool_ExecuteStream_LineByLine(t *testing.T) {
	var got []string
	progress := func(line string) { got = append(got, line) }

	result, err := TerminalTool{}.ExecuteStream(context.Background(), "terminal", map[string]any{
		"command": "echo line1 && echo line2 && echo line3",
	}, progress)
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}

	if len(got) != 3 {
		t.Errorf("expected 3 progress callbacks, got %d: %v", len(got), got)
	}
	for i, want := range []string{"line1", "line2", "line3"} {
		if i < len(got) && got[i] != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
	if result.Text != "line1\nline2\nline3" {
		t.Errorf("aggregated result = %q", result.Text)
	}
}

func TestTerminalTool_ExecuteStream_MergedStdoutAndStderr(t *testing.T) {
	var got []string
	progress := func(line string) { got = append(got, line) }

	// Write to stdout AND stderr; both should reach progress in the order
	// the shell flushes them. (sh tends to be unbuffered enough for this
	// test to be stable but we don't assert ordering — just that both are
	// present.)
	_, err := TerminalTool{}.ExecuteStream(context.Background(), "terminal", map[string]any{
		"command": "echo from-stdout; echo from-stderr 1>&2",
	}, progress)
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	joined := strings.Join(got, "|")
	if !strings.Contains(joined, "from-stdout") || !strings.Contains(joined, "from-stderr") {
		t.Errorf("both streams should reach progress; got: %s", joined)
	}
}

func TestTerminalTool_ExecuteStream_NilProgressIsExecute(t *testing.T) {
	// Calling Execute (which internally passes nil) must produce the same
	// aggregated result as ExecuteStream with a non-nil progress.
	streamResult, _ := TerminalTool{}.ExecuteStream(context.Background(), "terminal",
		map[string]any{"command": "echo hi"}, func(string) {})
	execResult, _ := TerminalTool{}.Execute(context.Background(), "terminal",
		map[string]any{"command": "echo hi"})
	if streamResult.Text != execResult.Text {
		t.Errorf("stream=%q exec=%q — should match", streamResult.Text, execResult.Text)
	}
}

func TestTerminalTool_ExecuteStream_NonZeroExitPreservesContract(t *testing.T) {
	// Exit code != 0 must still surface as result text, not Go error,
	// so the LLM can read what happened.
	result, err := TerminalTool{}.ExecuteStream(context.Background(), "terminal",
		map[string]any{"command": "echo before-fail; exit 1"}, nil)
	if err != nil {
		t.Fatalf("non-zero exit should NOT be a Go error: %v", err)
	}
	if !strings.Contains(result.Text, "before-fail") {
		t.Errorf("output should include pre-exit stdout: %q", result.Text)
	}
	if !strings.Contains(result.Text, "[exit:") {
		t.Errorf("output should include exit annotation: %q", result.Text)
	}
}

func TestTerminalTool_ContextCancel_KillsChild(t *testing.T) {
	// When the context is cancelled mid-run (e.g. user pressed Esc in the TUI),
	// the subprocess must be killed rather than left running.
	ctx, cancel := context.WithCancel(context.Background())

	// Start a long-running command that would sleep for 30s.
	go func() {
		// Cancel after a short delay so the test doesn't hang forever if the
		// fix is broken.
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	result, err := TerminalTool{}.Execute(ctx, "terminal", map[string]any{
		"command": "sleep 30",
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute should not error on cancellation: %v", err)
	}
	// The result should mention the signal/kill, not a clean exit.
	if !strings.Contains(result.Text, "[exit:") {
		t.Errorf("result should contain [exit:...] after kill; got: %q", result.Text)
	}
	// Must finish well before 30s — the cancellation killed it.
	if elapsed > 2*time.Second {
		t.Errorf("took %s — context cancellation did not kill the child promptly", elapsed)
	}
}

// Compile-time assertion that TerminalTool satisfies StreamingToolExecutor —
// catches a regression at build time, not runtime.
var _ agent.StreamingToolExecutor = TerminalTool{}

// ─── TerminalInputTool tests ────────────────────────────────────────────────

// TestTerminalTool_RespectsContextDeadline verifies that the synchronous terminal
// path shortens its timeout to the caller's context deadline, so a turn-level
// timeout is honoured even when TerminalTimeout is longer.
func TestTerminalTool_RespectsContextDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := TerminalTool{}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := tool.ExecuteStream(ctx, "terminal", map[string]any{"command": "sleep 30"}, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed %s — context deadline was not respected", elapsed)
	}
}

// TestTerminalTool_SubAgent_BackgroundRunsSync verifies that a run_in_background
// request from inside a sub-agent is ignored and the command runs synchronously,
// returning its real output in-band rather than a "started in background" id.
// Backgrounding from a sub-agent would leak the completion notice into the parent
// session and return a useless mid-state to the sub-agent.
func TestTerminalTool_SubAgent_BackgroundRunsSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	ctx := WithSubAgentMarker(context.Background())
	for _, mode := range []string{"async", "interactive"} {
		result, err := TerminalTool{}.Execute(ctx, "terminal", map[string]any{
			"command":           "echo sub-agent-hi",
			"run_in_background": mode,
		})
		if err != nil {
			t.Fatalf("mode %q: Execute: %v", mode, err)
		}
		if !strings.Contains(result.Text, "sub-agent-hi") {
			t.Errorf("mode %q: want real output in-band, got: %q", mode, result.Text)
		}
		if strings.Contains(result.Text, "background process") {
			t.Errorf("mode %q: sub-agent must not background; got: %q", mode, result.Text)
		}
		// The sub-agent must be told the mode was dropped, not silently downgraded.
		if !strings.Contains(result.Text, "not available to a sub-agent") {
			t.Errorf("mode %q: want an explanatory note, got: %q", mode, result.Text)
		}
	}
}

// TestTerminalTool_SubAgent_DetachedRunsSync verifies that detached:true from a
// sub-agent is ignored and the command runs synchronously — a sub-agent must not
// spawn a daemon that outlives it.
func TestTerminalTool_SubAgent_DetachedRunsSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	ctx := WithSubAgentMarker(context.Background())
	result, err := TerminalTool{}.Execute(ctx, "terminal", map[string]any{
		"command":  "echo sub-agent-detached",
		"detached": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Text, "sub-agent-detached") {
		t.Errorf("want real output in-band, got: %q", result.Text)
	}
	if strings.Contains(result.Text, "detached process") {
		t.Errorf("sub-agent must not detach; got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "not available to a sub-agent") {
		t.Errorf("want an explanatory note, got: %q", result.Text)
	}
}

// TestTerminalTool_SyncTimeoutKills verifies a synchronous command that exceeds
// its timeout is killed with an error and NOT moved to the background — for both
// a sub-agent and the main agent (timeout kill is universal; auto-promotion is
// gone). The default timeout applies when no `timeout` is passed.
func TestTerminalTool_SyncTimeoutKills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	orig := TerminalTimeout
	TerminalTimeout = 200 * time.Millisecond
	defer func() { TerminalTimeout = orig }()
	// Nothing should be promoted anymore, but reap defensively in case of a straggler.
	defer DefaultBackgroundManager().KillAll()

	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"sub-agent", WithSubAgentMarker(context.Background())},
		{"main agent", context.Background()},
	} {
		start := time.Now()
		result, err := TerminalTool{}.Execute(tc.ctx, "terminal", map[string]any{"command": "sleep 30"})
		if err != nil {
			t.Fatalf("%s: Execute: %v", tc.name, err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("%s: elapsed %s — timeout did not kill promptly", tc.name, elapsed)
		}
		if !strings.Contains(result.Text, "was killed") {
			t.Errorf("%s: want a kill/timeout error, got: %q", tc.name, result.Text)
		}
		if strings.Contains(result.Text, "background process") {
			t.Errorf("%s: timeout must not promote to background, got: %q", tc.name, result.Text)
		}
	}
}

// TestTerminalTool_ExplicitTimeout verifies the `timeout` parameter overrides the
// default: a short explicit timeout kills the command near that mark.
func TestTerminalTool_ExplicitTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	// Default is long; the explicit 1s timeout is what must fire.
	start := time.Now()
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "sleep 30",
		"timeout": float64(1), // JSON numbers arrive as float64
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("elapsed %s — explicit 1s timeout did not fire", elapsed)
	}
	if !strings.Contains(result.Text, "was killed") {
		t.Errorf("want a kill/timeout error, got: %q", result.Text)
	}
}

// TestTerminalTool_TimeoutValidation verifies out-of-range / malformed timeouts
// are rejected with a guiding error before the command runs.
func TestTerminalTool_TimeoutValidation(t *testing.T) {
	cases := []struct {
		name    string
		timeout any
		wantIn  string
	}{
		{"over max", float64(99999), "run_in_background"},
		{"zero", float64(0), "positive"},
		{"negative", float64(-5), "positive"},
		{"non-number", "abc", "number of seconds"},
	}
	for _, tc := range cases {
		_, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
			"command": "echo hi",
			"timeout": tc.timeout,
		})
		if err == nil {
			t.Errorf("%s: expected an error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantIn) {
			t.Errorf("%s: error should contain %q; got: %v", tc.name, tc.wantIn, err)
		}
	}
}

// TestTerminalTool_SubAgent_NotPromotable verifies a sub-agent's synchronous
// command registers NO promotable SyncSession, so the TUI Ctrl+B / web
// "Background" button can't promote it into a process that outlives the
// sub-agent. Without the guard, a manual promote signal would background the
// command and fire its completion notice into the parent session.
func TestTerminalTool_SubAgent_NotPromotable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	mgr := NewBackgroundManager()
	tool := TerminalTool{mgr: mgr}

	// Sub-agent command: run ~1s in the background so we can observe the manager
	// while it polls. It must never expose a promotable sync session.
	subDone := make(chan struct{})
	go func() {
		_, _ = tool.Execute(WithSubAgentMarker(context.Background()), "terminal", map[string]any{"command": "sleep 1"})
		close(subDone)
	}()
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mgr.HasSync() {
			t.Fatal("a sub-agent's sync command must not register a promotable SyncSession")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mgr.PromoteSync() // no-op: nothing registered
	<-subDone

	// Control: a non-sub-agent command DOES register one (proves the guard, not a
	// missing BeginSync, is what suppresses it).
	ctlDone := make(chan struct{})
	go func() {
		_, _ = tool.Execute(context.Background(), "terminal", map[string]any{"command": "sleep 1"})
		close(ctlDone)
	}()
	sawSync := false
	deadline = time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mgr.HasSync() {
			sawSync = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawSync {
		t.Error("a non-sub-agent sync command should register a promotable SyncSession")
	}
	mgr.PromoteSync() // let the control return promptly instead of waiting out the sleep
	<-ctlDone
}

func TestTerminalInputTool_Definition(t *testing.T) {
	def := TerminalInputTool{}.Definition()
	if def.Name != "terminal_input" {
		t.Errorf("Name = %q, want terminal_input", def.Name)
	}
	req, ok := def.Parameters["required"].([]string)
	if !ok {
		t.Fatal("required is not []string")
	}
	if len(req) != 2 || req[0] != "id" || req[1] != "input" {
		t.Errorf("required = %v, want [id input]", req)
	}
}

func TestTerminalInputTool_SendInput(t *testing.T) {
	// Background commands run through the platform shell (sandbox.go's
	// shellCommand). On POSIX that's `sh -c`, which execs the binary so it
	// inherits stdin directly and reads the piped line deterministically.
	// On Windows it's `pwsh -NonInteractive -Command`, which consumes the
	// redirected stdin into PowerShell's own $input stream and does not
	// reliably forward it to the spawned native process — so the child's
	// blocking read sometimes never sees the input and the test flakes with
	// an empty result. The interactive-stdin path is only reliable on POSIX
	// shells, so assert it there and skip on Windows.
	if runtime.GOOS == "windows" {
		t.Skip("terminal_input stdin delivery is non-deterministic through the pwsh -Command wrapper; POSIX-only assertion")
	}

	mgr := NewBackgroundManager()
	inputTool := TerminalInputTool{mgr: mgr}
	killTool := KillShellTool{mgr: mgr}

	// Use a small Go program as the test subject so it works on every OS
	// (Windows CI doesn't have head/sed). The program reads a line from
	// stdin and prints it back with a prefix.
	//
	// We compile it first with "go build" and then run the binary directly.
	// "go run" triggers a compilation on every launch; on Windows CI that
	// takes 1-3 s, which is longer than the 200 ms sleep below, so the
	// input arrives before the process is ready and is silently dropped.
	tmpDir := t.TempDir()
	prog := filepath.Join(tmpDir, "stdin_echo.go")
	src := `package main
import (
	"bufio"
	"fmt"
	"os"
)
func main() {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Printf("got: %s", line)
}
`
	if err := os.WriteFile(prog, []byte(src), 0644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	bin := filepath.Join(tmpDir, "stdin_echo")
	if os.PathSeparator == '\\' {
		bin += ".exe"
	}
	buildOut, err := exec.Command("go", "build", "-o", bin, prog).CombinedOutput()
	if err != nil {
		t.Fatalf("go build helper: %v\n%s", err, buildOut)
	}

	id, err := mgr.Start(bin, BgModeInteractive)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the program a moment to start and open its stdin reader.
	time.Sleep(200 * time.Millisecond)

	// Send input including a newline so the scanner can proceed.
	res, err := inputTool.Execute(context.Background(), "terminal_input", map[string]any{
		"id":    id,
		"input": "hello-world\n",
	})
	if err != nil {
		t.Fatalf("terminal_input: %v", err)
	}
	if !strings.Contains(res.Text, "Sent to") {
		t.Errorf("unexpected result: %q", res.Text)
	}

	// Wait for the process to finish, accumulating output as we poll.
	// Read is cursor-advancing: each call returns only the chunk produced
	// since the previous call, so the chunks must be concatenated — keeping
	// only the last one drops output that arrived before the exit was seen.
	var out string
	for i := 0; i < 100; i++ {
		chunk, status, _, _, _ := mgr.Read(id)
		out += chunk
		if strings.HasPrefix(status, "exited") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !strings.Contains(out, "got: hello-world") {
		t.Errorf("output should contain 'got: hello-world'; got: %q", out)
	}

	// Clean up (no-op if already exited).
	killTool.Execute(context.Background(), "kill_shell", map[string]any{"id": id})
}

func TestTerminalInputTool_ExitedProcess(t *testing.T) {
	mgr := NewBackgroundManager()
	inputTool := TerminalInputTool{mgr: mgr}

	// Start a trivial command that exits immediately.
	id, err := mgr.Start("echo done", BgModeInteractive)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for it to exit.
	for i := 0; i < 50; i++ {
		_, status, _, _, _ := mgr.Read(id)
		if strings.HasPrefix(status, "exited") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Sending input to an exited process should error.
	_, err = inputTool.Execute(context.Background(), "terminal_input", map[string]any{
		"id":    id,
		"input": "too late\n",
	})
	if err == nil {
		t.Error("expected error when writing to exited process")
	}
	if !strings.Contains(err.Error(), "already exited") {
		t.Errorf("error should mention 'already exited'; got: %v", err)
	}
}

func TestTerminalInputTool_UnknownID(t *testing.T) {
	inputTool := TerminalInputTool{}
	_, err := inputTool.Execute(context.Background(), "terminal_input", map[string]any{
		"id":    "bg_99999",
		"input": "hello\n",
	})
	if err == nil {
		t.Error("expected error for unknown id")
	}
	if !strings.Contains(err.Error(), "no background process") {
		t.Errorf("error should mention 'no background process'; got: %v", err)
	}
}

func TestTerminalInputTool_EmptyInput(t *testing.T) {
	inputTool := TerminalInputTool{}
	_, err := inputTool.Execute(context.Background(), "terminal_input", map[string]any{
		"id":    "bg_1",
		"input": "",
	})
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestTerminalInputTool_MissingID(t *testing.T) {
	inputTool := TerminalInputTool{}
	_, err := inputTool.Execute(context.Background(), "terminal_input", map[string]any{
		"input": "hello\n",
	})
	if err == nil {
		t.Error("expected error for missing id")
	}
}

func TestBacktickSubstitutionHint(t *testing.T) {
	// Fires only when BOTH signals are present: a backtick in the command and
	// "command not found" in the output.
	if got := backtickSubstitutionHint("gh pr create --body \"use `web_fetch`\"", "sh: web_fetch: command not found"); got == "" {
		t.Error("hint should fire on bash/zsh 'command not found'")
	}
	if got := backtickSubstitutionHint("gh pr create --body \"use `web_fetch`\"", "sh: 1: web_fetch: not found"); got == "" {
		t.Error("hint should fire on dash 'not found'")
	}
	if got := backtickSubstitutionHint("ls -la", "sh: foo: command not found"); got != "" {
		t.Errorf("hint must not fire without a backtick in the command, got %q", got)
	}
	if got := backtickSubstitutionHint("echo `date`", "Mon Jan 1 2026"); got != "" {
		t.Errorf("hint must not fire when there is no 'command not found' in output, got %q", got)
	}
}

func TestTerminalTool_Execute_BacktickSubstitutionHint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh backtick command substitution semantics; PowerShell differs")
	}
	// A double-quoted body with a backticked markdown span: the shell runs the
	// backtick content as a command, producing 'command not found'. The result
	// must carry the corrective reminder so the model uses stdin next time.
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo \"use the `definitely_not_a_real_cmd_xyz` tool\"",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// bash/zsh say "command not found"; dash (Linux /bin/sh) says "not found".
	if !strings.Contains(result.Text, "not found") {
		t.Fatalf("precondition: expected shell to report the command was not found, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "--body-file -") {
		t.Errorf("result should carry the backtick-substitution reminder, got %q", result.Text)
	}
}

func TestTerminalTool_Stdin_PipedVerbatim(t *testing.T) {
	// PowerShell's Get-Content (aliased as cat) requires -Path when no
	// pipeline input. stdin delivery through the pwsh -Command wrapper
	// does not reliably reach the spawned child on Windows.
	if runtime.GOOS == "windows" {
		t.Skip("stdin piping through PowerShell is non-deterministic; POSIX-only assertion")
	}

	// stdin text containing backticks, quotes, and special chars
	// must reach the process verbatim without shell interpretation.
	stdinBody := "line with `backticks` and \"double quotes\"\nsecond line\n"

	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "cat", // echoes stdin to stdout
		"stdin":   stdinBody,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Text != strings.TrimRight(stdinBody, "\n") {
		t.Errorf("stdin should reach process verbatim (trailing newline stripped by output trim).\ngot:  %q\nwant: %q", result.Text, strings.TrimRight(stdinBody, "\n"))
	}
}

func TestTerminalTool_Stdin_Background(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdin piping through PowerShell is non-deterministic; POSIX-only assertion")
	}

	mgr := NewBackgroundManager()
	term := TerminalTool{mgr: mgr}
	kill := KillShellTool{mgr: mgr}

	stdinBody := "hello stdin\n"

	res, err := term.ExecuteStream(context.Background(), "terminal", map[string]any{
		"command":           "cat",
		"run_in_background": "async",
		"stdin":             stdinBody,
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteStream bg: %v", err)
	}
	if !strings.Contains(res.Text, "Started async background process") {
		t.Fatalf("expected async background start, got: %s", res.Text)
	}
	// Extract the process id from "Started async background process <id>."
	id := strings.TrimPrefix(res.Text, "Started async background process ")
	id = strings.SplitN(id, ".", 2)[0]

	// Wait for the process to finish, accumulating output as we poll —
	// Read is cursor-advancing, so each call returns only the new chunk.
	var out string
	exited := false
	for i := 0; i < 100; i++ {
		chunk, status, _, _, _ := mgr.Read(id)
		out += chunk
		if strings.HasPrefix(status, "exited") {
			exited = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !exited {
		t.Fatal("background cat did not exit — stdin EOF likely never delivered")
	}
	if out != stdinBody {
		t.Errorf("bg stdin should reach process verbatim.\ngot:  %q\nwant: %q", out, stdinBody)
	}

	kill.Execute(context.Background(), "kill_shell", map[string]any{"id": id})
}
