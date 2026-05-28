//go:build windows

package memoryd

// supportedOnThisOS reports false on Windows. The v1 daemon relies on
// Signal(0) PID probes + SIGTERM graceful shutdown, neither of which
// behave reliably on Windows. The CLI surface refuses with a clear
// notice; chat-side memory falls back to the Phase 1 startup path on
// Windows automatically. Matches the sandbox's Windows fail-soft.
func supportedOnThisOS() bool { return false }
