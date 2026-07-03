package channel

import (
	"context"
	"fmt"
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
func (m *mockAdapter) ValidateConfig(cfg PlatformConfig) []string   { return m.validateErrs }

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

func TestManager_StartStop(t *testing.T) {
	tempHome(t)
	Register("mock", func(pc PlatformConfig) (Adapter, error) {
		return &mockAdapter{platform: "mock"}, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock": {"enabled": true},
		},
	}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start blocks; run it in a goroutine.
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()

	time.Sleep(50 * time.Millisecond)
	if !mgr.IsRunning() {
		t.Fatal("expected manager to be running")
	}

	mgr.Stop()
	<-done

	if mgr.IsRunning() {
		t.Fatal("expected manager to be stopped")
	}
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
	mgr.handleSessionMessage(context.Background(), ev)

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

func TestManager_SendReply(t *testing.T) {
	tempHome(t)
	mock := &mockAdapter{platform: "mock3"}
	Register("mock3", func(pc PlatformConfig) (Adapter, error) {
		return mock, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock3": {"enabled": true},
		},
	}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	// Manually store the adapter so sendReply works without Start().
	mgr.adapters.Store("mock3", mock)

	ev := InboundEvent{Platform: "mock3", ChatID: "c1", MessageID: "m1"}
	mgr.sendReply(ev, "hello back")

	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text, got %d", mock.sentTextCount())
	}
	last := mock.lastSentText()
	if last.text != "hello back" || last.chatID != "c1" {
		t.Fatalf("unexpected sent text: %+v", last)
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
