package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

const sessionsUsage = "usage: octo sessions [--all|-a]"

// runSessions handles `octo sessions`: print the most recent sessions for this
// directory so the user can pick an ID for `octo -c <id>`. The CLI twin of the
// TUI's /sessions command (it replaced the old --list-sessions flag).
//
// Scoped to the current directory to match what `octo -c` will actually
// resume — a list offering sessions that -c then refuses would be worse than
// no list. `--all` shows every session on the machine, which is the way to
// find one whose directory you've forgotten.
func runSessions(args []string, stdout, stderr io.Writer) int {
	all := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && (args[0] == "--all" || args[0] == "-a"):
		all = true
	case len(args) == 1 && (args[0] == "--help" || args[0] == "-h"):
		fmt.Fprintln(stdout, sessionsUsage)
		return 0
	default:
		fmt.Fprintln(stderr, sessionsUsage)
		return 2
	}

	var sessions []*agent.Session
	if all {
		// Every session, uncapped. This listing is the only way to find a
		// session whose directory you've forgotten, and the only place the
		// ones belonging to no directory appear at all — a "10 most recent
		// machine-wide" cut would leave both searches empty on any real
		// history. The scoped listing below stays capped: there, recency is
		// what the user wants.
		listed, err := agent.ListSessions(0)
		if err != nil {
			fmt.Fprintf(stderr, "octo sessions: %v\n", err)
			return 1
		}
		if len(listed) == 0 {
			fmt.Fprintln(stdout, "No saved sessions.")
			return 0
		}
		fmt.Fprintln(stdout, "Recent sessions, by directory (newest first):")
		printSessionsByDir(stdout, listed)
		fmt.Fprintln(stdout, "Resume with `octo -c <id>` from the session's own directory.")
		return 0
	}

	// Scoped listing. The directory is what decides which sessions -c can
	// resume, so failing to determine it is an error rather than an empty list
	// — matching how runChat treats the same failure.
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "octo sessions: sessions are listed per directory, and the current one could not be determined: %v\n", err)
		return 1
	}
	sessions, err = sessionsForDir(cwd, 10)
	if err != nil {
		fmt.Fprintf(stderr, "octo sessions: %v\n", err)
		return 1
	}
	if len(sessions) == 0 {
		fmt.Fprintf(stdout, "No sessions in %s yet (try `octo sessions --all`).\n", cwd)
		return 0
	}
	fmt.Fprintf(stdout, "Recent sessions in %s (newest first):\n", cwd)
	fmt.Fprintln(stdout, formatSessionList(sessions))
	fmt.Fprintln(stdout, "Resume with `octo -c <id>`, or bare `octo -c` to pick from a list.")
	return 0
}

// printSessionsByDir prints sessions grouped under the directory each belongs
// to. `--all` exists to find a session whose directory you have forgotten, so
// the directory is the answer it has to give — a flat list would show ids that
// `octo -c` refuses here without saying where they do work.
//
// Sessions with no directory at all get their own group, and it says where they
// can be opened: no directory can resume them, so a bare list of their ids would
// be a dead end. Group order follows first appearance, so it inherits the
// newest-first order of the listing.
func printSessionsByDir(w io.Writer, sessions []*agent.Session) {
	var order []string
	byDir := map[string][]*agent.Session{}
	for _, s := range sessions {
		dir := sessionDir(s.ID, s.WorkingDir)
		if _, seen := byDir[dir]; !seen {
			order = append(order, dir)
		}
		byDir[dir] = append(byDir[dir], s)
	}
	for _, dir := range order {
		fmt.Fprintln(w)
		if dir == "" {
			fmt.Fprintln(w, "  (no working directory of their own — open these in the Web UI: `octo serve`)")
		} else {
			fmt.Fprintf(w, "  %s\n", dir)
		}
		fmt.Fprintln(w, formatSessionList(byDir[dir]))
	}
	fmt.Fprintln(w)
}

// pickSessionSentinel is the value normalizeBareContinue inserts for a bare
// -c / --continue so flag parsing succeeds; runChat turns it into the
// interactive session picker. NUL can't collide with a real session ID.
const pickSessionSentinel = "\x00pick"

// normalizeBareContinue lets -c / --continue appear with no ID, meaning "pick
// a session interactively". The std flag package requires string flags to
// carry a value, so insert the sentinel when the flag is the last argument or
// the next one is another flag.
func normalizeBareContinue(args []string) []string {
	out := make([]string, 0, len(args)+1)
	for i, a := range args {
		out = append(out, a)
		if a == "-c" || a == "--continue" || a == "-continue" {
			if i == len(args)-1 || strings.HasPrefix(args[i+1], "-") {
				out = append(out, pickSessionSentinel)
			}
		}
	}
	return out
}

// sessionSelectItems renders sessions as picker rows: short ID + title as the
// label, created-at / model / turn count as the dimmed annotation. value
// carries the full ID so the resume path resolves it by exact match.
func sessionSelectItems(sessions []*agent.Session) []selectItem {
	items := make([]selectItem, 0, len(sessions))
	for _, s := range sessions {
		turns := s.TurnCount()
		plural := "s"
		if turns == 1 {
			plural = ""
		}
		items = append(items, selectItem{
			label: fmt.Sprintf("%s  %s", s.ShortID(), padCol(s.DisplayTitle(), 40)),
			desc:  fmt.Sprintf("%s  %s  %d turn%s", s.CreatedAt.Local().Format("2006-01-02 15:04"), s.Model, turns, plural),
			value: s.ID,
		})
	}
	return items
}
