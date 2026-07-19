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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/hooks"

	// The IM adapters self-register into the channel registry at init time.
	// The server is what runs them (startChannels) and looks them up by name
	// (channel.Find for task-notify pushes), so it owns the imports.
	_ "github.com/open-octo/octo-agent/internal/channel/adapters/dingtalk"
	_ "github.com/open-octo/octo-agent/internal/channel/adapters/discord"
	_ "github.com/open-octo/octo-agent/internal/channel/adapters/feishu"
	_ "github.com/open-octo/octo-agent/internal/channel/adapters/telegram"
	_ "github.com/open-octo/octo-agent/internal/channel/adapters/wecom"
	_ "github.com/open-octo/octo-agent/internal/channel/adapters/weixin"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/prompt"
	"github.com/open-octo/octo-agent/internal/scheduler"
	"github.com/open-octo/octo-agent/internal/skills"
	"github.com/open-octo/octo-agent/internal/tasks"
	"github.com/open-octo/octo-agent/internal/tools"
	"github.com/open-octo/octo-agent/internal/version"
	"github.com/open-octo/octo-agent/internal/workflow"
)

// getDefaultToolsFor bridges to tools.DefaultToolsFor for ws_handlers.go.
func getDefaultToolsFor(model string) []agent.ToolDefinition {
	return tools.DefaultToolsFor(model)
}

// Config holds server-level settings.
type Config struct {
	// Bind address (e.g. ":8088", "127.0.0.1:8088").
	Addr string

	// Provider and model override; zero values fall back to env/config.
	Provider string
	Model    string
	System   string

	// MaxTokens forwarded to the agent per turn.
	MaxTokens int

	// Tools enables the agentic tool loop.
	Tools bool

	// CORSOrigins lists additional allowed origins for CORS. vscode-webview://
	// origins are always allowed regardless — see isVSCodeWebviewOrigin.
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

	// WorkspaceDir overrides the default working directory new web sessions
	// are created with (see config.Config.WorkspaceDir / tools.ResolveWorkspaceDir).
	// Empty (default) falls back to ~/.octo/config.yml's workspace_dir, then
	// to no override at all — unchanged from today's curCwd() behavior. No
	// `octo serve` flag sets this; it exists mainly so tests can inject a
	// literal path without touching the real config file.
	WorkspaceDir string

	// Native, when non-nil, is the desktop shell's hook into OS-native
	// capabilities a browser can't provide (folder dialog, notifications).
	// It is nil under `octo serve` and set only by the Wails desktop build.
	// When nil, the /api/native/* routes are not registered, so serve exposes
	// no extra surface. See NativeBridge.
	Native NativeBridge

	// DisableRestart omits the restart_server tool from the agent's catalog.
	// Set by the desktop build, where the server runs in-process and has no
	// supervisor to respawn it — letting the agent call it would shut the
	// server down and leave the GUI window attached to a dead backend. Leave
	// false under `octo serve`, where the supervisor honours ExitRestart.
	DisableRestart bool
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
	// cwd/envCtx are the server's launch directory and its derived env-context
	// string, fixed at New. They are the DEFAULT a session's tools/prompt use
	// when it has no working dir of its own (agent.Session.WorkingDir), and the
	// root MCP/skills/file handlers resolve against. cwdMu guards reads so the
	// (cwd, envCtx) pair is always observed together.
	cwd        string
	envCtx     string
	cwdMu      sync.RWMutex
	memDir     string
	homeMemDir string

	// workspaceDir is the resolved default WorkingDir new web sessions get
	// when the caller requests none of their own (see cfg.WorkspaceDir /
	// tools.ResolveWorkspaceDir). "" means no override — sessions keep
	// falling back to cwd, exactly like before this field existed. Set at
	// New and updated by PUT /api/config/workspace_dir; workspaceDirMu guards
	// it against a data race between that handler and concurrent session
	// creation reading it.
	workspaceDir   string
	workspaceDirMu sync.RWMutex

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
	// liveSessions tracks the running turn's Session object under the same
	// lock: goal commands must mutate the record the turn is accounting
	// into, not a freshly-loaded copy the turn would later overwrite.
	sessionAgents   map[string]*agent.Agent
	liveSessions    map[string]*agent.Session
	sessionAgentsMu sync.Mutex

	// senderCache holds one sender per config entry name, built lazily for
	// sessions bound to a non-default model entry. Invalidated wholesale on
	// any /api/config/models mutation; the next turn rebuilds.
	senderCache   map[string]agent.Sender
	senderCacheMu sync.Mutex

	// titlePending guards one in-flight title generation per session
	// (lazily initialised by claimTitleGeneration).
	titlePending map[string]bool
	// pendingTitles carries a mid-turn generated title to the turn goroutine,
	// which adopts it after its final end-of-turn write (see storePendingTitle).
	pendingTitles map[string]string
	titleMu       sync.Mutex

	// WebSocket hub for real-time browser communication.
	wsHub *wsHub

	// interrupt cancellation per session.
	interrupts  map[string]context.CancelFunc
	interruptMu sync.Mutex

	// wakeupTimers holds the armed in-session loop wakeup per session
	// (schedule_wakeup tool, behind /loop), guarded by wakeupMu. The loop
	// coexists with user messages; it stops on an explicit interrupt / cancel
	// or the anti-leak lifetime bound. wakeupStart is the per-loop clock
	// (first-arm time, kept across ticks) backing that bound. See loop.go.
	wakeupTimers map[string]*time.Timer
	wakeupStart  map[string]time.Time
	wakeupMu     sync.Mutex

	// goalsEnabled mirrors the config's goal.enabled at server start: it
	// gates the goal-continuation kick after each turn (the goal tools'
	// visibility is gated separately via tools.SetGoalsEnabled). atomic.Bool
	// because syncGoalsEnabled can write it from inside ensureSender's
	// senderMu-guarded section (to close a race with initChannels/
	// reloadChannel, which read it from other goroutines with no lock of
	// their own) while every other reader/writer here still needs a
	// consistent, lock-free way to see the same value.
	goalsEnabled atomic.Bool

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

	// askSlots serializes interactive prompts per session: at most one
	// confirmation or question may be outstanding at a time. Concurrent askers
	// for the same session — parallel workflow agents, background tasks, all
	// sharing the session's gate — would otherwise each broadcast a prompt and
	// clobber the single pending* slot and the single frontend modal; only the
	// last is answerable, and every earlier asker's goroutine blocks until its
	// timeout (a hang). Each value is a cap-1 channel used as a lock.
	askSlots   map[string]chan struct{}
	askSlotsMu sync.Mutex

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

	// channelMu guards channel (re)start bookkeeping — startChannels runs at
	// boot, stopChannels at shutdown, and reloadChannel from an HTTP handler
	// after a config save; they must not race. channelCtx is the base context
	// all adapter goroutines derive from (cancelled by stopChannels);
	// adapterCancels holds each running adapter's own cancel so a single
	// platform can be stopped and restarted without touching the others.
	channelMu      sync.Mutex
	channelCtx     context.Context
	adapterCancels map[string]context.CancelFunc

	// channelGen bumps every time startOneChannelLocked (re)starts a platform
	// from scratch, and is handed to that start's runChannelWithRestart
	// goroutine as a snapshot. A crashed adapter's restart loop rebuilds a
	// fresh instance via ctor(pc) — a call that does not observe ctx
	// cancellation — so a concurrent stop/reload/restart of the SAME
	// platform (e.g. an operator reacting to the crash-loop this fix makes
	// visible in the Channels view) can advance the generation while that
	// ctor(pc) call is still in flight. Before writing anything back to
	// runningAdapters/adapterCancels, the restart loop re-checks its
	// snapshot against the current generation under channelMu; a mismatch
	// means another goroutine now owns this platform name, so it discards
	// its work instead of clobbering the new owner's bookkeeping.
	channelGen map[string]uint64

	// channelIssues records, per platform, why its adapter isn't healthy: a
	// startup skip reason (unregistered/construction failed/invalid config)
	// or the latest crash-and-restart status (#1121 — these used to be
	// logged, if at all, with no queryable record, so a misconfigured or
	// crashing platform's only visible symptom was "the bot never replies").
	// Absent (or "") means healthy. Guarded by its own mutex rather than
	// channelMu since it's read from the REST handlers on any goroutine,
	// independent of the (re)start bookkeeping channelMu protects.
	channelIssuesMu sync.Mutex
	channelIssues   map[string]string

	// channelWG tracks every runChannelWithRestart goroutine currently in
	// flight. Nothing in production Waits on it — stopChannels/reloadChannel
	// only cancel the relevant context(s) and return, which is enough for
	// correct shutdown behavior. It exists so tests can deterministically
	// block until a stopped adapter's goroutine has actually returned before
	// tearing down state that goroutine still reads (e.g. restoring a
	// package-level test override), instead of racing a fire-and-forget
	// background goroutine against test cleanup.
	channelWG sync.WaitGroup

	// weixinLogin tracks the in-flight web QR login flow (one at a time).
	weixinLogin weixinLoginFlow

	// mcpCleanup unregisters + closes the MCP registry connected at start.
	// Always non-nil after New (a no-op when no servers connected).
	mcpCleanup func()

	// mcpMu serialises MCP config mutations + registry swaps from the web
	// management API (and Shutdown) — SwapMCP must not run concurrently
	// with itself or with incremental connects. Also guards mcpOAuthFlows.
	mcpMu sync.Mutex

	// mcpOAuthFlows tracks in-flight web Authorization Code authorizations
	// by server name. Lazily initialised by the oauth/start handler.
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

	// Surface async-hook (spill queue) errors through the server log.
	hooks.SetSpillNotify(func(m string) { slog.Warn("hook", "err", m) })
	// Auto-trust the operator-chosen launch cwd's project hooks (a documented
	// decision — the operator started serve here). Every later project-hook load
	// is gated on IsTrusted, so switching working_dir into an untrusted repo does
	// NOT load its hooks; trusting happens only for this launch dir (recorded) or
	// dirs a CLI run trusted.
	if cwd, err := os.Getwd(); err == nil {
		if p := hooks.ProjectConfigPath(cwd); p != "" {
			if b, rerr := os.ReadFile(p); rerr == nil {
				_ = hooks.RecordTrust(p, hooks.Fingerprint(b))
			}
		}
	}

	// Materialize the binary's default skills/workflows to ~/.octo/skills-default
	// and ~/.octo/workflows-default so Discover() below finds them. The CLI's
	// run() does the same before runChat/runServe, but server.New() is also
	// called directly by the desktop app (which never goes through run()) — so
	// the init has to live here too. A no-op/cheap-pass once current (stamp
	// file), so calling it from both paths is harmless.
	_ = skills.MaterializeDefaults(version.Version)
	_ = tools.MaterializeDefaultWorkflows(version.Version)
	_ = workflow.PruneJournals()

	cwd, _ := os.Getwd()
	envCtx := buildEnvContext(cwd)

	skillReg := skills.Discover(cwd)
	fileCfg, _ := config.Load()
	skillReg.SetDisabled(fileCfg.Tools.DisabledSkills)
	skillsManifest := tools.SkillsManifest(skillReg)
	tools.SetSkills(skillReg)

	// Resolve the default workspace dir new web sessions get. cfg.WorkspaceDir
	// (no `octo serve` flag sets it today) takes precedence so tests can inject
	// a literal path without touching ~/.octo/config.yml; production falls back
	// to the file config's workspace_dir. A resolve error (e.g. no home dir)
	// degrades to "" — no override — rather than failing server startup.
	rawWorkspaceDir := cfg.WorkspaceDir
	if rawWorkspaceDir == "" {
		rawWorkspaceDir = fileCfg.WorkspaceDir
	}
	workspaceDir, err := tools.ResolveWorkspaceDir(rawWorkspaceDir)
	if err != nil {
		slog.Warn("could not resolve workspace_dir; new sessions keep using the launch directory", "err", err)
		workspaceDir = ""
	}

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
		workspaceDir:        workspaceDir,
		turnLocks:           map[string]*sync.Mutex{},
		turnRunning:         make(map[string]bool),
		entryBindings:       make(map[string]*cachedEntryBinding),
		sessionBindingLocks: map[string]*sync.Mutex{},
		steerQueues:         make(map[string][]agent.InboxItem),
		sessionAgents:       make(map[string]*agent.Agent),
		liveSessions:        make(map[string]*agent.Session),
		accessKey:           accessKey,
		confirmations:       make(map[string]chan string),
		questionChans:       make(map[string]chan tools.AskResponse),
		pendingQuestions:    make(map[string]wsEventRequestUserQuestion),
		pendingConfirms:     make(map[string]wsEventRequestConfirmation),
		askSlots:            make(map[string]chan struct{}),
		sessionInjectors:    make(map[string]*memory.Injector),
		wakeupTimers:        make(map[string]*time.Timer),
		wakeupStart:         make(map[string]time.Time),
	}

	// Register the WebSocket-backed asker so ask_user_question appears in the
	// tool catalog and can be dispatched through the browser.
	tools.SetAsker(s.wsAsker())

	// Register the restarter so restart_server appears in the tool catalog —
	// the agent can then honour "重启一下服务" from web or IM. Permission is
	// ask-class (defaults.yml): the web UI confirms via the browser prompt,
	// IM via the in-chat reply prompt. The restart drains in-flight turns
	// (including the one calling the tool) before the worker exits with
	// ExitRestart for the supervisor to respawn. The desktop build sets
	// DisableRestart and skips this — its server runs in-process with no
	// supervisor to respawn it, so the tool would just kill the backend.
	if !cfg.DisableRestart {
		tools.SetRestarter(s.Restart)
	}

	// Advertise send_message so a turn on any surface (web, IM, sub-agent) can
	// proactively push to an IM chat it isn't currently talking to — e.g. a web
	// session asked to message the user's WeChat.
	tools.SetMessenger(serverMessenger{s})

	// Guard the hosting process: the agent must not kill its own octo server
	// (it would drop the live session); restart_server is the graceful path.
	tools.SetServerGuard(true)

	// Server-managed sessions can be re-entered, so advertise schedule_wakeup
	// (the in-session loop mechanism, behind /loop); the per-session Waker is
	// stamped into each turn's ctx in runAgentTurnLoop.
	tools.SetWakerSupported(true)

	s.registerRoutes()
	s.initWS()
	if sender != nil {
		s.enableSubAgentTools()
	}
	s.enableMCP()
	if !cfg.NoChannel {
		s.initChannels()
	}

	s.http = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.corsMiddleware(s.mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // WS connections are long-lived
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
	// Refresh before reading MemoryBackendGuidance() below — enableSubAgentTools
	// runs at server startup (before any turn has ever called this) and once
	// more after onboarding, so without this the sub-agent template's baked-in
	// system prompt would never see the memory-backend guidance at all if it
	// was configured before the first real turn, since spawned sub-agents
	// reuse this template's System rather than recomposing it per spawn (see
	// app.NewSpawner below).
	app.RefreshMemoryBackend()
	var memInjection string
	if s.memDir != "" {
		memInjection = memory.RenderInjection(s.memDir, s.homeMemDir)
	}
	if g := tools.MemoryBackendGuidance(); g != "" {
		memInjection = strings.TrimSpace(memInjection + "\n\n" + g)
	}
	cwd, envCtx := s.curCwdEnv()
	cfg, _ := config.Load() // zero value on error still resolves correctly via EffectiveCoauthor
	template.System, template.LeanSystem = prompt.ComposePair(s.system, cwd, envCtx, s.curSkillsManifest(), tools.MCPManifestFor(model), memInjection, s.effectiveCoauthor(cfg))
	executor := tools.NewDefaultRegistry()
	spawner := app.NewSpawner(template, executor, func(ctx context.Context) []agent.ToolDefinition {
		return tools.DefaultToolsForCtx(ctx, s.model)
	})
	mgr := tools.NewSubAgentManager(spawner)
	mgr.SetSynchronous(true)
	tools.SetDefaultSubAgentManager(mgr)
	tools.SetTaskStore(tasks.New())
	// Session goals: every server turn stamps its session as the ctx-scoped
	// goal store (doAgentTurn); this only advertises the tools.
	s.syncGoalsEnabled()
}

// syncGoalsEnabled reads goal.enabled from the on-disk config and applies it
// to both the process-global tools flag and s.goalsEnabled. It touches
// neither senderMu nor any other lock — config.Load() only reads the
// filesystem — so callers can run it while already holding senderMu without
// risking the reentrant-lock deadlock enableSubAgentTools itself must avoid
// (see ensureSender).
func (s *Server) syncGoalsEnabled() {
	if cfg, err := config.Load(); err == nil && cfg.GoalEnabled() {
		tools.SetGoalsEnabled(true)
		s.goalsEnabled.Store(true)
	}
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

// ServeOn runs the server on a listener the caller already bound. The desktop
// shell (cmd/octo-desktop) uses it to grab an ephemeral loopback port, learn
// the address to point its window at, then serve on that same socket — closing
// the bind-after-probe race a fixed or re-derived port would open. Behaves like
// ListenAndServe otherwise.
func (s *Server) ServeOn(ln net.Listener) error { return s.serveOn(ln) }

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
	tools.SetMessenger(nil)
	tools.SetServerGuard(false)
	// Flush any queued async hooks (retention) to disk so a restart/shutdown
	// doesn't drop them; the next process redelivers.
	hooks.DrainSpill(5 * time.Second)
	if s.mcpCleanup != nil {
		s.mcpCleanup()
	}
	return s.http.Shutdown(ctx)
}

// api registers an authenticated route. The requireAuth wrapper is applied
// here, in one place, so a new route cannot forget it. /api/health,
// /api/version, the MCP OAuth callback, and static files are the only
// handlers registered directly on the mux — the OAuth callback bypasses
// auth deliberately (see handleMCPOAuthCallback's doc comment).
func (s *Server) api(pattern string, h http.HandlerFunc) {
	s.apiRoutes = append(s.apiRoutes, pattern)
	s.mux.HandleFunc(pattern, s.requireAuth(h))
}

// registerRoutes wires all handlers. API and WS routes require auth.
func (s *Server) registerRoutes() {
	s.api("POST /api/chat", s.handleCreateChat)
	s.api("POST /api/chat/{id}/turn", s.handleTurn)
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
	s.api("PATCH /api/sessions/{id}/permission_mode", s.handleUpdateSessionPermissionMode)
	s.api("PATCH /api/sessions/{id}/working_dir", s.handleUpdateSessionWorkingDir)
	s.api("GET /api/sessions/{id}/goal", s.handleGetSessionGoal)
	s.api("PUT /api/sessions/{id}/goal", s.handleUpdateSessionGoal)
	s.api("DELETE /api/sessions/{id}/goal", s.handleDeleteSessionGoal)
	s.api("PUT /api/sessions/{id}/group", s.handleSetSessionGroup)
	s.api("PUT /api/sessions/{id}/pin", s.handleSetSessionPin)
	s.api("POST /api/sessions/{id}/branch", s.handleBranchSession)
	s.api("POST /api/sessions/{id}/edit_message", s.handleEditMessage)
	// Session groups: a Web-UI-only sidebar organisation layer (see
	// session_groups.go). Registered unconditionally so both `octo serve` and
	// the desktop shell expose them.
	s.api("GET /api/session-groups", s.handleListSessionGroups)
	s.api("POST /api/session-groups", s.handleCreateSessionGroup)
	s.api("PUT /api/session-groups/order", s.handleReorderSessionGroups)
	s.api("PATCH /api/session-groups/{id}", s.handleUpdateSessionGroup)
	s.api("DELETE /api/session-groups/{id}", s.handleDeleteSessionGroup)
	s.api("GET /api/fs/list", s.handleFsList)
	if s.cfg.Native != nil {
		// Desktop build only: OS-native capabilities. Absent under `octo serve`.
		s.api("POST /api/native/pick-folder", s.handleNativePickFolder)
		s.api("POST /api/native/pick-file", s.handleNativePickFile)
		s.api("POST /api/native/notify", s.handleNativeNotify)
		s.api("GET /api/native/autostart", s.handleNativeAutostartGet)
		s.api("PUT /api/native/autostart", s.handleNativeAutostartSet)
		s.api("POST /api/native/window/toggle-maximise", s.handleNativeToggleMaximise)
		s.api("POST /api/native/window/minimise", s.handleNativeMinimise)
		s.api("POST /api/native/window/close", s.handleNativeClose)
		s.api("GET /api/native/window/state", s.handleNativeWindowState)
		s.api("POST /api/native/open-external", s.handleNativeOpenExternal)
		s.api("POST /api/native/save-file", s.handleNativeSaveFile)
	}
	s.api("GET /api/tools", s.handleListTools)
	s.api("GET /api/skills", s.handleListSkills)
	s.api("GET /api/workflows", s.handleListWorkflows)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/version", s.handleVersion)
	s.api("GET /api/channels", s.handleListChannels)
	s.api("GET /api/channels/available", s.handleAvailableChannels)
	s.api("GET /api/channels/{platform}", s.handleGetChannel)
	s.api("POST /api/channels/{platform}", s.handleSaveChannel)
	s.api("POST /api/channels/{platform}/reload", s.handleReloadChannel)
	s.api("DELETE /api/channels/{platform}", s.handleDeleteChannel)
	s.api("POST /api/channels/{platform}/test", s.handleTestChannel)
	s.api("GET /api/channels/recipients", s.handleChannelRecipients)
	s.api("POST /api/channels/{platform}/send", s.handleChannelSendText)
	s.api("POST /api/channels/{platform}/send-file", s.handleChannelSendFile)
	s.api("POST /api/channels/weixin/login", s.handleWeixinLoginStart)
	s.api("GET /api/channels/weixin/login", s.handleWeixinLoginStatus)
	s.api("DELETE /api/channels/weixin/login", s.handleWeixinLoginCancel)
	s.api("GET /api/tasks", s.handleListTasks)
	s.api("POST /api/tasks", s.handleCreateTask)
	s.api("DELETE /api/tasks/{id}", s.handleDeleteTask)
	s.api("POST /api/tasks/{id}/run", s.handleRunTask)
	s.api("PATCH /api/tasks/{id}", s.handlePatchTask)
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
	s.api("GET /api/config/endpoints", s.handleGetEndpoints)
	s.api("PUT /api/config/show_reasoning", s.handlePutShowReasoning)
	s.api("PUT /api/config/coauthor", s.handlePutCoauthor)
	s.api("PUT /api/config/language", s.handlePutLanguage)
	s.api("PUT /api/config/workspace_dir", s.handlePutWorkspaceDir)
	s.api("POST /api/config/test", s.handleTestConfig)
	// PR5: endpoint-level CRUD (design §10.2). The old /api/config/models*
	// routes are deleted — callers must use these.
	s.api("POST /api/config/endpoints", s.handleCreateEndpoint)
	s.api("PATCH /api/config/endpoints/{id}", s.handleUpdateEndpoint)
	s.api("DELETE /api/config/endpoints/{id}", s.handleDeleteEndpoint)
	s.api("POST /api/config/endpoints/{id}/models", s.handleAddEndpointModel)
	s.api("DELETE /api/config/endpoints/{id}/models/{model}", s.handleDeleteEndpointModel)
	s.api("POST /api/config/endpoints/{id}/default", s.handleSetEndpointDefault)
	s.api("POST /api/config/endpoints/{id}/lite", s.handleSetEndpointLite)

	// Browser automation setup
	s.api("GET /api/browser/status", s.handleBrowserStatus)
	s.api("POST /api/browser/verify", s.handleBrowserVerify)
	s.api("GET /api/browser/recordings", s.handleListBrowserRecordings)
	s.api("GET /api/browser/recordings/{name}", s.handleGetBrowserRecording)
	s.api("PUT /api/browser/recordings/{name}", s.handleSaveBrowserRecording)
	s.api("DELETE /api/browser/recordings/{name}", s.handleDeleteBrowserRecording)

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

	// Workflow detail, delete & export (list is registered above)
	s.api("GET /api/workflows/{name}", s.handleGetWorkflow)
	s.api("DELETE /api/workflows/{name}", s.handleDeleteWorkflow)
	s.api("GET /api/workflows/{name}/export", s.handleExportWorkflow)

	// MCP server management
	s.api("GET /api/mcp/servers", s.handleListMCPServers)
	s.api("GET /api/mcp/servers/{name}", s.handleGetMCPServer)
	s.api("POST /api/mcp/servers", s.handleCreateMCPServer)
	s.api("DELETE /api/mcp/servers/{name}", s.handleDeleteMCPServer)
	s.api("PATCH /api/mcp/servers/{name}/toggle", s.handleToggleMCPServer)
	s.api("POST /api/mcp/servers/{name}/reconnect", s.handleReconnectMCPServer)
	s.api("POST /api/mcp/servers/{name}/oauth/start", s.handleStartMCPOAuth)
	s.api("GET /api/mcp/servers/{name}/oauth/status", s.handleMCPOAuthStatus)
	s.mux.HandleFunc("GET /api/mcp/servers/{name}/oauth/callback", s.handleMCPOAuthCallback)
	s.api("POST /api/mcp/reload", s.handleReloadMCP)
	s.api("GET /api/config/toolsearch", s.handleGetToolSearch)
	s.api("PUT /api/config/toolsearch", s.handlePutToolSearch)

	// Benchmark
	s.api("POST /api/sessions/{id}/benchmark", s.handleBenchmark)

	// Memory detail
	s.api("GET /api/memories/{filename}", s.handleGetMemory)
	s.api("DELETE /api/memories/{filename}", s.handleDeleteMemory)

	// Version & restart
	s.api("POST /api/version/upgrade", s.handleVersionUpgrade)
	s.api("POST /api/restart", s.handleRestart)

	s.api("GET /ws", s.handleWS)

	// Static files (Web UI) — served from embedded filesystem.
	s.mux.Handle("/", s.staticHandler())
}

// corsMiddleware wraps the mux with CORS headers when configured, plus a
// built-in allowance for VS Code webview origins (isVSCodeWebviewOrigin) and
// the Obsidian desktop origin (isObsidianDesktopOrigin) so the octo VS Code
// extension's and Obsidian plugin's REST calls work without a --cors flag.
// That case can't reuse the CORSOrigins-driven fast path below, so the middleware
// now always runs rather than short-circuiting to next when CORSOrigins is
// empty; a bare OPTIONS request with no Origin header and no --cors
// configured now gets 204 instead of falling through to the mux, which is
// harmless (no client relies on that path 404ing/405ing).
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := isVSCodeWebviewOrigin(origin) || isObsidianDesktopOrigin(origin)
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

	s.askSlotsMu.Lock()
	delete(s.askSlots, id)
	s.askSlotsMu.Unlock()
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
// projectHooksTrusted reports whether the project-level hooks.yml at cwd is
// trusted (its current content fingerprint is in the trust store). The launch
// cwd is auto-trusted at New; other directories a session's working_dir is
// switched into are trusted only if a prior CLI run approved them — so a
// working_dir retarget can't silently load an untrusted repo's hooks.
func (s *Server) projectHooksTrusted(cwd string) bool {
	path := hooks.ProjectConfigPath(cwd)
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return hooks.IsTrusted(path, hooks.Fingerprint(b))
}

func (s *Server) buildAgent(sess *agent.Session) *agent.Agent {
	sender, model := s.senderForSession(sess)
	a := agent.New(sender, model)
	cwd, envCtx := s.sessionCwdEnv(sess)
	a.CWD = cwd
	a.MaxTokens = s.cfg.MaxTokens
	if s.goalsEnabled.Load() {
		// The session is the goal accountant on EVERY turn path built from
		// it (WS, REST, scheduled) — a goal created over one transport
		// must keep accounting when later turns arrive over another.
		a.GoalAcct = sess
	}
	if dir, err := sess.ChunkDir(); err == nil {
		a.ArchiveDir = dir // recall folded turns via the read tool
	}
	// Loaded once and reused below for effectiveCoauthor — a second
	// config.Load() there would re-read and re-parse the same file.
	// LoadCached (not Load): a config.yml that currently fails to parse
	// falls back to the last one that did, so a hand-edit typo degrades the
	// lite-model/coauthor resolution below to stale-but-correct settings
	// instead of the hardcoded defaults every live session would otherwise
	// revert to for as long as the file stays broken.
	cfg, cfgErr := config.LoadCached()
	if cfgErr == nil {
		a.LiteSender, a.LiteModel = s.liteSenderFromConfig(cfg)
		if a.LiteSender == nil {
			// No explicit lite entry — fall back to the vendor's registry
			// lite model on the session's OWN sender, keeping compaction on
			// the endpoint, key, and prompt cache the conversation uses.
			prov, baseURL := s.getProvider(), resolveBaseURL(s.getProvider(), cfg)
			if entry, ok := cfg.EntryByModel(sess.ModelConfig); ok {
				prov, baseURL = entry.Provider, entry.BaseURL
			}
			if lm := app.ImplicitLiteModel(prov, a.Model, baseURL); lm != "" {
				a.LiteSender = sender
				a.LiteModel = lm
			}
		}
		// Honor the configured auto-compaction threshold, the same way the CLI
		// does (cmd/octo/chat.go). Without this the server left CompactAutoFraction
		// at zero, so every web/desktop/IM turn fell back to the built-in 75%
		// default and compact_auto_pct in config.yml was silently ignored.
		if cfg.CompactAutoPct > 0 {
			a.CompactAutoFraction = float64(cfg.CompactAutoPct) / 100.0
		}
	}

	// Refresh the external memory backend from config before reading
	// MemoryBackendGuidance()/registering its hooks below — both need this
	// turn's config, not whatever the last turn (of any kind) left set. See
	// app.RefreshMemoryBackend's doc comment for why prepareToolTurn alone
	// (which runs after this function returns) is one turn too late.
	app.RefreshMemoryBackend()

	// L1: project memory embedded in the system prompt (stable across turns).
	var memInjection string
	if s.memDir != "" {
		memInjection = memory.RenderInjection(s.memDir, s.homeMemDir)
	}
	if g := tools.MemoryBackendGuidance(); g != "" {
		memInjection = strings.TrimSpace(memInjection + "\n\n" + g)
	}
	a.System, a.LeanSystem = prompt.ComposePair(s.system, cwd, envCtx, s.curSkillsManifest(), tools.MCPManifestFor(model), memInjection, s.effectiveCoauthor(cfg))

	// L2: attention-layer rules (triggered keywords) + save-nudge on milestone
	// tool results, plus any shell hooks (env/hooks.yml), unified on the agent's
	// hook engine. Shares the process seen-set so SessionStart resume fires once
	// per OS process across the serve process's many sessions.
	hookEngine := hooks.EngineFromEnvAndFiles(hooks.SharedSeen(), cwd, s.projectHooksTrusted(cwd))
	hookEngine.Notify = func(m string) { slog.Warn("hook", "err", m) }
	if s.memDir != "" {
		s.injectorFor(sess.ID).RegisterHooks(hookEngine)
	}
	// Workflow save-nudge — memory-independent, wired for every session.
	tools.NewWorkflowNudger().RegisterHooks(hookEngine)
	// Validate ~/.octo/config.yml right after the agent edits it.
	tools.NewConfigGuard().RegisterHooks(hookEngine)
	// Auto-store into the external memory backend (if configured) — a no-op
	// when none is set.
	tools.RegisterMemoryBackendHooks(hookEngine)
	a.Hooks = hookEngine
	a.HookMeta = hooks.Meta{SessionID: sess.ID, Transport: sess.BoundEntry, Cwd: cwd}
	if p, err := sess.SavePath(); err == nil {
		a.HookMeta.TranscriptPath = p
	}
	a.SessionStarted = sess.HookStarted || len(sess.Messages) > 0
	a.OnSessionStart = func() { sess.MarkHookStarted() }

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
	// Protocol is meaningful only for the Custom vendor, and only when the
	// resolved provider actually matches the config entry (same rule as key/model).
	protocol := ""
	if provName == entry.Provider {
		protocol = entry.Protocol
	}
	sender, err := app.NewSender(app.SenderOptions{
		Provider:        provName,
		APIKey:          apiKey,
		BaseURL:         resolveBaseURL(provName, cfg),
		Protocol:        protocol,
		ReasoningEffort: cfg.ReasoningEffort,
		ShowReasoning:   cfg.EffectiveShowReasoning(nil),
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

// effectiveCoauthor resolves whether the system prompt should instruct the
// agent to append a Co-authored-by line to git commits, for every code path
// that composes one via prompt.ComposePair. Before this existed, every one
// of those call sites passed a hardcoded `true` and never consulted
// cfg.Coauthor at all — the CLI (cmd/octo) correctly resolved it, but every
// web/API/channel turn ignored the setting entirely, coauthoring commits
// unconditionally no matter what config.yml said.
//
// Takes an already-loaded cfg instead of loading its own: every call site
// either already has one in scope for another purpose (buildAgent,
// buildChannelFactory need it for LiteSender resolution too) or loads exactly
// one for this call — either way, avoiding a second disk read + YAML parse
// of config.yml per turn. A zero-value cfg (config.Load failed) still
// resolves correctly, since EffectiveCoauthor checks OCTO_COAUTHOR before
// falling back to its built-in default (true) — unlike the old inline
// "config.Load() failed → return true" branch this replaced, which skipped
// the env check entirely on a load error.
func (s *Server) effectiveCoauthor(cfg config.Config) bool {
	return cfg.EffectiveCoauthor()
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

	cfg, err := config.LoadCached()
	if err != nil {
		return defaultSender, model
	}
	entry, ok := cfg.EntryByModel(sess.ModelConfig)
	if !ok {
		return defaultSender, model
	}
	if entry.Model != "" {
		model = entry.Model
	}

	sender, err := s.cachedSenderForEntry(sess.ModelConfig, entry)
	if err != nil {
		return defaultSender, model
	}
	return sender, model
}

// cachedSenderForEntry returns the entry's sender from the cache, building
// and caching it on first use.
//
// PR3 (design §9): the cache key is the raw model reference (ref), not
// entry.Model. ref is whatever the caller passed to cfg.EntryByModel — it's
// either a bare model string (legacy session, same key as before) or a
// composite id "<endpoint_id>::<model>" (new session, distinguishes the
// same model on two different endpoints). Using entry.Model as the key
// would collide across endpoints (claude-sonnet-4-6 on relay-a vs on
// official anthropic would share one cache entry, returning the wrong
// sender). The ref is the unambiguous identifier.
func (s *Server) cachedSenderForEntry(ref string, entry config.ModelEntry) (agent.Sender, error) {
	s.senderCacheMu.Lock()
	defer s.senderCacheMu.Unlock()
	key := ref
	if key == "" {
		key = entry.Model // fallback: no ref (rare) → behave like the old code
	}
	if cached, ok := s.senderCache[key]; ok {
		return cached, nil
	}
	sender, err := senderForEntry(entry)
	if err != nil {
		return nil, err
	}
	if s.senderCache == nil {
		s.senderCache = make(map[string]agent.Sender)
	}
	s.senderCache[key] = sender
	return sender, nil
}

// invalidateEndpointSenders drops every cached sender whose key is prefixed
// with "<endpointID>::". Called when an endpoint's connection params change
// (base_url, api_key, protocol) or the endpoint is renamed — every model
// under that endpoint must rebuild its sender because the connection it was
// built against is stale. Other endpoints' cached senders stay (hit rate
// preserved). Design §9.2.
func (s *Server) invalidateEndpointSenders(endpointID string) {
	if endpointID == "" {
		return
	}
	prefix := endpointID + "::"
	s.senderCacheMu.Lock()
	defer s.senderCacheMu.Unlock()
	for k := range s.senderCache {
		if strings.HasPrefix(k, prefix) {
			delete(s.senderCache, k)
		}
	}
}

// liteSenderFromConfig resolves the configured lite entry to a (sender,
// model) pair for compaction, or (nil, "") when none is configured or it
// can't be built — the agent then compacts on its primary sender.
//
// PR4 note: this currently passes cfg.LiteModel (the legacy bare-model
// field) as the cache ref, so the lite sender's cache key has no
// "<endpointID>::" prefix — invalidateEndpointSenders can't reach it even
// when the lite model lives under the endpoint being invalidated. Once
// PR4 switches Save to emit endpoints: and cfg.Lite is populated on Load,
// switch this arg to cfg.Lite so the lite sender participates in per-endpoint
// invalidation (§9.2).
func (s *Server) liteSenderFromConfig(cfg config.Config) (agent.Sender, string) {
	entry, ok := cfg.EntryByModel(cfg.Lite)
	if !ok || entry.Model == "" {
		return nil, ""
	}
	sender, err := s.cachedSenderForEntry(cfg.Lite, entry)
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
		return nil, fmt.Errorf("no API key for model %q (provider %q)", entry.Model, entry.Provider)
	}
	cfg, _ := config.LoadCached()
	return app.NewSender(app.SenderOptions{
		Provider:        entry.Provider,
		APIKey:          apiKey,
		BaseURL:         entry.BaseURL,
		Protocol:        entry.Protocol,
		ReasoningEffort: cfg.ReasoningEffort,
		ShowReasoning:   cfg.EffectiveShowReasoning(nil),
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

// curWorkspaceDir returns the server's default workspace dir under a read lock.
func (s *Server) curWorkspaceDir() string {
	s.workspaceDirMu.RLock()
	defer s.workspaceDirMu.RUnlock()
	return s.workspaceDir
}

// setWorkspaceDir replaces the server's default workspace dir under a write
// lock. Called by handlePutWorkspaceDir when the setting changes.
func (s *Server) setWorkspaceDir(dir string) {
	s.workspaceDirMu.Lock()
	s.workspaceDir = dir
	s.workspaceDirMu.Unlock()
}

// curCwd returns the server's working directory under a read lock.
func (s *Server) curCwd() string {
	s.cwdMu.RLock()
	defer s.cwdMu.RUnlock()
	return s.cwd
}

// curCwdEnv returns the server default working directory and its env-context
// string atomically under a single read lock, so callers that need both
// (prompt composition) never observe a torn pair.
func (s *Server) curCwdEnv() (string, string) {
	s.cwdMu.RLock()
	defer s.cwdMu.RUnlock()
	return s.cwd, s.envCtx
}

// sessionCwdEnv returns the working directory and env-context a turn for sess
// should run in: the session's own WorkingDir (with a freshly built env context
// — cheap, DetectToolchain is a PATH lookup) when it has one, otherwise the
// server default. Threading it through buildAgent means tool cwd, prompt env,
// and project-hook trust all follow the same per-session directory, so one
// session retargeting its dir can't silently move another's tools.
func (s *Server) sessionCwdEnv(sess *agent.Session) (string, string) {
	if sess != nil && sess.WorkingDir != "" {
		return sess.WorkingDir, buildEnvContext(sess.WorkingDir)
	}
	return s.curCwdEnv()
}

// sessionCwd returns the session's own working dir, or the server default when
// it has none. Used by the status/list surfaces so the UI shows where a
// session's tools actually run rather than the server launch dir.
func (s *Server) sessionCwd(sess *agent.Session) string {
	if sess != nil && sess.WorkingDir != "" {
		return sess.WorkingDir
	}
	return s.curCwd()
}

// sessionCwdByID resolves a session's working dir from its id alone, for the WS
// status pushes that only carry a session id. A live turn's agent (registered
// in sessionAgents) already holds the resolved cwd, so the common case avoids a
// disk read; a cold session falls back to loading its persisted WorkingDir,
// then to the server default.
func (s *Server) sessionCwdByID(sessionID string) string {
	s.sessionAgentsMu.Lock()
	a := s.sessionAgents[sessionID]
	s.sessionAgentsMu.Unlock()
	if a != nil && a.CWD != "" {
		return a.CWD
	}
	if sess, err := agent.LoadSession(sessionID); err == nil && sess.WorkingDir != "" {
		return sess.WorkingDir
	}
	return s.curCwd()
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
	if s.sender != nil {
		s.senderMu.Unlock()
		return nil
	}
	sender, model, provName, err := resolveProviderAndModel(s.cfg.Provider, s.cfg.Model)
	if err != nil {
		s.senderMu.Unlock()
		return err
	}
	if sender == nil {
		s.senderMu.Unlock()
		return fmt.Errorf("server not configured: complete setup via the Web UI")
	}
	s.sender = sender
	s.model = model
	s.provider = provName
	enableTools := s.cfg.Tools
	if enableTools {
		// Sync goal.enabled before releasing senderMu, not after. Once
		// unlocked, getSender() != nil is visible to other goroutines —
		// initChannels/reloadChannel gate solely on that and construct a
		// channel.Manager immediately, locking in whatever s.goalsEnabled
		// holds at that instant via SetGoalsEnabled. reloadChannel only
		// re-syncs when it constructs a fresh manager, so a channel-settings
		// save landing in the gap between unlock and enableSubAgentTools
		// (below) finishing would otherwise capture a stale value
		// permanently. syncGoalsEnabled needs no lock of its own, so running
		// it here adds no deadlock risk.
		s.syncGoalsEnabled()
	}
	s.senderMu.Unlock()

	// enableSubAgentTools reads the default sender via defaultSenderAndModel,
	// which takes senderMu — so it MUST run after the unlock above. senderMu is
	// not reentrant; calling it while still holding the lock self-deadlocks and
	// hangs every subsequent turn. (The startup path in New() enables tools
	// before any lock is held, which is why only this lazy onboarding-completion
	// path hit the deadlock — and why restarting the server worked around it.)
	if enableTools {
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

// reloadDefaultSender rebuilds the server's default sender and model from the
// current config so existing unbound sessions pick up the change on their next
// turn. Called whenever a global setting or a model-config entry changes. In
// onboarding mode (no API key yet) the sender stays nil, but s.model is still
// refreshed from config so a session created before ensureSender runs uses the
// right model.
func (s *Server) reloadDefaultSender() error {
	s.senderMu.Lock()
	defer s.senderMu.Unlock()
	sender, model, provName, err := resolveProviderAndModel(s.cfg.Provider, s.cfg.Model)
	if err != nil {
		return err
	}
	s.model = model
	if sender != nil {
		s.sender = sender
		s.provider = provName
	}
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
	return a.ask(ctx, q, false)
}

// AskSecret implements tools.SecretAsker: the same question bridge, but the
// event is flagged secret so the browser renders a password field. The answer
// returns to the runtime caller only — it never becomes a tool result or
// lands in conversation history.
func (a wsAsker) AskSecret(ctx context.Context, question string) (string, bool, error) {
	res, err := a.ask(ctx, tools.AskRequest{Question: question}, true)
	if err != nil {
		return "", false, err
	}
	if res.Cancelled {
		return "", true, nil
	}
	return res.Custom, false, nil
}

func (a wsAsker) ask(ctx context.Context, q tools.AskRequest, secret bool) (tools.AskResponse, error) {
	sessionID, ok := ctx.Value(ctxKeySessionID{}).(string)
	if !ok || sessionID == "" {
		return tools.AskResponse{}, fmt.Errorf("ask_user_question: no active WebSocket session")
	}

	// Serialize against other interactive prompts for this session (the same
	// slot requestConfirmation uses) so concurrent askers don't clobber the
	// single pending slot / frontend modal.
	release, err := a.s.acquireAskSlot(ctx, sessionID)
	if err != nil {
		return tools.AskResponse{Cancelled: true}, nil
	}
	defer release()

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
		Secret:      secret,
	}

	// Record the outstanding question so a tab that (re)subscribes mid-ask —
	// e.g. after a page refresh — gets it replayed instead of a dead spinner.
	a.s.pendingPromptMu.Lock()
	a.s.pendingQuestions[sessionID] = ev
	a.s.pendingPromptMu.Unlock()

	// dismiss both closes the modal on every tab (not just the one that
	// answered — without this, a tab that didn't answer keeps showing a dead
	// modal) and clears the cross-session "pending question" signal so the
	// sidebar badge / notification logic knows this session is no longer
	// waiting.
	dismiss := func() {
		a.s.wsHub.broadcast(sessionID, wsEventDismissUserQuestion{
			Type:       "dismiss_user_question",
			SessionID:  sessionID,
			QuestionID: qid,
		})
		a.s.wsHub.broadcast("", wsEventSessionActivity{
			Type:      "session_activity",
			SessionID: sessionID,
			Kind:      "question_resolved",
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
	a.s.wsHub.broadcast("", wsEventSessionActivity{
		Type:      "session_activity",
		SessionID: sessionID,
		Kind:      "question_pending",
	})

	// No timeout branch: an attended surface (a tab is open on this session
	// or could be) must wait for an actual answer, not silently give up on a
	// user who stepped away. Unattended contexts never reach here at all —
	// the tool isn't advertised when activeAsker is nil.
	select {
	case res := <-ch:
		cleanup()
		dismiss()
		return res, nil
	case <-ctx.Done():
		cleanup()
		dismiss()
		return tools.AskResponse{Cancelled: true}, nil
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
//
// #1105: the message alone ("Allow terminal?") gave the user nothing to
// judge — they authorized blind. buildConfirmDetail fills in what's
// actually being approved (the full command, the pending diff, or a
// generic listing of the tool's input) so ConfirmModal has something to
// render.
func (s *Server) permissionAskFrom(sessionID string) app.PermissionAsk {
	return func(ctx context.Context, toolName string, toolInput map[string]any) (bool, bool, error) {
		msg := fmt.Sprintf("Allow %s?", toolName)
		detail := buildConfirmDetail(toolName, toolInput)
		result, err := s.requestConfirmation(ctx, sessionID, msg, "yes_no_always", detail)
		if err != nil {
			return false, false, err
		}
		allow, remember := mapConfirmResult(result)
		return allow, remember, nil
	}
}

// buildConfirmDetail renders a tool's pending input for the permission
// modal. Terminal gets the literal command; edit_file gets the same
// removed/added preview as the post-execution result card (tools.EditUIDiff);
// everything else falls back to a sorted, length-capped key: value listing.
func buildConfirmDetail(toolName string, toolInput map[string]any) confirmDetail {
	detail := confirmDetail{ToolName: toolName}
	switch toolName {
	case "terminal":
		if cmd, ok := toolInput["command"].(string); ok {
			detail.Command = cmd
		}
	case "edit_file":
		// Best-effort preview: this is the literal old_string/new_string the
		// model asked for, not the post-normalization result EditFileTool.Execute
		// actually writes (it additionally runs findActualString/preserveQuoteStyle/
		// preserveIndentStyle against the real file — see edit_file.go). Re-running
		// that whole resolution here would mean reading the file a second time
		// before the user has even approved touching it. What matters for a
		// permission ask is showing what the model is trying to inject; cosmetic
		// quote/indent drift, or an old_string that turns out not to match, is
		// caught (safely, no write happens) when the tool actually runs.
		oldStr, okOld := toolInput["old_string"].(string)
		newStr, okNew := toolInput["new_string"].(string)
		if okOld && okNew && (oldStr != "" || newStr != "") {
			detail.Diff = tools.EditUIDiff(oldStr, newStr)
		} else {
			detail.Input = formatToolInputForConfirm(toolInput)
		}
	default:
		detail.Input = formatToolInputForConfirm(toolInput)
	}
	return detail
}

// formatToolInputForConfirm renders a tool's input map as sorted "key: value"
// lines for display in the permission modal. Multi-line values show their
// first 4 lines; each line is capped at 200 runes. Mirrors (at reduced
// complexity — no terminal wrapping/control-byte concerns in an HTML <pre>)
// the detail rules from the TUI's #1101 fix.
func formatToolInputForConfirm(toolInput map[string]any) string {
	if len(toolInput) == 0 {
		return ""
	}
	keys := make([]string, 0, len(toolInput))
	for k := range toolInput {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	const maxLineRunes = 200
	const maxValueLines = 4
	const maxKeys = 40 // defensive cap: per-value length/lines are bounded above, but not key count
	capLine := func(s string) string {
		r := []rune(s)
		if len(r) > maxLineRunes {
			return string(r[:maxLineRunes]) + "…"
		}
		return s
	}

	truncatedKeys := 0
	if len(keys) > maxKeys {
		truncatedKeys = len(keys) - maxKeys
		keys = keys[:maxKeys]
	}

	var b strings.Builder
	for _, k := range keys {
		val := fmt.Sprintf("%v", toolInput[k])
		lines := strings.Split(val, "\n")
		if len(lines) == 1 {
			fmt.Fprintf(&b, "%s: %s\n", k, capLine(lines[0]))
			continue
		}
		fmt.Fprintf(&b, "%s:\n", k)
		shown := lines
		folded := false
		if len(shown) > maxValueLines {
			shown = shown[:maxValueLines]
			folded = true
		}
		for _, l := range shown {
			fmt.Fprintf(&b, "  %s\n", capLine(l))
		}
		if folded {
			fmt.Fprintf(&b, "  … +%d more lines\n", len(lines)-maxValueLines)
		}
	}
	if truncatedKeys > 0 {
		fmt.Fprintf(&b, "… +%d more fields\n", truncatedKeys)
	}
	return strings.TrimRight(b.String(), "\n")
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
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	s.channelCfg = chCfg
	platforms := chCfg.EnabledPlatforms()
	if len(platforms) == 0 {
		// Nothing enabled yet. Keep channelCfg so reloadChannel can lazily
		// build the manager when the user adds a channel later — no full
		// server restart needed.
		return
	}
	s.channelMgr = channel.NewManager(chCfg, s.buildChannelFactory(), channel.BindByChatUser)
	s.channelMgr.SetGoalsEnabled(s.goalsEnabled.Load())
	s.channelMgr.SetModelOps(s.channelModelOps())
	slog.Info("channels enabled", "platforms", strings.Join(platforms, ", "))
}

// buildChannelFactory returns the agent factory the channel manager uses to
// spin up a fresh agent per IM session. No gate is set here: handleChannelMessage
// builds a fresh per-turn gate (configured mode + chat-interactive ask), the
// same shape prepareToolTurn gives web turns. A factory-time gate would freeze
// one policy snapshot for the session's whole life.
// System/LeanSystem and CWD are deliberately NOT set here (unlike buildAgent):
// runChannelTurns unconditionally recomposes both on every IM turn before the
// first LLM call ever happens, so baking them here once at session-creation
// time only produced a value nothing would ever read. MaxTokens and the
// LiteSender/LiteModel resolved below have no such per-turn refresh anywhere
// in the IM path, so they stay — this is genuinely the only place they're set
// for the session's whole lifetime.
func (s *Server) buildChannelFactory() func() *agent.Agent {
	return func() *agent.Agent {
		defaultSender, model := s.defaultSenderAndModel()
		a := agent.New(defaultSender, model)
		a.MaxTokens = s.cfg.MaxTokens
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
}

// channelModelOps builds the ModelOps the channel manager's /model command
// runs against: the configured endpoint+model table for the no-argument
// listing (grouped by endpoint, PR4b design §12.2), and a resolver mapping a
// model id to a (cached) sender. "default" unbinds the session back to the
// server's default sender, mirroring the web model picker's "default" entry
// (handleUpdateSessionModel). Unknown ids are rejected with the configured
// list — sending an unconfigured model name to the current endpoint would hit
// the wrong protocol/base URL.
//
// Resolve accepts three forms (design §5.4, §12.2):
//   - "default" — unbind, ride the server default.
//   - Composite id "<endpoint>::<model>" — precise; resolved via
//     cfg.ParseModelFlag so a missing endpoint or model produces the exact
//     "available: …" error.
//   - Bare model "<model>" — legacy back-compat; goes through ParseModelFlag's
//     default-priority path (default endpoint wins; ambiguous match picks
//     the first with a warning).
//
// ModelResolution.BoundEntry is the composite id — it's what gets persisted
// to the channel store so the binding survives restarts and round-trips
// through applyChannelModel (which reads it back via EntryByModel, itself
// composite-id-aware since PR2).
func (s *Server) channelModelOps() *channel.ModelOps {
	return &channel.ModelOps{
		List: func() []channel.ModelInfo {
			cfg, _ := config.LoadCached()
			infos := make([]channel.ModelInfo, 0, len(cfg.Endpoints))
			for _, ep := range cfg.Endpoints {
				for _, m := range ep.Models {
					cid := ep.CompositeID(m.Model)
					infos = append(infos, channel.ModelInfo{
						EndpointID:   ep.ID,
						EndpointName: ep.Name,
						Model:        m.Model,
						CompositeID:  cid,
						Provider:     ep.Provider,
						Default:      cid == cfg.Default,
					})
				}
			}
			return infos
		},
		Resolve: func(modelID string) (channel.ModelResolution, error) {
			if modelID == "default" {
				sender, model := s.defaultSenderAndModel()
				if model == "" {
					return channel.ModelResolution{}, fmt.Errorf("no default model configured")
				}
				return channel.ModelResolution{Sender: sender, Model: model}, nil
			}
			cfg, err := config.Load()
			if err != nil {
				return channel.ModelResolution{}, fmt.Errorf("loading config: %w", err)
			}
			ep, m, perr := cfg.ParseModelFlag(modelID)
			if perr != nil {
				return channel.ModelResolution{}, perr
			}
			entry := config.ModelEntry{
				Provider: ep.Provider,
				Model:    m.Model,
				BaseURL:  ep.BaseURL,
				APIKey:   ep.APIKey,
				Protocol: ep.Protocol,
				Vision:   m.Vision,
			}
			cid := ep.CompositeID(m.Model)
			sender, err := s.cachedSenderForEntry(cid, entry)
			if err != nil {
				return channel.ModelResolution{}, err
			}
			return channel.ModelResolution{Sender: sender, Model: m.Model, BoundEntry: cid}, nil
		},
	}
}

// applyChannelModel points the session's long-lived agent at the sender+model
// its backing store is bound to, so a binding set via IM /model or the web
// model picker takes effect on IM turns too — without it the factory's
// default, baked in at session creation, would silently win. Senders are
// swapped only when the persisted binding actually changes (tracked by
// sess.AppliedModelConfig): a never-bound session keeps whatever sender the
// factory injected. A binding that no longer resolves (entry deleted, key
// gone) falls back to the default, warning once, rather than failing the
// turn — the same degradation senderForSession gives REST turns.
func (s *Server) applyChannelModel(sess *channel.Session) {
	st := sess.Store
	if st == nil {
		return
	}

	if st.ModelConfig != "" {
		cfg, _ := config.LoadCached()
		if entry, ok := cfg.EntryByModel(st.ModelConfig); ok {
			if st.ModelConfig == sess.AppliedModelConfig {
				return // binding already live on the agent
			}
			if sender, err := s.cachedSenderForEntry(st.ModelConfig, entry); err != nil {
				slog.Warn("channel session model sender unavailable; using the default", "session", st.ID, "model", st.ModelConfig, "err", err)
				s.applyChannelDefault(sess, st)
			} else {
				sess.Agent.SetSender(sender)
				sess.Agent.Model = entry.Model
				sess.AppliedModelConfig = entry.Model
			}
			return
		}
		// The bound entry is gone: fall back to the default, warning only on
		// the live→stale flip instead of every turn.
		if sess.AppliedModelConfig == st.ModelConfig {
			slog.Warn("channel session bound to an unknown model entry; using the default", "session", st.ID, "model", st.ModelConfig)
		}
		s.applyChannelDefault(sess, st)
		return
	}

	// Unbound: only undo a sender a previous apply swapped in. A never-bound
	// session just tracks the store's raw model name (usually the factory's
	// own — a no-op) and keeps its factory sender.
	if sess.AppliedModelConfig != "" {
		s.applyChannelDefault(sess, st)
		return
	}
	if st.Model != "" {
		sess.Agent.Model = st.Model
	}
}

// applyChannelDefault runs the session on the server's default sender. The
// model comes from the store when set (the binding's model name or a raw
// override), matching how senderForSession degrades for REST turns.
func (s *Server) applyChannelDefault(sess *channel.Session, st *agent.Session) {
	sender, model := s.defaultSenderAndModel()
	if st.Model != "" {
		model = st.Model
	}
	sess.Agent.SetSender(sender)
	sess.Agent.Model = model
	sess.AppliedModelConfig = ""
}

// startChannels launches all enabled channel adapters in background goroutines.
func (s *Server) startChannels() {
	if s.cfg.NoChannel {
		return
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	if s.channelMgr == nil {
		return
	}
	for _, name := range s.channelCfg.EnabledPlatforms() {
		s.startOneChannelLocked(name)
	}
}

// channelManager returns the channel manager under channelMu. reloadChannel can
// build it lazily at runtime, so readers on other goroutines (notably the
// send_message tool via serverMessenger) must snapshot it through here rather
// than touching the field directly — a plain read would race the lazy write.
// Adapter-path reads (routeChannelEvent/handleChannelMessage) are exempt: their
// goroutines are only spawned after the manager is set under the same lock, so
// the write happens-before those reads.
func (s *Server) channelManager() *channel.Manager {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	return s.channelMgr
}

// channelBaseCtxLocked returns the base context every adapter goroutine derives
// from, creating it on first use. stopChannels cancels it to bring them all
// down. Caller holds channelMu.
func (s *Server) channelBaseCtxLocked() context.Context {
	if s.channelCtx == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.channelCtx = ctx
		s.channelCancel = cancel
	}
	return s.channelCtx
}

// startOneChannelLocked builds and launches a single platform adapter from the
// current channelCfg, tracking its own cancel so it can be stopped
// independently. No-op if it is already running or its config is missing/
// invalid. Caller holds channelMu.
func (s *Server) startOneChannelLocked(name string) {
	if _, ok := s.runningAdapters.Load(name); ok {
		return // already running
	}
	pc := s.channelCfg.Platform(name)
	if pc == nil {
		return
	}
	ctor, err := channel.Find(name)
	if err != nil {
		slog.Error("channel start failed", "channel", name, "err", err)
		s.recordChannelIssue(name, "adapter not registered: "+err.Error())
		return
	}
	ad, err := ctor(pc)
	if err != nil {
		slog.Error("channel adapter failed", "channel", name, "err", err)
		s.recordChannelIssue(name, "construction failed: "+err.Error())
		return
	}
	if errs := ad.ValidateConfig(pc); len(errs) > 0 {
		for _, e := range errs {
			slog.Warn("channel config issue", "channel", name, "detail", e)
		}
		s.recordChannelIssue(name, "invalid config: "+strings.Join(errs, "; "))
		return
	}

	ctx, cancel := context.WithCancel(s.channelBaseCtxLocked())
	if s.adapterCancels == nil {
		s.adapterCancels = make(map[string]context.CancelFunc)
	}
	if s.channelGen == nil {
		s.channelGen = make(map[string]uint64)
	}
	s.channelGen[name]++
	gen := s.channelGen[name]
	s.adapterCancels[name] = cancel
	s.runningAdapters.Store(name, ad)
	s.clearChannelIssue(name)
	s.channelWG.Add(1)
	go s.runChannelWithRestart(ctx, name, gen, pc, ctor, ad)
}

// channelRestartDelays bounds how aggressively a crashed adapter retries: a
// short delay for the first attempt (a transient network blip recovers
// fast), backing off so a persistently broken platform (revoked auth, a dead
// endpoint) doesn't hot-loop.
var channelRestartDelays = []time.Duration{
	2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute,
}

// maxChannelRestarts caps how many times a crashing adapter is restarted
// before giving up — still crashing after this many attempts means something
// structurally broken (revoked credentials, a permanently unreachable
// endpoint), not a transient blip, and endless retries would just spam logs
// forever with nothing for the user to act on differently.
const maxChannelRestarts = 10

// runChannelWithRestart runs ad to completion, then — unless ctx was
// cancelled deliberately (Stop()/reloadChannel) — logs the crash, records it
// where the Channels web view and the channel-manager skill's doctor flow
// can see it (recordChannelIssue), and retries with backoff instead of
// leaving the platform silently dead (#1121: previously `_ = a.Start(...)`
// discarded the error entirely and nothing ever ran again). Each retry
// rebuilds a fresh adapter instance via ctor rather than reusing the crashed
// one — Start isn't guaranteed reentrant (each adapter owns its own
// connections, goroutines, and internal queues), so a clean instance is the
// only safe way to retry, matching what a full server restart would do
// anyway. Config is not re-validated on retries: ValidateConfig failures are
// a permanent skip (startOneChannelLocked never reaches this function for
// those), not something a backoff loop should keep re-attempting.
//
// gen is the generation snapshot startOneChannelLocked captured when it
// launched this goroutine (see channelGen's doc comment) — every write back
// to runningAdapters/adapterCancels re-checks it first, since a concurrent
// stop/reload/restart of this same platform can slip in during the ctor(pc)
// call below, which does not observe ctx cancellation.
func (s *Server) runChannelWithRestart(ctx context.Context, name string, gen uint64, pc channel.PlatformConfig, ctor func(channel.PlatformConfig) (channel.Adapter, error), ad channel.Adapter) {
	defer s.channelWG.Done()
	restarts := 0
	for {
		err := ad.Start(ctx, func(ev channel.InboundEvent) {
			ev.Platform = name
			s.routeChannelEvent(ctx, ad, ev)
		})
		if ctx.Err() != nil {
			return // deliberate stop, not a crash
		}

		restarts++
		slog.Error("channel adapter exited unexpectedly", "channel", name, "err", err, "restart_attempt", restarts)
		if restarts > maxChannelRestarts {
			slog.Error("channel adapter exceeded restart limit — giving up", "channel", name, "restarts", restarts)
			s.recordChannelIssue(name, fmt.Sprintf("gave up after %d crashes: %v", restarts, err))
			s.channelMu.Lock()
			if s.channelGen[name] == gen {
				s.runningAdapters.Delete(name)
				delete(s.adapterCancels, name)
			}
			s.channelMu.Unlock()
			return
		}
		s.recordChannelIssue(name, fmt.Sprintf("restarting (%d/%d) after crash: %v", restarts, maxChannelRestarts, err))

		delay := channelRestartDelays[len(channelRestartDelays)-1]
		if restarts-1 < len(channelRestartDelays) {
			delay = channelRestartDelays[restarts-1]
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		newAd, cerr := ctor(pc)
		if cerr != nil {
			slog.Error("channel restart: rebuild failed", "channel", name, "err", cerr, "restart_attempt", restarts)
			s.recordChannelIssue(name, fmt.Sprintf("restart %d/%d rebuild failed: %v", restarts, maxChannelRestarts, cerr))
			continue // try again next loop, same restart budget and backoff schedule
		}
		ad = newAd
		s.channelMu.Lock()
		if s.channelGen[name] != gen {
			// Superseded while ctor(pc) was in flight — a concurrent
			// stop/reload/restart now owns this platform name. ad was never
			// Start()-ed, so there's nothing to Stop(); just discard it and
			// let this goroutine end without touching the new owner's state.
			s.channelMu.Unlock()
			slog.Warn("channel restart: superseded during rebuild, discarding stale instance", "channel", name)
			return
		}
		s.runningAdapters.Store(name, ad)
		s.channelMu.Unlock()
		s.clearChannelIssue(name)
	}
}

// recordChannelIssue notes why a platform's adapter isn't healthy (a startup
// skip reason, or a crash/restart status), queryable via GET /api/channels
// and the channel-manager skill's doctor flow.
func (s *Server) recordChannelIssue(name, reason string) {
	s.channelIssuesMu.Lock()
	if s.channelIssues == nil {
		s.channelIssues = make(map[string]string)
	}
	s.channelIssues[name] = reason
	s.channelIssuesMu.Unlock()
}

// clearChannelIssue marks a platform healthy again (a (re)start succeeded).
func (s *Server) clearChannelIssue(name string) {
	s.channelIssuesMu.Lock()
	delete(s.channelIssues, name)
	s.channelIssuesMu.Unlock()
}

// channelIssue returns the recorded reason a platform isn't healthy, or ""
// when it's never had one recorded (running fine, or never started).
func (s *Server) channelIssue(name string) string {
	s.channelIssuesMu.Lock()
	defer s.channelIssuesMu.Unlock()
	return s.channelIssues[name]
}

// stopOneChannelLocked stops a single running adapter (cancel its context and
// call Stop). No-op if it isn't running. Caller holds channelMu.
func (s *Server) stopOneChannelLocked(name string) {
	if cancel, ok := s.adapterCancels[name]; ok {
		cancel()
		delete(s.adapterCancels, name)
	}
	if v, ok := s.runningAdapters.Load(name); ok {
		_ = v.(channel.Adapter).Stop()
		s.runningAdapters.Delete(name)
	}
	// A deliberate stop (disable/delete/reload) isn't a health problem — clear
	// any crash/restart status left over from before it, so re-enabling later
	// starts from a clean slate instead of showing a stale crash reason.
	s.clearChannelIssue(name)
}

// reloadChannel applies a saved config change for one platform without a full
// server restart: it re-reads channels.yml, stops the platform's adapter if
// running, then starts it again when the new config has it enabled. It handles
// a fresh add (manager built lazily), a credential change (stop + start), and
// a disable/delete (stop only). Called from the channels REST handlers after a
// successful save.
func (s *Server) reloadChannel(platform string) {
	if s.cfg.NoChannel || s.getSender() == nil {
		return
	}
	chCfg, err := channel.LoadConfig()
	if err != nil {
		slog.Error("channel reload: load config", "channel", platform, "err", err)
		return
	}

	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	s.channelCfg = chCfg
	if s.channelMgr == nil {
		s.channelMgr = channel.NewManager(chCfg, s.buildChannelFactory(), channel.BindByChatUser)
		s.channelMgr.SetGoalsEnabled(s.goalsEnabled.Load())
		s.channelMgr.SetModelOps(s.channelModelOps())
	}

	s.stopOneChannelLocked(platform)
	if chCfg.IsEnabled(platform) {
		s.startOneChannelLocked(platform)
		slog.Info("channel reloaded", "channel", platform)
	} else {
		slog.Info("channel stopped", "channel", platform)
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
	// The adapter invokes this from its own inbound goroutine (not net/http), so
	// a panic anywhere in the IM turn path would crash the whole serve process.
	defer s.recoverBg("channel event (" + ev.Platform + ")")
	if s.handleChannelCommand(ad, ev) {
		return
	}
	// Button events (e.g. inline keyboard presses on Telegram, component
	// clicks on Discord, card actions on Feishu) answer a pending ask directly,
	// bypassing the text-based DeliverAskReply path. Button events are routed
	// before plain text so they work even when askButtonsOnly is set.
	if ev.ButtonID != "" {
		if sess := s.channelMgr.GetSession(ev); sess != nil {
			if sess.DeliverAskButton(ev.ChatID, ev.UserID, ev.ButtonID) {
				return
			}
		}
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
	case "/bind", "/clear", "/new":
		imKey := "im:" + string(s.channelMgr.KeyFor(ev))
		s.rememberedMu.Lock()
		delete(s.rememberedStores, imKey)
		s.rememberedMu.Unlock()
		s.injectorMu.Lock()
		delete(s.sessionInjectors, imKey)
		s.injectorMu.Unlock()
		// A fresh context drops any armed loop wakeup for this chat.
		s.cancelWakeup(imKey)
	case "/unbind":
		// /unbind detaches the chat but does NOT start a fresh context: an
		// already-armed loop keeps firing, and the running turn (if any) keeps
		// running — suppressDelivery makes their replies stay out of the IM
		// chat (the session is handed to web as a fallback), so the loop only
		// stops on an explicit /stop or schedule_wakeup(cancel=true). We still
		// drop the per-key latches so the next non-loop message rebuilds a clean
		// context under the chat's fresh session.
		imKey := "im:" + string(s.channelMgr.KeyFor(ev))
		s.rememberedMu.Lock()
		delete(s.rememberedStores, imKey)
		s.rememberedMu.Unlock()
		s.injectorMu.Lock()
		delete(s.sessionInjectors, imKey)
		s.injectorMu.Unlock()
	case "/stop":
		// /stop is the IM interrupt — also the hard stop for an armed loop.
		s.cancelWakeup("im:" + string(s.channelMgr.KeyFor(ev)))
	}
	// /compact runs an LLM summarize, so it's handled out of the (synchronous,
	// reply-string) CommandRouter: run it in a goroutine and report the result.
	if cmd == "/compact" {
		s.handleChannelCompact(ad, ev)
		return true
	}
	// Reserved commands must not be intercepted by a skill (matching
	// the TUI reservedReplCommands pattern).
	switch cmd {
	case "/stop", "/bind", "/unbind", "/clear", "/new", "/status", "/list", "/help", "/goal", "/compact":
		// fall through to CommandRouter
	case "/loop":
		// /loop is a built-in the model handles via the schedule_wakeup tool
		// (which documents the "/loop [interval] <task>" convention), not a
		// CommandRouter command — pass it through to the agent like the Web and
		// TUI surfaces do.
		return false
	default:
		// Unknown slash tokens that match a skill name are passed through as
		// normal user messages, same as the Web and TUI surfaces.
		skillName := strings.TrimPrefix(cmd, "/")
		s.skillReg.Reload()
		if _, ok := s.skillReg.Get(skillName); ok {
			return false
		}
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
		defer s.recoverBg("channel compaction")
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
	// routeChannelEvent dispatches this in its own goroutine (go
	// s.handleChannelMessage), so the whole IM turn runs outside any caller's
	// recover — guard it here, at the goroutine's actual entry, or a panic mid
	// turn crashes the serve process.
	defer s.recoverBg("channel turn (" + ev.Platform + ")")
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

	// If the session store file was deleted externally (e.g., from the web
	// UI) since this in-memory Session was created, its Store pointer goes
	// stale — acquireSessionBinding's authoritative reload below would
	// otherwise fail with a confusing "session ... not found" error instead
	// of the chat just starting fresh (#1079). Recreate the backing store to
	// match.
	sess.EnsureStoreExists()

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

	// Start a typing keepalive BEFORE sending any text — the WeChat iLink
	// protocol cancels the typing indicator on sendmessage, so it must
	// already be running when the acknowledgment message is sent so it can
	// immediately re-assert the typing state. It re-fires every 5s for the
	// rest of the turn — and any turns chained after it — so a multi-minute
	// agentic turn never goes dark; ctrl (built in runChannelTurns) stops it
	// early the moment the reply's first chunk actually reaches the user
	// (#1117).
	stopTyping := startTypingKeepalive(ad, ev.ChatID, ev.ContextToken)
	defer stopTyping()

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

	s.runChannelTurns(ctx, sess, ad, ev, content, stopTyping)

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
// URLs by the adapters) are decoded and persisted under ~/.octo/uploads. If the
// active model accepts vision they are queued as vision blocks; otherwise they
// are surfaced as path notes so the model can read_file them and the turn keeps
// running. Documents (delivered as a local Path) become "[Attached file: <path>]"
// notes. Per-file failures are logged and skipped. Returns the content to run
// with any file notes folded in.
func (s *Server) attachInboundFiles(sess *channel.Session, ev channel.InboundEvent) string {
	content := ev.Text
	var blocks []agent.ContentBlock
	var notes []string

	cfg, _ := config.LoadCached()
	vision := cfg.ModelVision(sess.Agent.Model)

	for _, f := range ev.Files {
		switch {
		case f.DataURL != "":
			block, _, err := saveImageAttachment(f.Name, f.DataURL)
			if err != nil {
				slog.Debug("channel image attachment", "platform", ev.Platform, "name", f.Name, "err", err)
				continue
			}
			if vision {
				blocks = append(blocks, block)
			} else {
				notes = append(notes, agent.AttachmentNote(block.ImagePath))
			}
		case f.Path != "":
			notes = append(notes, agent.AttachmentNote(f.Path))
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
	// Spawned via bare `go` (loop.go + the async-completion paths), so it runs a
	// full turn outside any recover — guard it here or a panic crashes the process.
	defer s.recoverBg("channel idle turn")

	// Acquire the persistent binding before locking the turn, unless the
	// session is suppressed (/unbind mid-turn). In that case the session is
	// being handed to web, and re-acquiring the channel binding would prevent
	// web takeover by writing BoundEntry="channel" back to the session file.
	// The turn still runs (keeping history alive) but the suppressed
	// UIController keeps output silent.
	if !sess.SuppressDelivery() {
		storeID := sess.Store.ID
		if ok, _, _ := s.acquireSessionBinding(storeID, agent.EntryChannel, false); !ok {
			return
		}
		defer s.releaseSessionBinding(storeID, agent.EntryChannel)
	}

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

	// No typing keepalive here: idle turns fire from a background completion,
	// not a message the user is actively waiting on — nothing to reassure.
	s.runChannelTurns(ctx, sess, ad, ev, strings.Join(agent.Texts(items), "\n\n"), nil)
}

// runChannelTurns executes one content-bearing turn plus any chained turns
// drained from the agent's Inbox. It is shared between user-initiated and
// idle-triggered channel turns. stopTyping, if non-nil, cancels the caller's
// typing-keepalive ticker the first time this chain's reply text reaches the
// user (see channel.NewUIController).
func (s *Server) runChannelTurns(ctx context.Context, sess *channel.Session, ad channel.Adapter, ev channel.InboundEvent, content string, stopTyping func()) {
	// Refresh the external memory backend from config — IM turns never go
	// through prepareToolTurn (only WS/REST/cron do), so this is the only
	// place that keeps it live for IM at all, not just on-time. Must run
	// before MemoryBackendGuidance()/RegisterMemoryBackendHooks below; see
	// app.RefreshMemoryBackend's doc comment.
	app.RefreshMemoryBackend()

	// Apply the session's persisted model binding (set via IM /model or the
	// web model picker) before anything reads sess.Agent.Model — the system
	// prompt compose just below keys the MCP manifest off it.
	s.applyChannelModel(sess)

	// Recompose the system prompt every turn so memory written and skills
	// imported/toggled since server start are visible — web turns get this
	// for free from buildAgent; the IM factory's compose-once snapshot went
	// stale until restart. Unconditional: the skills manifest changes at
	// runtime even when memory is disabled.
	var memInjection string
	if s.memDir != "" {
		memInjection = memory.RenderInjection(s.memDir, s.homeMemDir)
	}
	if g := tools.MemoryBackendGuidance(); g != "" {
		memInjection = strings.TrimSpace(memInjection + "\n\n" + g)
	}
	cwd, envCtx := s.sessionCwdEnv(sess.Store)
	sess.Agent.CWD = cwd          // keep tool cwd aligned with the per-session dir the prompt/hooks use
	cfg, _ := config.LoadCached() // zero value on error (no last-good yet) still resolves correctly via EffectiveCoauthor
	// Honor the configured auto-compaction threshold, like buildAgent does for
	// web/desktop turns. The IM agent is persistent across turns (not rebuilt via
	// buildAgent), so resolve it per turn — and reset to 0 when unset so a config
	// change (or its removal) takes effect on the next turn.
	sess.Agent.CompactAutoFraction = 0
	if cfg.CompactAutoPct > 0 {
		sess.Agent.CompactAutoFraction = float64(cfg.CompactAutoPct) / 100.0
	}
	sess.Agent.System, sess.Agent.LeanSystem = prompt.ComposePair(s.system, cwd, envCtx, s.curSkillsManifest(), tools.MCPManifestFor(sess.Agent.Model), memInjection, s.effectiveCoauthor(cfg))

	// L2 memory hooks + shell hooks, same engine buildAgent gives web turns,
	// rebuilt per IM turn. The injector is session-sticky (recall latch) and
	// dropped on /unbind; a fresh engine each turn just re-registers it.
	imEngine := hooks.EngineFromEnvAndFiles(hooks.SharedSeen(), cwd, s.projectHooksTrusted(cwd))
	imEngine.Notify = func(m string) { slog.Warn("hook", "err", m) }
	if s.memDir != "" {
		s.injectorFor("im:" + string(sess.Key)).RegisterHooks(imEngine)
	}
	// Workflow save-nudge — memory-independent, wired for every IM session.
	tools.NewWorkflowNudger().RegisterHooks(imEngine)
	// Validate ~/.octo/config.yml right after the agent edits it.
	tools.NewConfigGuard().RegisterHooks(imEngine)
	// Auto-store into the external memory backend (if configured) — a no-op
	// when none is set.
	tools.RegisterMemoryBackendHooks(imEngine)
	sess.Agent.Hooks = imEngine
	sess.Agent.HookMeta = hooks.Meta{SessionID: string(sess.Key), Transport: agent.EntryChannel, Cwd: cwd}
	// The persisted backing session (Store) carries the durable SessionStart
	// flag + transcript path; IM persists it after each turn, so MarkHookStarted
	// rides that Save. Store can be tombstoned by a concurrent /unbind — nil-skip.
	if st := sess.Store; st != nil {
		sess.Agent.HookMeta.SessionID = st.ID
		if p, err := st.SavePath(); err == nil {
			sess.Agent.HookMeta.TranscriptPath = p
		}
		sess.Agent.SessionStarted = st.HookStarted || len(st.Messages) > 0
		sess.Agent.OnSessionStart = func() { st.MarkHookStarted() }
		// Same as buildAgent gives web turns: without this, compaction still
		// folds history but never archives the originals, so IM loses the
		// "read the chunk back" recall web/CLI sessions get for free.
		if dir, err := st.ChunkDir(); err == nil {
			sess.Agent.ArchiveDir = dir
		}
	}

	// Per-turn permission gate, the same shape prepareToolTurn gives web
	// turns: the session's own mode (falling back to the global default for
	// sessions predating per-session modes) + an interactive ask that prompts
	// in the chat and consumes the next message as the answer. Built after
	// BeginRun so it can't race the previous turn's gate. An engine failure
	// aborts the turn — running ungated is never an acceptable fallback.
	mode := resolvePermissionMode()
	if st := sess.Store; st != nil && st.PermissionMode != "" {
		mode = permission.Mode(st.PermissionMode)
	}
	engine, err := permission.New(permissionConfigPath(), cwd, mode, s.memDir, s.homeMemDir)
	if err != nil {
		// Generic chat reply — err.Error() can leak local paths into a
		// group chat; the operator gets the detail on the server console.
		slog.Error("channel permission engine", "channel", ev.Platform, "err", err)
		ad.SendText(ev.ChatID, "⚠️ Permission engine unavailable — message not processed. Check the server logs.", ev.MessageID)
		return
	}
	engine.AttachRemembered(s.rememberedFor("im:" + string(sess.Key)))
	sess.Agent.Gate = app.NewPermissionGate(engine, s.channelPermissionAsk(sess, ad, ev))

	ctrl := channel.NewUIController(ad, ev.ChatID, ev.MessageID, stopTyping)
	// If /unbind lands mid-turn (suppressDelivery set on the session), the turn
	// keeps running but its reply must not be delivered to this IM chat — poll
	// the session's flag so a /unbind that arrives after the controller is
	// built still takes effect for the rest of the turn.
	ctrl.SetSuppressor(func() bool { return sess.SuppressDelivery() })

	// The persisted backing session carries the conversation's goal — the
	// same record every transport bound to this session sees. Wire it as the
	// accountant and the tool store; a tombstoned store (concurrent /unbind)
	// leaves goals unwired for this turn.
	goalStore := sess.GoalStore()
	goalsOn := s.goalsEnabled.Load() && goalStore != nil
	if goalsOn {
		sess.Agent.GoalAcct = goalStore
		ctx = tools.WithGoalStore(ctx, goalStore)
	} else {
		sess.Agent.GoalAcct = nil
	}

	var toolDefs []agent.ToolDefinition
	var executor agent.ToolExecutor
	if s.cfg.Tools {
		// Session-scoped tracker (same "im:"+sess.Key identity used for the
		// background/workflow managers below) so a file read_file'd in one chat
		// message is still "read" when a later message in the same conversation
		// writes it — a fresh-per-message tracker would forget every read as
		// soon as the message finished, same bug prepareToolTurn had for web.
		executor = tools.NewDefaultRegistryWithTracker(tools.SessionReadTracker("im:" + string(sess.Key)))

		// Per-message sub-agent manager bound to THIS chat's agent. Async, like
		// the web path: a background sub-agent Starts and reports completion via
		// SetOnExit below, and a default (foreground) fan-out of several
		// sub_agents runs concurrently rather than blocking one another
		// (dispatchTools parallelizes a sub_agent batch). Stamped into ctx BEFORE
		// toolDefs is computed below (#1133 follow-up) — the process-global
		// spawner slot is never set in server mode (only
		// SetDefaultSubAgentManager is, in enableSubAgentTools), so without
		// this ordering the top-level turn's own toolDefs would never
		// advertise workflow, even though this chat's spawned children (via
		// this spawner's own toolsFn) correctly would.
		spawner := app.NewSpawner(sess.Agent, executor, func(ctx context.Context) []agent.ToolDefinition {
			if goalsOn {
				return tools.DefaultToolsForCtx(ctx, sess.Agent.Model)
			}
			return tools.WithoutGoalTools(tools.DefaultToolsForCtx(ctx, sess.Agent.Model))
		})
		subMgr := tools.NewSubAgentManager(spawner)
		// A background sub-agent finishes after this turn's chain ends; its
		// result reaches the model via the agent Inbox + an idle follow-up turn,
		// the same channel every background process and workflow completion uses
		// (see wireChannelCompletionHooks). Without this an async sub-agent's
		// reply would be dropped, which is why this path used to force synchronous
		// dispatch. `ev` is the outer InboundEvent (the message that opened this
		// turn); `n` is the completion notification.
		subMgr.SetOnExit(func(n tools.SubAgentNotification) {
			sess.Agent.Inbox.Enqueue(tools.FormatSubAgentNote(n))
			go s.runChannelIdleTurn(context.Background(), sess, ad, ev)
		})
		ctx = tools.WithSubAgentManager(ctx, subMgr)

		toolDefs = tools.DefaultToolsForCtx(ctx, sess.Agent.Model)
		if !goalsOn {
			// Advertising a tool this turn can't execute is the #597 class.
			toolDefs = tools.WithoutGoalTools(toolDefs)
		}

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
		// Turn-scoped file sender for send_file's default (current-chat) mode:
		// the model's reply carries only text, so a generated image / chart /
		// document reaches this chat by pushing it through the adapter. The
		// send_file tool itself is advertised via DefaultToolsFor (messenger
		// registered); here we only pin which chat the no-target case sends to.
		ctx = tools.WithChannelSender(ctx, channelFileSender{ad: ad, chatID: ev.ChatID, replyTo: ev.MessageID})
		// Per-chat Waker so schedule_wakeup (the in-session loop) can pace this and
		// later turns. On wakeup it routes through runChannelIdleTurn, which
		// delivers via the adapter — including the wakeup-injected turns, which
		// flow back through here and re-stamp the waker so the loop continues.
		ctx = tools.WithWaker(ctx, imWaker{s: s, sess: sess, ad: ad, ev: ev})
		// Replay-secret cache scope: the backing agent session's ID, so a
		// session delete reaps its cached secrets; the chat key is the fallback
		// while no store is attached. (IM never COLLECTS secrets — no masked
		// input — but cache/env resolution still runs on this transport.)
		sid := "im:" + string(sess.Key)
		if sess.Store != nil {
			sid = sess.Store.ID
		}
		ctx = tools.WithSessionID(ctx, sid)
	}

	// Session title, web doAgentTurn parity: an IM session starts life with
	// agent.NewSession's "*Octo Agent" placeholder and — unlike a web turn —
	// nothing ever replaced it, so the session list showed the placeholder
	// forever. Same one mechanism: an async GenerateTitleOrSnippet within
	// TitleGenerationTimeout (an LLM title, else the first-message snippet),
	// broadcast live and handed to this turn's serialized persist path via
	// storePendingTitle — the generation goroutine never writes the session
	// file itself. A generation still in flight at turn end rides the next
	// turn's adoption; a failure leaves the placeholder and the next turn
	// retries (the claim is released either way).
	if st := sess.Store; st != nil && agent.IsAutoNamePlaceholder(st.Title) {
		sid := st.ID
		// Pre-turn snapshot plus the incoming user message — the turn loop
		// owns History and hasn't appended it yet (web titleMsgs parity).
		titleMsgs := append(append([]agent.Message{}, st.Messages...), agent.NewUserMessage(content))
		// No user text to title from at all (e.g. an attachments-only first
		// message): skip the throwaway call rather than pay for a hallucinated
		// title — the snippet fallback would come up empty anyway. The next
		// text-bearing user message retries.
		if agent.FirstUserSnippet(titleMsgs) != "" && s.claimTitleGeneration(sid) {
			go func() {
				defer s.recoverBg("channel title generation")
				defer s.releaseTitleGeneration(sid)
				ctx, cancel := context.WithTimeout(context.Background(), agent.TitleGenerationTimeout)
				defer cancel()
				t, terr := sess.Agent.GenerateTitleOrSnippet(ctx, titleMsgs)
				if terr != nil {
					slog.Warn("channel session title generation failed, falling back to message snippet", "session_id", sid, "err", terr)
				}
				if strings.TrimSpace(t) == "" {
					return
				}
				s.storePendingTitle(sid, t)
				s.broadcastSessionRenamed(sid, t)
			}()
		}
	}

	persist := func() {
		// Adopt a title generated while the turn ran (web doAgentTurn parity):
		// the generation goroutine only broadcast + stored it — this persist
		// path is the only writer of the session file. AdoptGeneratedTitle
		// refuses when the session's in-memory title is no longer a placeholder.
		if st := sess.Store; st != nil {
			if t := s.takePendingTitle(st.ID); t != "" {
				adopted, aerr := sess.AdoptGeneratedTitle(t)
				if aerr != nil {
					slog.Warn("channel session title adoption: save failed", "session_id", st.ID, "err", aerr)
				} else if adopted {
					// Converge any client whose list refetch saw the placeholder
					// since the mid-turn broadcast: disk is authoritative now.
					s.broadcastSessionRenamed(st.ID, t)
				}
			}
		}
		// Persist the conversation so it survives server restarts. Failure
		// must not eat the reply the user already got — log and move on.
		// Persist() also records the context-token count (under storeMu) so this
		// session shows accurate usage in the Web UI — parity with web/desktop.
		if err := sess.Persist(); err != nil {
			slog.Error("channel persist session", "channel", ev.Platform, "err", err)
		}
	}

	// Goal status before the turn, for the terminal-transition notice below —
	// an IM user isn't watching a status line, so transitions into a stopped
	// state must arrive as a chat message.
	var goalStatusBefore agent.GoalStatus
	if goalsOn {
		if g, ok := goalStore.GoalSnapshot(); ok {
			goalStatusBefore = g.Status
		}
	}

	// sess.Agent is persistent across turns (unlike Web's per-turn buildAgent),
	// so SessionTokens() is cumulative — snapshot it before the chain and diff
	// after, the same technique the goal accountant uses for its own totals.
	turnStart := time.Now()
	inBefore, outBefore := sess.Agent.SessionTokens()

	_, runErr := channel.RunAgent(ctx, sess, toolDefs, executor, ctrl, content)
	persist()

	// Steer messages that arrived after the turn's final inbox drain chain
	// into follow-up turns (web runAgentTurnLoop parity), and an active goal
	// keeps the chain going with hidden continuation prompts once the inbox
	// is dry (user input always drains first). Still inside BeginRun, so no
	// new turn can interleave. Persist after each chained turn — one crash
	// must cost at most one turn, not the whole chain. A chained error stops
	// the chain (its rollback already dropped the steer message; looping
	// would re-fail forever).
	for runErr == nil && ctx.Err() == nil {
		next := ""
		if items := sess.Agent.Inbox.Drain(); len(items) > 0 {
			next = strings.Join(agent.Texts(items), "\n\n")
		} else if goalsOn {
			if prompt, ok := goalStore.GoalContinuation(); ok {
				next = prompt
			}
		}
		if next == "" {
			break
		}
		_, runErr = channel.RunAgent(ctx, sess, toolDefs, executor, ctrl, next)
		persist()
	}

	if goalsOn && runErr != nil {
		// Park the continuation loop on any aborted or errored turn (the
		// zero-progress audit can't catch either — partial replies were
		// already billed); a rate-limited continuation turn parks harder as
		// usage_limited, until /goal resume. Web/TUI parity.
		if goalStore.GoalContinuationPending() && agent.IsRateLimitErr(runErr) {
			if g, gerr := goalStore.SetGoalStatus(agent.GoalUsageLimited); gerr == nil {
				ad.SendText(ev.ChatID, "⏸️ Goal usage limited (provider rate limit) — /goal resume to retry: "+g.Objective, ev.MessageID)
			}
		}
		goalStore.SuppressGoalContinuation()
	}

	// Surface a turn failure to the chat. Without this the user just sees
	// silence — no reply, no hint that the LLM call (rate limit, HTTP 5xx,
	// context length, auth) failed. A user interrupt (context.Canceled) is
	// expected and stays quiet.
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		ad.SendText(ev.ChatID, "⚠️ "+agent.UserFacingError(runErr), ev.MessageID)
	}

	// Terminal-transition notice: the model finished, blocked, or exhausted
	// the budget during this chain. usage_limited already sent its own line.
	// Built (not yet sent) before the per-turn summary below so a goal-ending
	// turn's summary can be skipped — the terminal notice already carries the
	// goal's own cumulative time/tokens, and sending both would double up.
	goalTerminalNotice := ""
	if goalsOn {
		if g, ok := goalStore.GoalSnapshot(); ok && g.Status != goalStatusBefore {
			switch g.Status {
			case agent.GoalComplete:
				goalTerminalNotice = "✅ Goal complete (" + agent.GoalUsageLine(g) + "): " + g.Objective
			case agent.GoalBlocked:
				goalTerminalNotice = "🚧 Goal blocked — the agent is at an impasse; /goal resume to retry: " + g.Objective
			case agent.GoalBudgetLimited:
				goalTerminalNotice = "⏸️ Goal budget reached (" + agent.GoalUsageLine(g) + ") — /goal edit <objective> to keep going: " + g.Objective
			}
		}
	}

	// Per-turn summary: elapsed time + tokens spent across this whole chain
	// (the user-visible message plus any chained continuation turns). Only on
	// a clean completion — an error already got its own message above, an
	// interrupt should stay quiet (matching the CLI's summary line), and a
	// goal-terminal turn skips it in favor of the terminal notice below.
	if runErr == nil && goalTerminalNotice == "" {
		inAfter, outAfter := sess.Agent.SessionTokens()
		tokens := (inAfter - inBefore) + (outAfter - outBefore)
		elapsed := agent.FormatElapsedSeconds(int64(time.Since(turnStart).Seconds()))
		ad.SendText(ev.ChatID, "⏱ "+elapsed+", "+agent.FormatGoalTokens(int64(tokens))+" tokens", ev.MessageID)
	}

	if goalTerminalNotice != "" {
		ad.SendText(ev.ChatID, goalTerminalNotice, ev.MessageID)
	}
}

// stopChannels shuts down all channel adapters and their sessions.
func (s *Server) stopChannels() {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	if s.channelCancel != nil {
		s.channelCancel()
	}
	for name := range s.adapterCancels {
		delete(s.adapterCancels, name)
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

// RunningChannels returns the sorted names of the IM platforms with a live
// adapter. Empty when channels are off or none have connected yet. Surfaced in
// the desktop tray.
func (s *Server) RunningChannels() []string {
	var names []string
	s.runningAdapters.Range(func(k, _ any) bool {
		if name, ok := k.(string); ok {
			names = append(names, name)
		}
		return true
	})
	sort.Strings(names)
	return names
}

// ConnectedClients reports how many WebSocket clients (web tabs, editor
// extensions) are currently connected. Surfaced in the desktop tray.
func (s *Server) ConnectedClients() int {
	if s.wsHub == nil {
		return 0
	}
	s.wsHub.mu.Lock()
	defer s.wsHub.mu.Unlock()
	return len(s.wsHub.connections)
}

// ConfiguredChannelCount reports how many IM platforms are configured in
// channels.yml (enabled or not). Surfaced in the desktop tray so the user
// can tell "configured" from "connected" at a glance.
func (s *Server) ConfiguredChannelCount() int {
	cfg, err := channel.LoadConfig()
	if err != nil {
		return 0
	}
	return len(cfg.Channels)
}
