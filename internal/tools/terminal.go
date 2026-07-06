// Package tools provides built-in tool implementations for the octo agentic
// loop. Each tool implements agent.ToolExecutor and exposes a Definition()
// method that returns the agent.ToolDefinition the LLM sees.
package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// TerminalTimeout is the maximum time a single terminal command may run
// synchronously before it is automatically promoted to a background process.
var TerminalTimeout = 120 * time.Second

// AsyncModeNotice is the model-facing instruction appended to an async
// background-launch tool result. Wrapped in <system-reminder> so
// StripRemindersForDisplay strips it from UI cards (TUI and web).
const AsyncModeNotice = "<system-reminder>This is an ASYNC background process (a one-shot task). DO NOT use terminal_output or terminal_input on it. The system will automatically notify you when it finishes, carrying its final output. While it runs, you may continue with other independent tasks. If you have no other task to do, report the launch to the user and stop — do not spin in a polling loop.</system-reminder>"

// InteractiveModeNotice is the model-facing instruction appended to an
// interactive background-launch tool result. Wrapped in <system-reminder> so UI
// card renderers strip it automatically.
const InteractiveModeNotice = "<system-reminder>This is an INTERACTIVE background process (a long-running service or REPL). You may use terminal_output to inspect its logs and terminal_input to send it commands. After launching a service, verify it with an external check (e.g., `curl http://localhost:PORT` or `pgrep`) rather than polling terminal_output in a tight loop.</system-reminder>"

// TerminalTool is an agent.ToolExecutor that runs shell commands through the
// system shell (`sh -c` on macOS/Linux, PowerShell on Windows; see
// shellCommand). Stdout and stderr are combined and returned as the tool
// result. Non-zero exit codes are reported as extra metadata in the result
// text rather than as a tool error, so the LLM can see the failure output and
// adapt.
//
// The LLM-facing tool name is "terminal" — calling it "bash" would imply a
// hard /bin/bash dependency, but the executor shells out via the platform
// shell (the model is told which one via the environment context).
//
// mgr, when non-nil, is the BackgroundManager used for run_in_background
// launches; nil falls back to the process-wide default manager. The field
// exists so tests can inject an isolated manager.
type TerminalTool struct{ mgr *BackgroundManager }

// parseBackgroundMode interprets the run_in_background parameter, which is now
// an enum string ("async" / "interactive") rather than a boolean. A missing
// key or empty string means synchronous. Any other value (including the legacy
// boolean true) surfaces as a tool error so the model gets immediate feedback.
func parseBackgroundMode(input map[string]any) (mode BackgroundMode, useBg bool, err error) {
	raw, ok := input["run_in_background"]
	if !ok {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf(
			"terminal: run_in_background must be a string (\"async\" or \"interactive\"), got %T. "+
				"Boolean values are no longer accepted.", raw)
	}
	if s == "" {
		return "", false, nil
	}
	switch s {
	case "async":
		return BgModeAsync, true, nil
	case "interactive":
		return BgModeInteractive, true, nil
	default:
		return "", false, fmt.Errorf(
			"terminal: run_in_background must be either \"async\" or \"interactive\" (got %q). "+
				"Use \"async\" for one-shot tasks like tests/builds/installs, \"interactive\" for "+
				"long-running services or REPLs. Omit it to run synchronously.", s)
	}
}

func (t TerminalTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// Definition returns the agent.ToolDefinition the LLM receives in the tools
// list. The JSON Schema describes a required "command" string and an optional
// "run_in_background" enum.
func (TerminalTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "terminal",
		Description: "Run a shell command in the system shell (POSIX sh on macOS/Linux, PowerShell on Windows — see the Shell line in the environment context) and return stdout+stderr. Use for file operations, running programs, etc. Prefer dedicated tools (read_file, write_file, edit_file, glob, grep) over raw shell commands when they exist.\n\nChoosing sync vs background: first ask whether the process must survive this octo session (killed when the session/conversation ends? or does it need to keep running independently, e.g. exposed to the internet or to other people)? If it must survive, skip straight to detached below — async/interactive are both tracked and killed with the session regardless of how long-running they are.\n- Default (no run_in_background): runs synchronously, and auto-promotes itself to an async background process if it's still running after 120s — you never have to predict duration up front. This is the right choice for almost everything, INCLUDING compiling/testing/installing/building/linting: most of these finish in a few seconds, and sync returns that output immediately, in this same tool call, with no process id and no second turn spent waiting for a notification. Use sync unless you already have concrete reason to expect the command to run well past a few tens of seconds.\n- run_in_background:\"async\" — for a ONE-SHOT task you already expect to run long (well past a few tens of seconds — e.g. a full monorepo build, a large test suite, a slow install) AND whose result you do NOT need before continuing. Do NOT reach for this just because the command's category is \"a build\" or \"a test run\" — most builds and tests are fast; run those synchronously and let auto-promotion handle it if you're wrong about the duration. Detaches immediately, returns a process id; the system automatically notifies you on completion. DO NOT use terminal_output or terminal_input; wait for the completion notification. Do NOT use async for an install if you immediately need the installed package for the following command — run that install synchronously instead.\n- run_in_background:\"interactive\" — LONG-RUNNING services, REPLs, watchers, servers you will keep inspecting/feeding from THIS session (e.g. `rails c`, `octo serve`, `docker compose up` while you iterate against it): detaches immediately, returns a process id. You may use terminal_output to inspect logs and terminal_input to feed commands. Verify the service with an external check (e.g., `curl http://localhost:PORT`, `pgrep`) rather than polling terminal_output in a tight loop. Still killed when the session ends — NOT for a tunnel/daemon meant to outlive octo (that's detached, below), even though both look like \"a server you started\".\n\nThere is no terminal_list tool. The [BACKGROUND COMPLETED] notification for each finished task includes a summary of other async and interactive tasks still running, so you can track in-flight work without listing processes. Keep the id returned by the original terminal launch; if you lose it, the next completion notice will show other in-flight tasks.\n\nBuffering: the process is connected via pipes, not a terminal, so stdio block-buffers its output — a chatty program's logs can sit unflushed and invisible to terminal_output for a long time. On macOS/Linux, when you will want live logs, prefix the command with `stdbuf -oL` (e.g., `stdbuf -oL npm run dev`) to force line buffering.\n\nTo feed text to a command's stdin, pass it via the stdin parameter instead of embedding it in the command string — embedded text gets interpreted by the shell (backticks, quotes, $), stdin is delivered verbatim.\n\nNEVER put backticks (`) inside a quoted shell string: every shell mangles them — POSIX sh/bash run the backticked text as command substitution (you'll see 'command not found' / 'not found' noise), and PowerShell treats the backtick as an escape character and silently drops it or turns `n/`t into control chars (corrupting the text with no error). For PR/issue/commit bodies (or any text) that contain markdown code spans, ALWAYS pass the text through the stdin parameter with `--body-file -` / `-F -` rather than `--body \"...`...\"`",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"stdin": map[string]any{
					"type":        "string",
					"description": "Text piped verbatim to the command's stdin, which is then closed (EOF). Use this — not interpolation into the command string — to pass long or special-character text (quotes, backticks, $) to commands that read stdin, e.g. `gh pr create --body-file -`, `git apply -`, `python script.py`.",
				},
				"run_in_background": map[string]any{
					"type":        "string",
					"enum":        []string{"async", "interactive"},
					"description": "Run detached in the background (no 120s timeout, non-blocking). Only use this if the next step does NOT need the command's output AND you already expect the command to run well past a few tens of seconds — omitting this runs synchronously and auto-promotes to background on its own if it turns out to be slow, so most builds/tests/installs (which finish in a few seconds) should just run synchronously instead of guessing async up front. \"async\" for one-shot tasks whose result can wait and that you expect to be genuinely long-running — the system auto-notifies on completion and terminal_output is not allowed. \"interactive\" for long-running services or REPLs — terminal_output and terminal_input are allowed. Either way the process is tracked by octo and KILLED when the session ends — including a tunnel/proxy meant to keep exposing a port (e.g. ngrok, cloudflared) or any daemon meant to outlive octo: use detached:true for those instead.",
				},
				"detached": map[string]any{
					"type":        "boolean",
					"description": "Launch the command as a daemon that DELIBERATELY outlives octo: it runs in its own session, is NOT tracked, and is NOT killed when the session ends. Returns only the OS pid — terminal_output and kill_shell cannot see it, so the user manages it themselves (e.g. `kill <pid>`). Use ONLY when the user explicitly wants a process to survive the agent (e.g. exposing a port with ngrok, starting a standalone server). For tasks that should be cleaned up with the session, use run_in_background instead. No `nohup`/`&` needed — detachment is handled for you. stdout+stderr go to log_file.",
				},
				"log_file": map[string]any{
					"type":        "string",
					"description": "Where a detached:true process writes stdout+stderr. Relative paths resolve against the working dir; ~ is expanded. Defaults to ./nohup.out. Ignored unless detached:true.",
				},
			},
			"required": []string{"command"},
		},
	}
}

// Execute runs the command and returns combined output. A non-zero exit code
// is appended to the output as `[exit: <error>]` rather than being surfaced
// as an error, giving the LLM visibility into what went wrong.
//
// Internally this delegates to ExecuteStream with a nil progress callback so
// both code paths share the same exec/scanner pipeline — only the streaming
// behavior changes.
func (t TerminalTool) Execute(ctx context.Context, name string, input map[string]any) (agent.ToolResult, error) {
	return t.ExecuteStream(ctx, name, input, nil)
}

// ExecuteStream runs the command and forwards each output line to progress
// as it arrives, returning the full aggregated stdout+stderr at the end.
// progress may be nil — in that case the behaviour is identical to Execute.
//
// stdout and stderr are merged into a single stream so the LLM sees them in
// chronological order (the same way they'd appear in an interactive terminal).
// Scanner buffer cap is 1 MiB per line — commands that emit a single 10MB-
// long line will get their final line truncated, but the more usual case of
// many short lines is unaffected.
//
// Timeout promotion: if the command exceeds TerminalTimeout (120 s) the
// original process continues running in the background (no restart). The
// caller receives the output produced so far plus a background id and a
// clear instruction to wait for the completion notification.
func (t TerminalTool) ExecuteStream(
	ctx context.Context,
	_ string,
	input map[string]any,
	progress func(chunk string),
) (agent.ToolResult, error) {
	command, _ := input["command"].(string)
	if command == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf(
			"terminal: the required \"command\" argument was empty or missing. " +
				"Put the shell command in the tool's JSON arguments as the \"command\" field, " +
				`e.g. {"command": "go test ./..."}. Do not call this tool with empty arguments — ` +
				"if you have nothing to run, respond with text instead.")
	}
	stdinText, _ := input["stdin"].(string)

	// Detached launch: a daemon that deliberately outlives octo. Not tracked, not
	// killed on exit — fire-and-forget. Checked before run_in_background so
	// detached wins if both are set.
	if det, _ := input["detached"].(bool); det {
		logFile, _ := input["log_file"].(string)
		pid, logPath, err := startDetached(ctx, command, logFile)
		if err != nil {
			return agent.ToolResult{Text: ""}, err
		}
		return agent.ToolResult{
			Text: fmt.Sprintf(
				"Started detached process (pid %d). It runs independently of octo and will NOT be tracked or stopped when this session ends — manage it yourself (e.g. `kill %d`). stdout+stderr → %s",
				pid, pid, logPath),
			UI: terminalUI(command, "running", fmt.Sprintf("detached, pid %d → %s", pid, logPath)),
		}, nil
	}

	bgMode, useBg, err := parseBackgroundMode(input)
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}

	// Background launch: detach, no timeout, return the id immediately. The
	// guard above still applies, so dangerous commands are blocked either way.
	if useBg {
		id, err := t.managerFor(ctx).Start(command, bgMode)
		if err != nil {
			return agent.ToolResult{Text: ""}, err
		}
		var stdinWarn string
		if stdinText != "" {
			if werr := t.managerFor(ctx).WriteStdinAndClose(id, stdinText); werr != nil {
				stdinWarn = stdinWriteWarning(werr)
			}
		}
		notice := AsyncModeNotice
		if bgMode == BgModeInteractive {
			notice = InteractiveModeNotice
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Started %s background process %s.%s\n\n%s", bgMode, id, stdinWarn, notice),
			UI:   terminalUI(command, "running", fmt.Sprintf("%s background process %s", bgMode, id)),
		}, nil
	}

	// Synchronous path: start as a background process so that if we hit the
	// timeout the original process simply keeps running — no kill, no restart.
	// We attach an onLine callback to collect output and stream to progress in
	// real time. The polling loop only checks status (exited/running).
	//
	// The collector is capped at maxBgOutputBytes: a command that floods stdout
	// faster than the 120s timeout (e.g. a runaway `yes`) would otherwise grow an
	// unbounded buffer and OOM the agent. Past the cap we keep the most recent
	// bytes and flag that earlier output was dropped — the same tail-retention
	// policy bgProcess.append uses for the background buffer.
	var (
		outMu   sync.Mutex
		out     []byte
		dropped bool
	)
	onLine := func(line string) {
		outMu.Lock()
		out = append(out, line...)
		if len(out) > maxBgOutputBytes {
			out = out[len(out)-maxBgOutputBytes:]
			dropped = true
		}
		outMu.Unlock()
		if progress != nil {
			progress(strings.TrimRight(line, "\n"))
		}
	}

	mgr := t.managerFor(ctx)
	id, err := mgr.Start(command, BgModeAsync, WithOnLine(onLine), WithVisible(false))
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}
	var stdinWarn string
	if stdinText != "" {
		if werr := mgr.WriteStdinAndClose(id, stdinText); werr != nil {
			stdinWarn = stdinWriteWarning(werr)
		}
	}

	// Register a SyncSession so TUI (Ctrl+B) and Web ("Background" button)
	// can promote this process before the timer fires.
	sess := mgr.BeginSync()
	defer mgr.EndSync()

	// snapshot returns the collected output so far, tab-expanded and with the
	// truncation marker prepended when the cap dropped earlier bytes. The
	// stdin-write warning (if any) is appended so every return path carries it.
	snapshot := func() string {
		outMu.Lock()
		body := strings.TrimRight(string(out), "\n")
		d := dropped
		outMu.Unlock()
		body = strings.ReplaceAll(body, "\t", "    ")
		if d {
			body = "[... earlier output truncated ...]\n" + body
		}
		return body + stdinWarn
	}

	// Poll until the process exits, the user promotes it, or the timeout fires.
	// Honour the caller's context deadline if it is sooner than TerminalTimeout.
	timeout := TerminalTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
			if timeout <= 0 {
				timeout = 1 * time.Millisecond
			}
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-sess.C():
			// User promoted (Ctrl+B in TUI / button in Web): make the process
			// visible in the background panel and return. NOT reaped.
			mgr.Promote(id)
			body := MaybeSpillOutput(id, snapshot())
			return agent.ToolResult{
				Text: fmt.Sprintf("%s\n\n[promoted to async background process %s]\n\n%s", body, id, AsyncModeNotice),
				UI:   terminalUI(command, "running", body),
			}, nil
		case <-timer.C:
			// Timer backstop: covers IM channels and forgotten browser tabs.
			// Identical outcome to the manual promote path.
			mgr.Promote(id)
			body := MaybeSpillOutput(id, snapshot())
			return agent.ToolResult{
				Text: fmt.Sprintf("%s\n\n[timeout: command exceeded %s and continues as async background process %s]\n\n%s", body, TerminalTimeout, id, AsyncModeNotice),
				UI:   terminalUI(command, "running", body),
			}, nil
		case <-ctx.Done():
			// User cancelled (Esc / Ctrl-C): kill the hidden process and reap
			// it — the output is returned here and now, nothing will poll it.
			mgr.Kill(id)
			body := snapshot()
			mgr.Remove(id)
			return agent.ToolResult{
				Text: body + "\n[exit: signal: killed]",
				UI:   terminalUI(command, "failed", body+"\n[exit: signal: killed]"),
			}, nil
		default:
		}

		_, status, _, _, _ := mgr.Read(id)
		if strings.HasPrefix(status, "exited") {
			body := MaybeSpillOutput(id, snapshot())
			// Reap the hidden process: its output has been captured and
			// returned, so the bgProcess (and its retained buffer) can go.
			mgr.Remove(id)
			hint := backtickSubstitutionHint(command, body)
			if status != "exited: 0" {
				text := hint + body + "\n[exit: " + strings.TrimPrefix(status, "exited: ") + "]"
				return agent.ToolResult{Text: text, UI: terminalUI(command, "failed", text)}, nil
			}
			return agent.ToolResult{Text: hint + body, UI: terminalUI(command, "success", body)}, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// backtickSubstitutionHint detects the classic failure where the model embeds
// markdown code spans (backticks) inside a double-quoted shell string — e.g.
// `gh pr create --body "use the `web_fetch` tool"`. The shell treats the
// backtick text as command substitution and tries to run it, producing
// "command not found" noise that the model misreads as a real failure (and
// silently drops the backticked text from the body). When both signals are
// present we prepend a corrective reminder so the model fixes it mid-session
// rather than self-rationalizing the errors. Wrapped in <system-reminder> so
// StripRemindersForDisplay keeps it out of the UI card.
func backtickSubstitutionHint(command, output string) string {
	// Shells word differently: bash/zsh say "command not found", dash (the
	// usual /bin/sh on Linux) says "<name>: not found". Match both.
	if !strings.Contains(command, "`") ||
		(!strings.Contains(output, "command not found") && !strings.Contains(output, ": not found")) {
		return ""
	}
	return "<system-reminder>The backticks (`) in your double-quoted shell string were executed by the shell as command substitution — that is what produced the \"command not found\" lines above; the command itself likely did NOT fail. If those backticks were meant as literal markdown code spans (e.g. in a PR/issue/commit body), re-run using the stdin parameter with `--body-file -` / `-F -` instead of embedding the text in --body.</system-reminder>\n\n"
}

// stdinWriteWarning formats a WriteStdinAndClose failure for the tool result.
// It is a warning rather than a hard error: a fast-exiting process that never
// reads stdin (or closes it early, like `head`) loses the race with the write,
// and the command's own output/exit status is the authoritative signal — but
// the model must still learn the text was not delivered.
func stdinWriteWarning(err error) string {
	return fmt.Sprintf("\n[warning: writing stdin failed: %v — the command ran without the stdin text]", err)
}

// terminalUI builds the "terminal" UI payload. The preview keeps the TAIL of
// the output — for shell commands the error/summary is at the bottom.
func terminalUI(command, status, output string) map[string]any {
	return map[string]any{
		"type":           "terminal",
		"command":        uiHead(command, 2, 200),
		"status":         status,
		"output_preview": uiTail(output, 16, 1200),
	}
}

// TerminalOutputTool reads new output (and status) from an INTERACTIVE
// background process launched with terminal run_in_background:"interactive".
type TerminalOutputTool struct{ mgr *BackgroundManager }

func (t TerminalOutputTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// Definition describes the required "id". Reading is non-destructive; to stop a
// process use the kill_shell tool.
func (TerminalOutputTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "terminal_output",
		Description: "Peek at the recent output of an INTERACTIVE background process launched with terminal run_in_background:\"interactive\". Snapshots the last N lines plus its status (running / exited). Read-only; to stop the process use kill_shell.\n\nUse this to CHECK PROGRESS of a still-running interactive process on demand — e.g. inspect a server's startup logs. You may NOT use terminal_output on async processes; async tasks must not be polled, so wait for the [BACKGROUND COMPLETED] notification instead. This is a snapshot, not a feed — repeated calls return the current tail, so do not call it in a loop. Repeated empty snapshots of a running process are detected as polling and will trigger a hard STOP reminder.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The background process id (e.g. \"bg_1\").",
				},
				"lines": map[string]any{
					"type":        "integer",
					"description": "How many trailing lines of output to return. Defaults to 50; use 0 for all retained output.",
				},
			},
			"required": []string{"id"},
		},
	}
}

// defaultTailLines is how many trailing lines terminal_output returns when the
// caller doesn't specify.
const defaultTailLines = 50

// Execute returns a snapshot of the process's last N lines plus a status line.
// Read-only and non-advancing — it never terminates the process (that's
// kill_shell) and never moves a read cursor, so it can't "block" and there is
// no polling incentive.
func (t TerminalOutputTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	id, _ := input["id"].(string)
	if id == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: id is required")
	}
	lines := defaultTailLines
	if v, ok := input["lines"].(float64); ok { // JSON numbers decode as float64
		lines = int(v)
	}
	mgr := t.managerFor(ctx)
	mode, ok := mgr.Mode(id)
	if !ok {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: no background process %q (it may have been reaped)", id)
	}
	if mode != BgModeInteractive {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: process %q is an async task; do not poll it. Wait for the [BACKGROUND COMPLETED] notification instead", id)
	}
	out, status, found, blocked, _ := mgr.Tail(id, lines)
	if !found {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: no background process %q (it may have been reaped)", id)
	}
	header := "[status: " + status + "]"
	if out == "" {
		msg := header + "\n(no output yet)"
		// A running process with nothing to show is, more often than not, the
		// pipe full-buffering trap: stdio block-buffers when not on a TTY, so
		// logs sit in the child's buffer. Teach the fix at the moment it bites.
		if status == "running" {
			msg += "\n(if this persists for a process that should be logging, its stdio is likely " +
				"block-buffered because it's piped — on macOS/Linux relaunch it as `stdbuf -oL <cmd>` " +
				"to force line buffering)"
		}
		// Hard stop for models that keep polling despite the "do not poll"
		// instructions. The agent loop's duplicate-tool-call detector would
		// eventually kill the turn; this surfaces the problem directly in the
		// tool result so the model sees why it must stop.
		if blocked {
			msg += "\n\n[STOP: repeated empty terminal_output calls detected. " +
				"Do not poll this process again. The system will push a [BACKGROUND COMPLETED] " +
				"notification when it finishes.]"
		}
		return agent.ToolResult{Text: msg}, nil
	}
	return agent.ToolResult{Text: header + "\n" + MaybeSpillOutput(id, out)}, nil
}

// TerminalInputTool sends text to the stdin of a running INTERACTIVE background
// process launched with terminal run_in_background:"interactive". Use to
// interact with long-running interactive applications (REPLs, configuration
// wizards, servers that accept commands via stdin).
type TerminalInputTool struct{ mgr *BackgroundManager }

func (t TerminalInputTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// Definition describes the required "id" and "input".
func (TerminalInputTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "terminal_input",
		Description: "Send text input to the stdin of a running INTERACTIVE background process launched with terminal run_in_background:\"interactive\". Use to interact with long-running interactive applications (e.g., REPLs, configuration wizards, servers that accept commands via stdin). You may NOT use terminal_input on async processes. The input is written verbatim — include a trailing newline (\\n) if the process expects line-based input.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The background process id (e.g. \"bg_1\").",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "The text to send to the process's stdin. Include a trailing \\n if the process reads line-by-line.",
				},
			},
			"required": []string{"id", "input"},
		},
	}
}

// Execute writes input to the process's stdin. Unknown or exited id is an error.
// Reject async processes: terminal_input is only meaningful for interactive
// background tasks.
func (t TerminalInputTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	id, _ := input["id"].(string)
	if id == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: id is required")
	}
	text, _ := input["input"].(string)
	if text == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: input is required")
	}
	mgr := t.managerFor(ctx)
	mode, ok := mgr.Mode(id)
	if !ok {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: no background process %q", id)
	}
	if mode != BgModeInteractive {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: process %q is an async task; do not send input to it", id)
	}
	if err := mgr.WriteStdin(id, text); err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: %w", err)
	}
	return agent.ToolResult{Text: fmt.Sprintf("Sent to %s.", id)}, nil
}

// KillShellTool terminates a background process started by TerminalTool with
// run_in_background:"async" or "interactive" and returns its final output —
// the counterpart to terminal_output, which only reads. Split out from
// terminal_output's old kill:true flag so "stop this process" is a first-class,
// obvious action.
type KillShellTool struct{ mgr *BackgroundManager }

func (t KillShellTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// Definition describes the required "id".
func (KillShellTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "kill_shell",
		Description: "Terminate a background process started by terminal with run_in_background:\"async\" or \"interactive\" (the id it returned), and return its final output. Use to stop a server, watcher, or other background command you no longer need. To read output without stopping it, use terminal_output.\n\nFor long-running services (servers, watchers), prefer signal 'SIGTERM' for graceful shutdown so the process can clean up connections and release ports. Use 'SIGKILL' (default) for one-shot tasks or when SIGTERM fails.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The background process id to terminate (e.g. \"bg_1\").",
				},
				"signal": map[string]any{
					"type":        "string",
					"enum":        []string{"SIGTERM", "SIGKILL", "SIGINT"},
					"description": "Signal to send. Defaults to SIGKILL. Use SIGTERM for graceful shutdown of servers and long-running services.",
				},
			},
			"required": []string{"id"},
		},
	}
}

// Execute kills the process, then returns its final remaining output. An
// unknown id is an error (Kill reports it); an already-exited process is a
// no-op kill and still returns its last output.
func (t KillShellTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	id, _ := input["id"].(string)
	if id == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("kill_shell: id is required")
	}
	sig, _ := input["signal"].(string)
	if sig == "" {
		sig = "SIGKILL"
	}
	mgr := t.managerFor(ctx)
	if !mgr.KillWithSignal(id, sig) {
		return agent.ToolResult{Text: ""}, fmt.Errorf("kill_shell: no background process %q", id)
	}
	// Give the process a moment to flush and the waiter to record exit.
	time.Sleep(50 * time.Millisecond)

	out, status, _, _, _ := mgr.Read(id) // found guaranteed: Kill succeeded
	header := "[killed] [status: " + status + "]"
	if out == "" {
		return agent.ToolResult{Text: header + "\n(no new output)"}, nil
	}
	return agent.ToolResult{Text: header + "\n" + MaybeSpillOutput(id, out)}, nil
}
