package channel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

// mockAdapter is a test double that records sent messages.
type mockAdapter struct {
	platform      string
	mu            sync.Mutex
	sentTexts     []sentText
	sentFiles     []sentFile
	updatedMsgs   []updatedMsg
	started       bool
	stopped       bool
	validateErrs  []string
	onMessageFunc func(InboundEvent)

	// issueMsgIDs makes SendText return sequential message IDs ("m1", "m2",
	// …) like real adapters do, so tests can exercise the in-place update
	// path (it only engages once a message ID is known). Off by default —
	// legacy tests assert plain send counts.
	issueMsgIDs bool
	// noUpdates reports SupportsMessageUpdates() == false (a DingTalk/WeCom/
	// Weixin-shaped platform).
	noUpdates bool
	// failUpdates makes UpdateMessage report failure (edit-size cap /
	// deleted message), without recording the attempt as delivered.
	failUpdates bool
	// failSends makes SendText report failure without recording the attempt
	// as delivered — simulates a network/API error on the fallback send path.
	failSends bool
}

type sentText struct {
	chatID  string
	text    string
	replyTo string
}
type sentFile struct {
	chatID  string
	path    string
	name    string
	replyTo string
}
type updatedMsg struct {
	chatID    string
	messageID string
	text      string
}

func (m *mockAdapter) Platform() string { return m.platform }
func (m *mockAdapter) Start(ctx context.Context, onMessage func(InboundEvent)) error {
	m.started = true
	m.onMessageFunc = onMessage
	<-ctx.Done()
	return ctx.Err()
}
func (m *mockAdapter) Stop() error {
	m.stopped = true
	return nil
}
func (m *mockAdapter) SendText(chatID, text, replyTo string) SendResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSends {
		return SendResult{OK: false, Error: "mock send failure"}
	}
	m.sentTexts = append(m.sentTexts, sentText{chatID, text, replyTo})
	res := SendResult{OK: true}
	if m.issueMsgIDs {
		res.MessageID = fmt.Sprintf("m%d", len(m.sentTexts))
	}
	return res
}
func (m *mockAdapter) SendFile(chatID, path, name, replyTo string) SendResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentFiles = append(m.sentFiles, sentFile{chatID, path, name, replyTo})
	return SendResult{OK: true}
}
func (m *mockAdapter) UpdateMessage(chatID, messageID, text string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failUpdates {
		return false
	}
	m.updatedMsgs = append(m.updatedMsgs, updatedMsg{chatID, messageID, text})
	return true
}
func (m *mockAdapter) SupportsMessageUpdates() bool                 { return !m.noUpdates }
func (m *mockAdapter) SendTyping(chatID, contextToken string) error { return nil }
func (m *mockAdapter) StopTyping(chatID, contextToken string) error { return nil }
func (m *mockAdapter) Flush(chatID string)                          {}
func (m *mockAdapter) SupportsButtons() bool                        { return false }
func (m *mockAdapter) SendButtons(chatID, text string, buttons []Button, replyTo string) SendResult {
	return m.SendText(chatID, text, replyTo)
}
func (m *mockAdapter) ValidateConfig(cfg PlatformConfig) []string { return m.validateErrs }

func (m *mockAdapter) sentTextCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sentTexts)
}
func (m *mockAdapter) lastSentText() sentText {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sentTexts) == 0 {
		return sentText{}
	}
	return m.sentTexts[len(m.sentTexts)-1]
}

func fakeAgentFactory() *agent.Agent {
	return agent.New(fakeSender{}, "test-model")
}

type fakeSender struct{}

func (f fakeSender) SendMessages(ctx context.Context, model, system string, messages []agent.Message, maxTokens int) (agent.Reply, error) {
	return agent.Reply{Content: "ok"}, nil
}

func TestManager_SessionBindingModes(t *testing.T) {
	ev := InboundEvent{Platform: "mock", ChatID: "chat1", UserID: "user1"}

	tests := []struct {
		mode BindingMode
		want SessionKey
	}{
		{BindByChatUser, "mock:chat1:user1"},
		{BindByChat, "mock:chat1"},
		{BindByUser, "mock:user1"},
	}

	for _, tt := range tests {
		got := sessionKeyFor(tt.mode, ev)
		if got != tt.want {
			t.Errorf("mode %q: got %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestManager_CommandRouter(t *testing.T) {
	Register("mock", func(pc PlatformConfig) (Adapter, error) {
		return &mockAdapter{platform: "mock"}, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock": {"enabled": true},
		},
	}
	tempHome(t)
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	// A session exists once the chat has spoken.
	ev := InboundEvent{Platform: "mock", ChatID: "c1", UserID: "u1"}
	mgr.GetOrCreateSession(ev)
	if mgr.SessionCount() != 1 {
		t.Fatalf("expected 1 session, got %d", mgr.SessionCount())
	}

	// /bind with no target explains usage and creates nothing.
	ev.Text = "/bind"
	if reply := mgr.CommandRouter(ev); !strings.Contains(strings.ToLower(reply), "usage") {
		t.Fatalf("expected usage hint for bare /bind, got %q", reply)
	}

	// /status
	ev.Text = "/status"
	if reply := mgr.CommandRouter(ev); reply == "" {
		t.Fatal("expected non-empty reply for /status")
	}

	// /list
	ev.Text = "/list"
	if reply := mgr.CommandRouter(ev); reply == "" {
		t.Fatal("expected non-empty reply for /list")
	}

	// /stop with no turn in flight (does not delete the session)
	ev.Text = "/stop"
	if reply := mgr.CommandRouter(ev); !strings.Contains(reply, "No task is running") {
		t.Fatalf("expected idle /stop reply, got %q", reply)
	}
	if mgr.SessionCount() != 1 {
		t.Fatalf("expected session to survive /stop, got %d", mgr.SessionCount())
	}

	// /unbind detaches the live session (history is kept on disk).
	ev.Text = "/unbind"
	mgr.CommandRouter(ev)
	if mgr.SessionCount() != 0 {
		t.Fatalf("expected 0 live sessions after /unbind, got %d", mgr.SessionCount())
	}
}

func TestManager_StopInterruptsRunningTurn(t *testing.T) {
	tempHome(t)
	cfg := &Config{Channels: map[string]PlatformConfig{}}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ev := InboundEvent{Platform: "mock", ChatID: "c1", UserID: "u1"}
	sess := mgr.GetOrCreateSession(ev)

	runCtx, done := sess.BeginRun(context.Background())
	defer done()

	ev.Text = "/stop"
	reply := mgr.CommandRouter(ev)
	if !strings.Contains(reply, "Task interrupted") {
		t.Fatalf("expected interrupt reply, got %q", reply)
	}
	if runCtx.Err() == nil {
		t.Fatal("expected the run context to be cancelled by /stop")
	}

	// A second /stop finds nothing to interrupt.
	if reply := mgr.CommandRouter(ev); !strings.Contains(reply, "No task is running") {
		t.Fatalf("expected idle reply on second /stop, got %q", reply)
	}
}

func TestSession_BeginRunSerialisesTurns(t *testing.T) {
	sess := &Session{}

	_, done1 := sess.BeginRun(context.Background())

	second := make(chan struct{})
	go func() {
		_, done2 := sess.BeginRun(context.Background())
		done2()
		close(second)
	}()

	select {
	case <-second:
		t.Fatal("second turn started before the first finished")
	case <-time.After(50 * time.Millisecond):
	}

	done1()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("second turn never started after the first finished")
	}
}

// TestManager_UnbindMidTurn_SuppressesButKeepsRunning: /unbind while a turn is
// in flight must suppress further IM delivery (so the chat is left) but must
// NOT cancel the turn — unlike /stop — and must keep the store file. This is
// the /unbind mid-turn contract that hands the session to web.
func TestManager_UnbindMidTurn_SuppressesButKeepsRunning(t *testing.T) {
	tempHome(t)
	cfg := &Config{Channels: map[string]PlatformConfig{}}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ev := InboundEvent{Platform: "mock", ChatID: "c1", UserID: "u1"}
	sess := mgr.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "q"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	path, err := sess.Store.SavePath()
	if err != nil {
		t.Fatal(err)
	}

	// Begin a turn and keep it in flight (don't call done).
	runCtx, done := sess.BeginRun(context.Background())
	defer done()

	ev.Text = "/unbind"
	reply := mgr.CommandRouter(ev)
	if !strings.Contains(strings.ToLower(reply), "unbound") {
		t.Fatalf("expected unbind reply, got %q", reply)
	}

	// The turn must NOT have been interrupted (/unbind is not /stop).
	if runCtx.Err() != nil {
		t.Fatal("/unbind must not cancel the in-flight turn")
	}
	if !sess.IsRunning() {
		t.Fatal("/unbind must leave the turn running")
	}
	// Suppress delivery from now on.
	if !sess.SuppressDelivery() {
		t.Fatal("/unbind mid-turn must set suppressDelivery")
	}
	// Store must be preserved.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("/unbind must keep the persisted history, but the file is gone: %v", err)
	}
}

// TestManager_UnbindThenBindBackRecoversRunningSession: /unbind mid-turn
// suppresses delivery and removes the session from the active map, but /bind
// back to the same session must recover the in-memory session object: clear
// suppressDelivery and re-add it to the sessions map so the running turn's
// remaining output resumes delivery.
func TestManager_UnbindThenBindBackRecoversRunningSession(t *testing.T) {
	tempHome(t)
	cfg := &Config{Channels: map[string]PlatformConfig{}}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ev := InboundEvent{Platform: "mock", ChatID: "c1", UserID: "u1"}
	sess := mgr.GetOrCreateSession(ev)
	sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: "q"})
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	storeID := sess.Store.ID

	// Begin a turn and keep it in flight.
	runCtx, done := sess.BeginRun(context.Background())
	defer done()

	// /unbind: suppress delivery, remove from sessions map.
	ev.Text = "/unbind"
	reply := mgr.CommandRouter(ev)
	if !strings.Contains(strings.ToLower(reply), "unbound") {
		t.Fatalf("expected unbind reply, got %q", reply)
	}
	if !sess.SuppressDelivery() {
		t.Fatal("/unbind must set suppressDelivery")
	}
	key := sessionKeyFor(BindByChatUser, ev)
	if _, ok := mgr.sessions.Load(key); ok {
		t.Fatal("/unbind must remove session from sessions map")
	}
	if runCtx.Err() != nil {
		t.Fatal("/unbind must not cancel the in-flight turn")
	}

	// /bind back to the same session by its store ID.
	ev.Text = "/bind " + storeID
	reply = mgr.CommandRouter(ev)
	if !strings.Contains(strings.ToLower(reply), "bound") {
		t.Fatalf("expected bind reply, got %q", reply)
	}

	// Verify: the SAME session object is back in the map, delivery restored.
	got, ok := mgr.sessions.Load(key)
	if !ok {
		t.Fatal("/bind must re-add the session to the sessions map")
	}
	gotSess := got.(*Session)
	if gotSess != sess {
		t.Fatal("/bind must recover the same session object, not create a new one")
	}
	if gotSess.SuppressDelivery() {
		t.Fatal("/bind must clear suppressDelivery on the recovered session")
	}
	if runCtx.Err() != nil {
		t.Fatal("/bind must not cancel the in-flight turn")
	}
}

func TestSession_InterruptIdleIsNoop(t *testing.T) {
	sess := &Session{}
	if sess.Interrupt() {
		t.Fatal("Interrupt on an idle session should report false")
	}
	// done() after Interrupt must not double-release or panic.
	ctx, done := sess.BeginRun(context.Background())
	if !sess.Interrupt() {
		t.Fatal("Interrupt on a running session should report true")
	}
	if ctx.Err() == nil {
		t.Fatal("expected cancelled ctx")
	}
	done()
}

func TestManager_AutoSessionCreation(t *testing.T) {
	tempHome(t)
	Register("mock2", func(pc PlatformConfig) (Adapter, error) {
		return &mockAdapter{platform: "mock2"}, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock2": {"enabled": true},
		},
	}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ev := InboundEvent{Platform: "mock2", ChatID: "c1", UserID: "u1", Text: "hello"}
	mgr.GetOrCreateSession(ev)

	if mgr.SessionCount() != 1 {
		t.Fatalf("expected 1 auto-created session, got %d", mgr.SessionCount())
	}

	sess := mgr.GetOrCreateSession(ev)
	if sess == nil {
		t.Fatal("expected session to exist")
	}
	if sess.ChatID != "c1" || sess.UserID != "u1" {
		t.Fatalf("unexpected session chat/user: %s/%s", sess.ChatID, sess.UserID)
	}
}

func TestManager_UnknownCommand(t *testing.T) {
	tempHome(t)
	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	ev := InboundEvent{Text: "/foobar"}
	reply := mgr.CommandRouter(ev)
	if reply == "" {
		t.Fatal("expected error reply for unknown command")
	}
	if !contains(reply, "Unknown command") {
		t.Fatalf("expected 'Unknown command' in reply, got: %s", reply)
	}
}

// TestCmdClear_RefusesWhileRunning: /clear must not race an in-flight turn.
func TestCmdClear_RefusesWhileRunning(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	sess := mgr.GetOrCreateSession(ev)
	runCtx, done := sess.BeginRun(context.Background())
	defer done()

	reply := mgr.cmdClear(ev)
	if !strings.Contains(strings.ToLower(reply), "can't clear") {
		t.Fatalf("expected refusal while running, got %q", reply)
	}
	if runCtx.Err() != nil {
		t.Fatal("/clear should not cancel the running turn")
	}
}

// TestCmdCompact_FoldsHistory: /compact summarizes older turns and persists the
// shorter history.
func TestCmdCompact_FoldsHistory(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	sess := mgr.GetOrCreateSession(ev)
	// Shrink the keep budget so the small test history still has something to fold.
	sess.Agent.CompactKeepFraction = 0.001

	for i := 0; i < 4; i++ {
		sess.Agent.History.Append(agent.Message{Role: agent.RoleUser, Content: strings.Repeat("a ", 500)})
		sess.Agent.History.Append(agent.Message{Role: agent.RoleAssistant, Content: strings.Repeat("b ", 500)})
	}
	if err := sess.Persist(); err != nil {
		t.Fatal(err)
	}
	before := len(sess.Agent.History.Snapshot())

	reply := mgr.cmdCompact(ev)
	if !strings.Contains(strings.ToLower(reply), "compact") &&
		!strings.Contains(strings.ToLower(reply), "folded") &&
		!strings.Contains(strings.ToLower(reply), "reclaimed") {
		t.Fatalf("unexpected compact reply %q", reply)
	}

	after := len(sess.Agent.History.Snapshot())
	if after >= before {
		t.Errorf("history not reduced: %d -> %d messages", before, after)
	}

	reloaded, err := agent.LoadSession(sess.Store.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Messages) != after {
		t.Errorf("persisted messages %d != in-memory %d", len(reloaded.Messages), after)
	}
}

// fakeModelOps returns a ModelOps with two configured models: "test-model"
// (the default, matching fakeAgentFactory) and "other-model".
func fakeModelOps() *ModelOps {
	return &ModelOps{
		List: func() []ModelInfo {
			return []ModelInfo{
				{Model: "test-model", Provider: "fake", Default: true},
				{Model: "other-model", Provider: "fake"},
			}
		},
		Resolve: func(modelID string) (ModelResolution, error) {
			switch modelID {
			case "other-model":
				return ModelResolution{Sender: fakeSender{}, Model: "other-model", BoundEntry: "other-model"}, nil
			case "default":
				return ModelResolution{Sender: fakeSender{}, Model: "test-model"}, nil
			default:
				return ModelResolution{}, fmt.Errorf("model %q is not configured (available: other-model, test-model)", modelID)
			}
		},
	}
}

// TestCmdModel_ListMarksCurrent: /model with no argument lists every
// configured model and marks the session's current one.
func TestCmdModel_ListMarksCurrent(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}
	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	mgr.SetModelOps(fakeModelOps())
	mgr.GetOrCreateSession(ev)

	reply := mgr.cmdModel(ev, "")
	for _, want := range []string{"test-model", "other-model", "current"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("listing should contain %q, got %q", want, reply)
		}
	}
}

// TestCmdModel_SwitchPersistsBinding: /model <name> swaps the live agent's
// sender+model and persists the binding to the session store.
func TestCmdModel_SwitchPersistsBinding(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}
	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	mgr.SetModelOps(fakeModelOps())
	sess := mgr.GetOrCreateSession(ev)

	reply := mgr.cmdModel(ev, "other-model")
	if !strings.Contains(reply, "other-model") {
		t.Fatalf("unexpected switch reply %q", reply)
	}
	if sess.Agent.Model != "other-model" {
		t.Errorf("agent model = %q, want other-model", sess.Agent.Model)
	}
	if sess.Store.ModelConfig != "other-model" || sess.Store.Model != "other-model" {
		t.Errorf("store binding = (%q, %q), want (other-model, other-model)", sess.Store.ModelConfig, sess.Store.Model)
	}
	// The binding must survive a reload from disk.
	reloaded, err := agent.LoadSession(sess.Store.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ModelConfig != "other-model" {
		t.Errorf("reloaded ModelConfig = %q, want other-model", reloaded.ModelConfig)
	}

	// /model default unbinds back to the default.
	if reply := mgr.cmdModel(ev, "default"); !strings.Contains(reply, "test-model") {
		t.Fatalf("unexpected default reply %q", reply)
	}
	if sess.Store.ModelConfig != "" {
		t.Errorf("ModelConfig after default = %q, want unbound", sess.Store.ModelConfig)
	}
}

// TestCmdModel_RejectsUnknownAndRunning: an unconfigured model leaves the
// session unchanged, and a switch is refused mid-turn.
func TestCmdModel_RejectsUnknownAndRunning(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}
	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	mgr.SetModelOps(fakeModelOps())
	sess := mgr.GetOrCreateSession(ev)

	reply := mgr.cmdModel(ev, "nope")
	if !strings.Contains(reply, "not configured") {
		t.Fatalf("expected not-configured error, got %q", reply)
	}
	if sess.Agent.Model != "test-model" {
		t.Errorf("agent model changed to %q on a failed switch", sess.Agent.Model)
	}
	if sess.Store.ModelConfig != "" {
		t.Errorf("store binding changed to %q on a failed switch", sess.Store.ModelConfig)
	}

	_, done := sess.BeginRun(context.Background())
	defer done()
	reply = mgr.cmdModel(ev, "other-model")
	if !strings.Contains(strings.ToLower(reply), "can't switch") {
		t.Fatalf("expected refusal while running, got %q", reply)
	}
}

// TestCmdModel_GracefulWithoutOpsOrSession: /model degrades cleanly when the
// server injected no ModelOps, and when the chat has no session yet.
func TestCmdModel_GracefulWithoutOpsOrSession(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	if reply := mgr.cmdModel(ev, "other-model"); !strings.Contains(reply, "unavailable") {
		t.Fatalf("expected unavailable reply without ModelOps, got %q", reply)
	}

	mgr.SetModelOps(fakeModelOps())
	if reply := mgr.cmdModel(ev, "other-model"); !strings.Contains(reply, "No active session") {
		t.Fatalf("expected no-session reply, got %q", reply)
	}
}

// TestCmdCompact_NoOpOnTinyHistory: /compact gracefully no-ops when there isn't
// enough history to fold safely.
func TestCmdCompact_NoOpOnTinyHistory(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	mgr.GetOrCreateSession(ev)

	reply := mgr.cmdCompact(ev)
	if !strings.Contains(strings.ToLower(reply), "nothing to compact") {
		t.Fatalf("expected no-op reply, got %q", reply)
	}
}

// TestCmdCompact_RefusesWhileRunning: /compact must not race an in-flight turn.
func TestCmdCompact_RefusesWhileRunning(t *testing.T) {
	tempHome(t)
	ev := InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"}

	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	sess := mgr.GetOrCreateSession(ev)
	runCtx, done := sess.BeginRun(context.Background())
	defer done()

	reply := mgr.cmdCompact(ev)
	if !strings.Contains(strings.ToLower(reply), "can't compact") {
		t.Fatalf("expected refusal while running, got %q", reply)
	}
	if runCtx.Err() != nil {
		t.Fatal("/compact should not cancel the running turn")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}
func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
