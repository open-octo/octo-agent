package app

import (
	"context"
	"io"
	"time"

	"github.com/Leihb/octo-agent/internal/config"
	"github.com/Leihb/octo-agent/internal/mcp"
	"github.com/Leihb/octo-agent/internal/tools"
	"github.com/Leihb/octo-agent/internal/version"
)

// mcpConnectTimeout bounds the whole non-interactive connect pass. It caps the
// per-server windows in mcp.connectOne (notably the 5-minute OAuth budget),
// since a non-interactive transport can't complete a device flow anyway — an
// OAuth server without a cached/refreshable token is skipped within this bound
// rather than hanging startup.
const mcpConnectTimeout = 60 * time.Second

// ConnectMCP loads the MCP server config under cwd, connects every server
// non-interactively, and registers the resulting surface so DefaultToolsFor /
// the tool executor pick it up. It is the server/IM counterpart to the CLI's
// own (interactive, OAuth-prompting) connect path — here authPromptFor is nil,
// so servers needing a fresh device flow are skipped (mcp.deviceFlow nil-guards
// the prompt) while stdio, header-auth, and cached-OAuth servers connect.
//
// Returns a cleanup that unregisters and closes the registry; it is always
// non-nil (a no-op when no servers connected), so callers can defer it
// unconditionally. warn receives per-server skip diagnostics.
func ConnectMCP(ctx context.Context, cwd string, warn io.Writer) (func(), error) {
	cfg, err := mcp.LoadConfig(cwd)
	if err != nil {
		return func() {}, err
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return func() {}, nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
	defer cancel() // only bounds the connect pass; live connections aren't tied to it

	info := mcp.Implementation{Name: "octo", Version: version.Version}
	reg := mcp.ConnectAll(connectCtx, cfg, info, nil /* no interactive OAuth */, warn, nil)
	if reg.Len() == 0 {
		reg.Close()
		return func() {}, nil
	}

	tools.SetMCPRegistry(reg)
	return func() {
		tools.SetMCPRegistry(nil)
		reg.Close()
	}, nil
}

// ToolSearchConfigFrom maps the persisted tools.tool_search config block onto
// the tools-package config. Unknown/empty "enabled" defaults to auto; numeric
// zero values are left for SetToolSearchConfig to backfill with its documented
// defaults. Shared by every entry point so Tool Search behaves identically.
func ToolSearchConfigFrom(c config.ToolSearchConfig) tools.ToolSearchConfig {
	mode := tools.ToolSearchAuto
	switch c.Enabled {
	case "on":
		mode = tools.ToolSearchOn
	case "off":
		mode = tools.ToolSearchOff
	}
	return tools.ToolSearchConfig{
		Mode:           mode,
		ThresholdPct:   c.ThresholdPct,
		SearchLimit:    c.SearchDefaultLimit,
		MaxSearchLimit: c.MaxSearchLimit,
	}
}
