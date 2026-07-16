// Package audit writes an append-only log of security-relevant tool
// decisions. It is intentionally minimal: one JSON line per event, flushed
// to ~/.octo/audit.log. Failures are logged to the application logger but
// never block the tool call being audited.
package audit

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/logfile"
)

// Event is one entry in the audit log.
type Event struct {
	Timestamp string         `json:"ts"`
	Tool      string         `json:"tool"`
	Input     map[string]any `json:"input"`
	Decision  string         `json:"decision"`
	Reason    string         `json:"reason,omitempty"`
}

// maxFieldLen caps each string value recorded from the tool input. The log
// exists to answer "what was denied and why", not to archive payloads: a
// denied write_file carries the entire file body in input["content"], and
// terminal commands can embed secrets — truncating keeps lines bounded and
// limits how much sensitive material lands on disk.
const maxFieldLen = 1024

// Rotation policy, mirroring internal/logfile's serve.log defaults.
const (
	maxLogBytes = 10 << 20 // 10 MiB
	logBackups  = 3
)

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
	return NewAt(defaultPath())
}

// NewAt builds a Logger that writes to the given path. An empty path yields
// a no-op logger.
func NewAt(path string) *Logger {
	return &Logger{path: path}
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
		Input:     truncateInput(input),
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
		// Once per logger lifetime: ensure the parent directory exists and
		// rotate an oversized log from a previous run. Rotation only happens
		// here — never mid-run — so a single process appends to one file.
		if mkErr := os.MkdirAll(filepath.Dir(l.path), 0o700); mkErr != nil {
			l.openErr = mkErr
			slog.Warn("audit: log directory not available", "path", l.path, "err", mkErr)
			return
		}
		if rotErr := logfile.RotateIfLarger(l.path, maxLogBytes, logBackups); rotErr != nil {
			// Rotation failure is not fatal — keep appending to the old file.
			slog.Warn("audit: failed to rotate log", "path", l.path, "err", rotErr)
		}
	})
	if l.openErr != nil {
		// Directory creation failed; already warned once inside once.Do, so
		// give up silently for the rest of the logger's lifetime.
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

// truncateInput returns a copy of input with every string value capped at
// maxFieldLen. Non-string values (numbers, bools, nested structures) are
// recorded as-is; only top-level strings carry the large payloads we care
// about (command, path, content, diff).
func truncateInput(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for k, v := range input {
		if s, ok := v.(string); ok && len(s) > maxFieldLen {
			v = s[:maxFieldLen] + "…(truncated)"
		}
		out[k] = v
	}
	return out
}
