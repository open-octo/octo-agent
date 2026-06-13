package server

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
)

// mcpStderrWriter adapts a stdio MCP subprocess's stderr stream into structured
// logging. Each complete line becomes one slog Debug record tagged source=mcp,
// so a child's diagnostics (e.g. CodeGraph's "[CodeGraph MCP] Auto-synced…")
// land in the host's log pipeline at debug level instead of interleaving with
// the server's own stderr output. At the default info level they stay quiet;
// OCTO_LOG_LEVEL=debug surfaces them.
//
// Writes arrive concurrently from every stdio server's stderr-copy goroutine,
// so access to the line buffer is mutex-guarded.
type mcpStderrWriter struct {
	mu  sync.Mutex
	buf []byte
	log *slog.Logger
}

func newMCPStderrWriter(log *slog.Logger) *mcpStderrWriter {
	if log == nil {
		log = slog.Default()
	}
	return &mcpStderrWriter{log: log}
}

func (w *mcpStderrWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if strings.TrimSpace(line) != "" {
			w.log.Debug(line, "source", "mcp")
		}
	}
	return len(p), nil
}
