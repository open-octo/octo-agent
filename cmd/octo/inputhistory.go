package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// inputHistoryCap bounds how many entries the TUI's persisted input history
// file keeps. Applied on load, so the file is trimmed once per session start
// rather than rewritten on every submit.
const inputHistoryCap = 1000

// defaultInputHistoryFile resolves the path used to persist the TUI's ↑/↓
// input-recall history across restarts. OCTO_INPUT_HISTORY_FILE wins so
// users (and tests) can redirect it. Empty return disables persistence.
//
// Entries are stored one JSON-encoded string per line (JSONL) rather than
// raw text, since a queued or pasted entry can itself contain newlines.
func defaultInputHistoryFile() string {
	if env := os.Getenv("OCTO_INPUT_HISTORY_FILE"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".octo", "input_history")
}

// loadInputHistory reads the persisted history, oldest first, capped to the
// most recent inputHistoryCap entries. A missing file or unreadable entries
// are non-fatal — the TUI just starts with less (or no) history. If the file
// on disk exceeds the cap, it is rewritten trimmed as a side effect.
func loadInputHistory(path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var entry string
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			continue // skip corrupt/foreign lines rather than failing the load
		}
		lines = append(lines, entry)
	}
	if len(lines) <= inputHistoryCap {
		return lines
	}
	trimmed := lines[len(lines)-inputHistoryCap:]
	rewriteInputHistory(path, trimmed)
	return trimmed
}

// rewriteInputHistory overwrites the history file with exactly entries, used
// to enforce inputHistoryCap on load. Best-effort: failures are ignored, same
// as appendInputHistoryLine.
func rewriteInputHistory(path string, entries []string) {
	if path == "" {
		return
	}
	var sb strings.Builder
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o600)
}

// appendInputHistoryLine persists one submitted/queued line to path. Errors
// are non-fatal — a read-only or missing ~/.octo just means the session's
// history doesn't survive restart, same tolerance as the plain REPL's
// readline history file.
func appendInputHistoryLine(path, line string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b = append(b, '\n')
	_, _ = f.Write(b)
}
