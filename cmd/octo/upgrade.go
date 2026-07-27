package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/upgrade"
	"github.com/open-octo/octo-agent/internal/version"
)

// runUpgrade handles `octo upgrade`: download the latest GitHub release,
// verify its SHA-256 against checksums.txt, and swap this binary in place.
func runUpgrade(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "Only report whether a newer release exists")
	force := fs.Bool("force", false, "Proceed despite a dev build or an already-latest version")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if *check {
		latest, err := upgrade.Check(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "octo upgrade: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "current: %s\n", version.String())
		fmt.Fprintf(stdout, "latest:  %s\n", latest)
		if upgrade.CompareVersions(strings.TrimPrefix(version.Version, "v"), latest) < 0 {
			fmt.Fprintln(stdout, "update available — run `octo upgrade` to install")
		} else {
			fmt.Fprintln(stdout, "up to date")
		}
		return 0
	}

	daemonPid, daemonRunning := runningServeDaemon()

	err := upgrade.Run(ctx, upgrade.Options{
		Force: *force,
		Log:   func(line string) { fmt.Fprintln(stdout, line) },
	})
	switch {
	case errors.Is(err, upgrade.ErrUpToDate):
		fmt.Fprintln(stdout, "already up to date")
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "octo upgrade: %v\n", err)
		if daemonRunning {
			fmt.Fprintf(stderr, "hint: an `octo serve` daemon is running (pid %d) and may be holding the binary in place (Windows locks a running executable) — run `octo serve stop`, retry the upgrade, then `octo serve -d`\n", daemonPid)
		}
		return 1
	}
	if daemonRunning {
		fmt.Fprintf(stdout, "done — the running `octo serve` daemon (pid %d) keeps using the old binary; restart it (`octo serve stop && octo serve -d`) to switch\n", daemonPid)
	} else {
		fmt.Fprintln(stdout, "done — a running `octo serve` picks the new binary up on its next restart")
	}
	return 0
}

// runningServeDaemon reports the pid of a live `octo serve -d` daemon, if
// any, via the shared ~/.octo/serve.pid contract. A foreground serve has no
// pid file and is invisible here — that's fine, the messages this feeds are
// best-effort guidance, not a gate.
func runningServeDaemon() (int, bool) {
	pidFile, err := daemonPidFile()
	if err != nil {
		return 0, false
	}
	pid, err := readPidFile(pidFile)
	if err != nil || !isProcessAlive(pid) {
		return 0, false
	}
	return pid, true
}
