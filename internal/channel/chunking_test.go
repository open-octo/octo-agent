package channel

import (
	"strings"
	"testing"
)

func TestSplitForSend_UnderLimit(t *testing.T) {
	got := SplitForSend("hello", 100)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %#v, want a single unmodified chunk", got)
	}
}

func TestSplitForSend_Empty(t *testing.T) {
	if got := SplitForSend("", 100); got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

// #1116: Discord's SendText posted content as-is with no cap at all — any
// reply over 2000 chars got HTTP 400 and vanished entirely. This pins that
// oversized text now comes back as multiple chunks, each within the limit.
func TestSplitForSend_OversizedTextProducesMultipleChunksWithinLimit(t *testing.T) {
	text := strings.Repeat("a paragraph of plain prose. ", 200) // ~5800 bytes
	chunks := SplitForSend(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 2000 {
			t.Errorf("chunk %d is %d bytes, over the 2000 limit", i, len(c))
		}
	}
	// Splitting must not lose or duplicate content.
	if got := strings.Join(chunks, " "); strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(text, " ", "") {
		// Whitespace at cut points is normalized (trimmed), so compare with
		// spaces stripped rather than asserting byte-identical rejoin.
		t.Fatalf("rejoined chunks lost or altered content")
	}
}

func TestSplitForSend_PrefersParagraphOverHardCut(t *testing.T) {
	text := strings.Repeat("x", 50) + "\n\n" + strings.Repeat("y", 50)
	chunks := SplitForSend(text, 60)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0] != strings.Repeat("x", 50) {
		t.Errorf("chunk 0 = %q, want the first paragraph cleanly", chunks[0])
	}
	if chunks[1] != strings.Repeat("y", 50) {
		t.Errorf("chunk 1 = %q, want the second paragraph cleanly", chunks[1])
	}
}

// A cut point landing mid-multi-byte-rune must back up to a full rune —
// this is the exact class of bug found in weixin.go's smartChunkText, which
// converts a byte offset from strings.LastIndex into a rune count without
// re-deriving it correctly for the *hard-cut* fallback path.
func TestSplitForSend_NeverSplitsARuneInHalf(t *testing.T) {
	text := strings.Repeat("中文字符测试", 100) // no spaces/newlines — forces hard cuts
	chunks := SplitForSend(text, 50)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if !isValidUTF8Chunk(c) {
			t.Errorf("chunk %d is not valid UTF-8: %q", i, c)
		}
	}
	if strings.Join(chunks, "") != text {
		t.Fatalf("rejoined chunks (no separators to trim here) must equal the original exactly")
	}
}

func isValidUTF8Chunk(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// #1116, item 3: a code fence straddling a cut point must close at the end
// of one chunk and reopen (same language tag) at the start of the next,
// instead of leaving an unclosed fence in one message and an un-opened
// continuation in another.
func TestSplitForSend_ClosesAndReopensStraddledFence(t *testing.T) {
	code := strings.Repeat("line_of_code()\n", 100)
	text := "intro\n\n```go\n" + code + "```\n\nend"
	const limit = 400
	chunks := SplitForSend(text, limit)
	if len(chunks) < 2 {
		t.Fatalf("expected the fence to force multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		// The synthetic close/reopen markup must never push a chunk over the
		// requested limit — see TestSplitForSend_FenceOverheadNeverExceedsLimit
		// for the case that caught this as a real bug.
		if len(c) > limit {
			t.Errorf("chunk %d is %d bytes, over the %d limit:\n%s", i, len(c), limit, c)
		}
		open, _ := fenceStateAfter(c, false, "")
		if open {
			t.Errorf("chunk %d ends with an unclosed fence:\n%s", i, c)
		}
		trimmed := strings.TrimSpace(c)
		if strings.Contains(trimmed, "```") && !strings.HasPrefix(trimmed, "```") {
			// fine — a fence can close mid-chunk (the final one, "```\n\nend")
			continue
		}
	}
	// Every chunk after the first that contains code lines must open with a
	// reopened ```go fence (not resume mid-code with no fence marker at all).
	for i := 1; i < len(chunks)-1; i++ {
		if !strings.HasPrefix(chunks[i], "```go\n") {
			t.Errorf("chunk %d should reopen the go fence, got prefix %q", i, chunks[i][:min(20, len(chunks[i]))])
		}
	}
}

// #1116 review finding: a fence that opens for the FIRST TIME inside the
// current piece (not carried over from a previous chunk) wasn't budgeted
// for — cutPoint picked a cut using the full, unreserved limit, and only
// afterward did the code discover the piece ends mid-fence and append a
// synthetic "\n```" that pushed the chunk past limit. Reproduced directly
// against the fix in code review before this test existed: with the bug,
// chunk 0 below came out at 52 bytes against a 50-byte limit.
func TestSplitForSend_FenceOverheadNeverExceedsLimit(t *testing.T) {
	text := "```go\n" + strings.Repeat("a", 42) + "\n" + strings.Repeat("bcdefghij", 20) + "\n```\n\nend"
	const limit = 50
	chunks := SplitForSend(text, limit)
	for i, c := range chunks {
		if len(c) > limit {
			t.Errorf("chunk %d is %d bytes, over the %d limit: %q", i, len(c), limit, c)
		}
	}
	if !strings.Contains(strings.Join(chunks, ""), "end") {
		t.Fatal("lost trailing content after the fence closes")
	}
}

// A language tag longer than maxFenceLangRunes must still be reserved for
// correctly — the overhead calculation must use the ALREADY-TRUNCATED tag's
// real byte length, not the model's original (possibly much longer) one.
func TestSplitForSend_LongLanguageTagStillRespectsLimit(t *testing.T) {
	longTag := strings.Repeat("x", 100) // far past maxFenceLangRunes (20)
	text := "```" + longTag + "\n" + strings.Repeat("code line\n", 50) + "```\n"
	const limit = 100
	chunks := SplitForSend(text, limit)
	for i, c := range chunks {
		if len(c) > limit {
			t.Errorf("chunk %d is %d bytes, over the %d limit: %q", i, len(c), limit, c)
		}
	}
}

func TestFenceStateAfter(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		wantOpen bool
		wantLang string
	}{
		{"no fence", "plain text", false, ""},
		{"opens untagged", "```\ncode", true, ""},
		{"opens tagged", "```python\ncode", true, "python"},
		{"opens and closes", "```go\ncode\n```", false, ""},
		{"already open, closes", "code\n```", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			open, lang := fenceStateAfter(c.text, false, "")
			if c.name == "already open, closes" {
				open, lang = fenceStateAfter(c.text, true, "go")
			}
			if open != c.wantOpen || lang != c.wantLang {
				t.Errorf("got (%v, %q), want (%v, %q)", open, lang, c.wantOpen, c.wantLang)
			}
		})
	}
}

func TestReopenFence(t *testing.T) {
	if got := reopenFence(false, "go", "body"); got != "body" {
		t.Errorf("closed fence should not modify body, got %q", got)
	}
	if got := reopenFence(true, "go", "body"); got != "```go\nbody" {
		t.Errorf("got %q, want reopened fence prefix", got)
	}
	if got := reopenFence(true, "", "body"); got != "```\nbody" {
		t.Errorf("got %q, want bare reopened fence prefix", got)
	}
}
