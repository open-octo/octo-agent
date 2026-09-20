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
// So ~/.octo/landing.json replaces them. Absent — the normal case — the web UI
// uses its built-in set, which stays translated through i18n. A file that is
// present is taken as written, in whatever language it was written in: it is
// one person's (or one team's) own copy, not something octo translates.

func landingConfigPath() string {
	p, err := datahome.Path("landing.json")
	if err != nil {
		return filepath.Join(".", ".octo", "landing.json")
	}
	return p
}

const (
	maxLandingCards    = 8
	maxLandingText     = 200
	maxLandingPrompt   = 8 << 10
	maxLandingIcon     = 64
	maxLandingFileSize = 256 << 10
)

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

type landingConfig struct {
	Title    string        `json:"title,omitempty"`
	Subtitle string        `json:"subtitle,omitempty"`
	Cards    []landingCard `json:"cards,omitempty"`
}

func clampLanding(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	// Cut on a rune boundary: these are display strings and often not ASCII.
	return strings.ToValidUTF8(s[:max], "")
}

// normalizeLanding keeps what is usable and drops the rest. A card without
// both a title and a prompt is not a card — it would render as an empty box
// that does nothing — but one bad entry must not cost the user the others.
func normalizeLanding(c landingConfig) landingConfig {
	out := landingConfig{
		Title:    clampLanding(c.Title, maxLandingText),
		Subtitle: clampLanding(c.Subtitle, maxLandingText),
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
	return out
}

// handleGetLanding returns the user's landing overrides, or an empty object.
//
// Every failure answers 200 with nothing overridden: a missing file is the
// normal case, and a malformed one should leave the user on the built-in
// landing page rather than on an error. The file is theirs to hand-edit, so a
// stray comma is a likely state, not an exceptional one.
func (s *Server) handleGetLanding(w http.ResponseWriter, r *http.Request) {
	empty := map[string]any{"landing": landingConfig{}, "path": landingConfigPath()}

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
		"path":    landingConfigPath(),
	})
}
