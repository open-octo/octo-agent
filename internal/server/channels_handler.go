package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/open-octo/octo-agent/internal/channel"
)

// ─── Channels REST API ────────────────────────────────────────────────────

// channelInfo is the safe-for-frontend view of a platform config (secrets masked).
type channelInfo struct {
	Platform  string            `json:"platform"`
	Enabled   bool              `json:"enabled"`
	Running   bool              `json:"running"`
	HasConfig bool              `json:"has_config"`
	Fields    map[string]string `json:"fields"`
}

var secretKeys = map[string]bool{
	"client_secret": true,
	"app_secret":    true,
	"secret":        true,
	"token":         true,
	"bot_token":     true,
	"bot_secret":    true,
}

// handleListChannels returns all platform configs with secrets masked.
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	cfg, err := channel.LoadConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]channelInfo, 0, len(cfg.Channels))
	for name, pc := range cfg.Channels {
		info := platformToInfo(name, pc)
		info.Running = s.isAdapterRunning(name)
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

// handleGetChannel returns a single platform's config.
func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	if platform == "" {
		writeError(w, http.StatusBadRequest, "missing platform name")
		return
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	pc := cfg.Platform(platform)
	if pc == nil {
		writeError(w, http.StatusNotFound, "platform not configured")
		return
	}

	info := platformToInfo(platform, pc)
	info.Running = s.isAdapterRunning(platform)
	writeJSON(w, http.StatusOK, info)
}

type channelUpdateRequest struct {
	Enabled bool              `json:"enabled"`
	Fields  map[string]string `json:"fields"`
}

// handleSaveChannel creates or updates a platform's config.
func (s *Server) handleSaveChannel(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	if platform == "" {
		writeError(w, http.StatusBadRequest, "missing platform name")
		return
	}

	var req channelUpdateRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Convert string fields to map[string]any for SetPlatform.
	fields := make(map[string]any)
	for k, v := range req.Fields {
		fields[k] = v
	}
	if _, ok := fields["enabled"]; !ok {
		fields["enabled"] = req.Enabled
	}

	cfg.SetPlatform(platform, fields)
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	// Hot-apply the change so a newly configured (or re-credentialed) channel
	// starts serving immediately — no manual server restart.
	s.reloadChannel(platform)

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleDeleteChannel removes a platform config entirely.
func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	if platform == "" {
		writeError(w, http.StatusBadRequest, "missing platform name")
		return
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cfg.RemovePlatform(platform)
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	// Stop the adapter now that its config is gone — no manual restart.
	s.reloadChannel(platform)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleTestChannel tests a connection to a platform with the given credentials.
func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	var req channelUpdateRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	platform := r.PathValue("platform")

	// Build PlatformConfig from request.
	pc := make(channel.PlatformConfig)
	for k, v := range req.Fields {
		pc[k] = v
	}

	ctor, err := channel.Find(platform)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ad, err := ctor(pc)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	errs := ad.ValidateConfig(pc)
	if len(errs) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": strings.Join(errs, "; ")})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Configuration valid. Connection test requires runtime start."})
}

// handleAvailableChannels returns all registered adapter platforms.
func (s *Server) handleAvailableChannels(w http.ResponseWriter, r *http.Request) {
	platforms := channel.RegisteredPlatforms()
	type availInfo struct {
		Platform string   `json:"platform"`
		Label    string   `json:"label"`
		Fields   []string `json:"fields"`
	}

	// Field definitions per platform.
	fieldDefs := map[string][]string{
		"dingtalk": {"client_id", "client_secret", "allowed_users"},
		"discord":  {"bot_token", "allowed_users"},
		"feishu":   {"app_id", "app_secret", "domain", "allowed_users"},
		"telegram": {"bot_token", "base_url", "parse_mode", "allowed_users"},
		"wecom":    {"bot_id", "secret", "webhook_key", "allowed_users"},
		"weixin":   {"base_url", "cred_path"},
	}

	labels := map[string]string{
		"dingtalk": "DingTalk (钉钉)",
		"discord":  "Discord",
		"feishu":   "Feishu (飞书)",
		"telegram": "Telegram",
		"wecom":    "WeCom (企业微信)",
		"weixin":   "WeChat (微信)",
	}

	out := make([]availInfo, 0, len(platforms))
	for _, p := range platforms {
		label := labels[p]
		if label == "" {
			label = p
		}
		fields := fieldDefs[p]
		if fields == nil {
			fields = []string{}
		}
		out = append(out, availInfo{Platform: p, Label: label, Fields: fields})
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

// ─── Proactive send (used by the CLI messenger to delegate here) ────────────

// handleChannelRecipients lists the chats the bot can currently push to (live
// sessions + the persisted /bind table), so a CLI/remote client can resolve a
// recipient the same way the in-process send_message tool does.
func (s *Server) handleChannelRecipients(w http.ResponseWriter, r *http.Request) {
	type recipient struct {
		Platform string `json:"platform"`
		ChatID   string `json:"chat_id"`
		UserID   string `json:"user_id,omitempty"`
		Active   bool   `json:"active"`
		Bound    bool   `json:"bound"`
	}
	out := []recipient{}
	if mgr := s.channelManager(); mgr != nil {
		for _, kc := range mgr.KnownChats() {
			out = append(out, recipient{kc.Platform, kc.ChatID, kc.UserID, kc.Active, kc.Bound})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipients": out})
}

// handleChannelSendText delivers a text message to a chat via the live adapter
// (falling back to a one-shot send from config). Same path scheduled-task
// notifications and the send_message tool use.
func (s *Server) handleChannelSendText(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	var req struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.ChatID) == "" || strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "chat_id and text are required")
		return
	}
	if err := s.channelSend(platform, req.ChatID, req.Text); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// handleChannelSendFile delivers a local file to a chat. The path is read from
// the server's own filesystem — the CLI client and serve run on the same host,
// so no upload is needed.
func (s *Server) handleChannelSendFile(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	var req struct {
		ChatID string `json:"chat_id"`
		Path   string `json:"path"`
		Name   string `json:"name"`
	}
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.ChatID) == "" || strings.TrimSpace(req.Path) == "" {
		writeError(w, http.StatusBadRequest, "chat_id and path are required")
		return
	}
	if err := s.channelSendFile(platform, req.ChatID, req.Path, req.Name); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// ─── Helpers ──────────────────────────────────────────────────────────────

func platformToInfo(name string, pc channel.PlatformConfig) channelInfo {
	fields := make(map[string]string)
	for k, v := range pc {
		if k == "enabled" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if secretKeys[k] && len(s) > 4 {
			s = maskSecret(s)
		}
		fields[k] = s
	}

	enabled := false
	if v, ok := pc["enabled"]; ok {
		switch val := v.(type) {
		case bool:
			enabled = val
		case string:
			enabled = val == "true"
		}
	}

	return channelInfo{
		Platform:  name,
		Enabled:   enabled,
		HasConfig: true,
		Fields:    fields,
	}
}

// isAdapterRunning reports whether the platform adapter is currently active.
func (s *Server) isAdapterRunning(platform string) bool {
	_, ok := s.runningAdapters.Load(platform)
	return ok
}

// maskSecret masks most of a secret string, keeping the first and last four
// runes visible. It measures by runes so it never splits a multi-byte UTF-8
// character (even though real secrets are usually ASCII).
func maskSecret(s string) string {
	r := []rune(s)
	n := len(r)
	if n <= 8 {
		return strings.Repeat("*", n)
	}
	return string(r[:4]) + strings.Repeat("*", n-8) + string(r[n-4:])
}
