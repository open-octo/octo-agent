package server

import (
	"os"
	"testing"
)

// TestMain pins HOME for the entire test binary. Turn paths spawn
// fire-and-forget goroutines (title generation, follow-up suggestions) that
// can outlive an individual test's t.Setenv("HOME") scope; once the test ends
// the env is restored, and a goroutine that resolves ~/.octo after that would
// write session files into the developer's real home directory (observed as
// "stub reply" sessions in the Web UI sidebar). With a process-lifetime temp
// HOME, nothing a leaked goroutine writes can escape the sandbox. Individual
// tests that t.Setenv their own HOME still work — they restore to this temp
// dir, not the real one.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "octo-server-test-home-")
	if err == nil {
		os.Setenv("HOME", tmp)
		os.Setenv("USERPROFILE", tmp)
	}
	code := m.Run()
	if tmp != "" {
		os.RemoveAll(tmp)
	}
	os.Exit(code)
}
