package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// TerminalSpillBytes is the size past which terminal output is written to a
// temp file instead of being returned to the LLM inline. ~16 KB (~4k tokens)
// is comfortably "too long" for a single tool result; below it the output is
// handed back unchanged.
const TerminalSpillBytes = 16 * 1024

// spillHeadLines and spillTailLines bound the inline preview when output is
// spilled. Build/test failures put their error at the tail, so we keep both
// ends — the common case is answered without the agent reading the file.
const (
	spillHeadLines = 50
	spillTailLines = 50
)

// spillPreviewMaxBytes bounds the inline preview in bytes, on top of the line
// bounds above. Lines alone can't bound it: a progress bar rewritten with \r
// arrives as one multi-hundred-KB line, so fifty "lines" can still be a
// quarter of a megabyte. ~8 KB (~2k tokens) leaves the preview useful while
// keeping a single notice from dominating the context window.
const spillPreviewMaxBytes = 8 * 1024

// spillMaxAge is how long a spill file lives before a later spill sweeps it.
// Files from a clean shutdown are removed immediately (CleanSpillFiles); this
// only catches the leftovers of a crashed session.
const spillMaxAge = 24 * time.Hour

// spillPrefixes are the filename prefixes of model-facing spill files in
// ~/.octo/tmp — terminal output (`term-`) and web_fetch bodies (`webfetch-`).
// These are session-scoped: removed on clean shutdown (CleanSpillFiles) and
// age-swept as crash leftovers.
var spillPrefixes = []string{"term-", "webfetch-"}

// cardSpillPrefix marks user-facing spill files: the full output behind a
// folded TUI card, hyperlinked from the scrollback. The scrollback outlives
// the process, so these deliberately survive shutdown and are reclaimed only
// by the age sweep.
const cardSpillPrefix = "card-"

// hasSpillPrefix reports whether name is a session-scoped spill file (the
// kind CleanSpillFiles removes on exit).
func hasSpillPrefix(name string) bool {
	for _, p := range spillPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// isSweepable reports whether name is any spill file the age sweep may
// reclaim — session-scoped ones and card spills alike.
func isSweepable(name string) bool {
	return hasSpillPrefix(name) || strings.HasPrefix(name, cardSpillPrefix)
}

// MaybeSpillOutput returns body unchanged when it is small enough to give the
// LLM directly. When body exceeds TerminalSpillBytes it writes the full body to
// a temp file and returns a bounded preview plus the file path and a one-line
// read hint, so the agent decides how to read the rest (read_file with
// offset/limit, or grep) instead of having the whole blob flood its context.
//
// The preview is bounded twice: by lines (head+tail) and by bytes
// (spillPreviewMaxBytes). Both are needed — output can be enormous in few lines
// as easily as in many, and one caller (the background-completion notice) has
// no tool_result backstop behind it, so whatever this returns is what reaches
// history.
//
// id names the source (e.g. a background process id) and is woven into the
// temp filename. A write failure still returns a capped preview: a missing file
// is survivable, an unbounded body is not.
func MaybeSpillOutput(id, body string) string {
	if len(body) <= TerminalSpillBytes {
		return body
	}

	path, err := writeSpillFile(id, body)
	if err != nil {
		return capSpillSegment(body, spillPreviewMaxBytes)
	}

	lines := strings.Split(body, "\n")
	head, tail := body, ""
	if len(lines) > spillHeadLines+spillTailLines {
		head = strings.Join(lines[:spillHeadLines], "\n")
		tail = strings.Join(lines[len(lines)-spillTailLines:], "\n")
	}
	if tail == "" {
		head = capSpillSegment(head, spillPreviewMaxBytes)
	} else {
		half := spillPreviewMaxBytes / 2
		head, tail = capSpillSegment(head, half), capSpillSegment(tail, half)
	}

	marker := fmt.Sprintf(
		"[output too long: %d lines / %s written to\n %s\n showing at most %s of it. read_file (offset/limit) or grep that path for the rest.]",
		len(lines), formatBytes(int64(len(body))), path, formatBytes(spillPreviewMaxBytes),
	)
	if tail == "" {
		return head + "\n\n" + marker
	}
	return head + "\n\n" + marker + "\n\n" + tail
}

// capSpillSegment bounds one end of the preview to max bytes, keeping both
// sides of the segment so a trailing error or exit code survives, and saying
// how much it dropped. Cuts are trimmed to rune boundaries: the result rides
// the JSON wire format, which requires valid UTF-8.
func capSpillSegment(s string, max int) string {
	if len(s) <= max {
		return s
	}
	marker := fmt.Sprintf("\n… [%s elided to fit context] …\n", formatBytes(int64(len(s)-max)))
	budget := max - len(marker)
	if budget < 0 {
		budget = 0
	}
	head := budget / 2
	return trimPartialRuneTail(s[:head]) + marker + trimPartialRuneHead(s[len(s)-(budget-head):])
}

// trimPartialRuneTail drops a trailing partial rune left by a byte slice.
func trimPartialRuneTail(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// trimPartialRuneHead drops a leading partial rune left by a byte slice.
func trimPartialRuneHead(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}

// writeSpillFile persists body under ~/.octo/tmp and returns the absolute path.
// The filename carries the source id and this process's pid so concurrent
// sessions never collide and CleanSpillFiles can find its own files.
func writeSpillFile(id, body string) (string, error) {
	dir, err := spillDir()
	if err != nil {
		return "", err
	}
	sweepOldSpillFiles(dir)
	name := fmt.Sprintf("term-%s-%d.log", sanitizeSpillID(id), os.Getpid())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// spillDir returns (creating if needed) ~/.octo/tmp.
func spillDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".octo", "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// sanitizeSpillID keeps the filename safe: an id is normally "bg_7", but guard
// against anything with path separators or spaces sneaking in.
func sanitizeSpillID(id string) string {
	if id == "" {
		id = "out"
	}
	repl := func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}
	return strings.Map(repl, id)
}

// sweepOldSpillFiles best-effort removes spill files older than spillMaxAge —
// the leftovers of sessions that crashed before CleanSpillFiles ran. Errors
// are ignored; this is housekeeping, not correctness.
func sweepOldSpillFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-spillMaxAge)
	for _, e := range entries {
		if e.IsDir() || !isSweepable(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// cardSpillSeq numbers card spills within this process so two folds in the
// same session never collide on a filename.
var cardSpillSeq atomic.Int64

// WriteCardSpill persists a tool call's full output so the TUI can hyperlink
// a folded card's "… +N lines" marker to it. Unlike the model-facing spills,
// these files survive session exit — the link lives in the terminal
// scrollback, which outlives the process — and are reclaimed by the age
// sweep instead of CleanSpillFiles. The deliberate consequence: tool output
// (secrets included) persists on disk up to spillMaxAge after the session
// ends, where term-/webfetch- spills die at shutdown. Returns the absolute
// path. The timestamp in the name keeps a recycled pid from overwriting a
// previous session's file while its scrollback link is still alive.
func WriteCardSpill(toolName, body string) (string, error) {
	dir, err := spillDir()
	if err != nil {
		return "", err
	}
	sweepOldSpillFiles(dir)
	name := fmt.Sprintf("%s%s-%d-%d-%d.log",
		cardSpillPrefix, sanitizeSpillID(toolName), os.Getpid(),
		time.Now().UnixNano(), cardSpillSeq.Add(1))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// CleanSpillFiles removes this process's spill files. Wire it into session
// shutdown next to KillAllBackground so a normal exit leaves no leftovers.
func CleanSpillFiles() {
	dir, err := spillDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	suffix := fmt.Sprintf("-%d.log", os.Getpid())
	for _, e := range entries {
		if hasSpillPrefix(e.Name()) && strings.HasSuffix(e.Name(), suffix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
