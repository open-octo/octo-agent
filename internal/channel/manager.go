package channel

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/permission"
	"github.com/open-octo/octo-agent/internal/tasks"
)

// BindingMode determines how an inbound message maps to an agent session.
type BindingMode string

const (
	// BindByChatUser creates one session per (chat, user) pair.
	// Best for group chats where multiple users interact independently.
	BindByChatUser BindingMode = "chat_user"

	// BindByChat creates one session per chat (group or DM).
	// Best for DMs or when all users in a group share one context.
	BindByChat BindingMode = "chat"

	// BindByUser creates one session per user across all chats.
	// Best when a single user wants continuity across multiple groups.
	BindByUser BindingMode = "user"
)

// SessionKey uniquely identifies a conversation session.
type SessionKey string

// sessionKeyFor returns the session key for an inbound event based on the binding mode.
func sessionKeyFor(mode BindingMode, ev InboundEvent) SessionKey {
	switch mode {
	case BindByChatUser:
		return SessionKey(ev.Platform + ":" + ev.ChatID + ":" + ev.UserID)
	case BindByUser:
		return SessionKey(ev.Platform + ":" + ev.UserID)
	default: // BindByChat
		return SessionKey(ev.Platform + ":" + ev.ChatID)
	}
}

// Session holds the agent and its binding state for one conversation.
type Session struct {
	Key   SessionKey
	Agent *agent.Agent

	// Store is the on-disk history backing this conversation (persist.go).
	// Loaded/initialised at session creation, written via Persist after
	// each turn so the conversation survives server restarts. storeMu
	// guards it: deleteStore tombstones the field while a turn may still
	// be running, and that turn's Persist must observe the nil.
	Store   *agent.Session
	storeMu sync.Mutex
	// Tasks is the conversation's task store. It lives as long as the session,
	// so task_* state persists across messages within one chat (a fresh
	// per-message store would reset the list every turn). Stamped into the turn
	// ctx by the IM handler; *tasks.Store satisfies tools.TaskStore.
	Tasks   *tasks.Store
	ChatID  string
	UserID  string
	BoundAt time.Time

	// runMu serialises agent turns within this session: the IM dispatcher runs
	// each message in its own goroutine, and two interleaved turns would
	// corrupt the agent's user/assistant history alternation. runCancel
	// (guarded by cancelMu, not runMu — /stop must not queue behind the very
	// turn it is trying to cancel) aborts the in-flight turn.
	runMu     sync.Mutex
	cancelMu  sync.Mutex
	runCancel context.CancelFunc

	// pendingAsk, while non-nil, claims the session's next message as
	// the answer to an interactive permission prompt (see ask.go). Guarded by
	// askMu, not runMu — the reply arrives while the asking turn holds runMu.
	// askChatID/askUserID pin which chat+user may answer.
	// askButtonsOnly, when true, prevents DeliverAskReply from consuming plain
	// text messages — only button callbacks (via DeliverAskButton) resolve the
	// ask (#1120).
	askMu          sync.Mutex
	pendingAsk     chan string
	askChatID      string
	askUserID      string
	askButtonsOnly bool
}

// BeginRun prepares one agent turn: it blocks until any previous turn in this
// session finishes, then returns a cancellable ctx for the run and a done
// func that releases the session. Always call done (typically deferred).
func (s *Session) BeginRun(ctx context.Context) (context.Context, func()) {
	s.runMu.Lock()
	ctx, cancel := context.WithCancel(ctx)
	s.cancelMu.Lock()
	s.runCancel = cancel
	s.cancelMu.Unlock()
	return ctx, func() {
		s.cancelMu.Lock()
		s.runCancel = nil
		s.cancelMu.Unlock()
		cancel()
		s.runMu.Unlock()
	}
}

// IsRunning reports whether an agent turn is currently in flight. The
// inbound dispatcher uses it to steer a mid-turn message into the running
// turn's Inbox instead of queueing a whole new turn behind runMu.
func (s *Session) IsRunning() bool {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	return s.runCancel != nil
}

// Interrupt cancels the session's in-flight agent turn, if any. It reports
// whether a turn was actually running.
func (s *Session) Interrupt() bool {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.runCancel == nil {
		return false
	}
	s.runCancel()
	s.runCancel = nil
	return true
}

// AgentFactory creates a new agent.Agent for a session.
type AgentFactory func() *agent.Agent

// newSession builds a Session with a fresh agent and per-conversation task
// store. Centralised so every binding path (explicit /bind, auto-create on
// first message, GetOrCreateSession) wires sessions identically.
func (m *Manager) newSession(key SessionKey, ev InboundEvent) *Session {
	s := &Session{
		Key:     key,
		Agent:   m.factory(),
		Tasks:   tasks.New(),
		ChatID:  ev.ChatID,
		UserID:  ev.UserID,
		BoundAt: time.Now(),
	}
	s.restoreOrInitStore(m.resolveStoreID(key))
	return s
}

// Manager owns the lifecycle of adapters and their bound sessions.
type Manager struct {
	cfg     *Config
	factory AgentFactory
	mode    BindingMode

	// sessions holds active sessions keyed by SessionKey.
	sessions sync.Map // SessionKey -> *Session

	// bindings is the persistent chat→session redirection set by /bind. When a
	// key is present, the chat routes to the recorded store instead of its
	// deterministic default (see resolveStoreID).
	bindings *bindingStore

	// goalsEnabled mirrors the main config's goal.enabled (default true,
	// matching config.Config.GoalEnabled's default). The server sets it via
	// SetGoalsEnabled once it has loaded the real config; until then /goal
	// stays enabled so tests and callers that never call SetGoalsEnabled see
	// the historical always-on behavior.
	goalsEnabled atomic.Bool
}

// NewManager creates a Manager. If mode is empty it defaults to BindByChatUser.
func NewManager(cfg *Config, factory AgentFactory, mode BindingMode) *Manager {
	if mode == "" {
		mode = BindByChatUser
	}
	m := &Manager{
		cfg:      cfg,
		factory:  factory,
		mode:     mode,
		bindings: newBindingStore(),
	}
	m.goalsEnabled.Store(true)
	return m
}

// SetGoalsEnabled mirrors the main config's goal.enabled into the manager, so
// the IM /goal command is gated the same way the REST and TUI surfaces are
// (internal/server/goal.go, cmd/octo/chat.go). Without this, /goal.enabled:
// false could be silently bypassed via any bound IM platform: a goal set
// through chat would persist to the session's backing store and spring back
// to life the moment a REST/TUI/Web surface later touches that same session.
func (m *Manager) SetGoalsEnabled(enabled bool) {
	m.goalsEnabled.Store(enabled)
}

// resolveStoreID returns the on-disk session store ID a chat should use: the
// session it was explicitly bound to via /bind, or its deterministic default.
func (m *Manager) resolveStoreID(key SessionKey) string {
	if id, ok := m.bindings.get(key); ok {
		return id
	}
	return sessionStoreID(key)
}

// CommandRouter processes slash commands and returns a reply text.
func (m *Manager) CommandRouter(ev InboundEvent) string {
	parts := strings.Fields(strings.TrimSpace(ev.Text))
	if len(parts) == 0 {
		return ""
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/bind":
		return m.cmdBind(ev, args)
	case "/stop":
		return m.cmdStop(ev)
	case "/unbind":
		return m.cmdUnbind(ev)
	case "/status":
		return m.cmdStatus(ev)
	case "/list":
		return m.cmdList()
	case "/clear":
		return m.cmdClear(ev)
	case "/new":
		return m.cmdNew(ev)
	case "/compact":
		return m.cmdCompact(ev)
	case "/goal":
		return m.cmdGoal(ev, strings.Join(args, " "))
	case "/help":
		return "Available: /bind [--force] <number|id>, /unbind, /list, /clear, /new, /compact, /goal, /stop, /status, /help"
	default:
		return fmt.Sprintf("Unknown command: %s", cmd)
	}
}

// cmdGoal applies the shared /goal grammar to the chat's persisted session.
// Goal state lives on the backing store, so it is visible to every transport
// bound to the same session. Mutations are safe against a running turn — the
// session's goal methods are mutex-guarded and the turn's accounting picks
// the change up at its next tick.
func (m *Manager) cmdGoal(ev InboundEvent, args string) string {
	if !m.goalsEnabled.Load() {
		return "Goals are disabled (goal.enabled)."
	}
	key := sessionKeyFor(m.mode, ev)
	val, loaded := m.sessions.Load(key)
	if !loaded {
		return "No active session. Send a message first, or /bind one."
	}
	sess := val.(*Session)
	store := sess.GoalStore()
	if store == nil {
		return "Goals are unavailable for this session."
	}
	return agent.GoalCommand(store, args)
}

// cmdClear wipes the current session's conversation history while keeping its
// binding and persisted store — the chat starts fresh without re-binding. The
// emptied history is persisted so a restart doesn't bring it back.
func (m *Manager) cmdClear(ev InboundEvent) string {
	key := sessionKeyFor(m.mode, ev)
	val, loaded := m.sessions.Load(key)
	if !loaded {
		return "No active session to clear."
	}
	sess := val.(*Session)
	if sess.IsRunning() {
		return "Can't clear while a turn is running — /stop it first or wait for it to finish."
	}
	// Serialize with agent turns so the clear cannot race a turn that starts
	// immediately after the IsRunning check.
	_, done := sess.BeginRun(context.Background())
	defer done()

	sess.Agent.ClearHistory()
	if err := sess.Persist(); err != nil {
		return fmt.Sprintf("Cleared, but saving the empty history failed: %v", err)
	}
	return "Conversation cleared. Starting fresh."
}

// cmdCompact force-compacts the current session's conversation history now,
// summarizing older turns to free up context. It refuses to run while an agent
// turn is in flight so it can't race the turn's own history mutations.
func (m *Manager) cmdCompact(ev InboundEvent) string {
	key := sessionKeyFor(m.mode, ev)
	val, loaded := m.sessions.Load(key)
	if !loaded {
		return "No active session to compact."
	}
	sess := val.(*Session)
	if sess.IsRunning() {
		return "Can't compact while a turn is running — /stop it first or wait for it to finish."
	}
	// Serialize with agent turns so the compaction cannot race a turn that
	// starts immediately after the IsRunning check.
	ctx, done := sess.BeginRun(context.Background())
	defer done()

	stats, err := sess.Agent.ForceCompact(ctx, nil)
	if err != nil {
		return fmt.Sprintf("Compact failed: %v", err)
	}
	if err := sess.Persist(); err != nil {
		return fmt.Sprintf("Compacted, but saving failed: %v", err)
	}
	if stats.FoldedMsgs == 0 && stats.ReclaimedTokens == 0 {
		return "Nothing to compact yet."
	}
	if stats.FoldedMsgs == 0 {
		return fmt.Sprintf("Reclaimed stale tool output · ~%d → ~%d tokens", stats.BeforeTokens, stats.AfterTokens)
	}
	return fmt.Sprintf("Compacted context · folded %d message(s) · ~%d → ~%d tokens", stats.FoldedMsgs, stats.BeforeTokens, stats.AfterTokens)
}

// cmdNew creates a brand-new session and binds the current chat to it. The old
// store (if any) is left on disk but detached from this chat, so /new is safe
// to use even when another entry owns the chat's usual session.
func (m *Manager) cmdNew(ev InboundEvent) string {
	key := sessionKeyFor(m.mode, ev)

	// Create a fresh session owned by channel.
	st := agent.NewSession(m.agentModel(), "")
	st.Source = "channel"
	st.Bind(agent.EntryChannel, false)
	_ = st.SetPermissionMode(string(permission.ResolveDefaultMode()))
	if err := st.Save(); err != nil {
		return fmt.Sprintf("Could not create new session: %v", err)
	}

	// Drop any existing override and point this chat at the new session.
	if _, err := m.bindings.remove(key); err != nil {
		return fmt.Sprintf("Could not clear old binding: %v", err)
	}
	if err := m.bindings.set(key, st.ID); err != nil {
		return fmt.Sprintf("Could not attach chat to new session: %v", err)
	}

	// Evict the live session so the next message builds against the new store.
	m.sessions.LoadAndDelete(key)

	return fmt.Sprintf("Started a new session [%s]. Send a message to begin.", st.ShortID())
}

// agentModel returns the model name the factory uses, so /new can create a
// session that matches the agent configuration without running a turn.
func (m *Manager) agentModel() string {
	if m.factory == nil {
		return ""
	}
	return m.factory().Model
}

// cmdBind attaches the current chat to an existing session chosen from /list,
// by its list number or (short/full) ID. The redirection is recorded in the
// persistent binding table so it survives a restart, and no history is
// deleted — /bind switches conversations rather than starting a fresh one.
// By default, if the target session is bound to another entry, the bind is
// rejected so IM cannot silently take over a session owned by CLI/TUI/Web.
// Pass --force to take over a session whose turn lease has expired.
func (m *Manager) cmdBind(ev InboundEvent, args []string) string {
	steal, targetArg, ok := parseBindArgs(args)
	if !ok {
		return "Usage: /bind [--force] <number|id> — run /list to see sessions, then attach this chat to one. History is preserved."
	}
	target := m.resolveBindTarget(targetArg)
	if target == nil {
		return fmt.Sprintf("No session matches %q. Run /list to see available sessions.", targetArg)
	}

	previousEntry := target.BoundEntry
	res, _, err := target.Bind(agent.EntryChannel, steal)
	if res == agent.Rejected {
		return fmt.Sprintf("Cannot bind: %v", err)
	}
	if err := target.Save(); err != nil {
		return fmt.Sprintf("Could not persist entry binding: %v", err)
	}

	key := sessionKeyFor(m.mode, ev)
	if err := m.bindings.set(key, target.ID); err != nil {
		// Roll back the entry binding so we don't leave the session claimed.
		target.Unbind(agent.EntryChannel)
		_ = target.Save()
		return fmt.Sprintf("Could not save the binding: %v", err)
	}
	// Drop any live session for this key, then rebuild it against the newly
	// bound store so the chat is ready immediately. A turn still in flight on
	// the old session keeps persisting to its own store — nothing is deleted.
	m.sessions.LoadAndDelete(key)
	m.sessions.Store(key, m.newSession(key, ev))
	if res == agent.Stolen {
		return fmt.Sprintf("Taken over %q [%s] from %s. History preserved.", target.DisplayTitle(), target.ShortID(), previousEntry)
	}
	return fmt.Sprintf("Bound to session %q [%s]. History preserved.", target.DisplayTitle(), target.ShortID())
}

// parseBindArgs extracts the optional --force flag and the target identifier
// from /bind arguments. It accepts both "/bind --force <id>" and
// "/bind <id> --force". The second return value is the target argument; the
// third reports whether the args are well-formed.
func parseBindArgs(args []string) (steal bool, target string, ok bool) {
	for _, a := range args {
		if strings.EqualFold(a, "--force") {
			steal = true
			continue
		}
		if target != "" {
			return false, "", false
		}
		target = a
	}
	if target == "" {
		return false, "", false
	}
	return steal, target, true
}

// resolveBindTarget maps a /bind argument to a persisted session: a 1-based
// index into the /list ordering (newest first), or a full / short ID match.
// Returns nil when nothing matches.
func (m *Manager) resolveBindTarget(arg string) *agent.Session {
	sessions, err := agent.ListSessions(0)
	if err != nil || len(sessions) == 0 {
		return nil
	}
	if n, err := strconv.Atoi(arg); err == nil {
		if n >= 1 && n <= len(sessions) {
			return sessions[n-1]
		}
		return nil
	}
	for _, s := range sessions {
		if s.ID == arg || s.ShortID() == arg {
			return s
		}
	}
	return nil
}

// cmdStop interrupts the session's in-flight agent turn (mirrors the Ctrl-C /
// web "interrupt" semantics; history is repaired by finishInterrupted and the
// session stays usable).
func (m *Manager) cmdStop(ev InboundEvent) string {
	key := sessionKeyFor(m.mode, ev)
	val, loaded := m.sessions.Load(key)
	if !loaded {
		return "No active session for this context."
	}
	if val.(*Session).Interrupt() {
		return "Task interrupted."
	}
	return "No task is running."
}

// cmdUnbind detaches the chat from its current session. If the chat was
// explicitly /bind-ed to another session, that override is dropped; if the
// chat owned its automatically-created session's entry binding, that binding
// is released so other entries can use it. No history is deleted.
func (m *Manager) cmdUnbind(ev InboundEvent) string {
	key := sessionKeyFor(m.mode, ev)

	released := false
	if val, ok := m.sessions.Load(key); ok {
		released = val.(*Session).UnbindStore(agent.EntryChannel)
	}

	hadOverride, err := m.bindings.remove(key)
	if err != nil {
		return fmt.Sprintf("Could not clear the binding: %v", err)
	}
	// Detach the live session without touching any store; the next message
	// re-attaches this chat to its own default session.
	m.sessions.LoadAndDelete(key)
	if hadOverride || released {
		return "Unbound. This chat reverted to its own session; the bound session's history was kept."
	}
	return "This chat wasn't bound to another session. Reset to its default session; no history was deleted."
}

// cmdStatus reports the current session state.
func (m *Manager) cmdStatus(ev InboundEvent) string {
	key := sessionKeyFor(m.mode, ev)
	val, ok := m.sessions.Load(key)
	if !ok {
		return "No active session. Send a message to start one, or /bind to attach to a saved session."
	}
	sess := val.(*Session)
	inTok, outTok := sess.Agent.SessionTokens()
	model := sess.Agent.Model
	status := "idle"
	if sess.IsRunning() {
		status = "running"
	}
	title := sess.Store.Title
	if title == "" {
		title = sess.Store.DisplayTitle()
	}
	return fmt.Sprintf("Session: %s | Model: %s | Status: %s | Since: %s | Tokens: %d in / %d out",
		title, model, status, sess.BoundAt.Format("15:04:05"), inTok, outTok)
}

// cmdList shows the persisted sessions a chat can attach to with /bind,
// newest first. The number printed here is the index /bind accepts.
func (m *Manager) cmdList() string {
	sessions, err := agent.ListSessions(20)
	if err != nil {
		return fmt.Sprintf("Could not list sessions: %v", err)
	}
	if len(sessions) == 0 {
		return "No saved sessions yet."
	}
	var b strings.Builder
	b.WriteString("Sessions (newest first) — /bind <number> to attach:\n")
	for i, s := range sessions {
		fmt.Fprintf(&b, "%d. %s  [%s]\n", i+1, s.DisplayTitle(), s.ShortID())
	}
	return strings.TrimRight(b.String(), "\n")
}

// KeyFor exposes the session key an inbound event maps to under this
// manager's binding mode, for callers that keep per-session state outside
// the manager (e.g. the server's remembered-permission stores).
func (m *Manager) KeyFor(ev InboundEvent) SessionKey {
	return sessionKeyFor(m.mode, ev)
}

// GetSession returns the session for the given inbound event, or nil if none exists.
func (m *Manager) GetSession(ev InboundEvent) *Session {
	key := sessionKeyFor(m.mode, ev)
	val, ok := m.sessions.Load(key)
	if !ok {
		return nil
	}
	return val.(*Session)
}

// GetOrCreateSession returns the existing session or creates a new one.
func (m *Manager) GetOrCreateSession(ev InboundEvent) *Session {
	key := sessionKeyFor(m.mode, ev)
	val, loaded := m.sessions.Load(key)
	if !loaded {
		sess := m.newSession(key, ev)
		val, _ = m.sessions.LoadOrStore(key, sess)
	}
	return val.(*Session)
}

// SessionCount returns the number of active sessions.
func (m *Manager) SessionCount() int {
	var count int
	m.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// KnownChat identifies an IM chat the bot can proactively push to: either a
// live session this process is serving, or a chat explicitly attached via
// /bind (persisted across restarts). It is the recipient set the send_message
// tool offers when the model has no bound chat of its own (e.g. a web turn
// asked to message the user's WeChat).
type KnownChat struct {
	Platform string
	ChatID   string
	UserID   string
	Active   bool // a live session in this process run
	Bound    bool // has an explicit /bind
}

// KnownChats enumerates the chats the bot can address, merging live sessions
// with the persisted /bind table. Entries are deduplicated by session key
// (which, under the server's BindByChatUser mode, is platform:chatID:userID); a
// chat that is both active and bound reports both flags. Chat/user IDs are
// recovered from the session key, whose format depends on the binding mode
// (see sessionKeyFor).
func (m *Manager) KnownChats() []KnownChat {
	idx := map[SessionKey]*KnownChat{}
	upsert := func(key SessionKey, chatID, userID string) *KnownChat {
		if kc, ok := idx[key]; ok {
			return kc
		}
		p, c, u := splitSessionKey(key)
		if chatID == "" {
			chatID = c
		}
		if userID == "" {
			userID = u
		}
		kc := &KnownChat{Platform: p, ChatID: chatID, UserID: userID}
		idx[key] = kc
		return kc
	}

	m.sessions.Range(func(k, v any) bool {
		key, _ := k.(SessionKey)
		sess, _ := v.(*Session)
		var chatID, userID string
		if sess != nil {
			chatID, userID = sess.ChatID, sess.UserID
		}
		upsert(key, chatID, userID).Active = true
		return true
	})
	for _, key := range m.bindings.keys() {
		upsert(key, "", "").Bound = true
	}

	out := make([]KnownChat, 0, len(idx))
	for _, kc := range idx {
		if kc.Platform == "" || kc.ChatID == "" {
			continue
		}
		out = append(out, *kc)
	}
	return out
}

// splitSessionKey recovers (platform, chatID, userID) from a session key.
// Keys are "platform:chatID[:userID]" (sessionKeyFor); a platform never
// contains a colon, so the first segment is unambiguous, and the remainder is
// split once more for the optional user segment.
func splitSessionKey(key SessionKey) (platform, chatID, userID string) {
	parts := strings.SplitN(string(key), ":", 3)
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return parts[0], parts[1], ""
	default:
		return string(key), "", ""
	}
}
