package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/memorybackend"
)

// activeMemoryBackend, when non-nil, backs the `memory_recall` tool and the
// automatic store hook (see RegisterMemoryBackendHooks). It is a process-global
// single instance — only one external memory backend can be configured at a
// time — mirroring activeSkills/SetSkills in skill.go.
var activeMemoryBackend memorybackend.Backend

// activeMemoryBackendAutoRecall toggles whether RegisterMemoryBackendHooks
// also installs the automatic pre-turn recall hook (see SetMemoryBackendAutoRecall).
var activeMemoryBackendAutoRecall bool

// SetMemoryBackend registers the memory backend that `memory_recall` and the
// automatic store hook use. Pass nil to disable.
func SetMemoryBackend(b memorybackend.Backend) { activeMemoryBackend = b }

// SetMemoryBackendAutoRecall toggles automatic pre-turn recall — call
// alongside SetMemoryBackend, reading MemoryBackendConfig.AutoRecall.
func SetMemoryBackendAutoRecall(on bool) { activeMemoryBackendAutoRecall = on }

// memoryBackendEnabled reports whether an external memory backend is
// configured — the gate for both advertising memory_recall and registering
// the auto-store hook.
func memoryBackendEnabled() bool { return activeMemoryBackend != nil }

// MemoryBackendGuidance is the system-prompt guidance shown when an external
// memory backend is configured. Empty when disabled. Distinct from — and
// composes alongside — the MEMORY.md guidance in internal/prompt/base.md:
// MEMORY.md is the agent-curated standing-instructions layer; this is a
// free-form semantic layer the backend itself extracts and indexes.
func MemoryBackendGuidance() string {
	if !memoryBackendEnabled() {
		return ""
	}
	return "### Memory backend\n" +
		"A long-term semantic memory backend (" + activeMemoryBackend.Name() + ") is connected. " +
		"Every turn is automatically saved to it after you respond — you don't need to do anything to store it. " +
		"Use `memory_recall` when you suspect something relevant was discussed in a prior session or conversation " +
		"and you need that context — including a personal fact, preference, or detail the user asks about that " +
		"isn't in MEMORY.md. Try `memory_recall` once before telling the user you don't know: MEMORY.md only " +
		"holds what's been explicitly curated there, not everything ever said. This is separate from MEMORY.md: " +
		"MEMORY.md is your curated standing guidance (preferences, rules, project decisions); this backend is " +
		"free-form semantic recall, not something you maintain by hand."
}

// MemoryRecallTool searches the configured external memory backend for
// content relevant to a query. The zero value reads from the package-level
// activeMemoryBackend set by SetMemoryBackend.
type MemoryRecallTool struct{}

func (MemoryRecallTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "memory_recall",
		Description: "Search the connected external memory backend for content relevant to a query. Use this " +
			"when you suspect something was discussed in a prior session or conversation and need that context, " +
			"or before telling the user you don't know a personal fact or preference that isn't in MEMORY.md.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What to search for.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (MemoryRecallTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	query, _ := input["query"].(string)
	if query == "" {
		return agent.ToolResult{}, fmt.Errorf("memory_recall: query is required")
	}
	if !memoryBackendEnabled() {
		return agent.ToolResult{}, fmt.Errorf("memory_recall: no memory backend is configured")
	}
	results, err := activeMemoryBackend.Recall(ctx, query)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("memory_recall: %w", err)
	}
	if len(results) == 0 {
		return agent.ToolResult{Text: "No relevant memories found."}, nil
	}
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("- " + r.Content)
		if r.Score != 0 {
			b.WriteString(" (score: " + strconv.FormatFloat(r.Score, 'f', 2, 64) + ")")
		}
	}
	return agent.ToolResult{Text: b.String()}, nil
}

// RegisterMemoryBackendHooks wires the automatic-store side effect into a
// session's hook engine: after each turn ends (EventStop, which carries the
// turn's UserInput/AssistantReply directly in its payload), the turn's
// content is stored in the background. In-proc hooks registered via
// RegisterInProc always run synchronously (only shell hooks support the
// async flag), so the store itself is dispatched in its own goroutine with a
// context decoupled from the turn's — that context may already be cancelled
// by the time this fires. Store errors are swallowed: a background hook has
// no tool_result channel to surface them to the agent, and store is
// best-effort by design (see the memory-backend design plan).
//
// No-op when no backend is configured. Call this alongside
// memory.Injector.RegisterHooks wherever a session builds a fresh
// hooks.Engine — there is no shared hook-registration chokepoint today.
//
// When SetMemoryBackendAutoRecall(true) is active, this also registers an
// EventUserPromptSubmit hook that calls Recall with the user's message and
// folds the result into that same turn's outgoing message — the same
// mechanism memory.Injector uses for MEMORY.md reminders (see
// internal/memory/injector.go), so no new agent-core plumbing is needed.
// This runs synchronously on the turn's critical path (unlike the
// fire-and-forget Store hook above), so it's wrapped in its own short
// timeout and swallows errors/empty results the same way Store does.
func RegisterMemoryBackendHooks(e *hooks.Engine) {
	if e == nil || !memoryBackendEnabled() {
		return
	}
	b := activeMemoryBackend
	e.RegisterInProc(hooks.EventStop, func(_ context.Context, p hooks.Payload) string {
		if p.UserInput == "" && p.AssistantReply == "" {
			return ""
		}
		content := strings.TrimSpace("User: " + p.UserInput + "\nAssistant: " + p.AssistantReply)
		// Strip <system-reminder> spans before storing so injected context
		// (recalled memories, background-task notes, goal context, …) never
		// lands in long-term memory and can't resurface via memory_recall.
		content = agent.StripSystemReminders(content)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = b.Store(ctx, content)
		}()
		return ""
	})

	if activeMemoryBackendAutoRecall {
		e.RegisterInProc(hooks.EventUserPromptSubmit, func(ctx context.Context, p hooks.Payload) string {
			if p.UserInput == "" {
				return ""
			}
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			results, err := b.Recall(ctx, p.UserInput)
			if err != nil || len(results) == 0 {
				return ""
			}
			var sb strings.Builder
			sb.WriteString("<system-reminder>\n")
			sb.WriteString("Relevant memories retrieved automatically for this message — no need to call " +
				"`memory_recall` again for this same question; only call it if you need to dig further " +
				"(a different angle, more results, or something this list doesn't cover):\n")
			for _, r := range results {
				// Recall may return content stored before the strip was added, or
				// externally indexed text carrying <system-reminder> spans — strip
				// them so they don't ride the automatic-injection path back in.
				sb.WriteString("- " + agent.StripSystemReminders(r.Content) + "\n")
			}
			sb.WriteString("</system-reminder>")
			return sb.String()
		})
	}
}
