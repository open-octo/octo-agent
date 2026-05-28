//go:build !windows

package memoryd

// supportedOnThisOS reports whether the daemon model works on this OS.
// Unix-like systems support the foreground-process + PID-file model
// memoryd v1 uses (Signal(0) probes, SIGTERM-driven shutdown).
func supportedOnThisOS() bool { return true }
