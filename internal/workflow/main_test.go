package workflow

import (
	"os"
	"testing"
)

// TestMain redirects $HOME (and %USERPROFILE% on Windows) to a throwaway temp
// dir for the whole package's test run, so journalsDir()'s os.UserHomeDir()
// call resolves under it instead of a developer's real home directory. Most
// tests here pass an explicit Options.JournalDir, but a few exercise Run's
// default-journal-dir fallback deliberately; without this redirect, those
// (and any future test that forgets to set JournalDir) would leave .jsonl
// files in the real ~/.octo/workflow-journals forever, since nothing prunes
// it mid-session.
func TestMain(m *testing.M) {
	// os.Exit below skips deferred functions, so restoration runs inline
	// after m.Run() returns rather than via defer.
	tmp, err := os.MkdirTemp("", "octo-workflow-home-test")
	var origHome, origProfile string
	var hadHome, hadProfile bool
	if err == nil {
		origHome, hadHome = os.LookupEnv("HOME")
		origProfile, hadProfile = os.LookupEnv("USERPROFILE")
		_ = os.Setenv("HOME", tmp)
		_ = os.Setenv("USERPROFILE", tmp)
	}

	code := m.Run()

	if err == nil {
		if hadHome {
			_ = os.Setenv("HOME", origHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadProfile {
			_ = os.Setenv("USERPROFILE", origProfile)
		} else {
			_ = os.Unsetenv("USERPROFILE")
		}
		_ = os.RemoveAll(tmp)
	}
	os.Exit(code)
}
