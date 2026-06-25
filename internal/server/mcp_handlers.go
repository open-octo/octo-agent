package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Leihb/octo-agent/internal/app"
	"github.com/Leihb/octo-agent/internal/config"
	"github.com/Leihb/octo-agent/internal/mcp"
	"github.com/Leihb/octo-agent/internal/tools"
)

// allowedMCPCommands lists stdio executables that are commonly used to launch
// MCP servers and are considered safe without an explicit user opt-in. Any
// other command requires allow_arbitrary_command=true.
var allowedMCPCommands = map[string]bool{
	"npx": true, "npm": true, "node": true,
	"uvx": true, "uv": true,
	"python": true, "python3": true,
	"cargo": true, "go": true, "ruby": true,
}

// parseStdioCommand splits a command line into executable and arguments,
// validates that the executable is a simple basename (no path separators,
// absolute paths, or shell metacharacters), and returns the parsed parts.
// If allowArbitrary is false and the basename is not in allowedMCPCommands,
// it returns an error telling the caller to opt in.
func parseStdioCommand(name, line string, allowArbitrary bool) (string, []string, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("mcp server %q: empty command", name)
	}
	base := fields[0]
	if filepath.IsAbs(base) || strings.ContainsAny(base, `/\;|&$<>`) || strings.Contains(base, "..") {
		return "", nil, fmt.Errorf("mcp server %q: command must be a simple basename", name)
	}
	base = filepath.Base(base)
	if !allowArbitrary && !allowedMCPCommands[base] {
		return "", nil, fmt.Errorf("mcp server %q: command %q is not in the allowlist; enable allow arbitrary command", name, base)
	}
	return base, fields[1:], nil
}

// MCP server management API. Reads merge the user-global and project-local
// configs (internal/mcp.LoadManaged); writes go to ~/.octo/mcp.json only —
// project-local entries are presented read-only. Mutations apply to the live
// registry incrementally (connect/disconnect just the touched server), so a
// change is effective for the next turn of every session without a restart.
// All mutation handlers hold s.mcpMu — the registry swap inside reload must
// not interleave with incremental connects.

type mcpServerInfo struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // "stdio" | "http" | "" (invalid entry)
	Source    string            `json:"source"`    // "user" | "project"
	Disabled  bool              `json:"disabled"`
	Invalid   string            `json:"invalid,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Auth      string            `json:"auth,omitempty"`
	Status    string            `json:"status"` // "connected" | "error" | "disabled" | "invalid" | "disconnected"
	Error     string            `json:"error,omitempty"`
	Tools     int               `json:"tools"`
}

// mcpServerList builds the management view: configured entries joined with
// live-registry state.
func (s *Server) mcpServerList() ([]mcpServerInfo, error) {
	managed, err := mcp.LoadManaged(s.cwd)
	if err != nil {
		return nil, err
	}
	reg := tools.ActiveMCPRegistry()
	out := make([]mcpServerInfo, 0, len(managed))
	for _, m := range managed {
		info := mcpServerInfo{
			Name:      m.Name,
			Transport: m.Entry.Kind(),
			Source:    m.Source,
			Disabled:  m.Entry.Disabled,
			Invalid:   m.Invalid,
			Command:   m.Entry.Command,
			Args:      m.Entry.Args,
			Env:       m.Entry.Env,
			URL:       m.Entry.URL,
			Headers:   m.Entry.Headers,
			Auth:      m.Entry.Auth,
		}
		switch {
		case m.Invalid != "":
			info.Status = "invalid"
		case m.Entry.Disabled:
			info.Status = "disabled"
		case reg != nil && reg.Get(m.Name) != nil:
			info.Status = "connected"
			info.Tools = len(reg.Get(m.Name).Tools)
		default:
			info.Status = "disconnected"
			if reg != nil {
				if msg, ok := reg.ConnectError(m.Name); ok {
					info.Status = "error"
					info.Error = msg
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *Server) writeMCPServerList(w http.ResponseWriter) {
	list, err := s.mcpServerList()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": list})
}

// connectIfLive dials one server into the active registry unless tools are
// disabled for this serve process. Connect failures are not surfaced as HTTP
// errors — the entry is saved either way and the per-server status reports
// the failure.
func (s *Server) connectIfLive(r *http.Request, name string, entry mcp.ServerEntry) {
	if !s.cfg.Tools || entry.Disabled {
		return
	}
	// r.Context() bounds only the dial — an established connection is not
	// tied to it. A client that gives up mid-connect aborts the attempt; the
	// recorded per-server error tells them what happened.
	_ = app.ConnectMCPServer(r.Context(), name, entry, nil)
}

// ─── GET /api/mcp/servers ───────────────────────────────────────────────────

func (s *Server) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	_ = r
	s.writeMCPServerList(w)
}

// ─── POST /api/mcp/servers ──────────────────────────────────────────────────

// Accepts either a single server:
//
//	{"name": "fs", "server": {"command": "npx", ...}}
//
// or a Claude Code-style bulk import (paste of an mcp.json):
//
//	{"mcpServers": {"fs": {...}, "api": {...}}}
//
// Single create rejects an existing name with 409 (edit goes through PATCH);
// bulk import upserts.
func (s *Server) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                  string                     `json:"name"`
		Server                *mcp.ServerEntry           `json:"server"`
		AllowArbitraryCommand bool                       `json:"allow_arbitrary_command"`
		Servers               map[string]mcp.ServerEntry `json:"mcpServers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	switch {
	case len(req.Servers) > 0:
		// Bulk import: validate everything first so a half-bad paste doesn't
		// land half the servers. For bulk import we do not require per-server
		// opt-in; instead we parse and normalize stdio commands and reject any
		// command that is not a simple allowlisted basename.
		for name, e := range req.Servers {
			if err := mcp.ValidateServerName(name); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if e.Command != "" {
				cmd, args, err := parseStdioCommand(name, e.Command, false)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				e.Command = cmd
				e.Args = append(args, e.Args...)
			}
			if err := e.Validate(name); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		for name, e := range req.Servers {
			if err := mcp.UpsertUserServer(name, e); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			s.connectIfLive(r, name, e)
		}
	case req.Name != "" && req.Server != nil:
		managed, err := mcp.LoadManaged(s.cwd)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, m := range managed {
			if m.Name == req.Name {
				writeError(w, http.StatusConflict, "an MCP server with this name already exists")
				return
			}
		}
		if req.Server.Command != "" {
			cmd, args, err := parseStdioCommand(req.Name, req.Server.Command, req.AllowArbitraryCommand)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			req.Server.Command = cmd
			req.Server.Args = append(args, req.Server.Args...)
		}
		if err := mcp.UpsertUserServer(req.Name, *req.Server); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.connectIfLive(r, req.Name, *req.Server)
	default:
		writeError(w, http.StatusBadRequest, "expected {name, server} or {mcpServers}")
		return
	}

	s.writeMCPServerList(w)
}

// userManagedServer resolves a path name to its managed entry, rejecting
// project-level entries (read-only from the web UI). Writes the HTTP error
// itself; the second return is false when the caller should stop.
func (s *Server) userManagedServer(w http.ResponseWriter, name string) (mcp.ManagedServer, bool) {
	managed, err := mcp.LoadManaged(s.cwd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return mcp.ManagedServer{}, false
	}
	for _, m := range managed {
		if m.Name != name {
			continue
		}
		if m.Source != "user" {
			writeError(w, http.StatusConflict, "project-level MCP servers are read-only here; edit .octo/mcp.json in the project")
			return mcp.ManagedServer{}, false
		}
		return m, true
	}
	writeError(w, http.StatusNotFound, "mcp server not found")
	return mcp.ManagedServer{}, false
}

// ─── PATCH /api/mcp/servers/{name} ──────────────────────────────────────────

func (s *Server) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Server                *mcp.ServerEntry `json:"server"`
		AllowArbitraryCommand bool             `json:"allow_arbitrary_command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Server == nil {
		writeError(w, http.StatusBadRequest, "expected {server: {...}}")
		return
	}

	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	if _, ok := s.userManagedServer(w, name); !ok {
		return
	}
	if req.Server.Command != "" {
		cmd, args, err := parseStdioCommand(name, req.Server.Command, req.AllowArbitraryCommand)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Server.Command = cmd
		req.Server.Args = append(args, req.Server.Args...)
	}
	if err := mcp.UpsertUserServer(name, *req.Server); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	app.DisconnectMCPServer(name)
	s.connectIfLive(r, name, *req.Server)
	s.writeMCPServerList(w)
}

// ─── DELETE /api/mcp/servers/{name} ─────────────────────────────────────────

func (s *Server) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	if _, ok := s.userManagedServer(w, name); !ok {
		return
	}
	if err := mcp.DeleteUserServer(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	app.DisconnectMCPServer(name)
	s.writeMCPServerList(w)
}

// ─── PATCH /api/mcp/servers/{name}/toggle ───────────────────────────────────

func (s *Server) handleToggleMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	m, ok := s.userManagedServer(w, name)
	if !ok {
		return
	}
	nowDisabled := !m.Entry.Disabled
	if err := mcp.SetUserServerDisabled(name, nowDisabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if nowDisabled {
		app.DisconnectMCPServer(name)
	} else {
		entry := m.Entry
		entry.Disabled = false
		s.connectIfLive(r, name, entry)
	}
	s.writeMCPServerList(w)
}

// ─── POST /api/mcp/servers/{name}/reconnect ─────────────────────────────────

// Reconnect works for any source (project entries too — it touches only the
// live connection, not the config files).
func (s *Server) handleReconnectMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	managed, err := mcp.LoadManaged(s.cwd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var entry *mcp.ManagedServer
	for i := range managed {
		if managed[i].Name == name {
			entry = &managed[i]
			break
		}
	}
	if entry == nil {
		writeError(w, http.StatusNotFound, "mcp server not found")
		return
	}
	if entry.Invalid != "" {
		writeError(w, http.StatusConflict, "cannot connect an invalid entry: "+entry.Invalid)
		return
	}
	if entry.Entry.Disabled {
		writeError(w, http.StatusConflict, "server is disabled; enable it first")
		return
	}
	if !s.cfg.Tools {
		writeError(w, http.StatusConflict, "tools are disabled on this server")
		return
	}
	_ = app.ConnectMCPServer(r.Context(), name, entry.Entry, nil)
	s.writeMCPServerList(w)
}

// ─── POST /api/mcp/reload ───────────────────────────────────────────────────

// Full reload from disk: picks up hand-edited config files and retries every
// failed connection in one pass.
func (s *Server) handleReloadMCP(w http.ResponseWriter, r *http.Request) {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	if !s.cfg.Tools {
		writeError(w, http.StatusConflict, "tools are disabled on this server")
		return
	}
	if err := app.SwapMCP(r.Context(), s.cwd, nil); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeMCPServerList(w)
}

// ─── GET /api/config/toolsearch ─────────────────────────────────────────────

type toolSearchSettings struct {
	Enabled            string `json:"enabled"` // "auto" | "on" | "off"
	ThresholdPct       int    `json:"threshold_pct"`
	SearchDefaultLimit int    `json:"search_default_limit"`
	MaxSearchLimit     int    `json:"max_search_limit"`
}

// toolSearchDefaults mirrors the documented tools.tool_search defaults
// (auto / 10% / 5 / 20) so the UI shows effective values, not zeros.
func toolSearchDefaults(c config.ToolSearchConfig) toolSearchSettings {
	out := toolSearchSettings{
		Enabled:            c.Enabled,
		ThresholdPct:       c.ThresholdPct,
		SearchDefaultLimit: c.SearchDefaultLimit,
		MaxSearchLimit:     c.MaxSearchLimit,
	}
	if out.Enabled == "" {
		out.Enabled = "auto"
	}
	if out.ThresholdPct == 0 {
		out.ThresholdPct = 10
	}
	if out.SearchDefaultLimit == 0 {
		out.SearchDefaultLimit = 5
	}
	if out.MaxSearchLimit == 0 {
		out.MaxSearchLimit = 20
	}
	return out
}

func (s *Server) handleGetToolSearch(w http.ResponseWriter, r *http.Request) {
	_ = r
	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	writeJSON(w, http.StatusOK, toolSearchDefaults(cfg.Tools.ToolSearch))
}

// ─── PUT /api/config/toolsearch ─────────────────────────────────────────────

// Persists tools.tool_search.enabled and applies it to the live process so
// the next turn of every session sees the change.
func (s *Server) handlePutToolSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled string `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch req.Enabled {
	case "auto", "on", "off":
	default:
		writeError(w, http.StatusBadRequest, `enabled must be "auto", "on", or "off"`)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	cfg.Tools.ToolSearch.Enabled = req.Enabled
	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}
	tools.SetToolSearchConfig(app.ToolSearchConfigFrom(cfg.Tools.ToolSearch))
	writeJSON(w, http.StatusOK, toolSearchDefaults(cfg.Tools.ToolSearch))
}
