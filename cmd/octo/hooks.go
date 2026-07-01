package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/Leihb/octo-agent/internal/hooks"
)

// runHooks backs `octo hooks <subcommand>`. Today it's just `list` — a
// read-only view of what's configured across the env shim, the user-level
// hooks.yml, and the project-level hooks.yml (with its trust status), so a 7-
// event hook surface with blocking and async is inspectable rather than opaque.
func runHooks(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		return runHooksList(stdout)
	default:
		fmt.Fprintf(stderr, "octo hooks: unknown subcommand %q (try: list)\n", sub)
		return 2
	}
}

func runHooksList(out io.Writer) int {
	printed := false

	// env shim.
	if r := hooks.LoadFromEnv(); r.Configured() {
		fmt.Fprintln(out, "Environment (OCTO_HOOK_*):")
		if r.PreTurnCmd != "" {
			fmt.Fprintf(out, "  UserPromptSubmit  %s\n", r.PreTurnCmd)
		}
		if r.PostTurnCmd != "" {
			fmt.Fprintf(out, "  Stop              %s  (async)\n", r.PostTurnCmd)
		}
		printed = true
	}

	// User-level ~/.octo/hooks.yml.
	if p := hooks.UserConfigPath(); p != "" {
		if printFileHooks(out, p, "User ("+p+"):", "") {
			printed = true
		}
	}

	// Project-level <cwd>/.octo/hooks.yml, annotated with trust status.
	if cwd, err := os.Getwd(); err == nil {
		if p := hooks.ProjectConfigPath(cwd); p != "" {
			if b, rerr := os.ReadFile(p); rerr == nil {
				status := "UNTRUSTED — run octo in this repo and approve to enable"
				if hooks.IsTrusted(p, hooks.Fingerprint(b)) {
					status = "trusted"
				}
				if printFileHooks(out, p, "Project ("+p+"):", status) {
					printed = true
				}
			}
		}
	}

	fmt.Fprintln(out, "Built-in: memory reminder (UserPromptSubmit) + save-nudge (PostToolUse) when a memory directory is present.")
	if !printed {
		fmt.Fprintln(out, "No shell hooks configured. See dev-docs/hooks-redesign.md for the hooks.yml schema.")
	}
	return 0
}

// printFileHooks prints the hooks in one hooks.yml. Returns whether anything
// was printed. A parse error is reported but not fatal (list keeps going).
func printFileHooks(out io.Writer, path, header, status string) bool {
	fc, err := hooks.LoadFileConfig(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(out, "%s\n  (unreadable: %v)\n", header, err)
		}
		return false
	}
	if len(fc.Hooks) == 0 {
		return false
	}
	if status != "" {
		fmt.Fprintf(out, "%s  [%s]\n", header, status)
	} else {
		fmt.Fprintln(out, header)
	}
	events := make([]string, 0, len(fc.Hooks))
	for ev := range fc.Hooks {
		events = append(events, ev)
	}
	sort.Strings(events)
	for _, ev := range events {
		for _, h := range fc.Hooks[ev] {
			line := fmt.Sprintf("  %-16s %s", ev, h.Command)
			if h.Matcher != "" {
				line += "  matcher=" + h.Matcher
			}
			if h.Async {
				line += "  (async)"
			}
			fmt.Fprintln(out, line)
		}
	}
	return true
}
