package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestStripANSI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text untouched", "ok\tgithub.com/x/y\t0.4s", "ok\tgithub.com/x/y\t0.4s"},
		{"sgr colour", "\x1b[31mred\x1b[0m", "red"},
		// The line from the report: vitest's summary, piped through grep.
		{
			"vitest summary",
			"\x1b[2m      Tests \x1b[22m \x1b[1m\x1b[32m763 passed\x1b[39m\x1b[22m\x1b[2m (763)\x1b[39m",
			"      Tests  763 passed (763)",
		},
		{"private csi", "\x1b[?25lworking\x1b[?25h", "working"},
		{"csi with intermediate", "\x1b[1 qprompt", "prompt"},
		{"osc terminated by st", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"osc terminated by bel", "\x1b]0;window title\x07after", "after"},
		{"charset selection", "\x1b(Bplain", "plain"},
		{"single-byte escape", "\x1bMup", "up"},
		{"unterminated sequence eats the rest", "head\x1b[31", "head"},
		{"lone trailing escape", "head\x1b", "head"},
		// Box-drawing and CJK are ordinary UTF-8 payload, not escapes.
		{"multibyte payload survives", "─── 失败 ───", "─── 失败 ───"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(stripANSI([]byte(tc.in))); got != tc.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A line without escapes must not be copied — every line of every command's
// output passes through here.
func TestStripANSI_NoEscapeReusesInput(t *testing.T) {
	in := []byte("no escapes here")
	if got := stripANSI(in); &got[0] != &in[0] {
		t.Error("stripANSI copied a line that had no escape sequences")
	}
}

func TestBackgroundManager_StripsANSIFromOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf escape quoting is sh-specific; stripANSI itself is covered above")
	}
	m := NewBackgroundManager()
	id, err := m.Start(context.Background(), `printf '\033[31mred\033[0m\n'`, BgModeAsync)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var out string
	waitFor(t, "process to exit", func() bool {
		o, s, found, _, _ := m.Read(id)
		out += o
		return found && strings.HasPrefix(s, "exited")
	})

	if strings.ContainsRune(out, esc) {
		t.Errorf("output kept an escape sequence: %q", out)
	}
	if strings.TrimSpace(out) != "red" {
		t.Errorf("output = %q, want %q", out, "red")
	}
}
