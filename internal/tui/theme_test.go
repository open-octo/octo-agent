package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestPanel_BordersTitleAndBody(t *testing.T) {
	out := Panel("queue (2)", "1. lint\n2. test")
	for _, want := range []string{"╭", "╯", "queue (2)", "1. lint", "2. test"} {
		if !strings.Contains(out, want) {
			t.Errorf("Panel missing %q in:\n%s", want, out)
		}
	}
}

func TestPanel_TitleOnly(t *testing.T) {
	out := Panel("background (1)", "")
	if !strings.Contains(out, "background (1)") || !strings.Contains(out, "╭") {
		t.Errorf("title-only panel should still draw a bordered box; got:\n%s", out)
	}
}

func TestBox_BordersContent(t *testing.T) {
	out := Box("⚠ permission\nterminal wants to run")
	for _, want := range []string{"╭", "│", "⚠ permission", "terminal wants to run"} {
		if !strings.Contains(out, want) {
			t.Errorf("Box missing %q in:\n%s", want, out)
		}
	}
}

func TestBgWash_LightDark(t *testing.T) {
	if bgWash('+', true) != bgAddedDark || bgWash('-', true) != bgRemovedDark {
		t.Error("dark background should use the deep washes")
	}
	if bgWash('+', false) != bgAddedLight || bgWash('-', false) != bgRemovedLight {
		t.Error("light background should use the pale washes")
	}
}

func TestChromaStyle_LightDark(t *testing.T) {
	if chromaStyle(true) != "github-dark" || chromaStyle(false) != "github" {
		t.Errorf("chromaStyle: dark=%q light=%q", chromaStyle(true), chromaStyle(false))
	}
}

func TestBanner_ContainsTitleAndInfo(t *testing.T) {
	out := Banner("v1.0", "claude", "~/proj", 40)
	for _, want := range []string{"◆ octo", "claude", "~/proj"} {
		if !strings.Contains(out, want) {
			t.Errorf("Banner missing %q in:\n%s", want, out)
		}
	}
}

// Key hints moved from the status bar into the startup banner.
func TestBanner_ContainsKeyHints(t *testing.T) {
	out := Banner("", "claude", "~/proj", 60)
	for _, want := range []string{"Enter send", "newline", "Ctrl+Q queue", "Esc interrupt", "Ctrl+C quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("Banner missing key hint %q in:\n%s", want, out)
		}
	}
}

func TestBanner_MinWidth(t *testing.T) {
	out := Banner("", "", "", 5)
	if !strings.Contains(out, "◆ octo") {
		t.Errorf("Banner should still render at tiny width; got:\n%s", out)
	}
}

// #1095: below ~104 columns (the art + gutter + info block's combined
// width) the two no longer fit side by side, and lipgloss.JoinHorizontal
// doesn't wrap — it would print past the terminal's actual width and let the
// terminal itself wrap unpredictably, misaligning the art against the info
// block. A comfortably wide terminal should still show the full art+info
// layout.
func TestBanner_ShowsArtWhenWide(t *testing.T) {
	out := Banner("v1.0", "claude", "~/proj", 110)
	if !strings.Contains(out, "██████") {
		t.Errorf("Banner should show ASCII art at a wide width; got:\n%s", out)
	}
}

// Below the combined width, Banner falls back to a text-only layout — no
// art, but the title/info/hints must still render.
func TestBanner_HidesArtWhenNarrow(t *testing.T) {
	out := Banner("v1.0", "claude", "~/proj", 60)
	if strings.Contains(out, "██████") {
		t.Errorf("Banner should drop ASCII art below the fit width; got:\n%s", out)
	}
	for _, want := range []string{"◆ octo", "claude", "~/proj"} {
		if !strings.Contains(out, want) {
			t.Errorf("narrow Banner missing %q in:\n%s", want, out)
		}
	}
}

func TestStatusBar_SegmentsAndHint(t *testing.T) {
	segs := [][2]string{{"model", "gpt-4"}, {"cost", "$0.01"}}
	out := StatusBar(segs, "Enter send", 30)
	for _, want := range []string{"gpt-4", "$0.01", "Enter send", "──"} {
		if !strings.Contains(out, want) {
			t.Errorf("StatusBar missing %q in:\n%s", want, out)
		}
	}
}

func TestStatusBar_ZeroWidth(t *testing.T) {
	out := StatusBar(nil, "hint", 0)
	if !strings.Contains(out, "hint") {
		t.Errorf("StatusBar should still render hint at width 0; got:\n%s", out)
	}
}

// #1095: a deep cwd used to make the segment line overflow width with no
// clamping at all, wrapping the status bar onto two lines.
func TestStatusBar_ClampsLongCwd(t *testing.T) {
	longCwd := "/Users/someone/Projects/very/deeply/nested/path/to/octo-agent"
	segs := [][2]string{{"model", "gpt-4"}, {"cwd", longCwd}}
	out := StatusBar(segs, "", 40)
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("status bar line exceeds width 40 (got %d): %q", w, line)
		}
	}
	if strings.Contains(out, longCwd) {
		t.Error("expected the long cwd to be shortened, found it unshortened")
	}
	// The leaf directory name is the most useful part of a path — it must
	// survive the shortening, not just the drive/root end.
	if !strings.Contains(out, "octo-agent") {
		t.Errorf("expected the cwd's leaf directory to survive shortening; got:\n%s", out)
	}
}

// A short cwd that already fits must render unmodified.
func TestStatusBar_ShortCwdUnclamped(t *testing.T) {
	segs := [][2]string{{"model", "gpt-4"}, {"cwd", "~/proj"}}
	out := StatusBar(segs, "", 60)
	if !strings.Contains(out, "~/proj") {
		t.Errorf("short cwd should render unmodified; got:\n%s", out)
	}
}

func TestShortenMiddle(t *testing.T) {
	// A string that already fits within maxW must render unchanged.
	in := "~/Projects/github/octo-agent"
	if got := shortenMiddle(in, 100); got != in {
		t.Errorf("shortenMiddle(%q, 100) = %q, want unchanged", in, got)
	}

	// Shortened form must respect the width budget, keep the "…" marker, and
	// preserve a prefix and a suffix of the original (not just truncate the tail).
	got := shortenMiddle(in, 15)
	if w := lipgloss.Width(got); w > 15 {
		t.Errorf("shortenMiddle result exceeds budget: %d > 15 (%q)", w, got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("shortenMiddle result missing ellipsis: %q", got)
	}
	if !strings.HasPrefix(got, "~/") {
		t.Errorf("shortenMiddle should keep the path's start: %q", got)
	}
	if !strings.HasSuffix(got, "agent") {
		t.Errorf("shortenMiddle should keep the path's end: %q", got)
	}
}
