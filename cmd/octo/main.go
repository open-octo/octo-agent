// Command octo is the Go implementation of the Octo AI agent.
//
// Milestones so far:
//   - M1: version / help / CLI scaffold
//   - M2: chat (single-turn, streaming, Anthropic + OpenAI)
//   - M3: interactive REPL + session persistence (this milestone)
//
// Tool use, skills, the web server, and IM bridges land in M4+.
// See dev-docs/CATCHUP_PLAN.md for the wider plan.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/open-octo/octo-agent/internal/sandbox"
	"github.com/open-octo/octo-agent/internal/shellpath"
	"github.com/open-octo/octo-agent/internal/skills"
	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/internal/version"
	"github.com/open-octo/octo-agent/internal/workflow"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the testable entry point. Splitting it out keeps main thin and
// lets the test harness drive the CLI without spawning a subprocess.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// GUI-/service-launched processes (macOS GUI/launchd, Linux .desktop/systemd)
	// inherit a minimal PATH that misses common user directories (e.g.
	// ~/.local/bin, /opt/homebrew/bin). Sync once at startup so stdio MCP servers
	// and shell tools find binaries in every mode (serve, interactive REPL,
	// headless) without requiring every user to write absolute paths in mcp.json.
	// A no-op on Windows and when the PATH already looks like a login shell's
	// (the common terminal launch).
	shellpath.SyncToLoginShell()

	// Materialize the binary's default skills/workflows to ~/.octo/skills-default
	// and ~/.octo/workflows-default so they're discoverable like any user
	// skill/workflow, and prune stale workflow run journals. Best-effort and a
	// fast no-op/pass once current; skipped for the internal fast-path commands.
	// Done before the len(args)==0 REPL early-return so a bare `octo` (the common
	// launch on Linux) still populates the defaults before Discover() runs.
	if len(args) == 0 || (args[0] != "__sandboxed-exec" && args[0] != "__complete") {
		_ = skills.MaterializeDefaults(version.Version)
		_ = tools.MaterializeDefaultWorkflows(version.Version)
		_ = workflow.PruneJournals()
	}

	if len(args) == 0 {
		return runChat(args, stdin, stdout, stderr)
	}

	switch args[0] {
	case "__sandboxed-exec":
		// Internal: the OS-sandbox re-exec shim (Linux). Applies confinement to
		// itself, then execs the real command. Not user-facing.
		return sandbox.ShimMain()
	case "__trash-backup":
		// Internal: the Windows safe-delete wrapper calls this to copy paths
		// into the trash before the real Remove-Item deletes them. Not
		// user-facing.
		return runTrashBackup(args[1:])
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "octo %s\n", version.String())
		return 0
	case "help", "--help", "-h":
		// "octo help <cmd>" prints the rich per-command help (examples, env
		// vars, key flags). Bare "octo help" prints the top-level command
		// list. Unknown subcommand → exit 2 so scripts can tell the diff
		// between "user asked for help" and "user typo'd".
		if len(args) > 1 {
			if !printCommandHelp(args[1], stdout) {
				fmt.Fprintf(stderr, "octo help: no help available for %q\n", args[1])
				fmt.Fprintln(stderr, "Run `octo help` to see the command list.")
				return 2
			}
			return 0
		}
		printUsage(stdout)
		return 0
	case "init":
		return runInit(args[1:], stdin, stdout, stderr)
	case "config":
		return runConfig(args[1:], stdin, stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdin, stdout, stderr)
	case "memory":
		return runMemory(args[1:], stdout, stderr)
	case "sessions":
		return runSessions(args[1:], stdout, stderr)
	case "trash":
		return runTrash(args[1:], stdout, stderr)
	case "skills":
		return runSkills(args[1:], stdout, stderr)
	case "workflows":
		return runWorkflows(args[1:], stdout, stderr)
	case "hooks":
		return runHooks(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdin, stdout, stderr)
	case "completion":
		return runCompletion(args[1:], stdout, stderr)
	case "upgrade":
		return runUpgrade(args[1:], stdout, stderr)
	case "browser":
		return runBrowser(args[1:], stdin, stdout, stderr)
	case "__complete":
		// Hidden subcommand the shell-completion scripts call back into.
		// Prints newline-separated candidates for the current command line.
		return runComplete(args[1:], stdout)
	default:
		// Anything else — flags or a positional message — is a chat
		// invocation: `octo "fix the bug"`, `octo -c last`, `octo --no-tools`.
		return runChat(args, stdin, stdout, stderr)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "octo — a functionality-first AI agent in a single Go binary.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: octo [flags] [\"message\"]")
	fmt.Fprintln(w, "       octo <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "With no arguments octo starts an interactive session; with a message")
	fmt.Fprintln(w, "(or piped stdin) it runs one headless agentic turn and exits.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  octo                                  Start an interactive session")
	fmt.Fprintln(w, "  octo \"summarise the README\"           Headless one-shot, then exit")
	fmt.Fprintln(w, "  echo \"explain this error\" | octo      Prompt from piped stdin")
	fmt.Fprintln(w, "  octo -c                               Pick a recent session to resume from a list")
	fmt.Fprintln(w, "  octo -c last                          Resume the most recent session")
	fmt.Fprintln(w, "  octo --no-tools                       Plain chat — disable the built-in tools")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common flags:")
	fmt.Fprintln(w, "  -c, --continue [id]      Resume a session — 'last', short ID, or substring; no ID = pick from a list")
	fmt.Fprintln(w, "  --take-over              When resuming, take over a session bound to another entry")
	fmt.Fprintln(w, "  --no-tools               Disable built-in tools (terminal, edit_file, …) + MCP/skills")
	fmt.Fprintln(w, "  --provider <name>        anthropic (default) | openai")
	fmt.Fprintln(w, "  --model <name>           Override the default model for the provider")
	fmt.Fprintln(w, "  --no-save                Don't auto-save the session to ~/.octo/sessions")
	fmt.Fprintln(w, "  --no-memory              Disable cross-session memory injection")
	fmt.Fprintln(w, "  --sandbox                OS-enforced confinement for terminal commands (macOS/Linux)")
	fmt.Fprintln(w, "  --permission-mode <m>    interactive (default; prompts on ask) | strict | auto")
	fmt.Fprintln(w, "  --quiet / --verbose      Less / more status chrome")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  config     Set your default provider/model (`octo config --fix` repairs it)")
	fmt.Fprintln(w, "  doctor     Check config + environment health (safe to run with a broken config)")
	fmt.Fprintln(w, "  serve      Start the HTTP server (REST + WebSocket + Web UI)")
	fmt.Fprintln(w, "  init       Analyze the repo and generate/update .octorules")
	fmt.Fprintln(w, "  memory     Manage cross-session memory (e.g. `octo memory list`)")
	fmt.Fprintln(w, "  sessions   List recent saved sessions (resume with `octo -c <id>`)")
	fmt.Fprintln(w, "  trash      Recover files the agent deleted or overwrote (list | restore | rm | empty)")
	fmt.Fprintln(w, "  skills     Manage skills (`octo skills list | add | update | path`)")
	fmt.Fprintln(w, "  workflows  List saved workflows (`octo workflows list | path | update`)")
	fmt.Fprintln(w, "  browser    Set up browser automation (attach to your logged-in Chrome)")
	fmt.Fprintln(w, "  upgrade    Download and install the latest release (--check to only compare)")
	fmt.Fprintln(w, "  completion Print shell-completion snippet (bash | zsh | fish)")
	fmt.Fprintln(w, "  version    Print the version and exit")
	fmt.Fprintln(w, "  help       Print this help and exit")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment:")
	fmt.Fprintln(w, "  ANTHROPIC_API_KEY / OPENAI_API_KEY      Required for the chosen provider")
	fmt.Fprintln(w, "  ANTHROPIC_BASE_URL / OPENAI_BASE_URL    Override the endpoint (proxies, compatible servers)")
	fmt.Fprintln(w, "  CUSTOM_API_KEY + CUSTOM_BASE_URL        Self-hosted / third-party endpoint (provider: custom, protocol via octo config)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run `octo help <command>` for examples + env vars (e.g. `octo help mcp`),")
	fmt.Fprintln(w, "`octo <command> --help` for a command's flags, or `octo -help` for all session flags.")
}
