package channel

import (
	"strings"
	"unicode/utf8"
)

// maxFenceLangRunes bounds how much of a code fence's language tag
// (```python, ```go, …) we echo back when reopening it across a chunk
// boundary — a model could in principle put something absurdly long after
// the backticks.
const maxFenceLangRunes = 20

// closeMarkerBytes is the exact byte cost of the synthetic "\n```" appended
// when a piece ends with a fence still open.
const closeMarkerBytes = len("\n```")

// SplitForSend splits text into chunks whose UTF-8 byte length does not
// exceed limit, for handing to a channel adapter's SendText where the
// platform enforces a hard per-message size cap (Discord: 2000, WeCom's
// markdown content: 4096 bytes, …). Without this, a reply longer than the
// cap either gets rejected outright (Discord) or silently truncated
// server-side — the routine case of a long agentic answer just never
// arrives, with nothing telling the user why (#1116).
//
// Cuts prefer a paragraph break, then a line break, then a space, and only
// hard-cut (always at a valid UTF-8 rune boundary — never a valid char is
// split) as a last resort.
//
// limit is a byte count, not a rune count: WeCom's cap is documented in
// bytes, and measuring in bytes is a strict superset of safety for
// platforms whose real limit is characters or UTF-16 units (Discord,
// Telegram) — the byte count of any string is never smaller than its
// UTF-16-unit or rune count, so this can only split a bit earlier than a
// character-counting platform strictly requires, never later.
//
// If a fenced code block (```lang ... ```) straddles a cut point, the fence
// is closed at the end of one chunk and reopened with the same language tag
// at the start of the next, so every chunk is complete, valid markdown on
// its own instead of rendering as a permanently-broken code block split
// across two IM messages. The reopen/close overhead is reserved from the
// budget dynamically (based on the real byte length of whatever's actually
// being reopened/closed), not a fixed guess, so this holds regardless of
// the fence's language tag — see the retry inside the loop below.
//
// limit is assumed to be at least a few dozen bytes, matching every current
// adapter (smallest is Discord's 2000) — for a limit small enough that even
// one rune plus fence markup can't fit, individual chunks can exceed limit;
// that's an intentionally unhandled degenerate case, not a realistic one.
func SplitForSend(text string, limit int) []string {
	if text == "" {
		return nil
	}
	if limit < 1 {
		limit = 1
	}
	if len(text) <= limit {
		return []string{text}
	}

	var chunks []string
	fenceOpen := false
	fenceLang := ""

	for len(text) > 0 {
		// Reserve room for reopening a fence carried over from the previous
		// chunk (exact byte cost of "```" + the actual tag + "\n" — computed
		// from the real fenceLang bytes, not an assumed-ASCII estimate, so a
		// multi-byte-rune tag is budgeted correctly too).
		reopenCost := 0
		if fenceOpen {
			reopenCost = len("```") + len(fenceLang) + len("\n")
		}
		budget := limit - reopenCost
		if budget < 1 {
			budget = 1
		}

		if len(text) <= budget {
			chunks = append(chunks, reopenFence(fenceOpen, fenceLang, text))
			break
		}

		cut := cutPoint(text, budget)
		piece := text[:cut]
		open, lang := fenceStateAfter(piece, fenceOpen, fenceLang)

		// A fence can open for the FIRST time inside this very piece — not
		// just carry over from the previous chunk — and budget above didn't
		// reserve for that case. If the piece still ends open (whether it
		// started that way or opened partway through), re-cut with the
		// closing "\n```" reserved too, so the synthetic closer this loop is
		// about to append never pushes the chunk past limit. cutPoint with a
		// smaller budget always returns a cut at or before the original (its
		// search window is a byte-for-byte prefix of the wider one), so this
		// single retry is enough — no risk of looping.
		if open {
			closeBudget := budget - closeMarkerBytes
			if closeBudget < 1 {
				closeBudget = 1
			}
			if closeBudget < cut {
				cut = cutPoint(text, closeBudget)
				piece = text[:cut]
				open, lang = fenceStateAfter(piece, fenceOpen, fenceLang)
			}
		}

		rest := strings.TrimLeft(text[cut:], " \n")
		chunk := reopenFence(fenceOpen, fenceLang, piece)
		if open {
			chunk = strings.TrimRight(chunk, "\n") + "\n```"
		}
		chunks = append(chunks, strings.TrimRight(chunk, " \n"))

		fenceOpen, fenceLang = open, lang
		text = rest
	}
	return chunks
}

// cutPoint returns a byte offset within the first limit bytes of text to
// cut at, preferring (in order) the last paragraph break, line break, or
// space in that window; falling back to a hard cut at limit. The hard-cut
// fallback always lands on a valid UTF-8 rune boundary and always makes
// forward progress (at least one full rune), even if limit is smaller than
// that rune's encoded size.
func cutPoint(text string, limit int) int {
	if limit >= len(text) {
		return len(text)
	}
	window := text[:limit]
	for _, sep := range []string{"\n\n", "\n", " "} {
		if idx := strings.LastIndex(window, sep); idx > 0 {
			return idx + len(sep)
		}
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	if cut == 0 {
		_, size := utf8.DecodeRuneInString(text)
		cut = size
	}
	return cut
}

// fenceStateAfter reports whether a ``` code fence is still open after
// text, given it started in state (startOpen, startLang). Fence markers are
// recognized on their own (trimmed) line starting with "```", matching the
// common case every LLM-generated markdown fence uses — it does not track
// fence length (````-nested fences) or ~~~ fences, both rare enough in
// agent output that the added complexity isn't worth it here.
func fenceStateAfter(text string, startOpen bool, startLang string) (open bool, lang string) {
	open, lang = startOpen, startLang
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}
		if open {
			open, lang = false, ""
			continue
		}
		open = true
		lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		if r := []rune(lang); len(r) > maxFenceLangRunes {
			lang = string(r[:maxFenceLangRunes])
		}
	}
	return open, lang
}

// reopenFence prepends a "```lang\n" line to body when a fence is open;
// returns body unchanged otherwise.
func reopenFence(open bool, lang, body string) string {
	if !open {
		return body
	}
	return "```" + lang + "\n" + body
}
