package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/datahome"
)

// ─── The new-session landing page ───────────────────────────────────────────
//
// The landing page's four cards are the first thing octo says to anyone, and
// what they say is "understand a project" / "build a tool" — two of the four
// aimed squarely at code. octo is not a coding CLI, and whoever is using it
// knows better than we do what their four things are: a lawyer's are not a
// writer's.
//
// So ~/.octo/landing/ replaces them:
//
//	~/.octo/landing/
//	├── config.json
//	└── hero.webp        (optional, any assets config.json references)
//
// A directory rather than a lone file because the page also has room above the
// cards, and filling it means shipping an image — or pointing at a Light App,
// which brings its own everything.
//
// Absent — the normal case — the web UI uses its built-in set, which stays
// translated through i18n. A config that is present is taken as written, in
// whatever language it was written in: it is one person's (or one team's) own
// copy, not something octo translates.

func landingDir() string {
	p, err := datahome.Path("landing")
	if err != nil {
		return filepath.Join(".", ".octo", "landing")
	}
	return p
}

func landingConfigPath() string { return filepath.Join(landingDir(), "config.json") }

const (
	maxLandingCards    = 8
	maxLandingApps     = 8
	maxLandingText     = 200
	maxLandingPrompt   = 8 << 10
	maxLandingIcon     = 64
	maxLandingFileSize = 256 << 10

	// The hero sits between the title and the cards, so it is bounded by what
	// leaves the cards and the composer on screen rather than by taste.
	minHeroHeight     = 80
	maxHeroHeight     = 420
	defaultHeroHeight = 180
)

// landingAssetTypes is what the landing directory may serve. Same reasoning as
// the theme assets: these are files from a directory the user can write, served
// from the app's own origin, so only inert formats — SVG carries script and
// stays out no matter what.
var landingAssetTypes = map[string]string{
	".webp": "image/webp",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".avif": "image/avif",
}

// landingCard is one starter: what it looks like, and what it puts in the
// composer. The prompt is loaded into the composer rather than sent, so the
// working directory and model pickers still apply and the text stays editable.
type landingCard struct {
	// Icon is either an iconify name ("ant-design:code-outlined") or anything
	// else — an emoji, a letter — which the UI draws as text.
	Icon   string `json:"icon,omitempty"`
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
}

// landingHero fills the space above the cards, from one of two sources.
type landingHero struct {
	// Image is a file in the landing directory. Animated GIF and WebP work,
	// which is what "put an animation there" usually means.
	Image string `json:"image,omitempty"`
	// App is a Light App slug, embedded as a frame — the whole of that
	// machinery (own origin, sandbox, bridge) applies unchanged. Exactly one
	// app: a landing page with several is a dashboard, and every frame is
	// something the first screen has to wait for.
	App    string `json:"app,omitempty"`
	Height int    `json:"height,omitempty"`
}

type landingConfig struct {
	Title    string        `json:"title,omitempty"`
	Subtitle string        `json:"subtitle,omitempty"`
	Hero     *landingHero  `json:"hero,omitempty"`
	Cards    []landingCard `json:"cards,omitempty"`
	// Apps are Light App slugs offered as shortcuts under the cards. Entry
	// points, not embeds — they cost nothing until clicked.
	Apps []string `json:"apps,omitempty"`
}

func clampLanding(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	// Cut on a rune boundary: these are display strings and often not ASCII.
	return strings.ToValidUTF8(s[:max], "")
}

// validLandingAsset accepts a plain filename in the landing directory. No
// directories, no traversal: the config names a file beside itself.
func validLandingAsset(name string) bool {
	if name == "" || len(name) > 128 || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	_, ok := landingAssetTypes[strings.ToLower(filepath.Ext(name))]
	return ok
}

// validLightAppSlug is the slug shape the Light App directories use.
func validLightAppSlug(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizeHero(h *landingHero) *landingHero {
	if h == nil {
		return nil
	}
	out := landingHero{Height: h.Height}
	// Image wins when both are given: it is the quieter of the two, and a page
	// that silently started running an app would be the worse surprise.
	if name := strings.TrimSpace(h.Image); validLandingAsset(name) {
		out.Image = name
	} else if slug := strings.TrimSpace(h.App); validLightAppSlug(slug) {
		out.App = slug
	}
	if out.Image == "" && out.App == "" {
		return nil
	}
	switch {
	case out.Height == 0:
		out.Height = defaultHeroHeight
	case out.Height < minHeroHeight:
		out.Height = minHeroHeight
	case out.Height > maxHeroHeight:
		out.Height = maxHeroHeight
	}
	return &out
}

// normalizeLanding keeps what is usable and drops the rest. A card without
// both a title and a prompt is not a card — it would render as an empty box
// that does nothing — but one bad entry must not cost the user the others.
func normalizeLanding(c landingConfig) landingConfig {
	out := landingConfig{
		Title:    clampLanding(c.Title, maxLandingText),
		Subtitle: clampLanding(c.Subtitle, maxLandingText),
		Hero:     normalizeHero(c.Hero),
	}
	for _, card := range c.Cards {
		title := clampLanding(card.Title, maxLandingText)
		prompt := clampLanding(card.Prompt, maxLandingPrompt)
		if title == "" || prompt == "" {
			continue
		}
		out.Cards = append(out.Cards, landingCard{
			Icon:   clampLanding(card.Icon, maxLandingIcon),
			Title:  title,
			Prompt: prompt,
		})
		if len(out.Cards) == maxLandingCards {
			break
		}
	}
	seen := map[string]bool{}
	for _, slug := range c.Apps {
		slug = strings.TrimSpace(slug)
		if !validLightAppSlug(slug) || seen[slug] {
			continue
		}
		seen[slug] = true
		out.Apps = append(out.Apps, slug)
		if len(out.Apps) == maxLandingApps {
			break
		}
	}
	return out
}

// handleGetLanding returns the user's landing overrides, or an empty object.
//
// Every failure answers 200 with nothing overridden: a missing directory is the
// normal case, and a malformed config should leave the user on the built-in
// landing page rather than on an error. The file is theirs to hand-edit, so a
// stray comma is a likely state, not an exceptional one.
func (s *Server) handleGetLanding(w http.ResponseWriter, r *http.Request) {
	empty := map[string]any{"landing": landingConfig{}, "dir": landingDir()}

	fi, err := os.Stat(landingConfigPath())
	if err != nil || fi.IsDir() || fi.Size() > maxLandingFileSize {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	data, err := os.ReadFile(landingConfigPath())
	if err != nil {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	var cfg landingConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"landing": normalizeLanding(cfg),
		"dir":     landingDir(),
	})
}

// handleGetLandingAsset serves one image from the landing directory — whatever
// the hero points at.
func (s *Server) handleGetLandingAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !validLandingAsset(name) {
		writeError(w, http.StatusNotFound, "landing_asset_not_found")
		return
	}
	data, err := os.ReadFile(filepath.Join(landingDir(), name))
	if err != nil {
		writeError(w, http.StatusNotFound, "landing_asset_not_found")
		return
	}
	w.Header().Set("Content-Type", landingAssetTypes[strings.ToLower(filepath.Ext(name))])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The user swaps this file while deciding what looks right; revalidating
	// keeps the edit-and-reload loop the docs promise.
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
