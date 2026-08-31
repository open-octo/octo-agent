package tools

import (
	"context"
	"fmt"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/skills"
)

// activeSkills, when non-nil and non-empty, backs the `skill` tool: it serves
// SKILL.md bodies on demand (progressive-disclosure L2). Set once at session
// start via SetSkills; mirrors the package-level activeSandbox/defaultBg.
var activeSkills *skills.Registry

// SetSkills registers the skills the `skill` tool serves and that DefaultTools
// uses to decide whether to advertise the tool. cmd/octo calls this at session
// start. Pass nil (or an empty registry) to disable.
func SetSkills(r *skills.Registry) { activeSkills = r }

// SkillsManifest is the full L1 skills section for the system prompt: the
// SKILL.md skills (skills.RenderManifest) plus recorded browser skills
// (RenderBrowserRecordingsManifest). Every caller uses this instead of
// skills.RenderManifest directly so browser recordings are always discoverable
// and never drop out when the manifest is rebuilt (e.g. on a server-side skill
// toggle/import). Returns "" when both are empty.
func SkillsManifest(r *skills.Registry) string {
	m := skills.RenderManifest(r)
	if b := RenderBrowserRecordingsManifest(); b != "" {
		if m != "" {
			m += "\n\n"
		}
		m += b
	}
	return m
}

// skillsEnabled reports whether any skill was discovered — the gate for both
// advertising and dispatching the skill tool.
func skillsEnabled() bool { return activeSkills != nil && activeSkills.Len() > 0 }

// SkillTool loads a skill's full SKILL.md body on demand. The model calls it
// after spotting a matching skill in the system-prompt "Available skills"
// manifest; the body returns as a tool_result, landing in history rather than
// the frozen system prefix. The zero value reads from the package-level
// registry set by SetSkills.
type SkillTool struct{}

func (SkillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "skill",
		Description: "Load the full instructions for an available skill by name. Skills are " +
			"listed in the system prompt under \"Available skills\". Call this with a skill's " +
			"name to get its step-by-step instructions, then follow them using the other tools.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name exactly as shown in the Available skills list.",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (SkillTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	name, _ := input["name"].(string)
	if name == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("skill: name is required")
	}
	if !skillsEnabled() {
		return agent.ToolResult{Text: ""}, fmt.Errorf("skill: no skills are available")
	}
	if err := skillAllowedForProfile(ctx, name); err != nil {
		return agent.ToolResult{Text: ""}, err
	}
	s, ok := activeSkills.Get(name)
	if !ok {
		// Miss: the skill may have been added or renamed after session start.
		// The system-prompt manifest is frozen (for prompt-cache stability), but
		// the tool re-scans disk so a freshly-dropped skill is still loadable
		// without restarting the session. The fast path above means this disk
		// scan only runs on a miss, not on every load.
		activeSkills.Reload()
		s, ok = activeSkills.Get(name)
	}
	if !ok {
		return agent.ToolResult{Text: ""}, fmt.Errorf("skill: unknown skill %q", name)
	}
	if s.System {
		if err := systemSkillDeniedForProfile(ctx, name); err != nil {
			return agent.ToolResult{Text: ""}, err
		}
	}
	// RenderSkill prefixes the skill's directory so the model can read any
	// files the body references (scripts, templates, reference docs).
	return agent.ToolResult{Text: skills.RenderSkill(s, "")}, nil
}

// skillAllowedForProfile enforces the manifest rule at load time: a
// non-builtin expert may only load skills its profile's tool_skills names.
// The manifest already hides everything else, but the manifest is advisory —
// a prompt-injected or guessed name must fail here, not succeed quietly. An
// expert's capabilities are configured from the outside (the Default agent or
// the Web UI), never widened from inside the session. Builtin profiles and
// context-less sessions (CLI/TUI, no profile store) stay unrestricted.
func skillAllowedForProfile(ctx context.Context, name string) error {
	store := profileStoreFromContext(ctx)
	agentID := sessionAgentIDFromContext(ctx)
	if store == nil || agentID == "" {
		return nil
	}
	p, ok := store.Get(agentID)
	if !ok || p.Source == agentprofile.SourceBuiltin {
		return nil
	}
	for _, s := range p.ToolSkills {
		if s == name {
			return nil
		}
	}
	return fmt.Errorf("skill: %q is not enabled for this agent — an expert's skills are configured by the user (Web UI → Experts) or the Default agent, not from inside the session", name)
}

// systemSkillDeniedForProfile refuses a system skill for a non-builtin
// profile. A system skill can't reach a non-builtin manifest, but every load
// path must hold the same line — an injected or guessed name is the whole
// reason to check at load rather than trust the manifest. Shared by the skill
// tool and the workflow prelude's skill().
func systemSkillDeniedForProfile(ctx context.Context, name string) error {
	store := profileStoreFromContext(ctx)
	agentID := sessionAgentIDFromContext(ctx)
	if store == nil || agentID == "" {
		return nil
	}
	if p, ok := store.Get(agentID); ok && p.Source != agentprofile.SourceBuiltin {
		return fmt.Errorf("skill: %q is a system skill reserved for the Default agent", name)
	}
	return nil
}
