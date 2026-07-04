package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/prompt"
)

// ─── Provider Presets ───────────────────────────────────────────────────────

// endpointVariant is the JSON shape the frontend expects for regional endpoints.
type endpointVariant struct {
	Label    string `json:"label"`
	LabelKey string `json:"label_key,omitempty"`
	BaseURL  string `json:"base_url"`
	Region   string `json:"region,omitempty"`
}

// providerPreset is the JSON shape returned by GET /api/providers.
type providerPreset struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	BaseURL          string            `json:"base_url"`
	API              string            `json:"api"`
	DefaultModel     string            `json:"default_model"`
	Models           []string          `json:"models,omitempty"`
	ModelVision      map[string]bool   `json:"model_vision,omitempty"` // model id → accepts image input; lets the form pre-fill the vision toggle
	LiteModel        string            `json:"lite_model,omitempty"`
	EndpointVariants []endpointVariant `json:"endpoint_variants,omitempty"`
	WebsiteURL       string            `json:"website_url,omitempty"`
	CustomEndpoint   bool              `json:"custom_endpoint,omitempty"`
}

// buildProviderPresets builds the response for GET /api/providers from the
// canonical app.Registry.  This is the single source of truth — a new vendor
// only needs one line in internal/app/provider.go.
func buildProviderPresets() []providerPreset {
	presets := make([]providerPreset, 0, len(app.Registry))
	for _, v := range app.Registry {
		variants := make([]endpointVariant, len(v.EndpointVariants))
		for i, ev := range v.EndpointVariants {
			variants[i] = endpointVariant{
				Label:    ev.Label,
				LabelKey: ev.LabelKey,
				BaseURL:  ev.BaseURL,
				Region:   ev.Region,
			}
		}
		presets = append(presets, providerPreset{
			ID:               v.ID,
			Name:             v.DisplayName,
			BaseURL:          v.DefaultBaseURL,
			API:              v.API,
			DefaultModel:     v.DefaultModel,
			Models:           app.VendorModels(v.ID),
			ModelVision:      app.VendorModelVisionMap(v.ID),
			LiteModel:        v.LiteModel,
			EndpointVariants: variants,
			WebsiteURL:       v.WebsiteURL,
			CustomEndpoint:   v.CustomEndpoint,
		})
	}
	return presets
}

// customProtocol maps the web form's anthropic_format toggle to the stored
// protocol string ("anthropic" | "openai") for the Custom vendor.
func customProtocol(anthropicFormat bool) string {
	if anthropicFormat {
		return "anthropic"
	}
	return "openai"
}

// entryUsesAnthropic reports whether an entry speaks the Anthropic wire format,
// so the web form's anthropic_format toggle round-trips. Named vendors use their
// registry protocol; the Custom vendor carries it in the entry's Protocol field.
func entryUsesAnthropic(e config.ModelEntry) bool {
	if app.VendorNeedsProtocol(e.Provider) {
		return e.Protocol == "anthropic"
	}
	return app.IsAnthropicProtocol(e.Provider)
}

// ─── GET /api/providers ─────────────────────────────────────────────────────

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": buildProviderPresets()})
}

// ─── GET /api/onboard/status ────────────────────────────────────────────────

func (s *Server) handleOnboardStatus(w http.ResponseWriter, r *http.Request) {
	phase := detectOnboardPhase()
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_onboard": phase != "",
		"phase":         phase,
	})
}

// detectOnboardPhase determines whether first-run setup is needed.
//
//	"key_setup"  — no API key configured (hard block).
//	"soul_setup" — key present but soul.md missing (soft nudge).
//	""           — fully configured.
func detectOnboardPhase() string {
	cfg, _ := config.Load()

	// Check if any provider key is available.
	hasKey := false
	for _, e := range cfg.Models {
		if e.APIKey != "" {
			hasKey = true
			break
		}
	}
	if !hasKey {
		for _, v := range app.Registry {
			if os.Getenv(v.APIKeyEnvVar) != "" {
				hasKey = true
				break
			}
		}
	}
	if !hasKey {
		return "key_setup"
	}

	// Check soul.md (IdentityPath also finds a legacy uppercase SOUL.md).
	soulPath := prompt.IdentityPath(octoDir(), "soul.md")
	if _, err := os.Stat(soulPath); os.IsNotExist(err) {
		return "soul_setup"
	}

	return ""
}

// ─── POST /api/onboard/complete ─────────────────────────────────────────────

func (s *Server) handleOnboardComplete(w http.ResponseWriter, r *http.Request) {
	// Ensure the ~/.octo directory exists.
	_ = os.MkdirAll(octoDir(), 0o700)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ─── GET /api/config ────────────────────────────────────────────────────────

// configResponse mirrors what the Ruby frontend expects.
type configResponse struct {
	Models          []modelConfig `json:"models,omitempty"`
	DefaultModelIdx int           `json:"default_model_idx,omitempty"`
	FontSize        string        `json:"font_size,omitempty"`
	Language        string        `json:"language,omitempty"`
	ShowReasoning   *bool         `json:"show_reasoning,omitempty"`
	Coauthor        *bool         `json:"coauthor,omitempty"`
}

type modelConfig struct {
	ID              string `json:"id"`
	Type            string `json:"type,omitempty"`
	Model           string `json:"model"`
	BaseURL         string `json:"base_url"`
	APIKeyMasked    string `json:"api_key_masked,omitempty"`
	Provider        string `json:"provider,omitempty"`
	AnthropicFormat bool   `json:"anthropic_format"`
	PermissionMode  string `json:"permission_mode,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ShowReasoning   *bool  `json:"show_reasoning,omitempty"`
	Vision          bool   `json:"vision"`
}

// defaultEntryIdx returns the index of the default entry: the one whose model
// matches DefaultModel, else 0.
func defaultEntryIdx(cfg config.Config) int {
	for i, e := range cfg.Models {
		if e.Model == cfg.DefaultModel {
			return i
		}
	}
	return 0
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()

	models := []modelConfig{}
	defaultIdx := defaultEntryIdx(cfg)

	for i, e := range cfg.Models {
		m := modelConfig{
			ID:              e.Model,
			Model:           e.Model,
			BaseURL:         e.BaseURL,
			APIKeyMasked:    maskKey(e.APIKey),
			Provider:        e.Provider,
			AnthropicFormat: entryUsesAnthropic(e),
			ReasoningEffort: e.ReasoningEffort,
			ShowReasoning:   e.ShowReasoning,
			Vision:          e.Vision,
		}
		switch {
		case i == defaultIdx:
			m.Type = "default"
			m.PermissionMode = cfg.PermissionMode
		case e.Model == cfg.LiteModel:
			m.Type = "lite"
		}
		models = append(models, m)
	}

	writeJSON(w, http.StatusOK, configResponse{
		Models:          models,
		DefaultModelIdx: defaultIdx,
		FontSize:        "medium",
		Language:        "en",
		ShowReasoning:   cfg.ShowReasoning,
		Coauthor:        cfg.Coauthor,
	})
}

// ─── PUT /api/config/show_reasoning ─────────────────────────────────────────

type putShowReasoningRequest struct {
	ShowReasoning bool `json:"show_reasoning"`
}

func (s *Server) handlePutShowReasoning(w http.ResponseWriter, r *http.Request) {
	var req putShowReasoningRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	cfg.ShowReasoning = &req.ShowReasoning
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}
	// Cached senders may have been built with the old global default.
	s.invalidateSenderCache()
	// Also rebuild the server's default sender so unbound existing sessions
	// pick up the new show_reasoning value on their next turn.
	if err := s.reloadDefaultSender(); err != nil {
		// Log but don't fail the request: config is already saved and the next
		// chat request will retry via ensureSender anyway.
		log.Printf("[server] reload default sender after show_reasoning change: %v", err)
	}

	// Push each session's own effective show_reasoning — resolved against ITS
	// OWN model entry (see entryForSession), which falls back to this new
	// global default only when that entry has no explicit override of its
	// own — so this doesn't paint a session pinned to a model with its own
	// show_reasoning: true/false with the new global value it's shielded from.
	sessions, _ := agent.ListSessions(50)
	for _, sess := range sessions {
		_, pm, re, sr, ctxUsage := s.sessionStatusFields(sess)
		if sr == nil {
			continue
		}
		s.wsHub.broadcast(sess.ID, map[string]any{
			"type":             "session_update",
			"session_id":       sess.ID,
			"working_dir":      s.sessionCwd(sess),
			"permission_mode":  pm,
			"reasoning_effort": re,
			"show_reasoning":   *sr,
			"context_usage":    ctxUsage,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "show_reasoning": req.ShowReasoning})
}

// ─── PUT /api/config/coauthor ───────────────────────────────────────────────

type putCoauthorRequest struct {
	Coauthor bool `json:"coauthor"`
}

// handlePutCoauthor updates config.Coauthor — whether the agent should
// append a Co-authored-by line to git commit messages. Unlike
// show_reasoning, this value is never baked into a cached sender:
// effectiveCoauthor (server.go) reads it fresh from config on every turn's
// prompt composition, so no sender-cache invalidation/reload and no
// per-session WS broadcast are needed here — the next turn on any session
// just picks it up.
func (s *Server) handlePutCoauthor(w http.ResponseWriter, r *http.Request) {
	var req putCoauthorRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	cfg.Coauthor = &req.Coauthor
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "coauthor": req.Coauthor})
}

// maskKey masks most of an API key, keeping the first and last four runes
// visible. It measures by runes so it never splits a multi-byte UTF-8 character.
func maskKey(k string) string {
	if k == "" {
		return ""
	}
	r := []rune(k)
	n := len(r)
	if n <= 8 {
		return strings.Repeat("*", n)
	}
	return string(r[:4]) + strings.Repeat("*", n-8) + string(r[n-4:])
}

// ─── POST /api/config/test ──────────────────────────────────────────────────

type testConfigRequest struct {
	Model           string `json:"model"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	Provider        string `json:"provider,omitempty"`
	Index           int    `json:"index"`
	AnthropicFormat bool   `json:"anthropic_format"`
}

// storedAPIKey returns the saved key of the config entry that best matches
// (provider, baseURL): an exact provider+endpoint match wins, else the first
// entry on the same provider. Empty when none is stored. Lets a connection
// test reuse the stored key when the provider is unchanged.
func storedAPIKey(cfg config.Config, provider, baseURL string) string {
	nb := strings.TrimRight(baseURL, "/")
	var sameProvider string
	for _, e := range cfg.Models {
		if e.APIKey == "" || e.Provider != provider {
			continue
		}
		if strings.TrimRight(e.BaseURL, "/") == nb {
			return e.APIKey
		}
		if sameProvider == "" {
			sameProvider = e.APIKey
		}
	}
	return sameProvider
}

func (s *Server) handleTestConfig(w http.ResponseWriter, r *http.Request) {
	var req testConfigRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Model == "" || req.BaseURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "model and base_url are required"})
		return
	}

	// Resolve the vendor: explicit request field > known endpoint > the Custom
	// catch-all (the URL is custom by then).
	providerName := app.ProviderCustom
	if req.Provider != "" && app.IsKnownVendor(req.Provider) {
		providerName = req.Provider
	} else if v := app.VendorByBaseURL(req.BaseURL); v != "" {
		providerName = v
	}
	// The Custom vendor has no fixed wire format; derive it from the request.
	protocol := ""
	if app.VendorNeedsProtocol(providerName) {
		protocol = customProtocol(req.AnthropicFormat)
	}

	// Resolve the key. An empty or still-masked field means "unchanged" — the
	// panel never prefills the key input, so editing a model and hitting Test
	// sends no key. Reuse the stored key of the matching entry instead of
	// demanding a re-type; fall back to the vendor's env var last.
	key := req.APIKey
	if key == "" || strings.Contains(key, "****") {
		key = ""
		if cfg, err := config.Load(); err == nil {
			key = storedAPIKey(cfg, providerName, req.BaseURL)
		}
	}
	if key == "" {
		key = os.Getenv(app.VendorAPIKeyEnvVar(providerName))
	}
	if key == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "no API key provided"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := app.TestConnection(ctx, providerName, key, req.BaseURL, req.Model, protocol); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Connection successful"})
}

// ─── POST /api/config/models ────────────────────────────────────────────────

type saveModelRequest struct {
	Type            string `json:"type"`
	Model           string `json:"model"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	Provider        string `json:"provider,omitempty"`
	AnthropicFormat bool   `json:"anthropic_format"`
	PermissionMode  string `json:"permission_mode,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ShowReasoning   *bool  `json:"show_reasoning,omitempty"`
	Vision          *bool  `json:"vision,omitempty"`
}

// applyModelRequestToEntry overlays the request onto an entry. An empty (or
// still-masked) api_key keeps the stored key — the panel echoes masked keys
// and omits unchanged ones. The vendor resolves from explicit request field >
// known endpoint > the entry's current value; only a brand-new entry with no
// signal at all falls back to the anthropic_format flag.
func applyModelRequestToEntry(req saveModelRequest, e *config.ModelEntry) {
	e.Model = req.Model
	e.BaseURL = req.BaseURL
	if req.APIKey != "" && !strings.Contains(req.APIKey, "****") {
		e.APIKey = req.APIKey
	}
	switch {
	case req.Provider != "":
		e.Provider = req.Provider
	case app.VendorByBaseURL(req.BaseURL) != "":
		e.Provider = app.VendorByBaseURL(req.BaseURL)
	case e.Provider != "":
		// keep the entry's current vendor
	case req.BaseURL == "":
		// No endpoint signal at all — fall back to the protocol root vendor.
		if req.AnthropicFormat {
			e.Provider = app.ProviderAnthropic
		} else {
			e.Provider = app.ProviderOpenAI
		}
	default:
		e.Provider = app.ProviderCustom
	}
	// Protocol is stored only for the Custom vendor; a named vendor pins its own,
	// so clear any stale value carried over from a previous Custom selection.
	if app.VendorNeedsProtocol(e.Provider) {
		e.Protocol = customProtocol(req.AnthropicFormat)
	} else {
		e.Protocol = ""
	}
	if req.ReasoningEffort != "" {
		e.ReasoningEffort = req.ReasoningEffort
	}
	if req.ShowReasoning != nil {
		e.ShowReasoning = req.ShowReasoning
	}
	// Vision is always recorded. Honour an explicit request value (the form's
	// toggle); otherwise resolve from the catalogue for a predefined model, or
	// fall back to the id heuristic for a custom / unknown one. Resolution runs
	// after the vendor is settled above, so VendorModelVision sees the final
	// provider.
	switch {
	case req.Vision != nil:
		e.Vision = *req.Vision
	default:
		if v, known := app.VendorModelVision(e.Provider, req.Model); known {
			e.Vision = v
		} else {
			e.Vision = config.ModelSupportsVision(req.Model)
		}
	}
}

// handleSaveModelConfig creates a new model entry (POST /api/config/models).
func (s *Server) handleSaveModelConfig(w http.ResponseWriter, r *http.Request) {
	var req saveModelRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	var entry config.ModelEntry
	applyModelRequestToEntry(req, &entry)
	// Model is the entry's identity, so it must be unique.
	if _, exists := cfg.EntryByModel(entry.Model); exists {
		writeError(w, http.StatusConflict, fmt.Sprintf("a model entry for %q already exists", entry.Model))
		return
	}
	cfg.Models = append(cfg.Models, entry)
	if cfg.DefaultModel == "" || len(cfg.Models) == 1 {
		cfg.DefaultModel = entry.Model
	}
	if req.PermissionMode != "" {
		cfg.PermissionMode = req.PermissionMode
	}

	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}
	// Rebuild the default sender/model so new unbound sessions pick up the new
	// entry (e.g. the first model saved during onboard, or a changed default).
	s.invalidateSenderCache()
	if err := s.reloadDefaultSender(); err != nil {
		log.Printf("[server] reload default sender after saving model config: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": entry.Model})
}

// handleUpdateModelConfig updates the entry named by {id}.
func (s *Server) handleUpdateModelConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing model id")
		return
	}

	var req saveModelRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Model is the entry's identity; an empty one would re-key the entry to ""
	// (unaddressable via the API), so reject it as POST does.
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	updated := false
	newID := id
	for i := range cfg.Models {
		if cfg.Models[i].Model == id {
			applyModelRequestToEntry(req, &cfg.Models[i])
			// Model is the entry id. When it changes, the id changes with it, so
			// reject a collision with another entry and carry the default/lite
			// refs that pointed at the old model over to the new one.
			if newModel := cfg.Models[i].Model; newModel != id {
				for j := range cfg.Models {
					if j != i && cfg.Models[j].Model == newModel {
						writeError(w, http.StatusConflict, fmt.Sprintf("a model entry for %q already exists", newModel))
						return
					}
				}
				newID = newModel
				if cfg.DefaultModel == id {
					cfg.DefaultModel = newModel
				}
				if cfg.LiteModel == id {
					cfg.LiteModel = newModel
				}
			}
			updated = true
			break
		}
	}
	if !updated {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no model config for %q", id))
		return
	}
	if req.PermissionMode != "" {
		cfg.PermissionMode = req.PermissionMode
	}

	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}
	// Editing the default entry (provider/model/endpoint/key) must rebuild the
	// default sender, or new unbound sessions keep using the stale startup one.
	s.invalidateSenderCache()
	if err := s.reloadDefaultSender(); err != nil {
		log.Printf("[server] reload default sender after updating model config: %v", err)
	}

	// id may have changed if the model changed — return it so the client can
	// refresh its reference.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": newID})
}

// handleDeleteModelConfig removes the entry named by {id} and repairs the
// default/lite references that pointed at it.
func (s *Server) handleDeleteModelConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing model id")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	kept := cfg.Models[:0]
	removed := false
	for _, e := range cfg.Models {
		if e.Model == id {
			removed = true
			continue
		}
		kept = append(kept, e)
	}
	if !removed {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no model config for %q", id))
		return
	}
	cfg.Models = kept
	if cfg.DefaultModel == id {
		cfg.DefaultModel = ""
		if len(cfg.Models) > 0 {
			cfg.DefaultModel = cfg.Models[0].Model
		}
	}
	if cfg.LiteModel == id {
		cfg.LiteModel = ""
	}

	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}
	// Deleting an entry may have repaired the default; rebuild the default
	// sender so new unbound sessions follow the new default.
	s.invalidateSenderCache()
	if err := s.reloadDefaultSender(); err != nil {
		log.Printf("[server] reload default sender after deleting model config: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSetLiteModelConfig toggles lite_model on the entry named by {id}:
// pointing it at the entry, or clearing it when the entry is already the
// lite model. The lite entry serves history compaction.
func (s *Server) handleSetLiteModelConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing model id")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}
	if _, ok := cfg.EntryByModel(id); !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no model config named %q", id))
		return
	}
	if cfg.LiteModel == id {
		cfg.LiteModel = "" // toggle off
	} else {
		cfg.LiteModel = id
	}

	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}
	s.invalidateSenderCache()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lite_model": cfg.LiteModel})
}

// handleSetDefaultModelConfig points default_model at the entry named by {id}.
func (s *Server) handleSetDefaultModelConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing model id")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}
	if _, ok := cfg.EntryByModel(id); !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no model config named %q", id))
		return
	}
	cfg.DefaultModel = id

	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	// The next agent turn should pick up the new default: rebuild the default
	// sender/model under senderMu so new unbound sessions follow it.
	s.invalidateSenderCache()
	if err := s.reloadDefaultSender(); err != nil {
		log.Printf("[server] reload default sender after setting default model: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
