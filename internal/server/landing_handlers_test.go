package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLanding(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "landing.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getLanding(t *testing.T, srv *Server) landingConfig {
	t.Helper()
	w := doJSON(t, srv, "GET", "/api/landing", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Landing landingConfig
		Path    string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Landing
}

// TestLanding_NoFile: the overwhelmingly common case. Nothing configured means
// nothing overridden, and the web UI keeps its built-in, translated cards.
func TestLanding_NoFile(t *testing.T) {
	themeHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if got.Title != "" || got.Subtitle != "" || len(got.Cards) != 0 {
		t.Fatalf("expected nothing overridden, got %+v", got)
	}
}

// TestLanding_RoundTrip: what the user wrote is what the UI gets.
func TestLanding_RoundTrip(t *testing.T) {
	home := themeHome(t)
	writeLanding(t, home, `{
	  "title": "今天做点什么？",
	  "subtitle": "挑一件事",
	  "cards": [
	    {"icon": "🔍", "title": "拆解一条小红书", "prompt": "帮我拆解这条笔记"},
	    {"icon": "ant-design:file-text-outlined", "title": "审一份合同", "prompt": "帮我看这份合同的风险"}
	  ]
	}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if got.Title != "今天做点什么？" || got.Subtitle != "挑一件事" {
		t.Errorf("header not carried: %+v", got)
	}
	if len(got.Cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(got.Cards))
	}
	if got.Cards[0].Icon != "🔍" || got.Cards[0].Title != "拆解一条小红书" {
		t.Errorf("first card: %+v", got.Cards[0])
	}
	// Both icon shapes travel untouched; the UI decides how to draw them.
	if got.Cards[1].Icon != "ant-design:file-text-outlined" {
		t.Errorf("iconify name not carried: %+v", got.Cards[1])
	}
}

// TestLanding_MalformedFallsBack: this file is hand-edited, so a stray comma is
// a likely state. It must cost the overrides, not the landing page.
func TestLanding_MalformedFallsBack(t *testing.T) {
	home := themeHome(t)
	writeLanding(t, home, `{"title": "broken",`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if got.Title != "" || len(got.Cards) != 0 {
		t.Fatalf("a malformed file should override nothing, got %+v", got)
	}
}

// TestLanding_DropsUnusableCards: a card with no prompt renders as a box that
// does nothing. One bad entry must not cost the user the rest.
func TestLanding_DropsUnusableCards(t *testing.T) {
	home := themeHome(t)
	writeLanding(t, home, `{"cards": [
	  {"title": "no prompt"},
	  {"prompt": "no title"},
	  {"title": "  ", "prompt": "  "},
	  {"title": "good", "prompt": "do the thing"}
	]}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if len(got.Cards) != 1 || got.Cards[0].Title != "good" {
		t.Fatalf("expected only the usable card, got %+v", got.Cards)
	}
}

// TestLanding_CapsCount: the grid wraps past four; an unbounded list would just
// push the composer off the screen.
func TestLanding_CapsCount(t *testing.T) {
	home := themeHome(t)
	var cards []string
	for i := range 20 {
		cards = append(cards, `{"title": "c`+string(rune('a'+i))+`", "prompt": "p"}`)
	}
	writeLanding(t, home, `{"cards": [`+strings.Join(cards, ",")+`]}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	if got := getLanding(t, srv); len(got.Cards) != maxLandingCards {
		t.Fatalf("expected %d cards, got %d", maxLandingCards, len(got.Cards))
	}
}

// TestLanding_ClampsText: a title is a line on a card, not a place to paste an
// essay — and the cut must not leave half a character.
func TestLanding_ClampsText(t *testing.T) {
	home := themeHome(t)
	long := strings.Repeat("一", maxLandingText)
	writeLanding(t, home, `{"title": "`+long+`", "cards": [{"title": "`+long+`", "prompt": "p"}]}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if len(got.Title) > maxLandingText {
		t.Errorf("title not clamped: %d bytes", len(got.Title))
	}
	if !json.Valid([]byte(`"` + got.Title + `"`)) {
		t.Error("clamping produced invalid UTF-8")
	}
	if len(got.Cards) != 1 || len(got.Cards[0].Title) > maxLandingText {
		t.Errorf("card title not clamped: %+v", got.Cards)
	}
}
