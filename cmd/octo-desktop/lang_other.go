//go:build !darwin && !windows

package main

// osLang has no cheap system query on Linux/BSD; preferredLang falls back to
// the LC_*/LANG environment, which desktop sessions there set.
func osLang() string { return "" }
