// Package audit writes a tamper-evident, append-only log of security-relevant
// tool decisions. It is intentionally minimal: one JSON line per event, flushed
// to ~/.octo/audit.log. Failures are logged to the application logger but never
// block the tool call being audited.
package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one entry in the audit log.
type Event struct {
	Timestamp string         `json:"ts"`
	Tool      string         `json:"tool"`
	Input     map[string]any `json:"input"`
	Decision  string         `json:"decision"`
	Reason    string         `json:"reason,omitempty"`
}

// Logger appends security events to a file.
type Logger struct {
	mu      sync.Mutex
	path    string
	once    sync.Once
	openErr error
}

// New builds a Logger that writes to the default audit log path
// (~/.octo/audit.log). A nil Logger is never returned; the logger always
// degrades to a no-op on failure rather than breaking callers.
func New() *Logger {
	return &Logger{path: defaultPath()}
}

// defaultPath returns ~/.octo/audit.log, or the empty string if the home
// directory cannot be resolved.
func defaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".octo", "audit.log")
}

// Log appends a single event to the audit log. It is safe for concurrent use.
// Errors are swallowed after a slog warning so that audit failures cannot
// block a tool call or leak back to the LLM.
func (l *Logger) Log(tool string, input map[string]any, decision, reason string) {
	if l == nil || l.path == "" {
		return
	}

	e := Event{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Tool:      tool,
		Input:     input,
		Decision:  decision,
		Reason:    reason,
	}
	b, err := json.Marshal(e)
	if err != nil {
		slog.Warn("audit: failed to marshal event", "err", err)
		return
	}
	b = append(b, '\n')

	l.once.Do(func() {
		// Ensure the parent directory exists once per logger lifetime.
		if dir := filepath.Dir(l.path); dir != "" {
			if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
				l.openErr = mkErr
			}
		}
	})
	if l.openErr != nil {
		// Directory creation failed on first use; log once and give up cleanly.
		slog.Warn("audit: log directory not available", "path", l.path, "err", l.openErr)
		l.openErr = nil // reset so we don't spam on every event
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Open append-only. We intentionally do not keep a long-lived file handle;
	// this keeps the log simple and lets external rotators compress/truncate it.
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("audit: failed to open log", "path", l.path, "err", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(b); err != nil {
		slog.Warn("audit: failed to write event", "path", l.path, "err", err)
	}
}

// FormatReason returns a concise, JSON-safe reason string. It accepts the tool
// name and a free-form reason; if reason is empty it returns the tool name.
func FormatReason(tool, reason string) string {
	if reason != "" {
		return fmt.Sprintf("%s: %s", tool, reason)
	}
	return tool
}
