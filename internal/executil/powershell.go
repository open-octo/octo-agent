package executil

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// PowerShellUTF8EncodingPrefix is prepended to every Windows PowerShell command
// so that child-process output is interpreted and forwarded as UTF-8.  Without
// this, a Chinese Windows host (default codepage 936 / GBK) decodes UTF-8 bytes
// from Python or other tools as GBK, producing the diamond-question-mark
// garbling users see in terminal output.
const PowerShellUTF8EncodingPrefix = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $OutputEncoding = [System.Text.Encoding]::UTF8; $env:PYTHONIOENCODING='utf-8'; `

// PowerShell returns the Windows shell to run commands through: PowerShell 7+
// (`pwsh`) when present — it's the modern, cross-platform build, supports
// `&&`/`||` pipeline chaining, and defaults its file cmdlets to UTF-8 rather
// than 5.1's ANSI and UTF-16 — else Windows PowerShell 5.1 (`powershell`),
// which ships with every supported Windows and is always available as the
// fallback.
//
// It lives here rather than beside any one caller because three entry points
// need the same answer — the terminal tool (internal/tools), hook scripts
// (internal/hooks), and clipboard image capture (cmd/octo) — and they must not
// disagree about which PowerShell a session is on.
//
// Resolved once per process: the answer cannot change without a restart, and
// every terminal command would otherwise pay the lookup.
func PowerShell() string { return resolvePowerShell() }

var resolvePowerShell = sync.OnceValue(func() string {
	return powerShellIn(os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"))
})

// powerShellIn is PowerShell's logic with the MSI search roots injected, so a
// test can exercise the selection without a real PowerShell 7 install.
//
// PATH lookup alone is not enough. A pwsh installed after this process started
// — notably by the installer's own EnsurePowerShell7 step, whose winget run
// finishes moments before the installer launches the app — is on the machine
// PATH but not on the environment block this process inherited, so LookPath
// misses it and the memoization above caches that miss for the whole session.
// Probing the MSI's fixed install location covers that, and covers every
// install channel rather than just octo-setup.exe.
func powerShellIn(msiRoots ...string) string {
	if path, err := exec.LookPath("pwsh"); err == nil {
		return path
	}
	if path := probePwshMSI(msiRoots...); path != "" {
		return path
	}
	return "powershell"
}

// probePwshMSI returns the pwsh.exe the PowerShell 7 MSI installs under one of
// the given Program Files roots, or "" when none holds one. Store-distributed
// pwsh needs no probing: it sits under WindowsApps behind an execution alias
// that is on PATH from the start, which LookPath resolves.
//
// The major-version directory is part of the MSI layout and is hardcoded, so
// this silently stops finding pwsh when PowerShell 8 lands under `PowerShell\8`.
func probePwshMSI(roots ...string) string {
	for _, dir := range roots {
		// A relative root would make exec.Cmd resolve the shell against the
		// session's working directory (cmd.Dir), running a pwsh.exe out of
		// whatever repo the user happens to be in.
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		path := filepath.Join(dir, "PowerShell", "7", "pwsh.exe")
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path
		}
	}
	return ""
}
