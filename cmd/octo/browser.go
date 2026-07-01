package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/browser"
	"github.com/open-octo/octo-agent/internal/config"
)

// defaultDebugPort is the conventional Chrome remote-debugging port. The
// chrome://inspect "Allow remote debugging" toggle serves the CDP endpoint
// here, and it's the documented value users paste into --remote-debugging-port.
const defaultDebugPort = 9222

// attachProbe attempts to attach to a running, remote-debugging-enabled Chrome
// on the given port and verifies a page-level CDP call works. It is a package
// variable so tests can substitute a fake (the real one needs a live browser).
// The detail string is shown to the user: open-tab count on success, the
// failure reason otherwise.
var attachProbe = realAttachProbe

// runBrowser dispatches `octo browser <subcommand>`. Today the only real
// subcommand is `setup`; bare `octo browser` runs it too.
func runBrowser(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "setup":
		return runBrowserSetup(stdin, stdout, stderr)
	case "-h", "--help", "help":
		browserHelp(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "octo browser: unknown subcommand %q\n", sub)
		fmt.Fprintln(stderr, "Run `octo browser --help` for usage.")
		return 2
	}
}

// runBrowserSetup walks the user through enabling Chrome remote debugging so
// the `browser` tool can drive their already-logged-in browser, then verifies
// the connection and wires browser.connect_port into the config on success.
func runBrowserSetup(stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, _ := config.Load()
	port := cfg.Browser.ConnectPort
	if port == 0 {
		port = defaultDebugPort
	}

	printBrowserSetupSteps(stdout, port)

	reader := newScannerLineReader(stdin, stdout)
	for {
		ok, detail := attachProbe(port)
		if ok {
			fmt.Fprintf(stdout, "\n✓ Connected to your Chrome on port %d (%s).\n", port, detail)
			if err := wireConnectPort(&cfg, port); err != nil {
				fmt.Fprintf(stderr, "  (couldn't save config: %v — set `browser: { connect_port: %d }` yourself)\n", err, port)
			} else {
				fmt.Fprintf(stdout, "  Saved browser.connect_port: %d to your config.\n", port)
			}
			fmt.Fprintln(stdout, "\nAll set — the `browser` tool will now attach to this Chrome.")
			return 0
		}

		fmt.Fprintf(stdout, "\n✗ Couldn't attach on port %d yet: %s\n", port, detail)
		line, ok := reader.ReadLine("Enable the toggle above (restart the browser if asked), then press Enter to retry — or 'q' to quit: ")
		if !ok || strings.EqualFold(strings.TrimSpace(line), "q") {
			fmt.Fprintln(stdout, "Setup paused — run `octo browser setup` again whenever you're ready.")
			return 1
		}
	}
}

// printBrowserSetupSteps explains why remote debugging is needed and how to
// turn it on per browser. The chrome://inspect toggle is the primary path: it
// keeps the user's logged-in session and survives Chrome's recent lockdown of
// remote debugging on the default profile.
func printBrowserSetupSteps(w io.Writer, port int) {
	fmt.Fprintln(w, `Browser automation lets octo drive your real, logged-in browser — clicking,
typing, uploading, and replaying recorded workflows on sites you're signed into.

To allow that, turn on remote debugging in the browser you want octo to use:

  Chrome   open  chrome://inspect/#remote-debugging
  Edge     open  edge://inspect/#remote-debugging

  Then tick "Allow remote debugging for this browser instance".
  (You may need to restart the browser for it to take effect.)`)
	fmt.Fprintf(w, "\nOnce enabled, the browser serves its debug endpoint on 127.0.0.1:%d.\n", port)
	fmt.Fprintln(w, "I'll check the connection now.")
}

// wireConnectPort persists browser.connect_port so the tool reuses this Chrome
// on every future session. No-op (no rewrite) when it's already set.
func wireConnectPort(cfg *config.Config, port int) error {
	if cfg.Browser.ConnectPort == port {
		return nil
	}
	cfg.Browser.ConnectPort = port
	return cfg.Save()
}

// realAttachProbe attaches the way the browser tool does — connect_port first
// (the chrome://inspect path, via /json/version), then default-profile
// discovery — and confirms a page-level CDP call works, since a browser-level
// connect can succeed while page CDP doesn't on recent Chrome.
func realAttachProbe(port int) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	b, err := browser.ConnectByPort(ctx, port)
	if err != nil {
		var derr error
		if b, derr = browser.DiscoverRunningChrome(ctx); derr != nil {
			return false, err.Error()
		}
	}
	defer b.Close()

	pages, err := b.Pages(ctx)
	if err != nil {
		return false, fmt.Sprintf("connected, but a CDP call failed: %v", err)
	}
	return true, fmt.Sprintf("%d open tab(s)", len(pages))
}

func browserHelp(w io.Writer) {
	fmt.Fprintln(w, `octo browser — set up browser automation.

The `+"`browser`"+` tool drives your real, logged-in browser over the Chrome
DevTools Protocol — clicking, typing, uploading, and replaying recorded
workflows. It needs remote debugging enabled on the browser you point it at.

Usage:
  octo browser setup    Guide you through enabling remote debugging, verify
                        the connection, and wire it into your config.

Enabling remote debugging:
  Chrome   chrome://inspect/#remote-debugging
  Edge     edge://inspect/#remote-debugging
  Tick "Allow remote debugging for this browser instance" (restart if asked).

This serves a debug endpoint on 127.0.0.1:9222; setup saves
browser.connect_port so the tool reuses your logged-in session every run.`)
}
