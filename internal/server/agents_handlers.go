package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/skills"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ─── Request/Response types ─────────────────────────────────────────────────

type agentRequest struct {
	ID              string                        `json:"id,omitempty"`
	Name            string                        `json:"name"`
	Description     string                        `json:"description"`
	Model           string                        `json:"model,omitempty"`
	Tools           []string                      `json:"tools,omitempty"`
	ToolSkills      []string                      `json:"tool_skills,omitempty"`
	SystemPrompt    string                        `json:"system_prompt,omitempty"`
	ChannelBindings []agentprofile.ChannelBinding `json:"channel_bindings,omitempty"`

	// Gallery display metadata — optional, lets any agent (curated or
	// user-authored) carry them.
	Category         string   `json:"category,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	TagsEN           []string `json:"tags_en,omitempty"`
	ExamplePrompts   []string `json:"example_prompts,omitempty"`
	ExamplePromptsEN []string `json:"example_prompts_en,omitempty"`
	Icon             string   `json:"icon,omitempty"`
	NameEN           string   `json:"name_en,omitempty"`
	DescriptionEN    string   `json:"description_en,omitempty"`
}

type agentBindRequest struct {
	Platform  string `json:"platform"`
	AdapterID string `json:"adapter_id,omitempty"`
	ChatID    string `json:"chat_id"`
}

type agentTransferRequest struct {
	AgentID string `json:"agent_id"`
}

// agentResponse is the wire shape for an agent profile. Stored files use the
// same frontmatter shape via agentprofile.Profile.
type agentResponse struct {
	ID              string                        `json:"id"`
	Name            string                        `json:"name"`
	Description     string                        `json:"description"`
	Model           string                        `json:"model,omitempty"`
	Tools           []string                      `json:"tools,omitempty"`
	ToolSkills      []string                      `json:"tool_skills,omitempty"`
	SystemPrompt    string                        `json:"system_prompt,omitempty"`
	ChannelBindings []agentprofile.ChannelBinding `json:"channel_bindings,omitempty"`

	// Gallery display metadata (see agentRequest).
	Category         string   `json:"category,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	TagsEN           []string `json:"tags_en,omitempty"`
	ExamplePrompts   []string `json:"example_prompts,omitempty"`
	ExamplePromptsEN []string `json:"example_prompts_en,omitempty"`
	Icon             string   `json:"icon,omitempty"`
	NameEN           string   `json:"name_en,omitempty"`
	DescriptionEN    string   `json:"description_en,omitempty"`

	// Source is always present: "default" (officially curated) or "user".
	// Builtin capability-tier profiles never reach this struct — Store.List
	// excludes them.
	Source string `json:"source"`
}

func agentToResp(p *agentprofile.Profile) agentResponse {
	return agentResponse{
		ID:               p.ID,
		Name:             p.Name,
		Description:      p.Description,
		Model:            p.Model,
		Tools:            p.Tools,
		ToolSkills:       p.ToolSkills,
		SystemPrompt:     p.SystemPrompt,
		ChannelBindings:  p.ChannelBindings,
		Category:         p.Category,
		Tags:             p.Tags,
		TagsEN:           p.TagsEN,
		ExamplePrompts:   p.ExamplePrompts,
		ExamplePromptsEN: p.ExamplePromptsEN,
		Icon:             p.Icon,
		NameEN:           p.NameEN,
		DescriptionEN:    p.DescriptionEN,
		Source:           string(p.Source),
	}
}

// ─── Handlers ───────────────────────────────────────────────────────────────

// agentStoreOrInit returns the profile store, initializing it if needed.
// The store is read-through, so this is cheap.
func (s *Server) agentStoreOrInit() *agentprofile.Store {
	if s.agentStore == nil {
		_ = s.agentRouter()
	}
	return s.agentStore
}

// handleListAgents serves GET /api/agents — list all profiles (excluding the
// code-defined default).
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	store := s.agentStoreOrInit()
	profiles := store.List()
	resp := make([]agentResponse, 0, len(profiles))
	for _, p := range profiles {
		resp = append(resp, agentToResp(p))
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetAgent serves GET /api/agents/:id — get a single profile.
func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.agentStoreOrInit().Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, agentToResp(p))
}

// handleCreateAgent serves POST /api/agents — create a new profile. Writes the
// .md file via the Store; the ID is derived from the name (slug) or taken
// from the request.
func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req agentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.TrimSpace(req.Description) == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}

	// Derive a slug ID from the name if not explicitly provided.
	id := req.ID
	if id == "" {
		id = slugify(req.Name)
	}
	if id == "" || !agentprofile.IsValidID(id) {
		writeError(w, http.StatusBadRequest, "id must be a valid slug ([a-z0-9][a-z0-9-]*, 1-32 chars)")
		return
	}

	// Validate tool and skill names against the canonical registries so users
	// get immediate feedback instead of silent filtering at runtime.
	if unknown := unknownToolNames(req.Tools, s.skillReg); len(unknown) > 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown tools: %s", strings.Join(unknown, ", ")))
		return
	}

	p := &agentprofile.Profile{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		CapabilitySpec: agentprofile.CapabilitySpec{
			Model:        req.Model,
			Tools:        req.Tools,
			ToolSkills:   req.ToolSkills,
			SystemPrompt: req.SystemPrompt,
		},
		ChannelBindings:  req.ChannelBindings,
		Category:         req.Category,
		Tags:             req.Tags,
		TagsEN:           req.TagsEN,
		ExamplePrompts:   req.ExamplePrompts,
		ExamplePromptsEN: req.ExamplePromptsEN,
		Icon:             req.Icon,
		NameEN:           req.NameEN,
		DescriptionEN:    req.DescriptionEN,
	}

	if err := s.agentStoreOrInit().Create(p); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, agentToResp(p))
	s.broadcastGlobal(map[string]any{"type": "agents_changed"})
}

// handleUpdateAgent serves PUT /api/agents/:id — update an existing profile.
// Builtin profiles are immutable through this endpoint.
func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	existing, ok := s.agentStoreOrInit().Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	// Validate tool and skill names against the canonical registries.
	if unknown := unknownToolNames(req.Tools, s.skillReg); len(unknown) > 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown tools: %s", strings.Join(unknown, ", ")))
		return
	}

	bindings := existing.ChannelBindings
	if req.ChannelBindings != nil {
		bindings = req.ChannelBindings
	}

	// Gallery metadata falls back to the existing profile's values when the
	// request omits a field — same rule as bindings above. This matters most
	// when forking a curated (SourceDefault) expert: the conversational edit
	// flow (expert-agent-manager) typically only sends name/description/
	// tools/system_prompt, and must not silently wipe the persona's
	// category/tags/examples/icon on its first fork into a user override.
	category := existing.Category
	if req.Category != "" {
		category = req.Category
	}
	tags := existing.Tags
	if req.Tags != nil {
		tags = req.Tags
	}
	tagsEN := existing.TagsEN
	if req.TagsEN != nil {
		tagsEN = req.TagsEN
	}
	examplePrompts := existing.ExamplePrompts
	if req.ExamplePrompts != nil {
		examplePrompts = req.ExamplePrompts
	}
	examplePromptsEN := existing.ExamplePromptsEN
	if req.ExamplePromptsEN != nil {
		examplePromptsEN = req.ExamplePromptsEN
	}
	icon := existing.Icon
	if req.Icon != "" {
		icon = req.Icon
	}
	nameEN := existing.NameEN
	if req.NameEN != "" {
		nameEN = req.NameEN
	}
	descriptionEN := existing.DescriptionEN
	if req.DescriptionEN != "" {
		descriptionEN = req.DescriptionEN
	}

	p := &agentprofile.Profile{
		ID:          existing.ID,
		Name:        req.Name,
		Description: req.Description,
		CapabilitySpec: agentprofile.CapabilitySpec{
			Model:        req.Model,
			Tools:        req.Tools,
			ToolSkills:   req.ToolSkills,
			SystemPrompt: req.SystemPrompt,
		},
		ChannelBindings:  bindings,
		Category:         category,
		Tags:             tags,
		TagsEN:           tagsEN,
		ExamplePrompts:   examplePrompts,
		ExamplePromptsEN: examplePromptsEN,
		Icon:             icon,
		NameEN:           nameEN,
		DescriptionEN:    descriptionEN,
	}

	if err := s.agentStoreOrInit().Update(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentToResp(p))
	s.broadcastGlobal(map[string]any{"type": "agents_changed"})
}

// handleDeleteAgent serves DELETE /api/agents/:id — remove a user-level profile.
// Builtin profiles and profiles with active channel bindings are protected.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.agentStoreOrInit().Delete(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
	s.broadcastGlobal(map[string]any{"type": "agents_changed"})
}

// handleBindAgent serves POST /api/agents/:id/bind — bind a profile to an IM chat.
func (s *Server) handleBindAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	if req.Platform == "" || req.ChatID == "" {
		writeError(w, http.StatusBadRequest, "platform and chat_id are required")
		return
	}

	p, ok := s.agentStoreOrInit().Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	// Append binding (avoid duplicates).
	for _, b := range p.ChannelBindings {
		if b.Platform == req.Platform && b.ChatID == req.ChatID && b.AdapterID == req.AdapterID {
			writeJSON(w, http.StatusOK, agentToResp(p))
			return
		}
	}
	p.ChannelBindings = append(p.ChannelBindings, agentprofile.ChannelBinding{
		Platform:  req.Platform,
		AdapterID: req.AdapterID,
		ChatID:    req.ChatID,
	})
	if err := s.agentStoreOrInit().Update(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentToResp(p))
	s.broadcastGlobal(map[string]any{"type": "agents_changed"})
}

// handleUnbindAgent serves DELETE /api/agents/:id/bind — remove an IM binding.
func (s *Server) handleUnbindAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	p, ok := s.agentStoreOrInit().Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	filtered := p.ChannelBindings[:0]
	for _, b := range p.ChannelBindings {
		if !(b.Platform == req.Platform && b.ChatID == req.ChatID && b.AdapterID == req.AdapterID) {
			filtered = append(filtered, b)
		}
	}
	p.ChannelBindings = filtered
	if err := s.agentStoreOrInit().Update(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentToResp(p))
	s.broadcastGlobal(map[string]any{"type": "agents_changed"})
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// slugify converts a name to a lowercase slug suitable as a profile ID.
func slugify(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	// Keep only [a-z0-9-], collapse runs of other chars into single dashes.
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == ' ' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	return s
}

// unknownToolNames returns tools in names that are neither a known built-in
// tool nor a known skill. When names is empty it returns nil (not validated,
// meaning "all tools" in the profile's allowlist).
func unknownToolNames(names []string, reg *skills.Registry) []string {
	if len(names) == 0 {
		return nil
	}
	known := make(map[string]bool, len(tools.KnownToolNames())+reg.Len())
	for _, n := range tools.KnownToolNames() {
		known[n] = true
	}
	for _, sk := range reg.List() {
		known[sk.Name] = true
	}
	var unknown []string
	for _, n := range names {
		if !known[n] {
			unknown = append(unknown, n)
		}
	}
	return unknown
}
