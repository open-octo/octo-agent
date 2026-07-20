package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/config"
)

// runDoctor is `octo doctor`: a read-only health check that works even when
// config.yml won't parse — the exact case that stops `octo` from starting. It
// checks the config file (parse + semantic Validate), runs a per-endpoint card
// (design §13.2: each channel's models + API key reachability), checks the
// Default/Lite references resolve, prints ✓/✗ lines, and exits non-zero when
// anything is wrong so it can be scripted. It never mutates anything
// (`octo config --fix` does the repairs it points to).
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
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		fmt.Fprintln(stdout, "  ! config.yml not created yet — octo uses env vars + built-in defaults")
	} else {
		note(true, "config.yml parses")
	}
	for _, p := range cfg.Validate() {
		note(false, p)
	}
	fmt.Fprintf(stdout, "  ✓ %d endpoint(s) configured\n", len(cfg.Endpoints))

	// Per-endpoint cards (design §13.2): each endpoint's models + API key
	// reachability.
	if len(cfg.Endpoints) == 0 {
		fmt.Fprintln(stdout, "  ! no endpoints configured yet — run `octo config` to set one up")
	} else {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Endpoints:")
		for _, ep := range cfg.Endpoints {
			header := ep.ID
			if ep.Name != "" && ep.Name != ep.ID {
				header = fmt.Sprintf("%s (%s)", ep.ID, ep.Name)
			}
			info := ep.Provider
			if ep.BaseURL != "" {
				info += ", " + ep.BaseURL
			} else {
				info += " (provider default)"
			}
			// API key check: env var or endpoint.APIKey.
			keyOk, keyStatus := endpointKeyStatus(ep)
			mark := "✓"
			if !keyOk {
				mark = "✗"
				problems++
			}
			fmt.Fprintf(stdout, "  %s %s — %s\n", mark, header, info)
			// Models list (one line, comma-joined).
			var modelNames []string
			for _, m := range ep.Models {
				modelNames = append(modelNames, m.Model)
			}
			fmt.Fprintf(stdout, "      %d model(s): %s\n", len(ep.Models), strings.Join(modelNames, ", "))
			fmt.Fprintf(stdout, "      %s API key: %s\n", mark, keyStatus)
		}
	}

	// References section: Default + Lite must resolve to an endpoint+model.
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "References:")
	if cfg.Default == "" {
		if len(cfg.Endpoints) > 0 {
			note(true, "default = (unset, falls back to first endpoint's first model)")
		} else {
			fmt.Fprintln(stdout, "  · default = (unset, no endpoints)")
		}
	} else {
		if ep, m, ok := cfg.ResolveDefault(); ok {
			fmt.Fprintf(stdout, "  ✓ default = %s (resolves: %s::%s)\n", cfg.Default, ep.ID, m.Model)
		} else {
			note(false, fmt.Sprintf("default = %s (does not resolve)", cfg.Default))
		}
	}
	if cfg.Lite == "" {
		fmt.Fprintln(stdout, "  · lite = (none)")
	} else {
		// Lite resolves? Scan Endpoints for the composite id (resolveCompositeID
		// is unexported in package config — replicate the lookup here).
		liteEp, liteM, liteOK := resolveEndpointModel(cfg.Endpoints, cfg.Lite)
		if liteOK {
			if cfg.Lite == cfg.Default {
				note(false, fmt.Sprintf("lite = %s (equals default — should be cleared)", cfg.Lite))
			} else {
				fmt.Fprintf(stdout, "  ✓ lite = %s (resolves: %s::%s, ≠ default)\n", cfg.Lite, liteEp.ID, liteM.Model)
			}
		} else {
			note(false, fmt.Sprintf("lite = %s (does not resolve)", cfg.Lite))
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

// resolveEndpointModel splits "<endpoint_id>::<model>" and finds the matching
// endpoint + model in the slice. Replicates config.Config.resolveCompositeID
// (unexported) for use in cmd/octo.
func resolveEndpointModel(endpoints []config.Endpoint, cid string) (config.Endpoint, config.EndpointModel, bool) {
	idx := strings.Index(cid, "::")
	if idx < 0 {
		return config.Endpoint{}, config.EndpointModel{}, false
	}
	epID := cid[:idx]
	modelName := cid[idx+2:]
	for _, ep := range endpoints {
		if ep.ID != epID {
			continue
		}
		for _, m := range ep.Models {
			if m.Model == modelName {
				return ep, m, true
			}
		}
	}
	return config.Endpoint{}, config.EndpointModel{}, false
}

// endpointKeyStatus reports whether a usable API key exists for the endpoint
// (env var or endpoint.APIKey), plus a human-readable status string. Mirrors
// the old apiKeyReachable/apiKeyStatus helpers but takes an Endpoint instead
// of a ModelEntry (PR5 deleted the flat Models path).
func endpointKeyStatus(ep config.Endpoint) (ok bool, status string) {
	envVar := app.VendorAPIKeyEnvVar(ep.Provider)
	if envVar == "" {
		envVar = strings.ToUpper(ep.Provider) + "_API_KEY"
	}
	if v := os.Getenv(envVar); v != "" {
		return true, "found via $" + envVar
	}
	if ep.APIKey != "" {
		return true, "found in endpoint.api_key"
	}
	return false, "missing (set $" + envVar + " or add api_key to the endpoint)"
}
