// Package serveenv loads ~/.octo/serve.env (if it exists) into the process
// environment at startup. It lets a GUI-launched process (desktop app, launchd
// agent, .desktop session) pick up API keys and other variables that it can't
// inherit from a login shell — the same file the systemd/launchd packaging
// templates ship as EnvironmentFile. Without it, `octo-desktop` can't see
// TAVILY_API_KEY / OPENAI_API_KEY etc. because GUI processes inherit a minimal
// environment without the user's shell profile.
//
// Format: simple KEY=VALUE, one per line; "#" comments and blank lines are
// skipped; an optional "export " prefix is tolerated. Key and value are
// whitespace-trimmed at both ends, so `KEY = value` and `KEY=value` are
// equivalent — deliberate internal spaces must be intentional (most parsers
// treat `.env` as single-line key/value and don't carry leading/trailing
// padding into the value).
//
// Variables already present in the process environment are NOT overridden:
// explicit `FOO=bar octo serve` or systemd `Environment=` win over the file.
// Load is best-effort: a missing file is a silent no-op, and a malformed line
// is skipped without aborting the rest.
package serveenv

import (
	"os"
	"path/filepath"
	"strings"
)

// envPath returns the absolute path to the serve.env file. Kept as a var so
// tests can redirect it.
var envPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".octo", "serve.env")
}

// Load reads ~/.octo/serve.env and sets any KEY=VALUE pair whose key is not
// already present in the process environment. Call once at startup — safe to
// call repeatedly if no concurrent goroutines are reading these keys yet
// (os.Setenv mutates process-global state, so concurrent Load + Getenv races).
func Load() {
	path := envPath()
	if path == "" {
		// No home dir — nothing to load.
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Missing file is the common case on a fresh install; not an error.
		return
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Tolerate "export KEY=VALUE" (some users copy-paste from shell rc).
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue // no "=" or empty key — skip
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Explicit env wins: don't clobber what systemd/launchd/CLI already set.
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
