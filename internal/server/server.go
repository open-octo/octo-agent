// Package server provides the HTTP server for octo's REST API and Web UI.
//
// It is the M8 milestone implementation: a stateless HTTP layer over the
// existing agent/session/tool infrastructure. Each request carries its own
// context; sessions are loaded from / saved to disk on every turn.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/app"
	"github.com/Leihb/octo-agent/internal/channel"

	// The IM adapters self-register into the channel registry at init time.
	// The server is what runs them (startChannels) and looks them up by name
	// (channel.Find for task-notify pushes), so it owns the imports.
	_ "github.com/Leihb/octo-agent/internal/channel/adapters/dingtalk"
	_ "github.com/Leihb/octo-agent/internal/channel/adapters/discord"
	_ "github.com/Leihb/octo-agent/internal/channel/adapters/feishu"
	_ "github.com/Leihb/octo-agent/internal/channel/adapters/telegram"
	_ "github.com/Leihb/octo-agent/internal/channel/adapters/wecom"
	_ "github.com/Leihb/octo-agent/internal/channel/adapters/weixin"
	"github.com/Leihb/octo-agent/internal/config"
	"github.com/Leihb/octo-agent/internal/memory"
	"github.com/Leihb/octo-agent/internal/permission"
	"github.com/Leihb/octo-agent/internal/prompt"
	"github.com/Leihb/octo-agent/internal/scheduler"
	"github.com/Leihb/octo-agent/internal/skills"
	"github.com/Leihb/octo-agent/internal/tasks"
	"github.com/Leihb/octo-agent/internal/tools"
)

// getDefaultToolsFor bridges to tools.DefaultToolsFor for ws_handlers.go.
func getDefaultToolsFor(model string) []agent.ToolDefinition {
	return tools.DefaultToolsFor(model)
}

// Config holds server-level settings.
type Config struct {
	// Bind address (e.g. ":8080", "127.0.0.1:8080").
	Addr string

	// Provider and model override; zero values fall back to env/config.
	Provider string
	Model    string
	System   string

	// MaxTokens forwarded to the agent per turn.
	MaxTokens int

	// Tools enables the agentic tool loop.
	Tools bool

	// CORSOrigins lists allowed origins for CORS. Empty disables CORS.
	CORSOrigins []string

	// NoChannel disables IM channel (DingTalk, Feishu, etc.) startup.
	// When false (default), channels are started from ~/.octo/channels.yml
	// alongside the HTTP server.
	NoChannel bool

	// AccessKey is the shared secret used to authenticate Web UI and API
	// requests. When empty, the server looks for OCTO_ACCESS_KEY env var.
	// If still empty, a random key is generated and printed on startup.
	AccessKey string

	// NoMemory disables cross-session memory injection.
	NoMemory bool

	// UpdateCheck enables the outbound latest-release lookup behind
	// GET /api/version. Only `octo serve` sets it; every other Server
	// constructor (tests included) stays network-silent and the endpoint
	// degrades to "current is latest".
	UpdateCheck bool
}

// Server is the HTTP server skeleton. It owns the mux, the agent factory,
// and the in-memory turn lock (one turn per session at a time).
type Server struct {
	cfg  Config
	mux  *http.ServeMux
	http *http.Server

	// agent factory state (resolved once at start)
	sender   agent.Sender
	model    string
	provider string
	system   string
	skillReg *skills.Registry
	// skillsManifest is recomposed when skills are toggled/imported (write) and
	// read on every turn's prompt.Compose; skillsMu guards the two against a
	// data race between the mutation handlers and concurrent turns.
	skillsManifest string
	skillsMu       sync.RWMutex
	// cwd/envCtx are the server's working directory and its derived env-context
	// string. They are mutated by the working_dir handler while read on every
	// turn (prompt composition, permission engine, file/mcp handlers), so cwdMu
	// guards them — the same pattern as skillsManifest/skillsMu above.
	cwd        string
	envCtx     string
	cwdMu      sync.RWMutex
	memDir     string
	homeMemDir string

	// session-scoped turn locks: one turn per session at a time.
	turnLocks map[string]*sync.Mutex
	turnMu    sync.Mutex

	// session-scoped memory injectors: one injector per session so triggered
	// rules are recalled at most once per session. Deleted alongside turnLocks.
	sessionInjectors map[string]*memory.Injector
	injectorMu       sync.Mutex

	// turnRunning tracks which sessions have an active turn goroutine.
	// Guarded by the session's turnLocks mutex.
	turnRunning map[string]bool

	// entryBindings is a short-lease cache of the authoritative binding state
	// stored in each session file. It is only an optimisation within this
	// process: the session file's BoundEntry field is the single source of
	// truth, so a server restart or a concurrent CLI/TUI process always sees
	// the persisted value.
	entryBindings   map[string]*cachedEntryBinding
	entryBindingsMu sync.Mutex

	// sessionBindingLocks serialises the load→bind→save sequence for each
	// session inside this process, so two goroutines cannot both read an
	// unbound file and write conflicting bindings.
	sessionBindingLocks   map[string]*sync.Mutex
	sessionBindingLocksMu sync.Mutex

	// steerQueues holds mid-turn user messages (steer) that arrive while a
	// turn is in flight.  Consumed by the turn loop after each iteration.
	steerQueues map[string][]agent.InboxItem
	steerMu     sync.Mutex

	// sessionAgents tracks the currently-running Agent per session so that
	// mid-turn steer messages can be injected directly into its Inbox.
	sessionAgents   map[string]*agent.Agent
	sessionAgentsMu sync.Mutex

	// senderCache holds one sender per config entry name, built lazily for
	// sessions bound to a non-default model entry. Invalidated wholesale on
	// any /api/config/models mutation; the next turn rebuilds.
	senderCache   map[string]agent.Sender
	senderCacheMu sync.Mutex

	// titlePending guards one in-flight title generation per session
	// (lazily initialised by claimTitleGeneration).
	titlePending map[string]bool
	titleMu      sync.Mutex

	// WebSocket hub for real-time browser communication.
	wsHub *wsHub

	// interrupt cancellation per session.
	interrupts  map[string]context.CancelFunc
	interruptMu sync.Mutex

	// confirmation channels (from request_user_feedback in browser).
	confirmations map[string]chan string
	confirmMu     sync.Mutex

	// question channels (from request_user_question in browser).
	questionChans map[string]chan tools.AskResponse
	questionMu    sync.Mutex

	// pending interactive prompts per session, replayed on WS (re)subscribe so
	// a page refresh doesn't orphan an in-flight question or confirmation —
	// the original broadcast only reached the tabs connected at the time.
	pendingQuestions map[string]wsEventRequestUserQuestion
	pendingConfirms  map[string]wsEventRequestConfirmation
	pendingPromptMu  sync.Mutex

	// live state tracking per session for WS replay on subscribe.
	liveStates  map[string]*sessionLiveState
	liveStateMu sync.RWMutex

	// scheduler for cron-based background tasks. schedulerMu serialises its
	// lazy initialisation (handlers and server start may race).
	scheduler   *scheduler.Scheduler
	schedulerMu sync.Mutex

	// channel manager for IM platform bridging (DingTalk, Feishu, etc.).
	// nil when NoChannel is set or no channels are configured.
	channelCfg      *channel.Config
	channelMgr      *channel.Manager
	channelCancel   context.CancelFunc
	runningAdapters sync.Map // string -> channel.Adapter

	// weixinLogin tracks the in-flight web QR login flow (one at a time).
	weixinLogin weixinLoginFlow

	// mcpCleanup unregisters + closes the MCP registry connected at start.
	// Always non-nil after New (a no-op when no servers connected).
	mcpCleanup func()

	// mcpMu serialises MCP config mutations + registry swaps from the web
	// management API (and Shutdown) — SwapMCP must not run concurrently
	// with itself or with incremental connects. Also guards mcpOAuthFlows.
	mcpMu sync.Mutex

	// mcpOAuthFlows tracks in-flight web device-flow authorizations by
	// server name. Lazily initialised by the oauth/start handler.
	mcpOAuthFlows map[string]*mcpOAuthFlow

	// accessKey is the shared secret for Web UI / API authentication.
	accessKey string

	// apiRoutes records every pattern registered through api(), so the
	// route-coverage test can assert each one rejects keyless non-loopback
	// requests.
	apiRoutes []string

	// Latest-release cache behind GET /api/version (see latestVersion).
	versionCheckMu   sync.Mutex
	versionLatest    string
	versionCheckedAt time.Time
	versionFailedAt  time.Time

	// upgradeRunning single-flights POST /api/version/upgrade.
	upgradeMu      sync.Mutex
	upgradeRunning bool

	// restartPending is set by Restart and read after the listener closes to
	// distinguish a restart-driven shutdown (exit ExitRestart) from a plain
	// one (exit 0).
	restartPending atomic.Bool

	// shutdownMu/shutdownDone/shutdownErr make Shutdown single-flight: the
	// first caller runs it, later callers wait on shutdownDone and read
	// shutdownErr. All zero-valued until the first Shutdown call.
	shutdownMu   sync.Mutex
	shutdownDone chan struct{}
	shutdownErr  error

	// drain gates every turn execution path during a restart. drainTimeout
	// overrides the 30s default in tests; zero means the default.
	drain        drainGate
	drainTimeout time.Duration

	// rememberedStores holds per-session "always allow" permission
	// decisions. Engines are rebuilt every turn (to pick up policy/mode
	// changes), so the decisions live here and attach to each fresh engine.
	// Keyed by web session ID or "im:<session key>". Guarded by rememberedMu.
	rememberedStores map[string]*permission.Remembered
	rememberedMu     sync.Mutex

	// senderMu serialises lazy initialisation of sender when the server starts
	// in onboarding mode (API key missing) and the user completes setup later.
	senderMu sync.Mutex
}

// New builds a Server. It resolves provider/model, discovers skills, and
// resolves the access key that gates non-loopback requests.
func New(cfg Config) (*Server, error) {
	sender, model, provName, err := resolveProviderAndModel(cfg.Provider, cfg.Model)
	if err != nil {
		return nil, err
	}

	cwd, _ := os.Getwd()
	envCtx := buildEnvContext(cwd)

	skillReg := skills.Discover(cwd)
	fileCfg, _ := config.Load()
	skillReg.SetDisabled(fileCfg.Tools.DisabledSkills)
	skillsManifest := skills.RenderManifest(skillReg)
	tools.SetSkills(skillReg)

	accessKey, generatedKey := resolveAccessKey(cfg.AccessKey, fileCfg)
	if generatedKey {
		// Persist so the key survives restarts — in particular the
		// self-restart supervisor's worker respawns, which would otherwise
		// log every browser out. On save failure the in-memory key still
		// works for this process lifetime.
		fileCfg.AccessKey = accessKey
		if err := fileCfg.Save(); err != nil {
			slog.Warn("could not persist access key; a new one will be generated next start", "err", err)
		}
	}

	// Resolve cross-session memory directory.
	var memDir, homeMemDir string
	if !cfg.NoMemory {
		if d, err := memory.Dir(memory.ProjectRoot(cwd)); err == nil {
			if memory.EnsureDir(d) == nil {
				memDir = d
			}
		}
		if d, err := memory.HomeDir(); err == nil {
			if memory.EnsureDir(d) == nil {
				homeMemDir = d
			}
		}
	}

	s := &Server{
		cfg:                 cfg,
		mux:                 http.NewServeMux(),
		sender:              sender,
		model:               model,
		provider:            provName,
		system:              cfg.System,
		skillReg:            skillReg,
		skillsManifest:      skillsManifest,
		cwd:                 cwd,
		envCtx:              envCtx,
		memDir:              memDir,
		homeMemDir:          homeMemDir,
		turnLocks:           map[string]*sync.Mutex{},
		turnRunning:         make(map[string]bool),
		entryBindings:       make(map[string]*cachedEntryBinding),
		sessionBindingLocks: map[string]*sync.Mutex{},
		steerQueues:         make(map[string][]agent.InboxItem),
		sessionAgents:       make(map[string]*agent.Agent),
		accessKey:           accessKey,
		confirmations:       make(map[string]chan string),
		questionChans:       make(map[string]chan tools.AskResponse),
		pendingQuestions:    make(map[string]wsEventRequestUserQuestion),
		pendingConfirms:     make(map[string]wsEventRequestConfirmation),
		sessionInjectors:    make(map[string]*memory.Injector),
	}

	// Register the WebSocket-backed asker so ask_user_question appears in the
	// tool catalog and can be dispatched through the browser.
	tools.SetAsker(s.wsAsker())

	// Register the restarter so restart_server appears in the tool catalog —
	// the agent can then honour "重启一下服务" from web or IM. Permission is
	// ask-class (defaults.yml): the web UI confirms via the browser prompt,
	// IM via the in-chat reply prompt. The restart drains in-flight turns
	// (including the one calling the tool) before the worker exits with
	// ExitRestart for the supervisor to respawn.
	tools.SetRestarter(s.Restart)

	s.registerRoutes()
	s.initWS()
	if sender != nil {
		s.enableSubAgentTools()
	}
	s.enableMCP()
	s.initChannels()

	s.http = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.corsMiddleware(s.mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE streams are long-lived
		IdleTimeout:  120 * time.Second,
	}

	return s, nil
}

// enableSubAgentTools registers the process-global sub-agent manager + task
// store so DefaultToolsFor advertises sub_agent / task_* . On
// the server these globals are gating sentinels and a never-hit fallback: every
// turn stamps its own ctx-scoped manager + store (bound to that turn's agent)
// via prepareToolTurn, and dispatch prefers the ctx-scoped ones — so concurrent
// sessions never share sub-agent or task state. The template agent only seeds
// the sentinel spawner. No-op when tools are disabled.
func (s *Server) enableSubAgentTools() {
	if !s.cfg.Tools {
		return
	}
	defaultSender, model := s.defaultSenderAndModel()
	template := agent.New(defaultSender, model)
	var memInjection string
	if s.memDir != "" {
		memInjection = memory.RenderInjection(s.memDir, s.homeMemDir)
	}
	cwd, envCtx := s.curCwdEnv()
	template.System, template.LeanSystem = prompt.ComposePair(s.system, cwd, envCtx, s.curSkillsManifest(), memInjection, true)
	executor := tools.NewDefaultRegistry()
	spawner := app.NewSpawner(template, executor, func() []agent.ToolDefinition {
		return tools.DefaultToolsFor(s.model)
	})
	mgr := tools.NewSubAgentManager(spawner)
	mgr.SetSynchronous(true)
	tools.SetDefaultSubAgentManager(mgr)
	tools.SetTaskStore(tasks.New())
}

// enableMCP applies the Tool Search config and connects the configured MCP
// servers non-interactively, registering their surface so DefaultToolsFor
// advertises them (or the Tool Search bridge). Best-effort: a misconfigured or
// unreachable server is logged and skipped. No-op when tools are disabled.
// mcpCleanup is always set (a no-op when nothing connected) so Shutdown can call
// it unconditionally.
func (s *Server) enableMCP() {
	s.mcpCleanup = func() {}
	if !s.cfg.Tools {
		return
	}
	if cfg, err := config.Load(); err == nil {
		tools.SetToolSearchConfig(app.ToolSearchConfigFrom(cfg.Tools.ToolSearch))
	}
	// Route stdio MCP subprocess stderr into structured logging (debug level)
	// so a child's chatter doesn't interleave with the server's own output.
	app.SetMCPChildStderr(newMCPStderrWriter(slog.Default()))
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if err := app.SwapMCP(context.Background(), s.curCwd(), os.Stderr); err != nil {
		slog.Error("mcp setup", "err", err)
	}
	s.mcpCleanup = func() {
		s.mcpMu.Lock()
		defer s.mcpMu.Unlock()
		app.ShutdownMCP()
	}
}

// ListenAndServe starts the HTTP server. The cron scheduler is armed before
// serving so persisted tasks fire from server start, not from the first hit
// on a task endpoint.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	return s.serveOn(ln)
}

// serveOn runs the server on an already-bound listener (split from
// ListenAndServe so tests can use an ephemeral port). A stop via Shutdown is
// reported as nil — it is the expected end of a server's life, not an error —
// unless a restart was requested, in which case ErrRestartRequested tells the
// caller to exit with ExitRestart.
func (s *Server) serveOn(ln net.Listener) error {
	s.initScheduler()
	s.startChannels()
	err := s.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		if s.restartPending.Load() {
			return ErrRestartRequested
		}
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the server, releasing the process-global
// sub-agent + task registrations installed by enableSubAgentTools.
//
// It is single-flight: Restart's background shutdown and the Ctrl-C signal
// path can race here, and the body mutates process-global registries that
// must not be torn down twice concurrently. The first caller runs the
// shutdown; later callers wait for it to finish (or their ctx to expire)
// and return the same error.
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownMu.Lock()
	if s.shutdownDone == nil {
		done := make(chan struct{})
		s.shutdownDone = done
		s.shutdownMu.Unlock()

		err := s.doShutdown(ctx)

		s.shutdownMu.Lock()
		s.shutdownErr = err
		s.shutdownMu.Unlock()
		close(done)
		return err
	}
	done := s.shutdownDone
	s.shutdownMu.Unlock()

	select {
	case <-done:
		s.shutdownMu.Lock()
		defer s.shutdownMu.Unlock()
		return s.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) doShutdown(ctx context.Context) error {
	s.stopChannels()
	// Kill background processes started via web/IM sessions so they don't
	// outlive the daemon — the same orphan-prevention the CLI/TUI do on exit.
	// terminal background commands run through the process-global manager
	// regardless of which session launched them, so one call covers all.
	tools.KillAllBackground()
	tools.KillAllSessionSubAgents()
	tools.SetDefaultSubAgentManager(nil)
	tools.SetTaskStore(nil)
	if s.mcpCleanup != nil {
		s.mcpCleanup()
	}
	return s.http.Shutdown(ctx)
}

// api registers an authenticated route. The requireAuth wrapper is applied
// here, in one place, so a new route cannot forget it; /api/health,
// /api/version, and static files are the only handlers registered directly
// on the mux.
func (s *Server) api(pattern string, h http.HandlerFunc) {
	s.apiRoutes = append(s.apiRoutes, pattern)
	s.mux.HandleFunc(pattern, s.requireAuth(h))
}

// registerRoutes wires all handlers. API and WS routes require auth.
func (s *Server) registerRoutes() {
	s.api("POST /api/chat", s.handleCreateChat)
	s.api("POST /api/chat/{id}/turn", s.handleTurnOrSSE)
	s.api("GET /api/sessions", s.handleListSessions)
	s.api("POST /api/sessions", s.handleCreateSession)
	s.api("POST /api/sessions/delete", s.handleDeleteSessions)
	s.api("GET /api/sessions/{id}", s.handleGetSession)
	s.api("GET /api/sessions/{id}/messages", s.handleGetSessionMessages)
	s.api("GET /api/sessions/{id}/artifacts", s.handleGetArtifact)
	s.api("DELETE /api/sessions/{id}", s.handleDeleteSession)
	s.api("PATCH /api/sessions/{id}", s.handleUpdateSession)
	s.api("PATCH /api/sessions/{id}/model", s.handleUpdateSessionModel)
	s.api("PATCH /api/sessions/{id}/reasoning_effort", s.handleUpdateSessionReasoningEffort)
	s.api("PATCH /api/sessions/{id}/show_reasoning", s.handleUpdateSessionShowReasoning)
	s.api("PATCH /api/sessions/{id}/working_dir", s.handleUpdateSessionWorkingDir)
	s.api("GET /api/tools", s.handleListTools)
	s.api("GET /api/skills", s.handleListSkills)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/version", s.handleVersion)
	s.api("GET /api/channels", s.handleListChannels)
	s.api("GET /api/channels/available", s.handleAvailableChannels)
	s.api("GET /api/channels/{platform}", s.handleGetChannel)
	s.api("POST /api/channels/{platform}", s.handleSaveChannel)
	s.api("DELETE /api/channels/{platform}", s.handleDeleteChannel)
	s.api("POST /api/channels/{platform}/test", s.handleTestChannel)
	s.api("POST /api/channels/weixin/login", s.handleWeixinLoginStart)
	s.api("GET /api/channels/weixin/login", s.handleWeixinLoginStatus)
	s.api("DELETE /api/channels/weixin/login", s.handleWeixinLoginCancel)
	s.api("GET /api/tasks", s.handleListTasks)
	s.api("POST /api/tasks", s.handleCreateTask)
	s.api("DELETE /api/tasks/{id}", s.handleDeleteTask)
	s.api("POST /api/tasks/{id}/run", s.handleRunTask)
	s.api("PATCH /api/tasks/{id}/toggle", s.handleToggleTask)
	s.api("GET /api/profile/soul", s.handleGetProfileSoul)
	s.api("GET /api/profile/user", s.handleGetProfileUser)
	s.api("GET /api/memories", s.handleGetMemories)
	s.api("GET /api/trash", s.handleGetTrash)
	s.api("POST /api/trash/empty", s.handleEmptyTrash)
	s.api("POST /api/trash/{id}/restore", s.handleRestoreTrash)
	s.api("DELETE /api/trash/{id}", s.handleDeleteTrash)

	// Onboard & config
	s.api("GET /api/onboard/status", s.handleOnboardStatus)
	s.api("POST /api/onboard/complete", s.handleOnboardComplete)
	s.api("GET /api/providers", s.handleListProviders)
	s.api("GET /api/config", s.handleGetConfig)
	s.api("PUT /api/config/show_reasoning", s.handlePutShowReasoning)
	s.api("POST /api/config/test", s.handleTestConfig)
	s.api("POST /api/config/models", s.handleSaveModelConfig)
	s.api("PATCH /api/config/models/{id}", s.handleUpdateModelConfig)
	s.api("DELETE /api/config/models/{id}", s.handleDeleteModelConfig)
	s.api("POST /api/config/models/{id}/default", s.handleSetDefaultModelConfig)
	s.api("POST /api/config/models/{id}/lite", s.handleSetLiteModelConfig)

	// Upload
	s.api("POST /api/upload", s.handleUpload)
	s.api("GET /api/uploads/{name}", s.handleGetUpload)

	// File action (open / download)
	s.api("POST /api/file-action", s.handleFileAction)

	// Skill toggle & delete
	s.api("PATCH /api/skills/{name}/toggle", s.handleToggleSkill)
	s.api("DELETE /api/skills/{name}", s.handleDeleteSkill)
	s.api("POST /api/skills/import", s.handleImportSkill)
	s.api("GET /api/skills/{name}/export", s.handleExportSkill)

	// Protean skill bridge
	s.api("GET /api/protean/info", s.handleProteanInfo)
	s.api("GET /api/protean/skills", s.handleProteanListSkills)
	s.api("POST /api/protean/record/start", s.handleProteanRecordStart)
	s.api("POST /api/protean/record/stop", s.handleProteanRecordStop)
	s.api("POST /api/protean/generate", s.handleProteanGenerate)
	s.api("POST /api/protean/run", s.handleProteanRun)

	// MCP server management
	s.api("GET /api/mcp/servers", s.handleListMCPServers)
	s.api("GET /api/mcp/servers/{name}", s.handleGetMCPServer)
	s.api("POST /api/mcp/servers", s.handleCreateMCPServer)
	s.api("PATCH /api/mcp/servers/{name}", s.handleUpdateMCPServer)
	s.api("DELETE /api/mcp/servers/{name}", s.handleDeleteMCPServer)
	s.api("PATCH /api/mcp/servers/{name}/toggle", s.handleToggleMCPServer)
	s.api("POST /api/mcp/servers/{name}/reconnect", s.handleReconnectMCPServer)
	s.api("POST /api/mcp/servers/{name}/oauth/start", s.handleStartMCPOAuth)
	s.api("GET /api/mcp/servers/{name}/oauth/status", s.handleMCPOAuthStatus)
	s.api("POST /api/mcp/reload", s.handleReloadMCP)
	s.api("GET /api/config/toolsearch", s.handleGetToolSearch)
	s.api("PUT /api/config/toolsearch", s.handlePutToolSearch)

	// Benchmark
	s.api("POST /api/sessions/{id}/benchmark", s.handleBenchmark)

	// Memory detail
	s.api("GET /api/memories/{filename}", s.handleGetMemory)
	s.api("DELETE /api/memories/{filename}", s.handleDeleteMemory)

	// Cron tasks (alias for scheduler tasks)
	s.api("GET /api/cron-tasks", s.handleListCronTasks)
	s.api("POST /api/cron-tasks/{name}/run", s.handleRunCronTask)
	s.api("PATCH /api/cron-tasks/{name}", s.handlePatchCronTask)

	// Version & restart
	s.api("POST /api/version/upgrade", s.handleVersionUpgrade)
	s.api("POST /api/restart", s.handleRestart)

	s.api("GET /ws", s.handleWS)

	// Static files (Web UI) — served from embedded filesystem.
	s.mux.Handle("/", s.staticHandler())
}

// corsMiddleware wraps the mux with CORS headers when configured.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	if len(s.cfg.CORSOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		wildcard := false
		for _, o := range s.cfg.CORSOrigins {
			if o == "*" {
				wildcard = true
				allowed = true
			} else if o == origin {
				allowed = true
			}
			if allowed {
				break
			}
		}
		if allowed {
			// Wildcard '*' must never be reflected as the concrete Origin when
			// credentials-related headers (Authorization, X-Access-Key) are
			// allowed; that would let any origin make authenticated requests.
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Access-Key")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// sessionTurnLock returns the mutex for a given session ID, creating it if
// needed. This serialises turns per session so concurrent POSTs to the same
// session don't interleave.
func (s *Server) sessionTurnLock(id string) *sync.Mutex {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if mu, ok := s.turnLocks[id]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	s.turnLocks[id] = mu
	return mu
}

// forgetTurnLock drops a session's turn mutex after the session is deleted, so
// the map doesn't accumulate locks for sessions that no longer exist.
func (s *Server) forgetTurnLock(id string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	delete(s.turnLocks, id)

	s.injectorMu.Lock()
	delete(s.sessionInjectors, id)
	s.injectorMu.Unlock()

	s.rememberedMu.Lock()
	delete(s.rememberedStores, id)
	s.rememberedMu.Unlock()

	s.entryBindingsMu.Lock()
	delete(s.entryBindings, id)
	s.entryBindingsMu.Unlock()
}

// cachedEntryBinding is a short-lease, in-process cache of a session's binding.
// The session file is the single source of truth; this cache only avoids
// reloading the file on every turn within one server process.
type cachedEntryBinding struct {
	entry   string
	expires time.Time
}

// entryBindingCacheLease is how long the in-process cache remains valid without
// being renewed. The on-disk lease is written for entryBindingLease (longer) so
// a cached owner can keep working without touching the file on every request.
const entryBindingCacheLease = 30 * time.Second

// entryBindingLease is how long a turn lease written to the session file
// remains authoritative. It must be long enough to cover most turns, short
// enough that a crashed process releases the binding quickly.
const entryBindingLease = 2 * time.Minute

// loadBoundSession reloads the session from disk and returns its authoritative
// BoundEntry. The returned session is always a fresh object.
func (s *Server) loadBoundSession(id string) (*agent.Session, error) {
	return agent.LoadSession(id)
}

// sessionBindingLock returns the mutex that serialises load→bind→save for id.
func (s *Server) sessionBindingLock(id string) *sync.Mutex {
	s.sessionBindingLocksMu.Lock()
	defer s.sessionBindingLocksMu.Unlock()
	if mu, ok := s.sessionBindingLocks[id]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	s.sessionBindingLocks[id] = mu
	return mu
}

// acquireSessionBinding claims the session for entry. It always reloads the
// session from disk first so the session file is the single source of truth.
// A local cache with a short lease avoids repeated disk reads for the same
// owner within this process. On success the binding and a turn lease are
// persisted, and the cache is refreshed. The load→bind→save sequence is
// serialised per session id.
func (s *Server) acquireSessionBinding(id, entry string, steal bool) (bool, string, error) {
	mu := s.sessionBindingLock(id)
	mu.Lock()
	defer mu.Unlock()

	s.entryBindingsMu.Lock()
	cache := s.entryBindings[id]
	s.entryBindingsMu.Unlock()

	now := time.Now()
	if cache != nil && cache.entry == entry && cache.expires.After(now) {
		// This process already owns an unexpired cache entry; just renew it.
		s.entryBindingsMu.Lock()
		s.entryBindings[id] = &cachedEntryBinding{entry: entry, expires: now.Add(entryBindingCacheLease)}
		s.entryBindingsMu.Unlock()
		return true, "", nil
	}

	// Authoritative check: reload from disk.
	sess, err := s.loadBoundSession(id)
	if err != nil {
		return false, "", err
	}

	bound := sess.BoundEntry
	if bound == "" || bound == entry {
		// Own it. Write back immediately so other processes see it.
		sess.Bind(entry, false)
		if err := sess.Save(); err != nil {
			return false, "", fmt.Errorf("persist binding: %w", err)
		}
		if err := sess.WriteLease(entry, now.Add(entryBindingLease)); err != nil {
			return false, "", fmt.Errorf("write lease: %w", err)
		}
		s.entryBindingsMu.Lock()
		s.entryBindings[id] = &cachedEntryBinding{entry: entry, expires: now.Add(entryBindingCacheLease)}
		s.entryBindingsMu.Unlock()
		return true, "", nil
	}

	if !steal {
		return false, "", fmt.Errorf("session is bound to %s since %s; close the other entry first", bound, sess.BoundAt.Format(time.RFC3339))
	}

	// Respect the in-memory cache when we have seen this binding recently; the
	// authoritative owner in another process may still be alive.
	if cache != nil && cache.entry == bound && cache.expires.After(now) {
		return false, "", fmt.Errorf("session is bound to %s and the local lease has not expired; cannot take over yet", bound)
	}
	// Respect the persisted turn lease: if another entry holds an unexpired
	// lease, refuse to steal even if our cache has expired.
	if holder, active := sess.LeaseActive(); active && holder != entry {
		return false, "", fmt.Errorf("session is bound to %s and a turn lease is active until %s; cannot take over while busy", bound, sess.LeaseExpires.Format(time.RFC3339))
	}

	sess.Bind(entry, true)
	if err := sess.Save(); err != nil {
		return false, "", fmt.Errorf("persist stolen binding: %w", err)
	}
	if err := sess.WriteLease(entry, now.Add(entryBindingLease)); err != nil {
		return false, "", fmt.Errorf("write stolen lease: %w", err)
	}
	s.entryBindingsMu.Lock()
	s.entryBindings[id] = &cachedEntryBinding{entry: entry, expires: now.Add(entryBindingCacheLease)}
	s.entryBindingsMu.Unlock()
	return true, fmt.Sprintf("session taken over from %s", bound), nil
}

// canForceBind reports whether a failed binding acquisition is recoverable
// by a force takeover. It is true when another entry owns the session but no
// turn lease is active, matching the /bind --force semantics for IM.
func (s *Server) canForceBind(id string, berr error) bool {
	if berr == nil {
		return false
	}
	sess, err := agent.LoadSession(id)
	if err != nil {
		return false
	}
	if sess.BoundEntry == "" || sess.BoundEntry == agent.EntryWeb {
		return false
	}
	_, active := sess.LeaseActive()
	return !active
}

// releaseSessionBinding clears the binding when this process owns it. It
// reloads the session from disk first to avoid clobbering a binding set by
// another process after the turn started. The turn lease is also cleared.
func (s *Server) releaseSessionBinding(id, entry string) {
	mu := s.sessionBindingLock(id)
	mu.Lock()
	defer mu.Unlock()

	sess, err := s.loadBoundSession(id)
	if err != nil {
		return
	}
	if sess.BoundEntry != entry {
		return
	}
	_ = sess.ClearLease()
	sess.Unbind(entry)
	_ = sess.Save()

	s.entryBindingsMu.Lock()
	delete(s.entryBindings, id)
	s.entryBindingsMu.Unlock()
}

// resolveUnderCWD joins path with cwd and checks that the resulting absolute
// path is inside cwd. It rejects absolute paths outside cwd, paths containing
// ".." that escape, and empty inputs. Returns (absPath, true) on success.
func resolveUnderCWD(cwd, path string) (string, bool) {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if cwd == "" || path == "" {
		return "", false
	}
	// Reject absolute paths outright; only relative paths under cwd are allowed.
	// The explicit slash/tilde/drive checks catch Unix-style paths on Windows
	// (where filepath.IsAbs("/etc/passwd") is false).
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") || strings.HasPrefix(path, "~") || (len(path) >= 2 && path[1] == ':') {
		return "", false
	}
	abs, err := filepath.Abs(filepath.Join(cwd, path))
	if err != nil {
		return "", false
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	// Ensure abs has the same volume/prefix as root and is not outside it.
	if !strings.HasPrefix(abs, root) || (len(abs) > len(root) && abs[len(root)] != filepath.Separator) {
		return "", false
	}
	return abs, true
}

// buildAgent creates a fresh agent for a turn. The caller must have locked
// the session's turn mutex.
func (s *Server) buildAgent(sess *agent.Session) *agent.Agent {
	sender, model := s.senderForSession(sess)
	a := agent.New(sender, model)
	cwd, envCtx := s.curCwdEnv()
	a.CWD = cwd
	a.MaxTokens = s.cfg.MaxTokens
	if dir, err := sess.ChunkDir(); err == nil {
		a.ArchiveDir = dir // recall folded turns via the read tool
	}
	if cfg, err := config.Load(); err == nil {
		a.LiteSender, a.LiteModel = s.liteSenderFromConfig(cfg)
		if a.LiteSender == nil {
			// No explicit lite entry — fall back to the vendor's registry
			// lite model on the session's OWN sender, keeping compaction on
			// the endpoint, key, and prompt cache the conversation uses.
			prov, baseURL := s.getProvider(), resolveBaseURL(s.getProvider(), cfg)
			if entry, ok := cfg.EntryByName(sess.ModelConfig); ok {
				prov, baseURL = entry.Provider, entry.BaseURL
			}
			if lm := app.ImplicitLiteModel(prov, a.Model, baseURL); lm != "" {
				a.LiteSender = sender
				a.LiteModel = lm
			}
		}
	}

	// L1: project memory embedded in the system prompt (stable across turns).
	var memInjection string
	if s.memDir != "" {
		memInjection = memory.RenderInjection(s.memDir, s.homeMemDir)
	}
	a.System, a.LeanSystem = prompt.ComposePair(s.system, cwd, envCtx, s.curSkillsManifest(), memInjection, true)

	// L2: attention-layer rules injected per user turn (triggered keywords),
	// plus the save-nudge appended to milestone tool results.
	if s.memDir != "" {
		inj := s.injectorFor(sess.ID)
		a.UserInputHook = inj.Reminder
		a.ToolResultHook = inj.SaveNudge
	}

	if len(sess.Messages) > 0 {
		a.History = sess.ToHistory()
	}
	return a
}

// injectorFor returns the session's memory injector, creating it on first
// use. The injector carries the once-per-session recall latch, so it must
// survive turns; it is created even when MEMORY.md has no structured rules —
// Reminder is silent then, but the save-nudge still needs the latch. Keyed
// by web session ID or "im:<session key>"; dropped via forgetTurnLock (web)
// or /unbind //bind (IM).
func (s *Server) injectorFor(key string) *memory.Injector {
	s.injectorMu.Lock()
	defer s.injectorMu.Unlock()
	if s.sessionInjectors == nil {
		s.sessionInjectors = make(map[string]*memory.Injector)
	}
	inj, ok := s.sessionInjectors[key]
	if !ok {
		rules := memory.ParseRules(s.memDir)
		if s.homeMemDir != "" {
			rules.Merge(memory.ParseRules(s.homeMemDir))
		}
		inj = memory.NewInjector(rules)
		s.sessionInjectors[key] = inj
	}
	return inj
}

// writeJSON is a convenience helper for JSON responses.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a structured error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readBodyJSON reads and unmarshals the request body.
func readBodyJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// ─── Provider resolution ────────────────────────────────────────────────────
//
// The server resolves provider/model/key/base-URL exactly as before, then hands
// the result to app.NewSender — internal/app is the single place that builds the
// vendor client, so the server no longer imports internal/provider.

func resolveProviderAndModel(flagProvider, flagModel string) (agent.Sender, string, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", "", fmt.Errorf("load config: %w", err)
	}

	entry := cfg.DefaultEntry()
	provName := firstNonEmpty(flagProvider, os.Getenv("OCTO_PROVIDER"), entry.Provider, "anthropic")
	model := flagModel
	if model == "" {
		model = modelFromEnv(provName)
	}
	if model == "" && provName == entry.Provider {
		model = entry.Model
	}
	if model == "" {
		model = defaultModelFor(provName)
	}
	if model == "" {
		return nil, "", "", fmt.Errorf("unknown provider %q", provName)
	}

	apiKey, err := resolveAPIKey(provName, cfg)
	if err != nil {
		return nil, "", "", err
	}
	if apiKey == "" {
		// No API key configured yet — server starts in onboarding mode.
		// Chat endpoints will retry on every request until the user completes
		// setup via the Web UI.
		return nil, model, provName, nil
	}
	sender, err := app.NewSender(app.SenderOptions{
		Provider:        provName,
		APIKey:          apiKey,
		BaseURL:         resolveBaseURL(provName, cfg),
		ReasoningEffort: entry.ReasoningEffort,
		ShowReasoning:   cfg.EffectiveShowReasoning(entry.ShowReasoning),
	})
	if err != nil {
		return nil, "", "", err
	}
	return sender, model, provName, nil
}

// resolveAPIKey returns the API key for the vendor: the provider's env var,
// else the default entry's stored key when it targets the same provider. An
// empty key is returned (not an error) so the server can start in onboarding
// mode and retry once the user completes setup via the Web UI.
func resolveAPIKey(name string, cfg config.Config) (string, error) {
	if !app.IsKnownVendor(name) {
		return "", fmt.Errorf("unknown provider %q", name)
	}
	envVar := app.VendorAPIKeyEnvVar(name)
	apiKey := os.Getenv(envVar)
	if entry := cfg.DefaultEntry(); apiKey == "" && entry.Provider == name {
		apiKey = entry.APIKey
	}
	return apiKey, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func modelFromEnv(provider string) string {
	envVar := strings.ToUpper(provider) + "_MODEL"
	return os.Getenv(envVar)
}

func defaultModelFor(provider string) string {
	return app.VendorDefaultModel(provider)
}

func resolveBaseURL(provider string, cfg config.Config) string {
	if entry := cfg.DefaultEntry(); entry.Provider == provider {
		return entry.BaseURL
	}
	return ""
}

// defaultSenderAndModel returns the server's current default sender and its
// model under senderMu. Callers that need both should use this to avoid
// observing a torn snapshot while the sender is being reloaded.
func (s *Server) defaultSenderAndModel() (agent.Sender, string) {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	return s.sender, s.model
}

// senderForSession resolves the (sender, model) a turn should run on. A
// session bound to a config entry (ModelConfig) gets that entry's sender from
// the cache, built on first use; everything else — unbound sessions, a
// missing entry (deleted since binding), or a build failure — falls back to
// the server's default sender so a stale binding degrades instead of
// breaking the turn.
func (s *Server) senderForSession(sess *agent.Session) (agent.Sender, string) {
	defaultSender, model := s.defaultSenderAndModel()
	if sess.Model != "" {
		model = sess.Model
	}
	if sess.ModelConfig == "" {
		return defaultSender, model
	}

	cfg, err := config.Load()
	if err != nil {
		return defaultSender, model
	}
	entry, ok := cfg.EntryByName(sess.ModelConfig)
	if !ok {
		return defaultSender, model
	}
	if entry.Model != "" {
		model = entry.Model
	}

	sender, err := s.cachedSenderForEntry(entry)
	if err != nil {
		return defaultSender, model
	}
	return sender, model
}

// cachedSenderForEntry returns the entry's sender from the cache, building
// and caching it on first use.
func (s *Server) cachedSenderForEntry(entry config.ModelEntry) (agent.Sender, error) {
	s.senderCacheMu.Lock()
	defer s.senderCacheMu.Unlock()
	if cached, ok := s.senderCache[entry.Name]; ok {
		return cached, nil
	}
	sender, err := senderForEntry(entry)
	if err != nil {
		return nil, err
	}
	if s.senderCache == nil {
		s.senderCache = make(map[string]agent.Sender)
	}
	s.senderCache[entry.Name] = sender
	return sender, nil
}

// liteSenderFromConfig resolves the configured lite entry to a (sender,
// model) pair for compaction, or (nil, "") when none is configured or it
// can't be built — the agent then compacts on its primary sender.
func (s *Server) liteSenderFromConfig(cfg config.Config) (agent.Sender, string) {
	entry, ok := cfg.EntryByName(cfg.LiteModel)
	if !ok || entry.Model == "" {
		return nil, ""
	}
	sender, err := s.cachedSenderForEntry(entry)
	if err != nil {
		return nil, ""
	}
	return sender, entry.Model
}

// senderForEntry builds a sender from one config entry: env key first (same
// rule as the default path), then the entry's stored key.
func senderForEntry(entry config.ModelEntry) (agent.Sender, error) {
	apiKey := os.Getenv(app.VendorAPIKeyEnvVar(entry.Provider))
	if apiKey == "" {
		apiKey = entry.APIKey
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key for entry %q (provider %q)", entry.Name, entry.Provider)
	}
	cfg, _ := config.Load()
	return app.NewSender(app.SenderOptions{
		Provider:        entry.Provider,
		APIKey:          apiKey,
		BaseURL:         entry.BaseURL,
		ReasoningEffort: entry.ReasoningEffort,
		ShowReasoning:   cfg.EffectiveShowReasoning(entry.ShowReasoning),
	})
}

// invalidateSenderCache drops every cached per-entry sender. Called on any
// model-config mutation so edited credentials/endpoints take effect on the
// next turn.
func (s *Server) invalidateSenderCache() {
	s.senderCacheMu.Lock()
	s.senderCache = nil
	s.senderCacheMu.Unlock()
}

// curSkillsManifest returns the current skills manifest under a read lock.
func (s *Server) curSkillsManifest() string {
	s.skillsMu.RLock()
	defer s.skillsMu.RUnlock()
	return s.skillsManifest
}

// setSkillsManifest replaces the skills manifest under a write lock. Called by
// the skill toggle/import handlers when the enabled set changes.
func (s *Server) setSkillsManifest(m string) {
	s.skillsMu.Lock()
	s.skillsManifest = m
	s.skillsMu.Unlock()
}

// curCwd returns the server's working directory under a read lock.
func (s *Server) curCwd() string {
	s.cwdMu.RLock()
	defer s.cwdMu.RUnlock()
	return s.cwd
}

// curCwdEnv returns the working directory and its env-context string atomically
// under a single read lock, so callers that need both (prompt composition)
// never observe a torn pair across a concurrent setCwd.
func (s *Server) curCwdEnv() (string, string) {
	s.cwdMu.RLock()
	defer s.cwdMu.RUnlock()
	return s.cwd, s.envCtx
}

// setCwd replaces the working directory and recomputes its env context under a
// write lock. Called by the working_dir handler.
func (s *Server) setCwd(dir string) {
	s.cwdMu.Lock()
	s.cwd = dir
	s.envCtx = buildEnvContext(dir)
	s.cwdMu.Unlock()
}

// buildEnvContext mirrors cmd/octo's env context builder.
func buildEnvContext(cwd string) string {
	var b strings.Builder
	b.WriteString("# Environment\n\n")
	if cwd != "" {
		fmt.Fprintf(&b, "- Working directory: %s\n", cwd)
	}
	fmt.Fprintf(&b, "- Today's date: %s\n", time.Now().Format("2006-01-02"))
	// Platform-shell guidance (dialect + install/PATH traps), shared with the
	// CLI builder so web sessions get the same orientation.
	b.WriteString(tools.ShellEnvNote())
	// Detected toolchain — same as the CLI builder, so web sessions know which
	// runtimes are present without probing by trial and error.
	b.WriteString(tools.ToolchainNote())
	return b.String()
}

// ensureSender lazily initialises the sender when the server started in
// onboarding mode (nil sender). It re-reads config on every call so that
// once the user completes onboard and saves the API key, the next chat
// request picks it up without a server restart. Thread-safe via senderMu.
func (s *Server) ensureSender() error {
	if s.getSender() != nil {
		return nil
	}
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	if s.sender != nil {
		return nil
	}
	sender, model, provName, err := resolveProviderAndModel(s.cfg.Provider, s.cfg.Model)
	if err != nil {
		return err
	}
	if sender == nil {
		return fmt.Errorf("server not configured: complete setup via the Web UI")
	}
	s.sender = sender
	s.model = model
	s.provider = provName
	if s.cfg.Tools {
		s.enableSubAgentTools()
	}
	return nil
}

// getSender returns the current default sender under senderMu.
func (s *Server) getSender() agent.Sender {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	return s.sender
}

// getProvider returns the current default provider id under senderMu.
func (s *Server) getProvider() string {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	return s.provider
}

// reloadDefaultSender rebuilds the server's default sender from current config.
// It is used when global settings (e.g. show_reasoning) change so existing
// sessions pick up the new defaults on their next turn. If the server is still
// in onboarding mode (nil sender) the call is a no-op.
func (s *Server) reloadDefaultSender() error {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	if s.sender == nil {
		return nil
	}
	sender, model, provName, err := resolveProviderAndModel(s.cfg.Provider, s.cfg.Model)
	if err != nil {
		return err
	}
	if sender == nil {
		return nil
	}
	s.sender = sender
	s.model = model
	s.provider = provName
	return nil
}

// ─── WebSocket Asker (ask_user_question bridge) ─────────────────────────────

// ctxKeySessionID is the context key used to pass the active session ID from
// the turn runner down to the wsAsker so it knows which browser tab to prompt.
type ctxKeySessionID struct{}

// wsAsker implements tools.Asker by broadcasting a structured question over
// WebSocket and blocking until the user answers (or the context is cancelled).
// It is safe for concurrent use across sessions because each question gets a
// unique ID and a private channel.
func (s *Server) wsAsker() tools.Asker {
	return wsAsker{s: s}
}

type wsAsker struct {
	s *Server
}

func (a wsAsker) Ask(ctx context.Context, q tools.AskRequest) (tools.AskResponse, error) {
	sessionID, ok := ctx.Value(ctxKeySessionID{}).(string)
	if !ok || sessionID == "" {
		return tools.AskResponse{}, fmt.Errorf("ask_user_question: no active WebSocket session")
	}

	qid := fmt.Sprintf("q_%d", time.Now().UnixNano())
	ch := make(chan tools.AskResponse, 1)

	a.s.questionMu.Lock()
	a.s.questionChans[qid] = ch
	a.s.questionMu.Unlock()

	ev := wsEventRequestUserQuestion{
		Type:        "request_user_question",
		SessionID:   sessionID,
		QuestionID:  qid,
		Question:    q.Question,
		Options:     q.Options,
		MultiSelect: q.MultiSelect,
		Header:      q.Header,
	}

	// Record the outstanding question so a tab that (re)subscribes mid-ask —
	// e.g. after a page refresh — gets it replayed instead of a dead spinner.
	a.s.pendingPromptMu.Lock()
	a.s.pendingQuestions[sessionID] = ev
	a.s.pendingPromptMu.Unlock()

	dismiss := func() {
		a.s.wsHub.broadcast(sessionID, wsEventDismissUserQuestion{
			Type:       "dismiss_user_question",
			SessionID:  sessionID,
			QuestionID: qid,
		})
	}

	cleanup := func() {
		a.s.questionMu.Lock()
		delete(a.s.questionChans, qid)
		a.s.questionMu.Unlock()
		a.s.pendingPromptMu.Lock()
		delete(a.s.pendingQuestions, sessionID)
		a.s.pendingPromptMu.Unlock()
	}

	a.s.wsHub.broadcast(sessionID, ev)

	select {
	case res := <-ch:
		cleanup()
		return res, nil
	case <-ctx.Done():
		cleanup()
		dismiss()
		return tools.AskResponse{Cancelled: true}, nil
	case <-time.After(5 * time.Minute):
		cleanup()
		dismiss()
		return tools.AskResponse{}, fmt.Errorf("ask_user_question: timed out waiting for user answer")
	}
}

// handleWSUserQuestionAnswer delivers a user answer from the browser to a
// pending wsAsker.Ask call.
func (s *Server) handleWSUserQuestionAnswer(qid string, choices []string, custom string, cancelled bool) {
	s.questionMu.Lock()
	defer s.questionMu.Unlock()
	ch, ok := s.questionChans[qid]
	if !ok {
		slog.Warn("user_question_answer: no pending question channel", "qid", qid)
		return
	}
	ch <- tools.AskResponse{
		Choices:   choices,
		Custom:    custom,
		Cancelled: cancelled,
	}
}

// permissionAskFrom adapts the server's requestConfirmation into an
// app.PermissionAsk so the Web UI can resolve "ask" class policy verdicts
// interactively. The modal offers yes / no / always; "always" allows AND
// remembers the decision in the session's Remembered store, so the same
// (tool, input) pair stops prompting for the rest of the session.
func (s *Server) permissionAskFrom(sessionID string) app.PermissionAsk {
	return func(ctx context.Context, toolName string, toolInput map[string]any) (bool, bool, error) {
		msg := fmt.Sprintf("Allow %s?", toolName)
		result, err := s.requestConfirmation(ctx, sessionID, msg, "yes_no_always")
		if err != nil {
			return false, false, err
		}
		allow, remember := mapConfirmResult(result)
		return allow, remember, nil
	}
}

// mapConfirmResult maps the confirmation modal's reply onto the permission
// answer: anything but the two explicit affirmatives denies.
func mapConfirmResult(result string) (allow, remember bool) {
	switch result {
	case "yes":
		return true, false
	case "always":
		return true, true
	default:
		return false, false
	}
}

// rememberedFor returns the session's "always allow" store, creating it on
// first use. forgetTurnLock drops it with the rest of the session state.
func (s *Server) rememberedFor(key string) *permission.Remembered {
	s.rememberedMu.Lock()
	defer s.rememberedMu.Unlock()
	if s.rememberedStores == nil {
		s.rememberedStores = make(map[string]*permission.Remembered)
	}
	r, ok := s.rememberedStores[key]
	if !ok {
		r = permission.NewRemembered()
		s.rememberedStores[key] = r
	}
	return r
}

// ─── Channel (IM Bridge) ───────────────────────────────────────────────────

// initChannels loads channel config and creates a manager when channels are
// configured and not disabled via --no-channel.
func (s *Server) initChannels() {
	if s.cfg.NoChannel {
		return
	}
	if s.getSender() == nil {
		// Skip channel init when the server is in onboarding mode (no API key
		// yet). Channels will be started on the next server restart after the
		// user completes setup.
		return
	}
	chCfg, err := channel.LoadConfig()
	if err != nil {
		slog.Error("channel config", "err", err)
		return
	}
	platforms := chCfg.EnabledPlatforms()
	if len(platforms) == 0 {
		return
	}

	// Build agent factory that mirrors the server's agent setup. No gate is
	// set here: handleChannelMessage builds a fresh per-turn gate (configured
	// mode + chat-interactive ask), the same shape prepareToolTurn gives web
	// turns. A factory-time gate would freeze one policy snapshot for the
	// session's whole life — and the old `gate, _ :=` form silently ran turns
	// ungated when engine construction failed.
	var memInjection string
	if s.memDir != "" {
		memInjection = memory.RenderInjection(s.memDir, s.homeMemDir)
	}
	factory := func() *agent.Agent {
		defaultSender, model := s.defaultSenderAndModel()
		a := agent.New(defaultSender, model)
		cwd, envCtx := s.curCwdEnv()
		a.CWD = cwd
		a.MaxTokens = s.cfg.MaxTokens
		a.System, a.LeanSystem = prompt.ComposePair(s.system, cwd, envCtx, s.curSkillsManifest(), memInjection, true)
		if cfg, err := config.Load(); err == nil {
			a.LiteSender, a.LiteModel = s.liteSenderFromConfig(cfg)
			if a.LiteSender == nil {
				if lm := app.ImplicitLiteModel(s.getProvider(), model, resolveBaseURL(s.getProvider(), cfg)); lm != "" {
					a.LiteSender = defaultSender
					a.LiteModel = lm
				}
			}
		}
		return a
	}

	s.channelCfg = chCfg
	s.channelMgr = channel.NewManager(chCfg, factory, channel.BindByChatUser)

	slog.Info("channels enabled", "platforms", strings.Join(platforms, ", "))
}

// startChannels launches all enabled channel adapters in background goroutines.
func (s *Server) startChannels() {
	if s.channelMgr == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.channelCancel = cancel

	// For each enabled platform, create adapter and start it.
	for _, name := range s.channelCfg.EnabledPlatforms() {
		pc := s.channelCfg.Platform(name)
		if pc == nil {
			continue
		}
		ctor, err := channel.Find(name)
		if err != nil {
			slog.Error("channel start failed", "channel", name, "err", err)
			continue
		}
		ad, err := ctor(pc)
		if err != nil {
			slog.Error("channel adapter failed", "channel", name, "err", err)
			continue
		}
		if errs := ad.ValidateConfig(pc); len(errs) > 0 {
			for _, e := range errs {
				slog.Warn("channel config issue", "channel", name, "detail", e)
			}
			continue
		}

		s.runningAdapters.Store(name, ad)
		go func(a channel.Adapter, platform string) {
			_ = a.Start(ctx, func(ev channel.InboundEvent) {
				ev.Platform = platform
				s.routeChannelEvent(ctx, a, ev)
			})
		}(ad, name)
	}
}

// routeChannelEvent dispatches one inbound IM event. Order matters:
// commands first, so /stop can cancel a turn that is itself blocked on a
// permission ask; then a pending ask claims the message as its answer —
// inline, NOT through the turn path, which would deadlock behind the runMu
// the asking turn still holds; otherwise the message starts an agent turn
// off the adapter's read loop (Session.BeginRun serialises turns per
// session, so concurrent messages in one chat can't interleave).
func (s *Server) routeChannelEvent(ctx context.Context, ad channel.Adapter, ev channel.InboundEvent) {
	if s.handleChannelCommand(ad, ev) {
		return
	}
	// Text-less events (stickers, images, voice) never answer an ask or
	// steer — an empty reply would consume the ask slot and deny for
	// nothing, and an empty steer adds noise.
	if strings.TrimSpace(ev.Text) != "" {
		if sess := s.channelMgr.GetSession(ev); sess != nil {
			if sess.DeliverAskReply(ev.ChatID, ev.UserID, ev.Text) {
				return
			}
			// Steer: a message arriving mid-turn rides the running turn's
			// Inbox (drained between loop iterations; leftovers chain in
			// handleChannelMessage) — web/CLI parity, instead of queueing a
			// whole second turn. Known small race: a turn that finishes
			// between this check and the enqueue leaves the message in the
			// Inbox until the chat's next turn — delayed, not lost. Under
			// shared-session bindings (BindByChat/BindByUser) this folds
			// OTHER users' messages into the running turn too — coherent
			// for a shared conversation, but a behavior those modes should
			// revisit if the server ever stops hardcoding BindByChatUser.
			if sess.IsRunning() {
				sess.Agent.Inbox.Enqueue(ev.Text)
				return
			}
		}
	}
	go s.handleChannelMessage(ctx, ad, ev)
}

// handleChannelCommand processes slash commands (e.g. /bind, /stop). Returns
// true if the event was a command. Without the "/" guard, plain text like
// "你好" would be parsed as an unknown command and never reach the agent.
func (s *Server) handleChannelCommand(ad channel.Adapter, ev channel.InboundEvent) bool {
	text := strings.TrimSpace(ev.Text)
	if !strings.HasPrefix(text, "/") {
		return false
	}
	// /bind switches the chat to a different session, /unbind reverts it to its
	// default, /clear wipes the conversation, and /new starts a brand-new one —
	// all start a fresh context. The session's remembered permission allows and
	// its memory-injector latch are scoped to the previous conversation, so drop
	// them here: the manager owns session stores but not these server-side
	// per-key latches.
	cmd := strings.ToLower(strings.Fields(text)[0])
	switch cmd {
	case "/unbind", "/bind", "/clear", "/new":
		imKey := "im:" + string(s.channelMgr.KeyFor(ev))
		s.rememberedMu.Lock()
		delete(s.rememberedStores, imKey)
		s.rememberedMu.Unlock()
		s.injectorMu.Lock()
		delete(s.sessionInjectors, imKey)
		s.injectorMu.Unlock()
	}
	// /compact runs an LLM summarize, so it's handled out of the (synchronous,
	// reply-string) CommandRouter: run it in a goroutine and report the result.
	if cmd == "/compact" {
		s.handleChannelCompact(ad, ev)
		return true
	}
	if reply := s.channelMgr.CommandRouter(ev); reply != "" {
		ad.SendText(ev.ChatID, reply, ev.MessageID)
	}
	return true
}

// handleChannelCompact force-compacts an IM session's history and reports the
// outcome. The summarize is an LLM call, so it runs in a goroutine; BeginRun
// serialises it against any agent turn in the same session.
func (s *Server) handleChannelCompact(ad channel.Adapter, ev channel.InboundEvent) {
	sess := s.channelMgr.GetSession(ev)
	if sess == nil {
		ad.SendText(ev.ChatID, "No active session to compact.", ev.MessageID)
		return
	}
	if sess.IsRunning() {
		ad.SendText(ev.ChatID, "A task is running — try /compact again once it finishes.", ev.MessageID)
		return
	}

	storeID := sess.Store.ID
	if ok, _, berr := s.acquireSessionBinding(storeID, agent.EntryChannel, false); !ok {
		ad.SendText(ev.ChatID, "⚠️ "+berr.Error(), ev.MessageID)
		return
	}

	if fresh, err := agent.LoadSession(storeID); err == nil {
		sess.Store = fresh
	}

	go func() {
		defer s.releaseSessionBinding(storeID, agent.EntryChannel)
		ctx, done := sess.BeginRun(context.Background())
		defer done()
		stats, err := sess.Agent.ForceCompact(ctx, nil)
		switch {
		case err != nil:
			ad.SendText(ev.ChatID, "Compact failed: "+err.Error(), ev.MessageID)
		case stats.FoldedMsgs == 0 && stats.ReclaimedTokens == 0:
			ad.SendText(ev.ChatID, "Nothing to compact yet.", ev.MessageID)
		default:
			if perr := sess.Persist(); perr != nil {
				ad.SendText(ev.ChatID, "Compacted, but saving failed: "+perr.Error(), ev.MessageID)
				return
			}
			ad.SendText(ev.ChatID, fmt.Sprintf("Compacted — folded %d message(s).", stats.FoldedMsgs), ev.MessageID)
		}
	}()
}

// handleChannelMessage runs an agent turn for a channel inbound event and
// wires completion hooks so background processes / workflows can trigger
// follow-up turns when the session goes idle (web kickIdleSteerTurn parity).
func (s *Server) handleChannelMessage(ctx context.Context, ad channel.Adapter, ev channel.InboundEvent) {
	if err := s.drain.begin(); err != nil {
		// Restart drain in progress. The adapter is still up (it stops only
		// after the drain, so in-flight replies get delivered) — tell the
		// user to retry instead of silently dropping the message.
		ad.SendText(ev.ChatID, "The server is restarting — please send that again in a moment.", ev.MessageID)
		return
	}
	defer s.drain.end()

	sess := s.channelMgr.GetOrCreateSession(ev)
	if sess == nil {
		return
	}

	// Enforce entry binding: the session file is the single source of truth.
	// If another entry (web/tui/cli) owns this session, refuse to run the turn
	// rather than interleaving with it across processes.
	storeID := sess.Store.ID
	if ok, _, berr := s.acquireSessionBinding(storeID, agent.EntryChannel, false); !ok {
		ad.SendText(ev.ChatID, "⚠️ "+berr.Error()+"\nReply /new to start a fresh session, or run /list then /bind --force <number> to take it over.", ev.MessageID)
		return
	}
	defer s.releaseSessionBinding(storeID, agent.EntryChannel)

	// Reload the authoritative session after acquiring the binding. Another
	// process may have saved since the manager restored it, and we must not
	// persist through a stale Store pointer.
	if fresh, err := agent.LoadSession(storeID); err == nil {
		sess.Store = fresh
	}

	// Waits for any in-flight turn in this session, then makes this turn
	// cancellable by /stop (Session.Interrupt).
	ctx, done := sess.BeginRun(ctx)
	defer done()

	// Start typing keepalive BEFORE sending any text. The WeChat iLink protocol
	// cancels the typing indicator on sendmessage, so the keepalive must already
	// be running when the acknowledgment message is sent so it can immediately
	// re-assert the typing state. Other platforms treat this as a no-op or a
	// self-expiring hint (Telegram, Discord) — safe to call universally.
	if err := ad.SendTyping(ev.ChatID, ev.ContextToken); err != nil {
		slog.Debug("channel sendTyping", "platform", ev.Platform, "err", err)
	}
	defer func() {
		if err := ad.StopTyping(ev.ChatID, ev.ContextToken); err != nil {
			slog.Debug("channel stopTyping", "platform", ev.Platform, "err", err)
		}
	}()

	// Acknowledge receipt immediately so the user knows the message landed
	// while the agent is working. Mirrors Ruby channel_manager behaviour.
	ad.SendText(ev.ChatID, "Thinking…", ev.MessageID)

	// Wire completion hooks before the turn starts so any process launched
	// during this turn can notify the model, and any completion that lands
	// after the synchronous turn chain has finished can kick an idle turn.
	s.wireChannelCompletionHooks(sess, ad, ev)

	// Bridge inbound attachments (images, documents) into the turn before it
	// runs — images become vision blocks on the user message, documents become
	// read_file-able path notes. Without this only ev.Text reaches the model.
	content := s.attachInboundFiles(sess, ev)

	s.runChannelTurns(ctx, sess, ad, ev, content)

	// A steer that arrived during a restart drain would die with the
	// process (the Inbox is memory-only) — give it the same retry notice a
	// non-steered message gets. Leftovers outside a drain stay queued for
	// the chat's next turn: delayed, not lost.
	if s.drain.isDraining() && sess.Agent.Inbox.HasPending() {
		sess.Agent.Inbox.Drain()
		ad.SendText(ev.ChatID, "The server is restarting — please send that again in a moment.", ev.MessageID)
	}
}

// attachInboundFiles bridges an inbound event's attachments into the agent
// turn, mirroring the web composer's parseUserFiles. Images (delivered as data
// URLs by the adapters) are decoded, persisted under ~/.octo/uploads, and
// queued as vision blocks merged into the next user message; documents
// (delivered as a local Path) become "[Attached file: <path>]" notes so the
// model can open them with read_file. Per-file failures are logged and
// skipped. Returns the content to run with any file notes folded in.
func (s *Server) attachInboundFiles(sess *channel.Session, ev channel.InboundEvent) string {
	content := ev.Text
	var blocks []agent.ContentBlock
	var notes []string
	for _, f := range ev.Files {
		switch {
		case f.DataURL != "":
			block, _, err := saveImageAttachment(f.Name, f.DataURL)
			if err != nil {
				slog.Debug("channel image attachment", "platform", ev.Platform, "name", f.Name, "err", err)
				continue
			}
			blocks = append(blocks, block)
		case f.Path != "":
			notes = append(notes, fmt.Sprintf("[Attached file: %s]", f.Path))
		}
	}
	if len(blocks) > 0 {
		// Consumed once by the first RunAgent's appendUserInput; chained turns
		// in runChannelTurns won't re-attach them.
		sess.Agent.AttachUserBlocks(blocks)
	}
	if len(notes) > 0 {
		content = strings.TrimSpace(content + "\n\n" + strings.Join(notes, "\n"))
	}
	return content
}

// wireChannelCompletionHooks registers per-session hooks for background
// processes and workflows. The hook enqueues a <system-reminder> note for the
// model and tries to start an idle follow-up turn so IM sessions react to
// async completions without waiting for the user's next message.
func (s *Server) wireChannelCompletionHooks(sess *channel.Session, ad channel.Adapter, ev channel.InboundEvent) {
	if !s.cfg.Tools {
		return
	}
	key := "im:" + string(sess.Key)
	bgMgr := tools.SessionBackgroundManager(key)
	bgMgr.SetOnExit(func(e tools.BgExit) {
		sess.Agent.Inbox.Enqueue(tools.FormatBgNoteWithSummary(bgMgr, e))
		go s.runChannelIdleTurn(context.Background(), sess, ad, ev)
	})
	wfMgr := tools.SessionWorkflowManager(key)
	wfMgr.SetOnDone(func(n tools.WorkflowNotification) {
		sess.Agent.Inbox.Enqueue(tools.FormatWorkflowNote(n))
		go s.runChannelIdleTurn(context.Background(), sess, ad, ev)
	})
}

// runChannelIdleTurn tries to run a follow-up turn for async completions that
// arrived while the session was idle. It competes with the synchronous turn
// path for the session's runMu; BeginRun serialises them, so only one wins.
func (s *Server) runChannelIdleTurn(ctx context.Context, sess *channel.Session, ad channel.Adapter, ev channel.InboundEvent) {
	// Acquire the persistent binding before locking the turn: this matches the
	// user-initiated IM path and prevents idle follow-up turns from
	// interleaving with another entry (web/cli/tui) that has taken over the
	// session while we were idle.
	storeID := sess.Store.ID
	if ok, _, _ := s.acquireSessionBinding(storeID, agent.EntryChannel, false); !ok {
		return
	}
	defer s.releaseSessionBinding(storeID, agent.EntryChannel)

	// Enroll in the drain gate so graceful shutdown waits for idle follow-up
	// turns just like user-initiated channel turns and web turns.
	if err := s.drain.begin(); err != nil {
		return
	}
	defer s.drain.end()

	ctx, done := sess.BeginRun(ctx)
	defer done()

	items := sess.Agent.Inbox.Drain()
	if len(items) == 0 {
		return
	}

	s.runChannelTurns(ctx, sess, ad, ev, strings.Join(agent.Texts(items), "\n\n"))
}

// runChannelTurns executes one content-bearing turn plus any chained turns
// drained from the agent's Inbox. It is shared between user-initiated and
// idle-triggered channel turns.
func (s *Server) runChannelTurns(ctx context.Context, sess *channel.Session, ad channel.Adapter, ev channel.InboundEvent, content string) {
	// Recompose the system prompt every turn so memory written and skills
	// imported/toggled since server start are visible — web turns get this
	// for free from buildAgent; the IM factory's compose-once snapshot went
	// stale until restart. Unconditional: the skills manifest changes at
	// runtime even when memory is disabled.
	var memInjection string
	if s.memDir != "" {
		memInjection = memory.RenderInjection(s.memDir, s.homeMemDir)
	}
	cwd, envCtx := s.curCwdEnv()
	sess.Agent.System, sess.Agent.LeanSystem = prompt.ComposePair(s.system, cwd, envCtx, s.curSkillsManifest(), memInjection, true)

	// L2 memory hooks, same pair buildAgent gives web turns: keyword
	// reminders on user input, save-nudge on milestone tool results. The
	// injector is session-sticky (recall latch) and dropped on /unbind.
	if s.memDir != "" {
		inj := s.injectorFor("im:" + string(sess.Key))
		sess.Agent.UserInputHook = inj.Reminder
		sess.Agent.ToolResultHook = inj.SaveNudge
	}

	// Per-turn permission gate, the same shape prepareToolTurn gives web
	// turns: configured mode + an interactive ask that prompts in the chat
	// and consumes the next message as the answer. Built after BeginRun so
	// it can't race the previous turn's gate. An engine failure aborts the
	// turn — running ungated is never an acceptable fallback.
	engine, err := permission.New(permissionConfigPath(), cwd, resolvePermissionMode(), s.memDir, s.homeMemDir)
	if err != nil {
		// Generic chat reply — err.Error() can leak local paths into a
		// group chat; the operator gets the detail on the server console.
		slog.Error("channel permission engine", "channel", ev.Platform, "err", err)
		ad.SendText(ev.ChatID, "⚠️ Permission engine unavailable — message not processed. Check the server logs.", ev.MessageID)
		return
	}
	engine.AttachRemembered(s.rememberedFor("im:" + string(sess.Key)))
	sess.Agent.Gate = app.NewPermissionGate(engine, s.channelPermissionAsk(sess, ad, ev))

	ctrl := channel.NewUIController(ad, ev.ChatID, ev.MessageID)

	var toolDefs []agent.ToolDefinition
	var executor agent.ToolExecutor
	if s.cfg.Tools {
		executor = tools.NewDefaultRegistry()
		toolDefs = tools.DefaultToolsFor(sess.Agent.Model)

		// Per-message sub-agent manager bound to THIS chat's agent —
		// synchronous, since a chat message is request/response with no
		// follow-up channel for an async result.
		spawner := app.NewSpawner(sess.Agent, executor, func() []agent.ToolDefinition {
			return tools.DefaultToolsFor(sess.Agent.Model)
		})
		subMgr := tools.NewSubAgentManager(spawner)
		subMgr.SetSynchronous(true)
		ctx = tools.WithSubAgentManager(ctx, subMgr)
		// The task store lives on the session, so the task list persists
		// across messages in this chat.
		ctx = tools.WithTaskStore(ctx, sess.Tasks)
		// Per-chat background manager: completions are surfaced to the model
		// via the Inbox and also trigger idle follow-up turns above.
		ctx = tools.WithBackgroundManager(ctx, tools.SessionBackgroundManager("im:"+string(sess.Key)))
		// Per-chat workflow manager so background workflows are isolated per
		// conversation (their own wf_N namespace and concurrency budget). Its
		// completion hook nudges the model via the same Inbox path as bg/sub-agent.
		ctx = tools.WithWorkflowManager(ctx, tools.SessionWorkflowManager("im:"+string(sess.Key)))
		// Turn-scoped asker: ask_user_question prompts in this chat instead
		// of falling back to the process-global wsAsker, which broadcasts to
		// browser tabs an IM session doesn't have (the question would hang
		// until /stop).
		ctx = tools.WithAsker(ctx, s.channelAsker(sess, ad, ev))
		// Turn-scoped file sender + the IM-only send_file tool: the model's
		// reply carries text, so the only way to put a generated image / chart
		// / document in front of the user is to push it through the adapter.
		// Withheld from the shared default tool list (DefaultToolsFor); added
		// here because only an IM turn has a chat to send to.
		ctx = tools.WithChannelSender(ctx, channelFileSender{ad: ad, chatID: ev.ChatID, replyTo: ev.MessageID})
		toolDefs = append(toolDefs, tools.SendFileToolDef())
	}

	persist := func() {
		// Persist the conversation so it survives server restarts. Failure
		// must not eat the reply the user already got — log and move on.
		if err := sess.Persist(); err != nil {
			slog.Error("channel persist session", "channel", ev.Platform, "err", err)
		}
	}

	_, runErr := channel.RunAgent(ctx, sess, toolDefs, executor, ctrl, content)
	persist()

	// Steer messages that arrived after the turn's final inbox drain chain
	// into follow-up turns (web runAgentTurnLoop parity). Still inside
	// BeginRun, so no new turn can interleave. Persist after each chained
	// turn — one crash must cost at most one turn, not the whole chain. A
	// chained error stops the chain (its rollback already dropped the steer
	// message; looping would re-fail forever).
	for runErr == nil && ctx.Err() == nil {
		items := sess.Agent.Inbox.Drain()
		if len(items) == 0 {
			break
		}
		_, runErr = channel.RunAgent(ctx, sess, toolDefs, executor, ctrl, strings.Join(agent.Texts(items), "\n\n"))
		persist()
	}
}

// stopChannels shuts down all channel adapters and their sessions.
func (s *Server) stopChannels() {
	if s.channelCancel != nil {
		s.channelCancel()
	}
	if s.channelMgr != nil {
		_ = s.channelMgr.Stop()
	}
	// Clear entries in place rather than reassigning the sync.Map: a concurrent
	// HTTP handler (handleListChannels/handleGetChannel → isAdapterRunning) may
	// be calling Load() during shutdown, and a sync.Map must not be copied or
	// replaced after first use. Range+Delete is safe under concurrent access.
	s.runningAdapters.Range(func(k, _ any) bool {
		s.runningAdapters.Delete(k)
		return true
	})
}
