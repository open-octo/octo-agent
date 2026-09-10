package main

import (
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/open-octo/octo-agent/internal/logfile"
	"github.com/open-octo/octo-agent/internal/serveproc"
)

// setupCLILog routes slog and the stdlib logger to a self-rotating
// ~/.octo/cli.log, returning a close func — or nil if the file can't be
// opened, which leaves the default stderr in place.
//
// Only the TUI calls this. bubbletea paints the terminal in place, so a log
// line on stderr lands in the middle of a frame and corrupts it; the headless
// one-shot path has no such conflict and keeps stderr. The stdlib logger is
// redirected alongside slog because several subsystems still log through it,
// and a torn frame doesn't care which package wrote the line.
//
// Mirrors setupHubLog in cmd/octo-desktop, including the OCTO_LOG_LEVEL
// handling, so all three backends behave alike.
func setupCLILog() func() {
	path, err := serveproc.CLILogPath()
	if err != nil {
		return nil
	}
	lw, err := logfile.Open(path, logfile.DefaultMaxBytes, logfile.DefaultBackups)
	if err != nil {
		return nil
	}
	prevWriter := log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(lw, &slog.HandlerOptions{Level: cliLogLevel()})))
	log.SetOutput(lw)
	return func() {
		// Put the stdlib logger back before the file closes: anything logged
		// during shutdown should still reach the terminal rather than a
		// closed writer.
		log.SetOutput(prevWriter)
		_ = lw.Close()
	}
}

// cliLogLevel reads OCTO_LOG_LEVEL (debug|info|warn|error), defaulting to
// info — matching `octo serve` and the desktop hub.
func cliLogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OCTO_LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
