package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/config"
)

// ctxKeyProfileStore is the context key for the per-turn agentprofile.Store.
type ctxKeyProfileStore struct{}

// WithProfileStore attaches an agentprofile.Store to the context so the
// sub_agent tool can resolve subagent_type names against profiles. The store
// is read-through, so profile edits (API, Web UI, direct .md edits) take
// effect on the next resolution without any reload call.
func WithProfileStore(ctx context.Context, store *agentprofile.Store) context.Context {
	return context.WithValue(ctx, ctxKeyProfileStore{}, store)
}

// profileStoreFromContext returns the context's profile store, or nil.
func profileStoreFromContext(ctx context.Context) *agentprofile.Store {
	if s, ok := ctx.Value(ctxKeyProfileStore{}).(*agentprofile.Store); ok {
		return s
	}
	return nil
}

// profileNames returns a comma-separated list of available profile names
// (built-ins + user-defined) for error messages.
func profileNames(store *agentprofile.Store) string {
	names := []string{"default", "code-review", "explore", "general"}
	if store != nil {
		for _, p := range store.List() {
			names = append(names, p.ID)
		}
	}
	sort.Strings(names[1:]) // keep "default" first
	return strings.Join(names, ", ")
}

// AgentTool is the unified sub-agent tool. It replaces the previous
// explore_agent / plan_agent / general_agent /
// code_review_agent split with a single tool controlled by parameters.
//
// Parameters:
//   - description: short label for UI/logging
//   - prompt:      the task. Self-contained — the child starts with zero
//     conversation context and can't see this conversation.
//   - subagent_type: agent type (explore, general, code-review, or a
//     user-defined agent from ~/.octo/agents). Required.
//   - run_in_background: when true the agent runs async and you are notified
//     on completion. When false (default) it blocks and returns the result.
//   - model: optional model override ("lite" resolves to the endpoint's lite
//     model, falling back to the parent's model when none is configured)
//   - tools: optional tool-name allowlist for the child
//
// The tool is advertised only when a SubAgentManager is registered.
type AgentTool struct{}

func (t AgentTool) Definition() agent.ToolDefinition { return t.DefinitionFor("") }

// DefinitionFor is Definition with session-model context: when the registry
// knows which model this turn runs on, the model-override parameter lists the
// sibling models reachable on that model's endpoint (the child reuses the
// parent's endpoint connection, so only those are valid overrides).
func (AgentTool) DefinitionFor(sessionModel string) agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "sub_agent",
		Description: "Launch an autonomous sub-agent to handle a focused sub-task. " +
			"The sub-agent runs with its own context window and tool budget. " +
			"Use when you need parallel investigation, a fresh context for an isolated " +
			"sub-problem, or when the task is well-defined enough to delegate.\n\n" +
			"The child always starts with zero conversation context and the persona named by " +
			"subagent_type — it can never see this conversation, so make the prompt a complete, " +
			"self-contained task description (file paths, constraints, deliverable). " +
			"'explore' for read-only investigation/research and planning, " +
			"'general' for delegated work that modifies files, 'code-review' for an independent " +
			"read of changes. To branch the conversation itself, use the session branch feature " +
			"instead — a sub-agent is not a conversation fork.\n\n" +
			"Set run_in_background=true when you are dispatching multiple independent sub-agents that can run in parallel, " +
			"or when a sub-agent is expected to take a while. You will be notified when it completes. " +
			"Leave it false (default) to block and receive the result directly when the task is short. " +
			"(Some transports run every sub-agent synchronously; the result says so when it does.)\n\n" +
			"Follow up with sub_agent_send. Do not poll sub_agent_status while waiting for a background sub-agent; " +
			"wait for the completion notification instead. Use sub_agent_status only to list running agents or when you " +
			"suspect a sub-agent is stuck. Use sub_agent_kill to terminate a stuck or no-longer-needed agent.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"description": map[string]any{
					"type":        "string",
					"description": "Short human-readable label for this sub-agent (3-7 words). Shown in progress UI; doesn't shape behavior. Example: 'Investigate auth middleware'.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The task for the sub-agent. Make it self-contained: include all context the sub-agent needs (file paths, constraints, deliverable) since it can't see this conversation. State the expected output shape (a summary, a list, a YES/NO).",
				},
				"subagent_type": map[string]any{
					"type":        "string",
					"description": "Required agent type: 'explore' (read-only research), 'general' (full toolbelt), 'code-review' (read-only review), or a user-defined agent from ~/.octo/agents.",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "When true, run asynchronously and receive a notification on completion. When false (default), block until the agent finishes and return its result directly.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": subAgentModelParamDesc(sessionModel),
				},
				"tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional tool-name allowlist for the sub-agent. Omit to inherit your tools (minus sub_agent itself — no recursion).",
				},
			},
			"required": []string{"description", "prompt", "subagent_type"},
		},
	}
}

// subAgentModelParamBase documents the model-override parameter without any
// endpoint context: the plain default plus the "lite" keyword.
const subAgentModelParamBase = "Optional model override. Defaults to the parent's model. " +
	"Pass \"lite\" to run the sub-agent on the endpoint's configured lite model — right for " +
	"mechanical subtasks where speed/cost beats quality; it falls back to the parent's model " +
	"when no lite model is configured."

// subAgentModelParamDesc returns the model-override parameter description,
// appending the sibling models of the session model's endpoint when the
// config resolves them. Only same-endpoint models are listed because the
// child reuses the parent's endpoint connection — a model served elsewhere
// would be rejected by the endpoint.
func subAgentModelParamDesc(sessionModel string) string {
	if sessionModel == "" {
		return subAgentModelParamBase
	}
	cfg, err := config.LoadCached()
	if err != nil {
		return subAgentModelParamBase
	}
	return subAgentModelParamDescFor(cfg, sessionModel)
}

// subAgentModelParamDescFor is subAgentModelParamDesc over an explicit config,
// split out so tests don't touch the on-disk config cache.
func subAgentModelParamDescFor(cfg config.Config, sessionModel string) string {
	for _, ep := range cfg.Endpoints {
		for _, m := range ep.Models {
			if m.Model != sessionModel {
				continue
			}
			names := make([]string, 0, len(ep.Models))
			for _, mm := range ep.Models {
				name := mm.Model
				if name == ep.LiteModel {
					name += " (lite)"
				}
				names = append(names, name)
			}
			return subAgentModelParamBase +
				" Models available on the current endpoint: " + strings.Join(names, ", ") + "."
		}
	}
	// Session model not in the config (e.g. an ad-hoc --model value): no list.
	return subAgentModelParamBase
}

func (AgentTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	if IsSubAgent(ctx) {
		return agent.ToolResult{Text: ""}, fmt.Errorf("sub_agent: a sub-agent cannot spawn another sub-agent")
	}

	desc := strings.TrimSpace(stringArg(input, "description"))
	prompt := strings.TrimSpace(stringArg(input, "prompt"))
	if prompt == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("sub_agent: prompt is required")
	}
	if desc == "" {
		desc = firstLine(prompt)
	}

	mgr := resolveSubAgentManager(ctx, nil)
	if mgr == nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("sub_agent: sub-agent dispatch is not configured for this session")
	}

	// subagent_type is required: every sub-agent is a fresh, typed agent.
	// Conversation branching belongs to the session branch feature, not to a
	// sub-agent the user can't talk to.
	subagentType := strings.TrimSpace(stringArg(input, "subagent_type"))
	if subagentType == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("sub_agent: subagent_type is required. Available: %s", profileNames(profileStoreFromContext(ctx)))
	}
	store := profileStoreFromContext(ctx)
	var profile *agentprofile.Profile
	if store != nil {
		if p, ok := store.Get(subagentType); ok {
			profile = p
		}
	} else {
		// Fallback for callers that haven't wired a store (e.g. legacy CLI):
		// resolve via the deprecated preset loader.
		if p, ok := lookupAgentPreset(subagentType); ok {
			profile = &agentprofile.Profile{
				ID:          p.name,
				Description: p.description,
				CapabilitySpec: agentprofile.CapabilitySpec{
					SystemPrompt:    p.persona,
					ReadOnly:        p.readOnly,
					LeanContext:     p.leanSystem,
					Tools:           p.tools,
					DisallowedTools: p.disallowedTools,
					Model:           p.model,
				},
			}
		}
	}
	if profile == nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("sub_agent: unknown subagent_type %q. Available: %s", subagentType, profileNames(store))
	}

	// Build the spawn request. Call-level tools/model win over the profile's
	// frontmatter defaults; the profile fills in what the call left unset.
	callTools := stringSliceArg(input, "tools")
	callModel := strings.TrimSpace(stringArg(input, "model"))
	req := SpawnRequest{
		Description: desc,
		AgentType:   subagentType,
		Prompt:      prompt,
		Tools:       callTools,
		Model:       callModel,
	}
	req.SystemSuffix = profile.SystemPrompt
	req.ReadOnly = profile.ReadOnly
	req.LeanSystem = profile.LeanContext
	req.DisallowedTools = profile.DisallowedTools
	if len(callTools) == 0 {
		req.Tools = profile.Tools
	}
	if callModel == "" {
		req.Model = profile.Model
	}

	// Determine sync vs async
	runInBackground := boolArg(input, "run_in_background")
	// A transport with no follow-up-turn channel (the CLI one-shot — the only
	// synchronous mode) forces sync even if the model asked for background —
	// and tells the model, rather than silently downgrading its choice. TUI,
	// web session, and IM turns all stay async: they re-inject completions as
	// idle follow-up turns (see SetSynchronous).
	forcedSync := false
	if runInBackground && mgr.Synchronous() {
		runInBackground = false
		forcedSync = true
	}

	if runInBackground {
		id, err := mgr.Start(req)
		if err != nil {
			return agent.ToolResult{Text: ""}, fmt.Errorf("sub_agent: %w", err)
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Started sub-agent %s. You will be notified when it completes.", id),
		}, nil
	}

	// Synchronous path — block and return the result.
	res, err := mgr.RunSync(ctx, req)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("sub_agent: %w", err)
	}
	// User promoted the running synchronous sub-agent to background.
	if res.StopReason == "promoted" {
		return agent.ToolResult{
			Text: fmt.Sprintf("Sub-agent %s was promoted to background. You will be notified when it completes.", res.AgentID),
		}, nil
	}
	text := withAgentTag(res.AgentID, res.Reply)
	// Surface a truncated result rather than passing a partial reply off as
	// complete: a sub-agent that hit its turn limit returns partial work.
	if res.StopReason == "max_turns" {
		text += "\n\n[INCOMPLETE: this sub-agent hit its turn limit — the result above is partial. Re-launch with a narrower task, or treat it as unfinished.]"
	}
	if forcedSync {
		text += "\n\n[note: ran synchronously and returned its full result here — this transport doesn't support background sub-agents, so run_in_background was ignored.]"
	}
	return agent.ToolResult{Text: text}, nil
}

// boolArg pulls a boolean argument, defaulting to false.
func boolArg(input map[string]any, key string) bool {
	raw, ok := input[key]
	if !ok {
		return false
	}
	if v, ok := raw.(bool); ok {
		return v
	}
	return false
}
