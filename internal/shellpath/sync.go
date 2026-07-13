package shellpath

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// SyncToLoginShell updates the current process's PATH to include directories
// defined by the user's login shell. This matters for GUI/service launches,
// which inherit a minimal PATH that lacks common user directories: on macOS a
// process started from the GUI or launchd misses ~/.local/bin, /opt/homebrew/bin,
// etc.; on Linux a .desktop/AppImage or systemd-user launch may not source the
// login shell's profile at all.
func SyncToLoginShell() {
	syncToLoginShell(loginShellPath)
}

func syncToLoginShell(resolver func() (string, error)) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		// Only Unix-y desktops have this GUI-vs-login-shell PATH split. On
		// Windows the GUI PATH already comes from the registry (system + user),
		// so there is nothing to reconcile.
		return
	}
	current := os.Getenv("PATH")
	if looksLikeFullPath(current) {
		return
	}
	shellPath, err := resolver()
	if err != nil || shellPath == "" {
		return
	}
	merged := mergePaths(current, shellPath)
	if merged != current {
		os.Setenv("PATH", merged)
	}
}

func loginShellPath() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		// $SHELL is almost always set; fall back to each platform's default.
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/bash"
		}
	}
	// -l: login shell, so the user's profile files are loaded (macOS zsh:
	// ~/.zprofile / ~/.zshenv; Linux bash: ~/.bash_profile / ~/.profile).
	out, err := exec.Command(shell, "-l", "-c", "echo $PATH").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// looksLikeFullPath reports whether current already looks like a login/terminal
// PATH rather than the minimal PATH a GUI- or launchd-launched macOS process
// inherits. That minimal PATH consists solely of standard system directories;
// a login shell always adds at least one user- or package-manager directory
// (Homebrew, ~/.local/bin, ~/go/bin, ...). So we treat the presence of ANY
// entry outside the system set as "already full" and skip the sync, and a PATH
// made up only of system directories as "needs sync".
//
// This is deliberately stricter than checking for a few known good directories:
// path_helper can inject /usr/local/bin into an otherwise-minimal GUI PATH, so
// keying on "contains /usr/local/bin" would falsely conclude the PATH is full
// while ~/.local/bin (where the target binary actually lives) is still missing.
func looksLikeFullPath(current string) bool {
	if current == "" {
		return false
	}
	// The canonical system directories on macOS (/etc/paths + path_helper) and
	// Linux (the distro default PATH). None of these is user- or
	// package-manager-specific, so a PATH containing only these carries no
	// evidence that the login shell's environment was applied.
	systemDirs := map[string]struct{}{
		"/usr/local/sbin":  {},
		"/usr/local/bin":   {},
		"/usr/sbin":        {},
		"/usr/bin":         {},
		"/sbin":            {},
		"/bin":             {},
		"/usr/games":       {},
		"/usr/local/games": {},
	}
	for _, p := range strings.Split(current, string(os.PathListSeparator)) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := systemDirs[p]; !ok {
			return true
		}
	}
	return false
}

// mergePaths returns a PATH that keeps every entry from a, then appends any
// entries from b that are not already present. Order is preserved and duplicates
// are removed.
func mergePaths(a, b string) string {
	sep := string(os.PathListSeparator)
	seen := make(map[string]struct{})
	var parts []string
	for _, p := range strings.Split(a, sep) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		parts = append(parts, p)
	}
	for _, p := range strings.Split(b, sep) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		parts = append(parts, p)
	}
	return strings.Join(parts, sep)
}
