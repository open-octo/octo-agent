package main

import (
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/server"
)

// A session belongs to the directory it works in, and the CLI only offers the
// ones belonging to the directory it was started in. That is what lets the TUI
// honour a session's recorded working directory at all: the alternative —
// resuming a session that worked somewhere else — would drag `octo -c` out of
// whatever repo the user is standing in, which is why the working directory
// used to be ignored here entirely.
//
// A session with no directory of its own — written before any transport
// recorded one — therefore belongs to no directory and is not offered here at
// all, by any form of -c. Resuming it would have to run it in whatever
// directory you happened to be in, which is the same drift this scoping exists
// to remove, only with the two halves of one session disagreeing instead of two
// surfaces. Those sessions open in the Web UI, where the server resolves a
// directory for them (the configured workspace).

// sessionDir returns the directory a session belongs to: its project's
// directory when it is in one, otherwise the directory recorded on the session
// itself. The same precedence the server resolves tool cwd with
// (Server.resolveSessionDir), minus the server-default fallback — there is no
// server here, and "wherever some process was launched" is not an identity.
func sessionDir(sessionID, own string) string {
	if dir := server.ProjectDirForSession(sessionID); dir != "" {
		return dir
	}
	return own
}

// sessionInDir reports whether s belongs to dir. Both sides are normalised so
// a symlinked checkout, or a path typed one way and recorded another, still
// match (memory.NormalizeDir is what the project registry compares with).
func sessionInDir(s *agent.Session, dir string) bool {
	return sessionInNormalizedDir(s, memory.NormalizeDir(dir))
}

// sessionInNormalizedDir is sessionInDir with the scope side already normalised,
// for the scans below: normalising it is a filesystem walk (EvalSymlinks) and
// the scope does not change between sessions.
func sessionInNormalizedDir(s *agent.Session, normalizedDir string) bool {
	own := sessionDir(s.ID, s.WorkingDir)
	if own == "" || normalizedDir == "" {
		return false
	}
	return memory.NormalizeDir(own) == normalizedDir
}

// sessionsForDir returns the sessions belonging to dir, newest first, capped at
// n (all of them when n <= 0).
//
// The full list is loaded and then filtered, rather than asking for n sessions
// and filtering those: agent.ListSessions applies its cap after sorting, so the
// n most recent sessions machine-wide could easily contain none of this
// directory's. Loading everything costs no more than it did before — the capped
// call parses every transcript head too.
func sessionsForDir(dir string, n int) ([]*agent.Session, error) {
	all, err := agent.ListSessions(0)
	if err != nil {
		return nil, err
	}
	scope := memory.NormalizeDir(dir)
	out := []*agent.Session{}
	for _, s := range all {
		if !sessionInNormalizedDir(s, scope) {
			continue
		}
		out = append(out, s)
		if n > 0 && len(out) == n {
			break
		}
	}
	return out, nil
}

// resolveSessionInDir resolves a -c argument against the sessions belonging to
// dir: "last" is this directory's most recent session rather than the
// machine's, and an id or id fragment only matches within the directory. An id
// that resolves elsewhere is reported with the directory it belongs to, since
// "no session matches" would be a lie the user can't act on.
func resolveSessionInDir(input, dir string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("session id is empty")
	}
	scoped, err := sessionsForDir(dir, 0)
	if err != nil {
		return "", err
	}
	if input == "last" {
		if len(scoped) == 0 {
			return "", fmt.Errorf("no sessions for %s yet", dir)
		}
		return scoped[0].ID, nil
	}
	for _, s := range scoped {
		if s.ID == input {
			return s.ID, nil
		}
	}
	var matches []string
	for _, s := range scoped {
		if strings.Contains(s.ID, input) {
			matches = append(matches, s.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", elsewhereError(input, dir)
	}
	return "", fmt.Errorf("ambiguous session %q matches %d sessions in %s:\n  %s",
		input, len(matches), dir, strings.Join(matches, "\n  "))
}

// elsewhereError explains a -c argument that matched nothing here: it may still
// name a real session that belongs to another directory, or one from before the
// TUI recorded a directory at all.
func elsewhereError(input, dir string) error {
	id, err := agent.ResolveSessionID(input)
	if err != nil {
		return fmt.Errorf("no session in %s matches %q", dir, input)
	}
	own := ""
	if sess, lerr := agent.LoadSession(id); lerr == nil {
		own = sessionDir(sess.ID, sess.WorkingDir)
	}
	if own == "" {
		return fmt.Errorf("session %s has no working directory of its own, so no directory can resume it — open it in the Web UI (`octo serve`)", input)
	}
	return fmt.Errorf("session %s belongs to %s — run octo from there to resume it", input, own)
}
