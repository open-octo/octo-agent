package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeLanding(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".octo", "landing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeLandingAsset drops a file beside the config, the way a user would.
func writeLandingAsset(t *testing.T, home, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(home, ".octo", "landing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
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
		Dir     string
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

// TestLanding_HeroImage: the quiet half of filling the empty space above the
// cards — a file beside the config, animated or not.
func TestLanding_HeroImage(t *testing.T) {
	home := themeHome(t)
	writeLanding(t, home, `{"hero": {"image": "hero.webp", "height": 240}}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if got.Hero == nil || got.Hero.Image != "hero.webp" || got.Hero.Height != 240 {
		t.Fatalf("hero not carried: %+v", got.Hero)
	}
	if got.Hero.App != "" {
		t.Error("an image hero must not also name an app")
	}
}

// TestLanding_HeroApp: the other half — a Light App embedded in that space.
func TestLanding_HeroApp(t *testing.T) {
	home := themeHome(t)
	writeLanding(t, home, `{"hero": {"app": "clock"}}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if got.Hero == nil || got.Hero.App != "clock" {
		t.Fatalf("hero app not carried: %+v", got.Hero)
	}
	if got.Hero.Height != defaultHeroHeight {
		t.Errorf("expected the default height, got %d", got.Hero.Height)
	}
}

// TestLanding_HeroImageWinsOverApp: a page that silently started running an app
// would be the worse surprise of the two.
func TestLanding_HeroImageWinsOverApp(t *testing.T) {
	home := themeHome(t)
	writeLanding(t, home, `{"hero": {"image": "hero.png", "app": "clock"}}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if got.Hero == nil || got.Hero.Image != "hero.png" || got.Hero.App != "" {
		t.Fatalf("expected the image to win: %+v", got.Hero)
	}
}

// TestLanding_HeroRejectsUnusable: the hero names a file beside the config, and
// an SVG is a document that carries script — neither may turn into a path.
func TestLanding_HeroRejectsUnusable(t *testing.T) {
	home := themeHome(t)
	for _, body := range []string{
		`{"hero": {"image": "../../../etc/passwd"}}`,
		`{"hero": {"image": "sub/dir/hero.png"}}`,
		`{"hero": {"image": "evil.svg"}}`,
		`{"hero": {"image": "notes.txt"}}`,
		`{"hero": {"app": "../sketch"}}`,
		`{"hero": {"app": "Sketch"}}`,
		`{"hero": {"height": 200}}`,
	} {
		writeLanding(t, home, body)
		srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
		if got := getLanding(t, srv); got.Hero != nil {
			t.Errorf("%s: expected no hero, got %+v", body, got.Hero)
		}
	}
}

// TestLanding_HeroHeightClamped: the hero shares the screen with the cards and
// the composer, so it cannot be told to take all of it.
func TestLanding_HeroHeightClamped(t *testing.T) {
	home := themeHome(t)
	for _, tc := range []struct{ given, want int }{
		{5000, maxHeroHeight},
		{1, minHeroHeight},
		{-40, minHeroHeight},
	} {
		writeLanding(t, home, `{"hero": {"image": "h.png", "height": `+strconv.Itoa(tc.given)+`}}`)
		srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
		got := getLanding(t, srv)
		if got.Hero == nil || got.Hero.Height != tc.want {
			t.Errorf("height %d: got %+v, want %d", tc.given, got.Hero, tc.want)
		}
	}
}

// TestLanding_PinnedApps: shortcuts, deduplicated and capped; a slug that could
// not name a Light App directory never reaches the UI.
func TestLanding_PinnedApps(t *testing.T) {
	home := themeHome(t)
	writeLanding(t, home, `{"apps": ["sketch", "sketch", "../evil", "Board", "board", ""]}`)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	got := getLanding(t, srv)
	if len(got.Apps) != 2 || got.Apps[0] != "sketch" || got.Apps[1] != "board" {
		t.Fatalf("unexpected apps: %v", got.Apps)
	}
}

// TestLandingAsset_Serves: the hero's image comes back as an image, and only
// the shapes the whitelist knows.
func TestLandingAsset_Serves(t *testing.T) {
	home := themeHome(t)
	writeLandingAsset(t, home, "hero.png", []byte("PNGBYTES"))
	writeLandingAsset(t, home, "evil.svg", []byte(`<svg><script>alert(1)</script></svg>`))
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "GET", "/api/landing/assets/hero.png", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("served as %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}

	for _, path := range []string{
		"/api/landing/assets/evil.svg",
		"/api/landing/assets/missing.png",
		"/api/landing/assets/..%2f..%2fconfig.json",
	} {
		if w := doJSON(t, srv, "GET", path, ""); w.Code == 200 {
			t.Errorf("%s: expected a refusal, got 200", path)
		}
	}
}

// TestLanding_UnreadableConfig: the config is hand-placed, so it can be the
// wrong kind of thing entirely, or far too big to be a landing page. Neither
// may cost the user the page itself.
func TestLanding_UnreadableConfig(t *testing.T) {
	home := themeHome(t)
	dir := filepath.Join(home, ".octo", "landing")
	if err := os.MkdirAll(filepath.Join(dir, "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	if got := getLanding(t, srv); len(got.Cards) != 0 || got.Title != "" {
		t.Fatalf("a directory named config.json overrode something: %+v", got)
	}

	if err := os.RemoveAll(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	writeLanding(t, home, `{"title": "`+strings.Repeat("x", maxLandingFileSize)+`"}`)
	srv = mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	if got := getLanding(t, srv); got.Title != "" {
		t.Fatalf("an oversized config was read anyway: %q", got.Title)
	}
}

// TestLandingAsset_ExtensionCase: the whitelist lower-cases before matching, so
// a file named the way a camera names it still serves.
func TestLandingAsset_ExtensionCase(t *testing.T) {
	home := themeHome(t)
	writeLandingAsset(t, home, "HERO.JPG", []byte("JPEGBYTES"))
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "GET", "/api/landing/assets/HERO.JPG", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("served as %q", ct)
	}
}

// TestLandingAsset_RefusesDotNames: "." and ".." never name an image, and the
// refusal should not rest on the extension check alone.
func TestLandingAsset_RefusesDotNames(t *testing.T) {
	themeHome(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, name := range []string{".", "..", "...", ".png"} {
		if w := doJSON(t, srv, "GET", "/api/landing/assets/"+name, ""); w.Code == 200 {
			t.Errorf("%q: expected a refusal, got 200", name)
		}
	}
}
