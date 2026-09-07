package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/browser"
)

// The type result must let the model see when the field did not end up
// holding what it sent — the autofilled-login case — and point at clear.
func TestTypeResultText(t *testing.T) {
	cases := []struct {
		name         string
		text         string
		st           browser.FieldState
		want, absent []string
	}{
		{
			name:   "clean type echoes the value and no warning",
			text:   "alice",
			st:     browser.FieldState{Found: true, Value: "alice", ValueLen: 5},
			want:   []string{`typed "alice" into #u`, `field now holds "alice"`},
			absent: []string{"clear"},
		},
		{
			name: "prefilled field shows old+new and tells the model to clear",
			text: "alice",
			st:   browser.FieldState{Found: true, Value: "alicealice", ValueLen: 10},
			want: []string{`field now holds "alicealice"`, "autofill", "use clear"},
		},
		{
			name:   "password never echoes content, only lengths",
			text:   "hunter2",
			st:     browser.FieldState{Found: true, Password: true, ValueLen: 14},
			want:   []string{"typed 7 chars into #u", "field now holds 14 chars", "use clear"},
			absent: []string{"hunter2"},
		},
		{
			name:   "password with matching length has no warning",
			text:   "hunter2",
			st:     browser.FieldState{Found: true, Password: true, ValueLen: 7},
			want:   []string{"field now holds 7 chars"},
			absent: []string{"clear", "hunter2"},
		},
		{
			// JS reports code points, Go counts runes: an emoji is one on both
			// sides, so a clean type into an empty password field must not warn.
			name:   "emoji password counts code points on both sides",
			text:   "pw🔑",
			st:     browser.FieldState{Found: true, Password: true, ValueLen: 3},
			want:   []string{"typed 3 chars into #u", "field now holds 3 chars"},
			absent: []string{"clear"},
		},
		{
			// A textarea's later lines are where the typed text lands; the
			// readback must show them, not just the first line.
			name:   "multi-line value is shown whole",
			text:   "new",
			st:     browser.FieldState{Found: true, Value: "old line\nnew", ValueLen: 12},
			want:   []string{`field now holds "old line\nnew"`, "use clear"},
			absent: []string{`holds "old line"` + " —"},
		},
		{
			name:   "element gone falls back to the plain confirmation",
			text:   "alice",
			st:     browser.FieldState{},
			want:   []string{`typed "alice" into #u`},
			absent: []string{"holds"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := typeResultText(c.text, "#u", c.st)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("result %q lacks %q", got, w)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("result %q should not contain %q", got, a)
				}
			}
		})
	}
}

// A long CJK value is capped by rune with an explicit marker — never cut
// inside a code point (which %q would render as \x escapes the model reads as
// real content).
func TestQuoteFieldValue_CapsByRune(t *testing.T) {
	long := strings.Repeat("中", 260)
	got := quoteFieldValue(long)
	if strings.Contains(got, `\x`) {
		t.Errorf("value cut inside a code point: %s", got)
	}
	if !strings.Contains(got, "… (260 chars total)") {
		t.Errorf("missing truncation marker: %s", got)
	}
	if strings.Count(got, "中") != 200 {
		t.Errorf("want exactly 200 runes kept, got %d", strings.Count(got, "中"))
	}
	if short := quoteFieldValue("ok"); short != `"ok"` {
		t.Errorf("short value = %s", short)
	}
}

// observe must make a prefilled field unmistakable and keep password content
// out of the transcript.
func TestRenderObserve_PrefilledFields(t *testing.T) {
	digest := []browser.DigestElement{
		{Text: "Username", Selector: `input[name="user"]`, Value: "alice", ValueLen: 5},
		{Text: "Password", Selector: `input[name="pw"]`, Password: true, ValueLen: 14},
		{Text: "Search", Selector: "#q"},
		{Text: "Sign in", Selector: "#submit"},
	}
	out := renderObserve("Login", "https://x.test/login", digest, nil)

	for _, w := range []string{
		"page: Login — https://x.test/login",
		`- Username  →  input[name="user"]  (prefilled: "alice")`,
		`- Password  →  input[name="pw"]  (prefilled password, 14 chars)`,
		"- Search  →  #q\n",
		"- Sign in  →  #submit\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("observe output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "#q  (") {
		t.Errorf("empty field must not be marked prefilled:\n%s", out)
	}
}

func TestRenderObserve_ErrorAndEmpty(t *testing.T) {
	if out := renderObserve("", "", nil, errors.New("boom")); !strings.Contains(out, "(could not read elements: boom)") {
		t.Errorf("error case: %q", out)
	}
	if out := renderObserve("T", "u", nil, nil); !strings.Contains(out, "(none found)") {
		t.Errorf("empty case: %q", out)
	}
}
