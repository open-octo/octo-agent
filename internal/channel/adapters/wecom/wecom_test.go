package wecom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/open-octo/octo-agent/internal/channel"
)

// fakeWecom is a WebSocket server speaking the aibot frame protocol: it acks
// the subscribe (rejecting wrong secrets), pushes queued callbacks, and acks
// aibot_send_msg frames.
type fakeWecom struct {
	mu        sync.Mutex
	callbacks []map[string]any
	sent      []map[string]any
	srv       *httptest.Server
	wsURL     string
}

type rawFrame struct {
	Cmd     string `json:"cmd"`
	Headers struct {
		ReqID string `json:"req_id"`
	} `json:"headers"`
	Body map[string]any `json:"body"`
}

func newFakeWecom(t *testing.T) *fakeWecom {
	f := &fakeWecom{}
	upgrader := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		f.runConn(t, conn)
	}))
	t.Cleanup(f.srv.Close)
	f.wsURL = "ws" + strings.TrimPrefix(f.srv.URL, "http")
	return f
}

func (f *fakeWecom) runConn(t *testing.T, conn *websocket.Conn) {
	for {
		var frame rawFrame
		if err := conn.ReadJSON(&frame); err != nil {
			return
		}
		switch frame.Cmd {
		case "aibot_subscribe":
			if frame.Body["secret"] != "good-secret" {
				conn.WriteJSON(map[string]any{
					"headers": map[string]string{"req_id": frame.Headers.ReqID},
					"errcode": 40001, "errmsg": "invalid secret",
				})
				continue
			}
			conn.WriteJSON(map[string]any{
				"headers": map[string]string{"req_id": frame.Headers.ReqID},
				"errcode": 0,
			})
			// Auth OK — push queued inbound callbacks.
			f.mu.Lock()
			cbs := f.callbacks
			f.mu.Unlock()
			for _, cb := range cbs {
				conn.WriteJSON(map[string]any{
					"cmd":     "aibot_msg_callback",
					"headers": map[string]string{"req_id": "cb_1"},
					"body":    cb,
				})
			}
		case "aibot_send_msg":
			f.mu.Lock()
			f.sent = append(f.sent, frame.Body)
			f.mu.Unlock()
			conn.WriteJSON(map[string]any{
				"headers": map[string]string{"req_id": frame.Headers.ReqID},
				"errcode": 0,
			})
		case "ping":
			conn.WriteJSON(map[string]any{
				"headers": map[string]string{"req_id": frame.Headers.ReqID},
				"errcode": 0,
			})
		}
	}
}

func textCallback(chatID, chatType, userID, content string) map[string]any {
	return map[string]any{
		"msgtype":  "text",
		"chatid":   chatID,
		"chattype": chatType,
		"msgid":    "msg-1",
		"from":     map[string]string{"userid": userID},
		"text":     map[string]string{"content": content},
	}
}

func newTestAdapter(t *testing.T, f *fakeWecom, extra map[string]any) *Adapter {
	cfg := channel.PlatformConfig{"bot_id": "aib123", "secret": "good-secret", "ws_url": f.wsURL}
	for k, v := range extra {
		cfg[k] = v
	}
	ad, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return ad.(*Adapter)
}

func collectEvents(t *testing.T, a *Adapter, want int) []channel.InboundEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var mu sync.Mutex
	var events []channel.InboundEvent
	done := make(chan struct{})
	go func() {
		_ = a.Start(ctx, func(ev channel.InboundEvent) {
			mu.Lock()
			events = append(events, ev)
			if len(events) >= want {
				cancel()
			}
			mu.Unlock()
		})
		close(done)
	}()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return events
}

func TestNew_RequiresCredentials(t *testing.T) {
	if _, err := New(channel.PlatformConfig{}); err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if _, err := New(channel.PlatformConfig{"bot_id": "x"}); err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestStart_DeliversTextMessage(t *testing.T) {
	f := newFakeWecom(t)
	f.callbacks = []map[string]any{textCallback("chat-1", "single", "user-7", "hello octo")}
	a := newTestAdapter(t, f, nil)

	events := collectEvents(t, a, 1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Platform != "wecom" || ev.ChatID != "chat-1" || ev.UserID != "user-7" ||
		ev.Text != "hello octo" || ev.ChatType != "direct" || ev.MessageID != "msg-1" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestStart_GroupChatType(t *testing.T) {
	f := newFakeWecom(t)
	f.callbacks = []map[string]any{textCallback("chat-g", "group", "user-7", "hi")}
	a := newTestAdapter(t, f, nil)

	events := collectEvents(t, a, 1)
	if len(events) != 1 || events[0].ChatType != "group" {
		t.Fatalf("expected group event, got %+v", events)
	}
}

// #1122: without bot_nickname configured, group gating stays off — matches
// pre-fix behavior so existing deployments that haven't set it don't
// suddenly go silent in every group.
func TestStart_GroupChatType_NoNicknameConfiguredNoGate(t *testing.T) {
	f := newFakeWecom(t)
	f.callbacks = []map[string]any{textCallback("chat-g", "group", "user-7", "hi, no mention here")}
	a := newTestAdapter(t, f, nil)

	events := collectEvents(t, a, 1)
	if len(events) != 1 {
		t.Fatalf("expected the ungated group message to pass through, got %+v", events)
	}
}

// #1122: with bot_nickname configured, a group message that doesn't mention
// the bot is dropped — previously every group message was processed
// regardless of chat type. The mention itself is stripped from the text
// that reaches the agent, matching Telegram/Feishu/DingTalk's own handling
// of their mention syntax.
func TestStart_GroupRequiresMentionWhenNicknameConfigured(t *testing.T) {
	f := newFakeWecom(t)
	f.callbacks = []map[string]any{
		textCallback("chat-g", "group", "user-7", "hi everyone, no mention here"),
		textCallback("chat-g", "group", "user-7", "@octo help me with this"),
	}
	a := newTestAdapter(t, f, map[string]any{"bot_nickname": "octo"})

	events := collectEvents(t, a, 1)
	if len(events) != 1 {
		t.Fatalf("expected only the @-mentioning message to pass, got %+v", events)
	}
	if events[0].Text != "help me with this" {
		t.Fatalf("expected the mention to be stripped from the forwarded text, got: %+v", events[0])
	}
}

// A single (non-group) chat should never be mention-gated, even with
// bot_nickname configured — the gate only applies to groups.
func TestStart_SingleChatNeverMentionGated(t *testing.T) {
	f := newFakeWecom(t)
	f.callbacks = []map[string]any{textCallback("user-7", "single", "user-7", "no mention needed here")}
	a := newTestAdapter(t, f, map[string]any{"bot_nickname": "octo"})

	events := collectEvents(t, a, 1)
	if len(events) != 1 {
		t.Fatalf("expected the DM to pass through ungated, got %+v", events)
	}
}

func TestStart_AllowedUsersFilters(t *testing.T) {
	f := newFakeWecom(t)
	f.callbacks = []map[string]any{
		textCallback("c", "single", "stranger", "hi"),
		textCallback("c", "single", "friend", "yo"),
	}
	a := newTestAdapter(t, f, map[string]any{"allowed_users": "friend"})

	events := collectEvents(t, a, 1)
	if len(events) != 1 || events[0].UserID != "friend" {
		t.Fatalf("allowed_users not enforced: %+v", events)
	}
}

// #1123: an unsupported msgtype (voice, video, …) used to hit a bare
// `return` before chatID was even resolved — not even a log line, and
// certainly no signal to the user their message was received. It should now
// get a text acknowledgement instead, and not be forwarded as an agent turn.
func TestStart_UnsupportedMediaAcknowledged(t *testing.T) {
	f := newFakeWecom(t)
	f.callbacks = []map[string]any{
		{
			"msgtype":  "voice",
			"chatid":   "chat-1",
			"chattype": "single",
			"msgid":    "msg-1",
			"from":     map[string]string{"userid": "user-7"},
		},
	}
	a := newTestAdapter(t, f, nil)

	// want=1 is unreachable (no InboundEvent should fire); collectEvents
	// still returns once its internal 3s timeout elapses, by which point the
	// ack round-trip below has long completed.
	events := collectEvents(t, a, 1)
	if len(events) != 0 {
		t.Fatalf("expected no InboundEvent for an unsupported-media-only message, got %+v", events)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 1 {
		t.Fatalf("expected one acknowledgement to be sent, got %d", len(f.sent))
	}
}

func TestStart_AuthFailureIsFatal(t *testing.T) {
	f := newFakeWecom(t)
	cfg := channel.PlatformConfig{"bot_id": "aib123", "secret": "wrong", "ws_url": f.wsURL}
	ad, _ := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err := ad.Start(ctx, func(channel.InboundEvent) {})
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("expected auth error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("auth failure should abort without reconnect attempts")
	}
}

func TestSendText_WaitsForAck(t *testing.T) {
	f := newFakeWecom(t)
	a := newTestAdapter(t, f, nil)

	// Establish the connection first.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Start(ctx, func(channel.InboundEvent) {})

	deadline := time.Now().Add(2 * time.Second)
	for {
		res := a.SendText("chat-1", "**hello**", "")
		if res.OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("send never succeeded: %+v", res)
		}
		time.Sleep(20 * time.Millisecond)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("no aibot_send_msg frame received")
	}
	last := f.sent[len(f.sent)-1]
	if last["msgtype"] != "markdown" || last["chatid"] != "chat-1" {
		t.Fatalf("unexpected send body: %+v", last)
	}
}

// #1116: SendText previously sent content as a single unbounded payload.
// WeCom rejects markdown content over 4096 bytes outright (errcode 40058)
// rather than truncating it, so an unsplit long reply was dropped entirely.
func TestSendText_SplitsLongMessages(t *testing.T) {
	f := newFakeWecom(t)
	a := newTestAdapter(t, f, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Start(ctx, func(channel.InboundEvent) {})

	long := strings.Repeat("word ", 1500) // ~7500 bytes, over maxMessageBytes
	deadline := time.Now().Add(2 * time.Second)
	var res channel.SendResult
	for {
		res = a.SendText("chat-1", long, "")
		if res.OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("send never succeeded: %+v", res)
		}
		time.Sleep(20 * time.Millisecond)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) < 2 {
		t.Fatalf("expected split into >=2 messages, got %d", len(f.sent))
	}
	for _, frame := range f.sent {
		md, _ := frame["markdown"].(map[string]any)
		content, _ := md["content"].(string)
		if len(content) > maxMessageBytes {
			t.Fatalf("chunk exceeds cap: %d bytes", len(content))
		}
	}
}

func TestValidateConfig(t *testing.T) {
	a := &Adapter{}
	if errs := a.ValidateConfig(channel.PlatformConfig{}); len(errs) != 1 {
		t.Fatalf("expected 1 error for empty config, got %v", errs)
	}
	if errs := a.ValidateConfig(channel.PlatformConfig{"bot_id": "x", "secret": "y"}); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	// Webhook-only (push notifications) is a valid configuration.
	if errs := a.ValidateConfig(channel.PlatformConfig{"webhook_key": "k"}); len(errs) != 0 {
		t.Fatalf("expected no errors for webhook-only, got %v", errs)
	}
	// A half-filled aibot pair is flagged even when the webhook is present.
	if errs := a.ValidateConfig(channel.PlatformConfig{"bot_id": "x", "webhook_key": "k"}); len(errs) != 1 {
		t.Fatalf("expected 1 error for half aibot pair, got %v", errs)
	}
}

// TestSendText_WebhookFallback covers the proactive-push path used by
// channel.SendOnce from octo serve: no WebSocket connection, message goes to
// the configured group-robot webhook.
func TestSendText_WebhookFallback(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "k1" {
			t.Errorf("key = %q, want k1", r.URL.Query().Get("key"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
	}))
	defer ts.Close()

	a, err := New(channel.PlatformConfig{"webhook_url": ts.URL + "?key=k1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res := a.SendText("ignored-chat", "task done", "")
	if !res.OK {
		t.Fatalf("send: %s", res.Error)
	}
	if got["msgtype"] != "markdown" {
		t.Errorf("msgtype = %v", got["msgtype"])
	}
	md, _ := got["markdown"].(map[string]any)
	if md["content"] != "task done" {
		t.Errorf("content = %v", md["content"])
	}
}

func TestSendText_WebhookErrcode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 93000, "errmsg": "invalid webhook url"})
	}))
	defer ts.Close()

	a, err := New(channel.PlatformConfig{"webhook_url": ts.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if res := a.SendText("c", "x", ""); res.OK {
		t.Fatal("want failure on non-zero errcode")
	}
}

func TestSendText_NoConnectionNoWebhook(t *testing.T) {
	a, err := New(channel.PlatformConfig{"bot_id": "x", "secret": "y"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := a.SendText("c", "x", "")
	if res.OK {
		t.Fatal("want failure when not connected and no webhook configured")
	}
}

func TestSanitizeWCMediaURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://wework.qpic.cn/wwpic/123", true},
		{"https://qyapi.weixin.qq.com/cgi-bin/media/get?media_id=abc", true},
		{"https://qq.com", true},
		{"https://foo.qq.com/", true},
		{"https://foo.weixinbridge.com/bar#frag", true},
		{"http://wework.qpic.cn/xx", false},
		{"https://attacker.com/?qpic.cn", false},
		{"https://qpic.cn.attacker.com/xx", false},
		{"https://foo@wework.qpic.cn/xx", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got, err := sanitizeWCMediaURL(tc.url)
			if tc.ok {
				if err != nil {
					t.Fatalf("want allow, got error: %v", err)
				}
				if got == "" {
					t.Fatal("want non-empty sanitized URL")
				}
			} else {
				if err == nil {
					t.Fatalf("want reject, got %q", got)
				}
			}
		})
	}
}

func TestDownloadWCMedia_RejectNonAllowlisted(t *testing.T) {
	_, err := downloadWCMedia("https://attacker.com/internal", "")
	if err == nil {
		t.Fatal("expected rejection of non-allowlisted URL")
	}
}
