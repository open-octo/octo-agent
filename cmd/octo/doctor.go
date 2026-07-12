package main

import (
	"fmt"
	"io"

	"github.com/open-octo/octo-agent/internal/config"
)

// runDoctor is `octo doctor`: a read-only health check that works even when
// config.yml won't parse — the exact case that stops `octo` from starting. It
// checks the config file (parse + semantic Validate) and a couple of
// environment essentials, prints ✓/✗ lines, and exits non-zero when anything is
// wrong so it can be scripted. It never mutates anything (`octo config --fix`
// does the repairs it points to).
func runDoctor(_ []string, _ io.Reader, stdout, stderr io.Writer) int {
	problems := 0
	note := func(ok bool, msg string) {
		mark := "✓"
		if !ok {
			mark = "✗"
			problems++
		}
		fmt.Fprintf(stdout, "  %s %s\n", mark, msg)
	}

	fmt.Fprintln(stdout, "octo doctor — checking your setup")
	fmt.Fprintln(stdout)

	path, perr := config.Path()
	if perr != nil {
		fmt.Fprintf(stderr, "  ✗ cannot resolve config path: %v\n", perr)
		return 1
	}
	fmt.Fprintf(stdout, "config: %s\n", path)

	cfg, err := config.Load()
	if err != nil {
		note(false, fmt.Sprintf("config.yml does not parse: %v", err))
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "  → run `octo config --fix` to restore the last good backup, or edit the file by hand.")
		fmt.Fprintf(stdout, "\n%d problem(s) found.\n", problems)
		return 1
	}
	note(true, "config.yml parses")
	for _, p := range cfg.Validate() {
		note(false, p)
	}

	// Environment essentials — only meaningful once a model is configured.
	if len(cfg.Models) == 0 {
		fmt.Fprintln(stdout, "  ! no models configured yet — run `octo config` to set one up")
	} else {
		def := cfg.DefaultEntry()
		prov := def.Provider
		if prov == "" {
			prov = providerAnthropic
		}
		if apiKeyReachable(prov, def) {
			note(true, fmt.Sprintf("default model %q (%s): API key found", def.Model, prov))
		} else {
			note(false, fmt.Sprintf("default model %q (%s): %s", def.Model, prov, apiKeyStatus(prov, def)))
		}
	}

	fmt.Fprintln(stdout)
	if problems == 0 {
		fmt.Fprintln(stdout, "All checks passed.")
		return 0
	}
	fmt.Fprintf(stdout, "%d problem(s) found — `octo config --fix` can repair config issues.\n", problems)
	return 1
}
