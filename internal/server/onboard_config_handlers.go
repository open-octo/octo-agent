package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/prompt"
	"github.com/open-octo/octo-agent/internal/tools"
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
	for _, ep := range cfg.Endpoints {
		if ep.APIKey != "" {
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

// configResponse mirrors what the Svelte frontend expects. PR5: the Models
// field is gone (the frontend reads endpoints via GET /api/config/endpoints);
// only the global settings remain. ReasoningEffort is the global reasoning
// level (PR5 deleted per-entry reasoning).
type configResponse struct {
	FontSize        string `json:"font_size,omitempty"`
	Language        string `json:"language,omitempty"`
	ShowReasoning   *bool  `json:"show_reasoning,omitempty"`
	Coauthor        *bool  `json:"coauthor,omitempty"`
	WorkspaceDir    string `json:"workspace_dir,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()

	// Coauthor, unlike ShowReasoning, has an OCTO_COAUTHOR env-var layer above
	// the config file, so the raw cfg.Coauthor pointer can disagree with what
	// commits actually get. Report the resolved value the Settings toggle is
	// meant to reflect, not the on-disk-only one.
	effCoauthor := cfg.EffectiveCoauthor()
	writeJSON(w, http.StatusOK, configResponse{
		FontSize:        "medium",
		Language:        cfg.Language,
		ShowReasoning:   cfg.ShowReasoning,
		Coauthor:        &effCoauthor,
		WorkspaceDir:    cfg.WorkspaceDir,
		ReasoningEffort: cfg.ReasoningEffort,
	})
}

// ─── GET /api/config/endpoints ───────────────────────────────────────────────

// endpointsResponse is the two-level shape served by GET /api/config/endpoints
// (design §10.1). PR4b ships only the read path: the frontend renders this as
// read-only endpoint cards with their model lists; the write path (CRUD on
// endpoints) lands in PR5 alongside the Save format switch.
//
// Security: APIKey is never echoed. HasAPIKey reports presence so the UI can
// badge "已配置 / 未设置" without exposing the secret.
type endpointsResponse struct {
	Endpoints []endpointConfigJSON `json:"endpoints"`
	Default   string               `json:"default,omitempty"`
	Lite      string               `json:"lite,omitempty"`
}

// endpointConfigJSON is one channel in the two-level response.
//
// APIKey is deliberately absent from the JSON shape: the read API never echoes
// the secret. HasAPIKey is the only key-related field the frontend gets, so it
// can badge "已配置 / 未设置" without learning the key itself (design §10.1,
// §19.1).
type endpointConfigJSON struct {
	ID        string              `json:"id"`
	Name      string              `json:"name,omitempty"`
	Provider  string              `json:"provider"`
	BaseURL   string              `json:"base_url,omitempty"`
	Protocol  string              `json:"protocol,omitempty"`
	HasAPIKey bool                `json:"has_api_key"`
	LiteModel string              `json:"lite_model,omitempty"`
	Models    []endpointModelJSON `json:"models"`
}

// endpointModelJSON is one model under an endpoint.
type endpointModelJSON struct {
	Model  string `json:"model"`
	Vision bool   `json:"vision"`
}

// handleGetEndpoints serves the two-level endpoint view. Data comes straight
// from config.Load's in-memory Endpoints (syncEndpointsFromModels already
// projects the legacy flat Models list into this shape, so PR4b works against
// either schema). Default/Lite are echoed as composite ids verbatim — on a
// legacy flat config they're synthesised by syncEndpointsFromModels, on a
// hand-written new-schema config they're whatever the user typed.
func (s *Server) handleGetEndpoints(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()

	out := endpointsResponse{
		Endpoints: make([]endpointConfigJSON, 0, len(cfg.Endpoints)),
		Default:   cfg.Default,
		Lite:      cfg.Lite,
	}
	for _, ep := range cfg.Endpoints {
		em := endpointConfigJSON{
			ID:        ep.ID,
			Name:      ep.Name,
			Provider:  ep.Provider,
			BaseURL:   ep.BaseURL,
			Protocol:  ep.Protocol,
			HasAPIKey: ep.APIKey != "",
			LiteModel: ep.LiteModel,
			Models:    make([]endpointModelJSON, 0, len(ep.Models)),
		}
		for _, m := range ep.Models {
			em.Models = append(em.Models, endpointModelJSON{Model: m.Model, Vision: m.Vision})
		}
		out.Endpoints = append(out.Endpoints, em)
	}
	writeJSON(w, http.StatusOK, out)
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

// ─── PUT /api/config/language ────────────────────────────────────────────────

type putLanguageRequest struct {
	Language string `json:"language"`
}

func (s *Server) handlePutLanguage(w http.ResponseWriter, r *http.Request) {
	var req putLanguageRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Language != "en" && req.Language != "zh" {
		writeError(w, http.StatusBadRequest, "language must be 'en' or 'zh'")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	cfg.Language = req.Language
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "language": req.Language})
}

// ─── PUT /api/config/workspace_dir ───────────────────────────────────────────

type putWorkspaceDirRequest struct {
	WorkspaceDir string `json:"workspace_dir"`
}

// handlePutWorkspaceDir updates the global default working directory used for
// new web sessions. It accepts the raw config value: empty clears the override
// and falls back to the server's launch directory, "auto" resolves to
// ~/Desktop/octo, and anything else is stored as a literal path. The server's
// resolved default is also updated so new sessions pick it up immediately
// without a restart.
func (s *Server) handlePutWorkspaceDir(w http.ResponseWriter, r *http.Request) {
	var req putWorkspaceDirRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("load config: %v", err))
		return
	}

	cfg.WorkspaceDir = req.WorkspaceDir
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	// Resolve the new value immediately so the running server applies it to
	// new sessions without needing a restart. On error, fall back to no
	// override; the config is already saved.
	resolved, err := tools.ResolveWorkspaceDir(req.WorkspaceDir)
	if err != nil {
		slog.Warn("could not resolve workspace_dir; new sessions keep using the launch directory", "err", err)
		resolved = ""
	}
	s.setWorkspaceDir(resolved)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace_dir": req.WorkspaceDir})
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

// storedAPIKey returns the saved key of the endpoint that best matches
// (provider, baseURL): an exact provider+endpoint match wins, else the first
// endpoint on the same provider. Empty when none is stored. Lets a connection
// test reuse the stored key when the provider is unchanged.
func storedAPIKey(cfg config.Config, provider, baseURL string) string {
	nb := strings.TrimRight(baseURL, "/")
	var sameProvider string
	for _, ep := range cfg.Endpoints {
		if ep.APIKey == "" || ep.Provider != provider {
			continue
		}
		if strings.TrimRight(ep.BaseURL, "/") == nb {
			return ep.APIKey
		}
		if sameProvider == "" {
			sameProvider = ep.APIKey
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

// ─── POST/PATCH/DELETE /api/config/endpoints (CRUD) ─────────────────────────
//
// PR5 (design §10.2): endpoint-level CRUD. The handler layer is thin — it
// parses the request, calls config.UpsertEndpoint / UpsertModel /
// SetDefaultComposite / RenameEndpoint under config.Mutate (flock + atomic
// Save), and triggers s.invalidateEndpointSenders when connection params or
// the endpoint id changes. Model-level mutations (add/delete model under an
// endpoint) also invalidate per the coarse-grained policy (design §6).
//
// Old /api/config/models* handlers (POST/PATCH/DELETE) were deleted in the
// same slice — callers must use these endpoint-level routes. The web UI
// continues to read endpoints via GET /api/config/endpoints (PR4b) and will
// gain editing affordances in a follow-up PR.

type createEndpointRequest struct {
	ID       string            `json:"id"`
	Name     string            `json:"name,omitempty"`
	Provider string            `json:"provider"`
	BaseURL  string            `json:"base_url,omitempty"`
	APIKey   string            `json:"api_key,omitempty"`
	Protocol string            `json:"protocol,omitempty"`
	Models   []endpointModelIn `json:"models,omitempty"`
}

type endpointModelIn struct {
	Model  string `json:"model"`
	Vision bool   `json:"vision"`
}

type updateEndpointRequest struct {
	// NewID, when non-empty, triggers a rename (RenameEndpoint + cascade).
	NewID    string `json:"new_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// endpointJSONOut is the response shape for a single endpoint — same as
// endpointConfigJSON from GET /api/config/endpoints, kept inline here so the
// CRUD handlers don't depend on the read handler's type staying put.
type endpointJSONOut struct {
	ID        string              `json:"id"`
	Name      string              `json:"name,omitempty"`
	Provider  string              `json:"provider"`
	BaseURL   string              `json:"base_url,omitempty"`
	Protocol  string              `json:"protocol,omitempty"`
	HasAPIKey bool                `json:"has_api_key"`
	LiteModel string              `json:"lite_model,omitempty"`
	Models    []endpointModelJSON `json:"models"`
}

func endpointToJSON(ep config.Endpoint) endpointJSONOut {
	out := endpointJSONOut{
		ID:        ep.ID,
		Name:      ep.Name,
		Provider:  ep.Provider,
		BaseURL:   ep.BaseURL,
		Protocol:  ep.Protocol,
		HasAPIKey: ep.APIKey != "",
		LiteModel: ep.LiteModel,
		Models:    make([]endpointModelJSON, 0, len(ep.Models)),
	}
	for _, m := range ep.Models {
		out.Models = append(out.Models, endpointModelJSON{Model: m.Model, Vision: m.Vision})
	}
	return out
}

// handleCreateEndpoint: POST /api/config/endpoints
func (s *Server) handleCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req createEndpointRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}

	var created config.Endpoint
	if err := config.Mutate(func(cfg *config.Config) error {
		// Reject id collision — Validate would flag it as unfixable anyway.
		for _, ep := range cfg.Endpoints {
			if ep.ID == req.ID {
				return fmt.Errorf("endpoint id %q already exists", req.ID)
			}
		}
		ep := config.Endpoint{
			ID:       req.ID,
			Name:     req.Name,
			Provider: req.Provider,
			BaseURL:  req.BaseURL,
			APIKey:   req.APIKey,
			Protocol: req.Protocol,
		}
		for _, m := range req.Models {
			if m.Model == "" {
				continue
			}
			ep.Models = append(ep.Models, config.EndpointModel{Model: m.Model, Vision: m.Vision})
		}
		cfg.UpsertEndpoint(ep)
		created = ep
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A brand-new endpoint has no cached senders yet, so no invalidation
	// needed. New endpoints with models could in principle be referenced
	// immediately, but the cache is populated lazily on the first turn.
	writeJSON(w, http.StatusCreated, endpointToJSON(created))
}

// handleUpdateEndpoint: PATCH /api/config/endpoints/{id}
// Without new_id: updates connection params / name in place.
// With new_id: renames the endpoint (RenameEndpoint + Default/Lite cascade +
// invalidateEndpointSenders on the old id).
func (s *Server) handleUpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing endpoint id")
		return
	}
	var req updateEndpointRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	var updated config.Endpoint
	if err := config.Mutate(func(cfg *config.Config) error {
		idx := -1
		for i := range cfg.Endpoints {
			if cfg.Endpoints[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: %s", config.ErrEndpointNotFound, id)
		}
		// Rename path: new_id triggers RenameEndpoint (which rewrites
		// Default/Lite composite-id prefixes) before applying field updates.
		if req.NewID != "" && req.NewID != id {
			if err := cfg.RenameEndpoint(id, req.NewID); err != nil {
				return err
			}
			// RenameEndpoint updated Endpoints[idx].ID in place; re-find it
			// under the new id so the field patches below land on the right
			// endpoint.
			for i := range cfg.Endpoints {
				if cfg.Endpoints[i].ID == req.NewID {
					idx = i
					break
				}
			}
		}
		ep := cfg.Endpoints[idx]
		// Apply field patches. Empty strings in the request mean "unchanged"
		// EXCEPT for Protocol/BaseURL where the user may legitimately want to
		// clear them — but we can't distinguish "omitted" from "cleared" with
		// the current JSON shape. PR5 errs on the side of "empty = unchanged"
		// for all fields; clearing a field requires sending the previous
		// value. (A follow-up could switch to *string / omitempty:true to
		// distinguish, but the current web UI doesn't need it.)
		if req.Name != "" {
			ep.Name = req.Name
		}
		if req.Provider != "" {
			ep.Provider = req.Provider
		}
		if req.BaseURL != "" {
			ep.BaseURL = req.BaseURL
		}
		if req.APIKey != "" {
			ep.APIKey = req.APIKey
		}
		if req.Protocol != "" {
			ep.Protocol = req.Protocol
		}
		cfg.Endpoints[idx] = ep
		updated = ep
		return nil
	}); err != nil {
		if errors.Is(err, config.ErrEndpointNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, config.ErrEndpointIDInUse) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Invalidate cached senders for the endpoint — connection params may have
	// changed (coarse-grained: we invalidate even if only Name changed). If a
	// rename happened, invalidate the OLD id (senders cached under "<old>::...").
	invalidID := id
	if req.NewID != "" && req.NewID != id {
		invalidID = id
	}
	s.invalidateEndpointSenders(invalidID)
	writeJSON(w, http.StatusOK, endpointToJSON(updated))
}

// handleDeleteEndpoint: DELETE /api/config/endpoints/{id}
func (s *Server) handleDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing endpoint id")
		return
	}
	if err := config.Mutate(func(cfg *config.Config) error {
		idx := -1
		for i := range cfg.Endpoints {
			if cfg.Endpoints[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("%w: %s", config.ErrEndpointNotFound, id)
		}
		cfg.Endpoints = append(cfg.Endpoints[:idx], cfg.Endpoints[idx+1:]...)
		// Clear Default/Lite if they pointed at the deleted endpoint's prefix.
		if strings.HasPrefix(cfg.Default, id+"::") {
			cfg.Default = ""
		}
		if strings.HasPrefix(cfg.Lite, id+"::") {
			cfg.Lite = ""
		}
		return nil
	}); err != nil {
		if errors.Is(err, config.ErrEndpointNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateEndpointSenders(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAddEndpointModel: POST /api/config/endpoints/{id}/models
func (s *Server) handleAddEndpointModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing endpoint id")
		return
	}
	var req endpointModelIn
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	var updated config.Endpoint
	if err := config.Mutate(func(cfg *config.Config) error {
		if err := cfg.UpsertModel(id, config.EndpointModel{Model: req.Model, Vision: req.Vision}); err != nil {
			return err
		}
		for _, ep := range cfg.Endpoints {
			if ep.ID == id {
				updated = ep
				break
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, config.ErrEndpointNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateEndpointSenders(id)
	writeJSON(w, http.StatusOK, endpointToJSON(updated))
}

// handleDeleteEndpointModel: DELETE /api/config/endpoints/{id}/models/{model}
func (s *Server) handleDeleteEndpointModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	model := r.PathValue("model")
	if id == "" || model == "" {
		writeError(w, http.StatusBadRequest, "missing endpoint id or model")
		return
	}
	if err := config.Mutate(func(cfg *config.Config) error {
		for i := range cfg.Endpoints {
			if cfg.Endpoints[i].ID != id {
				continue
			}
			kept := cfg.Endpoints[i].Models[:0]
			for _, m := range cfg.Endpoints[i].Models {
				if m.Model == model {
					continue
				}
				kept = append(kept, m)
			}
			cfg.Endpoints[i].Models = kept
			// Clear Default/Lite if they pointed at the deleted model.
			if cfg.Default == id+"::"+model {
				cfg.Default = ""
			}
			if cfg.Lite == id+"::"+model {
				cfg.Lite = ""
			}
			return nil
		}
		return fmt.Errorf("%w: %s", config.ErrEndpointNotFound, id)
	}); err != nil {
		if errors.Is(err, config.ErrEndpointNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateEndpointSenders(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSetEndpointDefault: POST /api/config/endpoints/{id}/default
// Sets cfg.Default to "<id>::<first model under this endpoint>". The caller
// can't pick a specific model via this endpoint (PR5 design: first suffices;
// a follow-up could add a `?model=` query).
func (s *Server) handleSetEndpointDefault(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing endpoint id")
		return
	}
	var newDefault string
	if err := config.Mutate(func(cfg *config.Config) error {
		for _, ep := range cfg.Endpoints {
			if ep.ID != id {
				continue
			}
			if len(ep.Models) == 0 {
				return fmt.Errorf("endpoint %q has no models", id)
			}
			newDefault = ep.CompositeID(ep.Models[0].Model)
			cfg.SetDefaultComposite(newDefault)
			return nil
		}
		return fmt.Errorf("%w: %s", config.ErrEndpointNotFound, id)
	}); err != nil {
		if errors.Is(err, config.ErrEndpointNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Default switch does NOT invalidate senders (design §6) — only changes
	// which sender ResolveDefault returns next turn.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "default": newDefault})
}

// handleSetEndpointLite: POST /api/config/endpoints/{id}/lite
// Sets cfg.Lite to "<id>::<first model under this endpoint>".
func (s *Server) handleSetEndpointLite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing endpoint id")
		return
	}
	var newLite string
	if err := config.Mutate(func(cfg *config.Config) error {
		for _, ep := range cfg.Endpoints {
			if ep.ID != id {
				continue
			}
			if len(ep.Models) == 0 {
				return fmt.Errorf("endpoint %q has no models", id)
			}
			newLite = ep.CompositeID(ep.Models[0].Model)
			cfg.Lite = newLite
			return nil
		}
		return fmt.Errorf("%w: %s", config.ErrEndpointNotFound, id)
	}); err != nil {
		if errors.Is(err, config.ErrEndpointNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lite": newLite})
}
