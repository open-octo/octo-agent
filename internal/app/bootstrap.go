package app

import (
	"context"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/memorybackend"
	"github.com/open-octo/octo-agent/internal/tasks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// ToolEnv is the wired tool environment for a session: the executor the agent
// loop dispatches tool calls through, the manager that tracks async sub-agents
// (sub_agent), and a model-aware tool-list function. Call
// ToolsFor again after MCP connects so the list (or its Tool Search bridge)
// picks up the new surface. ToolsFor takes a ctx (server/cron callers pass the
// turn's ctx so tools.DefaultToolsForCtx can see that turn's ctx-scoped
// sub-agent manager, #1133); the CLI/TUI's single-session path can pass any
// ctx since it has no ctx-scoped manager and always resolves through the
// process-global slots this function also registers.
type ToolEnv struct {
	Executor    tools.DefaultRegistry
	SubAgentMgr *tools.SubAgentManager
	ToolsFor    func(ctx context.Context) []agent.ToolDefinition
}

// WireTools sets up the tool environment every full-loop entry point shares —
// the CLI today, the HTTP server and IM bridge as they migrate. It builds a
// read-before-write executor, registers the sub-agent spawner globally (so
// sub_agent appears in the catalog, which is why SetSpawner must run before
// any DefaultTools call), creates the async sub-agent manager, and — when
// enableTasks — installs the session task store.
//
// It returns the wired environment and a cleanup that resets the process-global
// registrations (spawner, task store); defer it for the session's lifetime. The
// caller still owns the agent's Sender, System prompt, knobs, gate, asker, and
// MCP connection strategy — those legitimately differ per entry point.
func WireTools(a *agent.Agent, enableTasks bool) (ToolEnv, func()) {
	executor := tools.NewDefaultRegistry()
	toolsFor := func(ctx context.Context) []agent.ToolDefinition { return tools.DefaultToolsForCtx(ctx, a.Model) }

	spawner := NewSpawner(a, executor, toolsFor)
	tools.SetSpawner(spawner)
	mgr := tools.NewSubAgentManager(spawner)
	tools.SetDefaultSubAgentManager(mgr)

	// LLM-backed distill (record_stop) + self-heal (run_skill) for browser skills.
	tools.SetBrowserSkillGenerator(MakeSkillGenerator(a.Sender, a.Model))
	tools.SetBrowserHealer(MakeBrowserHealer(a.Sender, a.Model))

	// Gate image content (browser screenshots) on the active model's vision
	// capability so a text-only model isn't handed images its endpoint rejects.
	cfg, cfgErr := config.Load()
	if cfgErr == nil {
		tools.SetBrowserVision(cfg.ModelVision(a.Model))
	}

	// Optional external semantic memory backend (hindsight/mem0/memos). A
	// bad Type/BaseURL just leaves it unconfigured rather than failing
	// session start — the user finds out on the first memory_recall call.
	if cfgErr == nil && cfg.MemoryBackendEnabled() {
		if b, err := memorybackend.New(memorybackend.Config{
			Type:      cfg.MemoryBackend.Type,
			BaseURL:   cfg.MemoryBackend.BaseURL,
			APIKey:    cfg.MemoryBackend.APIKey,
			Namespace: cfg.MemoryBackend.Namespace,
		}); err == nil {
			tools.SetMemoryBackend(b)
		}
	}

	cleanup := func() {
		tools.SetDefaultSubAgentManager(nil)
		tools.SetSpawner(nil)
		tools.SetBrowserSkillGenerator(nil)
		tools.SetBrowserHealer(nil)
		tools.SetBrowserVision(true)
		tools.ResetBrowserSession()
		tools.SetMemoryBackend(nil)
	}
	if enableTasks {
		tools.SetTaskStore(tasks.New())
		prev := cleanup
		cleanup = func() {
			prev()
			tools.SetTaskStore(nil)
		}
	}

	return ToolEnv{Executor: executor, SubAgentMgr: mgr, ToolsFor: toolsFor}, cleanup
}
