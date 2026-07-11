//go:build darwin

package main

import (
	"os/exec"
	"strings"
)

// osLang returns the user's preferred UI language from macOS's AppleLanguages
// (its first element), e.g. "zh-Hans-CN" or "en-US". Empty on failure so the
// caller falls back to the environment.
func osLang() string {
	out, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return ""
	}
	// The output is a plist array; the first quoted token is the preferred one.
	if i := strings.Index(string(out), "\""); i >= 0 {
		rest := string(out)[i+1:]
		if j := strings.Index(rest, "\""); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}
