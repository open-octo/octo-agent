package tools

import (
	"context"
	"fmt"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
)

// EnableOwnSkillTool lets a non-builtin expert agent opt an installed skill
// into its own profile, closing the install → use loop without waiting for
// the Web UI or the Default Agent: the expert installs a skill via the
// terminal tool (`octo skills add ...`), then calls this to add it to its
// profile's tool_skills. The write goes through agentprofile.Store.Update, so
// a curated expert is forked into a ~/.octo/agents/<id>.md override (the same
// semantics as editing it from the gallery UI), and the per-turn profile
// re-resolution makes the skill visible from the expert's next message.
//
// Advertised only when the turn's context resolves to a non-builtin profile
// (enableOwnSkillOn in registry.go) — builtin profiles and context-less CLI
// sessions already see every installed skill, so the tool would be a no-op
// slot for them. It must additionally be in the profile's tools allowlist,
// like any other tool.
type EnableOwnSkillTool struct{}

func (EnableOwnSkillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "enable_own_skill",
		Description: "Enable an already-installed skill for yourself (this agent). Adds the skill " +
			"to your profile's tool_skills so it appears in your Available skills list from your " +
			"next message — load it with the skill tool after that. The skill must be installed " +
			"first: if the result says it is missing, install it (e.g. via the terminal tool: " +
			"`octo skills add <owner/repo[/path]>`) and retry. Enabling an already-enabled skill " +
			"is a no-op.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The installed skill's name (its directory name, as `octo skills list` shows it).",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (EnableOwnSkillTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	name, _ := input["name"].(string)
	if name == "" {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: name is required")
	}
	store := profileStoreFromContext(ctx)
	agentID := sessionAgentIDFromContext(ctx)
	if store == nil || agentID == "" {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: this session has no agent profile context")
	}
	profile, ok := store.Get(agentID)
	if !ok {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: profile %q not found", agentID)
	}
	if profile.Source == agentprofile.SourceBuiltin {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: builtin agents already see every installed skill")
	}
	for _, s := range profile.ToolSkills {
		if s == name {
			return agent.ToolResult{Text: fmt.Sprintf("Skill %q is already enabled for your profile — load it with the skill tool.", name)}, nil
		}
	}
	if !skillsEnabled() {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: no skills are installed — install one first (e.g. via the terminal tool: `octo skills add <owner/repo[/path]>`)")
	}
	skill, ok := activeSkills.Get(name)
	if !ok {
		// The skill may have been installed after this registry was built —
		// same rescan-on-miss fast path as the skill tool.
		activeSkills.Reload()
		skill, ok = activeSkills.Get(name)
	}
	if !ok {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: skill %q is not installed (or is disabled) — install it first (e.g. via the terminal tool: `octo skills add <owner/repo[/path]>`), then retry", name)
	}
	if skill.System {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: skill %q is a system skill reserved for the Default agent and cannot be enabled for an expert", name)
	}
	profile.ToolSkills = append(profile.ToolSkills, name)
	if err := store.Update(profile); err != nil {
		return agent.ToolResult{}, fmt.Errorf("enable_own_skill: %v", err)
	}
	msg := fmt.Sprintf("Enabled skill %q for your profile (%s). It appears in your Available skills list from your next message — load it with the skill tool then.", name, agentID)
	if profile.Source == agentprofile.SourceDefault {
		msg += " Note: your curated profile was forked into a personal override, so future official content updates to this expert no longer apply to you."
	}
	return agent.ToolResult{Text: msg}, nil
}
