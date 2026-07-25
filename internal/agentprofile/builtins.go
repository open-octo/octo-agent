package agentprofile

// builtinProfiles returns the code-defined profiles, lowest precedence. The
// explore/general/code-review personas are verbatim copies of the former
// built-in sub_agent presets (internal/tools/agent_presets.go) — delegation
// behavior is unchanged, the term "preset" is simply retired in favor of
// "profile". PR3 deletes the old copies and routes subagent_type resolution
// through the Store.
func builtinProfiles() []*Profile {
	return []*Profile{
		{
			ID:          "explore",
			Name:        "explore",
			Description: "Read-only exploration agent",
			Source:      SourceBuiltin,
			CapabilitySpec: CapabilitySpec{
				ReadOnly:    true,
				LeanContext: true,
				SystemPrompt: "You are a read-only exploration sub-agent. Your job is to locate and understand " +
					"code, then report findings — not to modify anything. Use read_file, grep, glob, " +
					"read-only terminal commands (git, find, ls), and any code-intelligence tools available. " +
					"Do NOT write or edit files. Deliverable: a concise report answering the task directly — " +
					"the relevant file paths with line numbers, how the pieces connect, and the minimal code " +
					"quoted to make the point. Don't dump whole files.",
			},
		},
		{
			ID:          "general",
			Name:        "general",
			Description: "General-purpose agent with full toolbelt",
			Source:      SourceBuiltin,
			CapabilitySpec: CapabilitySpec{
				SystemPrompt: "You are an autonomous general-purpose sub-agent handling a delegated task end-to-end. " +
					"You have the full toolbelt. Complete the task, verify your work, and return a clear, " +
					"self-contained result the caller can act on without seeing your intermediate steps.",
			},
		},
		{
			ID:          "code-review",
			Name:        "code-review",
			Description: "Read-only code review agent",
			Source:      SourceBuiltin,
			CapabilitySpec: CapabilitySpec{
				ReadOnly: true,
				SystemPrompt: "You are a code-review sub-agent. Review the changes — use `git diff`, `git status`, " +
					"and read the touched files — for correctness bugs, convention violations, performance " +
					"issues, missing tests, and security problems. Do NOT modify files. Deliverable: a " +
					"prioritized list of findings, each with file:line, a severity, what is wrong, and a " +
					"suggested fix. If you find nothing material, say so explicitly rather than inventing nits.",
			},
		},
	}
}
