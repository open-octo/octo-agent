package tools

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// BuildEnvContext renders the machine-level "# Environment" block shared by the
// CLI and server session builders. It owns the lines the two have in common —
// working directory, home, today's date, timezone, OS/arch, locale, and the
// shell/toolchain notes — so an env-context change lands here once instead of
// in both cmd/octo and internal/server.
//
// The git-branch line is rendered only when ok is true, and the branch/dirty
// state is supplied by the caller rather than probed here. The CLI passes its
// cwd's repo state (its cwd is a repository); the server's cwd is a workspace
// with no repo at it (see appendProjectEnvContext), so it passes ok=false and
// the repo branches are shown per mounted source folder instead.
//
// Taken once at session start (the composed prompt is frozen for the session),
// so git state is a snapshot — fresh enough to orient the model without
// re-rendering per turn and busting the prompt cache.
func BuildEnvContext(cwd, branch string, dirty, ok bool) string {
	var b strings.Builder
	b.WriteString("# Environment\n\n")
	if cwd != "" {
		fmt.Fprintf(&b, "- Working directory: %s\n", cwd)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		fmt.Fprintf(&b, "- Home directory: %s\n", home)
	}
	if ok {
		state := "clean"
		if dirty {
			state = "uncommitted changes"
		}
		fmt.Fprintf(&b, "- Git branch: %s (%s)\n", branch, state)
	}
	now := time.Now()
	fmt.Fprintf(&b, "- Today's date: %s\n", now.Format("2006-01-02"))
	// Report the local timezone (abbreviation + UTC offset) so the model can
	// convert API timestamps (e.g. cron next_run/last_run, which come back as
	// ISO-8601 UTC with a trailing "Z") to local time instead of guessing.
	_, offset := now.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	fmt.Fprintf(&b, "- Timezone: %s (UTC%s%02d:%02d)\n", now.Format("MST"), sign, offset/3600, (offset%3600)/60)
	fmt.Fprintf(&b, "- OS/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	// Locale (LANG/LC_ALL) tells the model the default encoding and sort order,
	// so it can anticipate e.g. a GBK Windows console or a non-UTF-8 locale.
	if lang := Locale(); lang != "" {
		fmt.Fprintf(&b, "- Locale: %s\n", lang)
	}
	// Platform-shell guidance (dialect + install/PATH/UAC/sudo/CLT traps) and
	// the detected toolchain — shared with both builders.
	b.WriteString(ShellEnvNote())
	b.WriteString(ToolchainNote())
	return b.String()
}
