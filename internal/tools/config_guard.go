package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/hooks"
)

// ConfigGuard validates ~/.octo/config.yml immediately after the agent edits it,
// so a broken edit surfaces in the agent's context right away instead of
// degrading silently. Because LoadCached keeps the last good config on a parse
// error, a bad edit otherwise looks like it took effect — the agent gets no
// signal until something downstream misbehaves. This closes that gap by folding
// a warning into the turn on the very next step after the edit.
type ConfigGuard struct{}

// NewConfigGuard returns a ready guard.
func NewConfigGuard() *ConfigGuard { return &ConfigGuard{} }

// RegisterHooks wires the guard onto a session's hook engine: check on
// PostToolUse whether the tool just wrote the config file and, if so, validate
// it. The returned string (empty when the file is fine) is folded into the
// agent's context like any other PostToolUse hook output.
func (g *ConfigGuard) RegisterHooks(e *hooks.Engine) {
	if g == nil || e == nil {
		return
	}
	e.RegisterInProc(hooks.EventPostToolUse, func(_ context.Context, p hooks.Payload) string {
		if !touchedConfigFile(p.ToolName, p.ToolInput) {
			return ""
		}
		return validateConfigFile()
	})
}

// touchedConfigFile reports whether a just-run tool wrote ~/.octo/config.yml.
// The tool-name switch is first so the common non-writing tools short-circuit
// before any filesystem/env lookup. edit_file/write_file carry the target in
// "path"; terminal is best-effort — arbitrary shell can't be parsed, so it
// requires BOTH ".octo" and "config.yml" in the command (a plain project
// "config.yml" won't match, only the octo config's distinctive path does; a
// rare false positive costs one harmless re-validation).
func touchedConfigFile(tool string, input map[string]any) bool {
	switch tool {
	case "edit_file", "write_file":
		p, _ := input["path"].(string)
		if p == "" {
			return false
		}
		cfgPath, err := config.Path()
		if err != nil {
			return false
		}
		return sameFile(p, cfgPath)
	case "terminal":
		cmd, _ := input["command"].(string)
		return cmd != "" && strings.Contains(cmd, "config.yml") && strings.Contains(cmd, ".octo")
	}
	return false
}

// sameFile reports whether path a (as the model wrote it, possibly ~-relative)
// resolves to the same absolute path as b.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, err1 := filepath.Abs(expandHomePath(a))
	absB, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func expandHomePath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// validateConfigFile loads the config raw (Load, not LoadCached) and returns a
// warning if it no longer parses or has semantic problems; "" when it is fine.
func validateConfigFile() string {
	cfg, err := config.Load()
	if err != nil {
		return "⚠️ ~/.octo/config.yml no longer parses: " + err.Error() +
			" — your change did NOT take effect (the server kept the last valid config). Fix the file before relying on the edit."
	}
	if probs := cfg.Validate(); len(probs) > 0 {
		return "⚠️ ~/.octo/config.yml parsed but has problems: " + strings.Join(probs, "; ") +
			". These fall back silently — fix them if unintended."
	}
	return ""
}
