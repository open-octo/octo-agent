package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// toolchainProbes is the curated set of developer tools an agent reaches for
// most often. Each entry maps a single reported name to the candidate
// executables that satisfy it — `python` is satisfied by python3 or python,
// `pip` by pip3 or pip — so the model sees one stable name regardless of which
// variant is on PATH. Kept tight on purpose: a longer list is noise the model
// pays for on every turn.
//
// bundledFallback marks probes that also resolve via the octo-managed
// ~/.octo/bin fallback (populated by the Windows/macOS installers with uv/bun
// — see internal/tools/sandbox.go's withBundledBinPath). It's scoped to just
// those two: every other probe here is a real developer-machine dependency
// octo never bundles, so checking ~/.octo/bin for them would be pointless.
var toolchainProbes = []struct {
	name            string
	cmds            []string
	bundledFallback bool
}{
	{"git", []string{"git"}, false},
	{"gh", []string{"gh"}, false},
	{"node", []string{"node"}, false},
	{"npm", []string{"npm"}, false},
	{"python", []string{"python3", "python"}, false},
	{"pip", []string{"pip3", "pip"}, false},
	{"uv", []string{"uv"}, true},
	{"bun", []string{"bun"}, true},
	{"go", []string{"go"}, false},
	{"docker", []string{"docker"}, false},
	{"make", []string{"make"}, false},
}

// DetectToolchain reports which curated developer tools resolve on the current
// PATH, plus (for uv/bun) octo's own bundled ~/.octo/bin fallback. The PATH
// check is via exec.LookPath — a filesystem lookup, not a subprocess — so it
// stays cheap enough to call on every context build, including the server's
// per-turn recompose. Versions are deliberately not probed; the agent runs
// `<tool> --version` on demand when it actually needs one. On Windows
// LookPath honours PATHEXT, so npm.cmd / npx.cmd resolve as expected.
//
// The bundled-dir check matters because withBundledBinPath (sandbox.go)
// already makes a bundled uv/bun resolvable inside every child process octo
// spawns — without this check, DetectToolchain would wrongly report "missing"
// for a tool that terminal commands can actually run. The rendered note
// (ToolchainNote) doesn't distinguish "on system PATH" vs "via octo's bundled
// copy": from the model's perspective the tool is equally usable either way,
// and the mechanism is diagnostic trivia it doesn't need to act on.
func DetectToolchain() (present, missing []string) {
	bundled := bundledBinDir()
	for _, p := range toolchainProbes {
		found := false
		for _, c := range p.cmds {
			if _, err := exec.LookPath(c); err == nil {
				found = true
				break
			}
			if p.bundledFallback && bundled != "" {
				if info, err := os.Stat(filepath.Join(bundled, bundledBinName(c))); err == nil && !info.IsDir() {
					found = true
					break
				}
			}
		}
		if found {
			present = append(present, p.name)
		} else {
			missing = append(missing, p.name)
		}
	}
	return present, missing
}

// bundledBinName returns the platform-specific file name for cmd inside
// ~/.octo/bin (the installer stages a plain "uv"/"bun" on macOS/Linux and
// "uv.exe"/"bun.exe" on Windows).
func bundledBinName(cmd string) string {
	if runtime.GOOS == "windows" {
		return cmd + ".exe"
	}
	return cmd
}

// ToolchainNote renders the detected/missing toolchain as a session-context
// block, pairing with ShellEnvNote (which says how to install on this platform)
// so the model knows both what is missing and how to add it. Returns "" only in
// the degenerate case where nothing was probed.
func ToolchainNote() string {
	present, missing := DetectToolchain()
	var b strings.Builder
	if len(present) > 0 {
		b.WriteString("- Detected tools on PATH: " + strings.Join(present, ", ") + ".\n")
	}
	if len(missing) > 0 {
		b.WriteString("- Not found (install on demand if a task needs one): " + strings.Join(missing, ", ") + ".\n")
	}
	return b.String()
}
