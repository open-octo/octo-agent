package server

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/hooks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// fullFakeAdapter implements the whole channel.Adapter surface with no-ops,
// recording SendText calls — handleChannelMessage runs a real turn through
// the UIController, which touches typing/update methods too.
type fullFakeAdapter struct {
	mu              sync.Mutex
	sent            []string
	sendTypingCount int
	stopTypingCount int
}

func (a *fullFakeAdapter) Platform() string { return "fake" }
func (a *fullFakeAdapter) Start(ctx context.Context, _ func(channel.InboundEvent)) error {
	<-ctx.Done()
	return nil
}
func (a *fullFakeAdapter) Stop() error { return nil }
func (a *fullFakeAdapter) SendText(chatID, text, replyTo string) channel.SendResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, text)
	return channel.SendResult{OK: true, MessageID: "f1"}
}
func (a *fullFakeAdapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *fullFakeAdapter) UpdateMessage(chatID, messageID, text string) bool { return true }
func (a *fullFakeAdapter) SupportsMessageUpdates() bool                      { return false }
func (a *fullFakeAdapter) SendTyping(chatID, contextToken string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sendTypingCount++
	return nil
}
func (a *fullFakeAdapter) StopTyping(chatID, contextToken string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopTypingCount++
	return nil
}
func (a *fullFakeAdapter) Flush(chatID string)   {}
func (a *fullFakeAdapter) SupportsButtons() bool { return false }
func (a *fullFakeAdapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true, MessageID: "f1"}
}
func (a *fullFakeAdapter) ValidateConfig(channel.PlatformConfig) []string { return nil }

func (a *fullFakeAdapter) texts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.sent...)
}

func (a *fullFakeAdapter) typingCounts() (sendTyping, stopTyping int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sendTypingCount, a.stopTypingCount
}

// chanServer returns a mustServer with a channel manager wired to stub agents.
func chanServer(t *testing.T) *Server {
	t.Helper()
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(&stubSender{}, "stub-model")
	}, channel.BindByChat)
	return srv
}

func evFor(text string) channel.InboundEvent {
	return channel.InboundEvent{Platform: "fake", ChatID: "c1", UserID: "u1", MessageID: "m1", Text: text}
}

// TestRouteChannelEvent_PendingAskConsumesMessage: while a permission prompt
// is pending, the next plain message answers it instead of starting a turn.
func TestRouteChannelEvent_PendingAskConsumesMessage(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("seed"))
	replyCh, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	srv.routeChannelEvent(context.Background(), ad, evFor("yes"))

	select {
	case got := <-replyCh:
		if got != "yes" {
			t.Errorf("ask received %q, want %q", got, "yes")
		}
	case <-time.After(time.Second):
		t.Fatal("pending ask did not receive the message")
	}
}

// TestRouteChannelEvent_CommandBeatsPendingAsk: /stop must reach the command
// router even while an ask is pending — it cancels the turn that is asking.
func TestRouteChannelEvent_CommandBeatsPendingAsk(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("seed"))
	replyCh, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	srv.routeChannelEvent(context.Background(), ad, evFor("/stop"))

	select {
	case got := <-replyCh:
		t.Fatalf("command %q was consumed by the ask", got)
	default:
	}
	if len(ad.texts()) == 0 {
		t.Fatal("command produced no reply")
	}
}

// TestRouteChannelEvent_NormalMessageRunsTurn: with no pending ask, a plain
// message starts an agent turn that replies through the adapter.
func TestRouteChannelEvent_NormalMessageRunsTurn(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	srv.routeChannelEvent(context.Background(), ad, evFor("hello"))

	waitFor(t, func() bool {
		for _, txt := range ad.texts() {
			if strings.Contains(txt, "stub reply") {
				return true
			}
		}
		return false
	})
}

// TestHandleChannelMessage_SetsPerTurnGate: every IM turn gets a fresh
// permission gate (configured mode + chat-interactive ask) on the session
// agent — the factory-time strict gate is gone.
func TestHandleChannelMessage_SetsPerTurnGate(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("seed"))
	if sess.Agent.Gate != nil {
		t.Fatal("factory must not pre-set a gate anymore")
	}

	srv.handleChannelMessage(context.Background(), ad, evFor("hello"))

	if sess.Agent.Gate == nil {
		t.Error("handleChannelMessage must set a per-turn permission gate")
	}
}

// TestHandleChannelMessage_TypingKeepaliveWired is an end-to-end regression
// test for #1117: handleChannelMessage must start the typing keepalive
// before running the turn and stop it exactly once by the time it returns.
// This catches a future refactor silently dropping stopTyping somewhere in
// the handleChannelMessage -> runChannelTurns -> NewUIController chain — the
// unit tests for channel_typing.go and UIController's stopTyping callback
// each pass even if the wiring between them breaks.
func TestHandleChannelMessage_TypingKeepaliveWired(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	srv.handleChannelMessage(context.Background(), ad, evFor("hello"))

	sendTyping, stopTyping := ad.typingCounts()
	if sendTyping == 0 {
		t.Error("handleChannelMessage must call SendTyping at least once")
	}
	if stopTyping != 1 {
		t.Errorf("handleChannelMessage must call StopTyping exactly once, got %d", stopTyping)
	}
}

// TestRouteChannelEvent_OtherUserCannotAnswer pins the reply-scoping rule
// under a shared-session binding (BindByChat): a second user's "yes" in the
// same chat must not answer the requester's prompt.
func TestRouteChannelEvent_OtherUserCannotAnswer(t *testing.T) {
	srv := chanServer(t) // BindByChat: one session per chat, shared by users
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("seed"))
	replyCh, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	other := evFor("yes")
	other.UserID = "u2"
	srv.routeChannelEvent(context.Background(), ad, other)

	select {
	case got := <-replyCh:
		t.Fatalf("another user's reply %q answered the ask", got)
	default:
	}
}

// TestRouteChannelEvent_EmptyTextNeverAnswers: a text-less event (sticker,
// image) must not consume — and thereby deny — a pending ask.
func TestRouteChannelEvent_EmptyTextNeverAnswers(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	sess := srv.channelMgr.GetOrCreateSession(evFor("seed"))
	replyCh, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	srv.routeChannelEvent(context.Background(), ad, evFor("   "))

	select {
	case got := <-replyCh:
		t.Fatalf("empty text %q answered the ask", got)
	default:
	}
}

// blockingSender lets a test hold a turn open: the first call blocks until
// release is closed; inputs records the last user message of each call.
type blockingSender struct {
	mu      sync.Mutex
	calls   int
	inputs  []string
	started chan struct{} // signalled once per call
	release chan struct{} // first call blocks on this
}

func (b *blockingSender) record(msgs []agent.Message) (first bool) {
	b.mu.Lock()
	b.calls++
	first = b.calls == 1
	if len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		text := last.Content
		if text == "" {
			for _, blk := range last.Blocks {
				if blk.Type == "text" {
					text = blk.Text
				}
			}
		}
		b.inputs = append(b.inputs, text)
	}
	b.mu.Unlock()
	b.started <- struct{}{}
	return first
}

func (b *blockingSender) SendMessages(_ context.Context, _, _ string, msgs []agent.Message, _ int) (agent.Reply, error) {
	if b.record(msgs) {
		<-b.release
	}
	return agent.Reply{Content: "blocking reply"}, nil
}

func (b *blockingSender) StreamMessages(ctx context.Context, model, system string, msgs []agent.Message, maxTokens int, onChunk func(string), _ func(string)) (agent.Reply, error) {
	return b.SendMessages(ctx, model, system, msgs, maxTokens)
}

func (b *blockingSender) snapshot() (int, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, append([]string(nil), b.inputs...)
}

// TestRouteChannelEvent_MidTurnMessageSteers: a message during a running IM
// turn rides the turn's Inbox and chains into a follow-up turn — it must not
// queue as an independent second turn, and it must not be lost.
func TestRouteChannelEvent_MidTurnMessageSteers(t *testing.T) {
	sender := &blockingSender{started: make(chan struct{}, 8), release: make(chan struct{})}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(sender, "stub-model")
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}

	srv.routeChannelEvent(context.Background(), ad, evFor("first message"))
	<-sender.started // turn 1 is now blocked inside the sender

	srv.routeChannelEvent(context.Background(), ad, evFor("steer me in"))
	sess := srv.channelMgr.GetSession(evFor("x"))
	if !sess.Agent.Inbox.HasPending() {
		t.Fatal("mid-turn message did not land in the running turn's Inbox")
	}

	close(sender.release) // let turn 1 finish; the leftover steers must chain
	<-sender.started      // the chained turn hit the sender

	waitFor(t, func() bool { calls, _ := sender.snapshot(); return calls >= 2 })
	_, inputs := sender.snapshot()
	found := false
	for _, in := range inputs {
		if strings.Contains(in, "steer me in") {
			found = true
		}
	}
	if !found {
		t.Errorf("steer text never reached the model; inputs = %q", inputs)
	}
}

// TestHandleChannelMessage_PersistsTurn: an IM turn's history must land on
// disk so a fresh manager (post-restart) restores the conversation.
func TestHandleChannelMessage_PersistsTurn(t *testing.T) {
	tmp := t.TempDir() // isolated HOME: deterministic store IDs cross-pollinate otherwise
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	srv.handleChannelMessage(context.Background(), ad, evFor("please remember 42"))

	fresh := channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(&stubSender{}, "stub-model")
	}, channel.BindByChat)
	restored := fresh.GetOrCreateSession(evFor("x"))
	msgs := restored.Agent.History.Snapshot()
	if len(msgs) < 2 {
		t.Fatalf("restored history has %d messages, want >=2 (user+assistant)", len(msgs))
	}
}

// TestHandleChannelMessage_RefreshesSystemPerTurn: memory written after the
// session was created must be visible on the next turn without a restart.
func TestHandleChannelMessage_RefreshesSystemPerTurn(t *testing.T) {
	srv := chanServer(t)
	srv.memDir = t.TempDir()
	ad := &fullFakeAdapter{}

	srv.handleChannelMessage(context.Background(), ad, evFor("turn one"))
	sess := srv.channelMgr.GetSession(evFor("x"))
	if strings.Contains(sess.Agent.System, "fresh-fact-9000") {
		t.Fatal("memory marker present before it was written")
	}

	if err := os.WriteFile(srv.memDir+"/MEMORY.md", []byte("- fresh-fact-9000"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.handleChannelMessage(context.Background(), ad, evFor("turn two"))
	if !strings.Contains(sess.Agent.System, "fresh-fact-9000") {
		t.Error("system prompt not recomposed: memory written mid-session is invisible to IM turns")
	}
}

// TestHandleChannelMessage_WiresMemoryHooks: IM turns carry the L2 memory
// hooks (keyword reminders + save-nudge) like web turns do via buildAgent.
func TestHandleChannelMessage_WiresMemoryHooks(t *testing.T) {
	srv := chanServer(t)
	srv.memDir = t.TempDir()
	ad := &fullFakeAdapter{}

	srv.handleChannelMessage(context.Background(), ad, evFor("hello"))

	sess := srv.channelMgr.GetSession(evFor("x"))
	if !sess.Agent.Hooks.Configured(hooks.EventUserPromptSubmit) {
		t.Error("IM agent missing UserPromptSubmit hook (keyword reminders)")
	}
	if !sess.Agent.Hooks.Configured(hooks.EventPostToolUse) {
		t.Error("IM agent missing PostToolUse hook (save-nudge)")
	}
}

// TestHandleChannelMessage_RejectsTurnWhenBoundToOtherEntry: when the session
// is owned by another entry, the channel turn is rejected and the reply hints
// at /new and /bind --force.
func TestHandleChannelMessage_RejectsTurnWhenBoundToOtherEntry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	// Pre-create the chat's deterministic store and mark it web-bound.
	sess := srv.channelMgr.GetOrCreateSession(evFor("seed"))
	storeID := sess.Store.ID
	path, err := sess.Store.SavePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	webBound := agent.NewSession("stub-model", "")
	webBound.ID = storeID
	webBound.Source = "channel"
	webBound.BoundEntry = agent.EntryWeb
	webBound.BoundAt = time.Now()
	if err := webBound.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := agent.LoadSession(storeID)
	if err != nil {
		t.Fatal(err)
	}
	sess.Store = loaded

	srv.handleChannelMessage(context.Background(), ad, evFor("hello again"))

	waitFor(t, func() bool {
		for _, txt := range ad.texts() {
			if strings.Contains(txt, "bound to web") {
				return true
			}
		}
		return false
	})

	for _, txt := range ad.texts() {
		if strings.Contains(txt, "bound to web") {
			if !strings.Contains(txt, "/new") || !strings.Contains(txt, "/list") || !strings.Contains(txt, "/bind --force <number>") {
				t.Errorf("rejection message missing hints: %q", txt)
			}
			return
		}
	}
	t.Fatal("expected rejection text from adapter")
}

// TestHandleChannelMessage_RecoversWhenSessionFileDeletedExternally (#1079):
// if the chat's bound session was deleted from the web UI while this IM
// session sat idle in the manager's cache, the next message used to hit
// acquireSessionBinding's authoritative LoadSession and fail outright with a
// confusing "session ... not found" error — the reporter's expectation was
// that the chat should just start fresh instead.
func TestHandleChannelMessage_RecoversWhenSessionFileDeletedExternally(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := chanServer(t)
	ad := &fullFakeAdapter{}

	// Establish the chat's session and give it some history, exactly like an
	// ordinary prior conversation.
	sess := srv.channelMgr.GetOrCreateSession(evFor("seed"))
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "old message"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	path, err := sess.Store.SavePath()
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the web UI deleting the session file entirely — nothing else
	// (no other entry's binding) takes its place.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	srv.handleChannelMessage(context.Background(), ad, evFor("hello again"))

	waitFor(t, func() bool { return len(ad.texts()) > 0 })

	for _, txt := range ad.texts() {
		if strings.Contains(txt, "not found") {
			t.Fatalf("turn surfaced the stale-session error instead of starting fresh: %q", txt)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("session file was not recreated on disk: %v", err)
	}
}

// TestHandleChannelMessage_AdvertisesWorkflowToTopLevelTurn (#1133 follow-up):
// runChannelTurns used to compute the top-level turn's tool list before
// stamping the ctx-scoped sub-agent manager that carries its spawner, so
// tools.DefaultToolsForCtx saw no manager yet for that call — the IM
// channel's own turn never advertised workflow (only its spawned children
// did, via their own ctx-threaded toolsFn), even though the IM path never
// depended on prepareToolTurn's removed global swap in the first place (it
// doesn't call prepareToolTurn at all — it builds its own sub-agent manager
// inline).
func TestHandleChannelMessage_AdvertisesWorkflowToTopLevelTurn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	rec := &recordingSender{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(rec, "stub-model")
	}, channel.BindByChat)

	ad := &fullFakeAdapter{}
	srv.handleChannelMessage(context.Background(), ad, evFor("hello"))

	waitFor(t, func() bool { return len(rec.lastTools) > 0 })

	found := false
	for _, d := range rec.lastTools {
		if d.Name == "workflow" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("workflow not advertised to top-level IM turn; got %v", rec.lastTools)
	}
}

// TestHandleChannelMessage_BackgroundCompletionTriggersIdleTurn: when an
// async background process finishes after the synchronous turn chain has
// ended, the completion note is drained into a follow-up idle turn so the
// model can react without waiting for the user's next message.
func TestHandleChannelMessage_BackgroundCompletionTriggersIdleTurn(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(&stubSender{}, "stub-model")
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}

	srv.handleChannelMessage(context.Background(), ad, evFor("hello"))

	// Wait for the user-initiated turn to finish.
	waitFor(t, func() bool {
		for _, txt := range ad.texts() {
			if strings.Contains(txt, "stub reply") {
				return true
			}
		}
		return false
	})

	// Simulate an async background process exiting after the turn went idle.
	sess := srv.channelMgr.GetSession(evFor("x"))
	bgMgr := tools.SessionBackgroundManager("im:" + string(sess.Key))
	bgMgr.FireExitHook(tools.BgExit{ID: "bg_1", Command: "make build", Status: "exited: 0", NewOutput: "done"})

	// The idle turn should react to the completion note.
	waitFor(t, func() bool {
		count := 0
		for _, txt := range ad.texts() {
			if strings.Contains(txt, "stub reply") {
				count++
			}
		}
		return count >= 2
	})

	// The completion note must have reached the model via history.
	found := false
	for _, m := range sess.Agent.History.Snapshot() {
		if strings.Contains(m.Content, "[BACKGROUND COMPLETED]") {
			found = true
			break
		}
	}
	if !found {
		t.Error("background completion note never reached the agent's history")
	}
}

// TestRunChannelIdleTurn_SkipsWhenBoundToOtherEntry: if another entry has
// taken over the session while the IM chat was idle, the idle follow-up turn
// must not run.
func TestRunChannelIdleTurn_SkipsWhenBoundToOtherEntry(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.channelMgr = channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(&stubSender{}, "stub-model")
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}
	// Unique chat id so this test's deterministic store ID can't collide with
	// other channel tests (evFor hardcodes ChatID "c1"). They share the
	// process-wide TestMain HOME, and turn paths leak fire-and-forget save
	// goroutines that outlive a test — one of those re-saving its EntryChannel
	// session over our EntryWeb binding mid-test is what made this flaky.
	ev := channel.InboundEvent{Platform: "fake", ChatID: "c-idle-skip", UserID: "u1", MessageID: "m1", Text: "hello"}

	// Pre-create the store file bound to web so the idle turn sees the
	// authoritative binding and refuses to run.
	// First create the channel session to learn its deterministic store ID,
	// then overwrite the file with a web-bound meta record.
	sess := srv.channelMgr.GetOrCreateSession(ev)
	storeID := sess.Store.ID
	path, err := sess.Store.SavePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	webBound := agent.NewSession("stub-model", "")
	webBound.ID = storeID
	webBound.Source = "channel"
	webBound.BoundEntry = agent.EntryWeb
	webBound.BoundAt = time.Now()
	if err := webBound.Save(); err != nil {
		t.Fatal(err)
	}

	// Reload so the in-memory store reflects the web-bound file.
	loaded, err := agent.LoadSession(storeID)
	if err != nil {
		t.Fatal(err)
	}
	sess.Store = loaded

	sess.Agent.Inbox.Enqueue("<system-reminder>[BACKGROUND COMPLETED] bg done.</system-reminder>")
	srv.runChannelIdleTurn(context.Background(), sess, ad, ev)

	// The adapter must not have sent a reply from the idle turn.
	for _, txt := range ad.texts() {
		if strings.Contains(txt, "stub reply") {
			t.Fatal("idle turn ran while session was bound to another entry")
		}
	}
}

// TestInjectorFor_SessionStickyAndDroppedOnUnbind: the injector's
// once-per-session recall latch must survive turns but reset with /unbind.
func TestInjectorFor_SessionStickyAndDroppedOnUnbind(t *testing.T) {
	srv := chanServer(t)
	srv.memDir = t.TempDir()
	ad := &fullFakeAdapter{}
	ev := evFor("/unbind")

	key := "im:" + string(srv.channelMgr.KeyFor(ev))
	first := srv.injectorFor(key)
	if first == nil {
		t.Fatal("injectorFor returned nil")
	}
	if srv.injectorFor(key) != first {
		t.Error("injector must be sticky across turns in one session")
	}

	srv.handleChannelCommand(ad, ev)
	if srv.injectorFor(key) == first {
		t.Error("/unbind must drop the session injector (fresh recall latch)")
	}
}

// TestInjectorFor_DroppedOnNew: the injector's once-per-session recall latch
// must reset with /new, which creates a brand-new session.
func TestInjectorFor_DroppedOnNew(t *testing.T) {
	srv := chanServer(t)
	srv.memDir = t.TempDir()
	ad := &fullFakeAdapter{}
	ev := evFor("/new")

	key := "im:" + string(srv.channelMgr.KeyFor(ev))
	first := srv.injectorFor(key)
	if first == nil {
		t.Fatal("injectorFor returned nil")
	}

	srv.handleChannelCommand(ad, ev)
	if srv.injectorFor(key) == first {
		t.Error("/new must drop the session injector (fresh recall latch)")
	}
}
