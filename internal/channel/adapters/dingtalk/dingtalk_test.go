package dingtalk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
)

// stubAPI fakes the DingTalk token + robot send endpoints and records the
// send requests it receives.
type stubAPI struct {
	mu    sync.Mutex
	sends []struct {
		path string
		body map[string]any
	}
}

func (s *stubAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "tok-1", "expireIn": 7200})
		case "/v1.0/robot/oToMessages/batchSend", "/v1.0/robot/groupMessages/send":
			if got := r.Header.Get("x-acs-dingtalk-access-token"); got != "tok-1" {
				t.Errorf("token header = %q, want tok-1", got)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.sends = append(s.sends, struct {
				path string
				body map[string]any
			}{r.URL.Path, body})
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"processQueryKey": "ok"})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func testAdapter(ts *httptest.Server) *Adapter {
	return &Adapter{
		clientID:     "appkey",
		clientSecret: "secret",
		robotCode:    "appkey",
		apiBase:      ts.URL,
		http:         ts.Client(),
		webhooks:     make(map[string]webhookEntry),
	}
}

// TestSendText_ProactiveUserPush covers the standalone path: no
// sessionWebhook cached, a bare user id routes to the one-on-one batch API,
// and the first token fetch happens synchronously.
func TestSendText_ProactiveUserPush(t *testing.T) {
	stub := &stubAPI{}
	ts := stub.server(t)
	defer ts.Close()

	a := testAdapter(ts)
	if res := a.SendText("staff007", "task done", ""); !res.OK {
		t.Fatalf("send: %s", res.Error)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(stub.sends))
	}
	got := stub.sends[0]
	if got.path != "/v1.0/robot/oToMessages/batchSend" {
		t.Fatalf("path = %s", got.path)
	}
	users, _ := got.body["userIds"].([]any)
	if len(users) != 1 || users[0] != "staff007" {
		t.Errorf("userIds = %v, want [staff007]", got.body["userIds"])
	}
	if got.body["robotCode"] != "appkey" || got.body["msgKey"] != "sampleMarkdown" {
		t.Errorf("body = %v", got.body)
	}
}

// TestSendText_ProactiveGroupPush routes a cid-prefixed openConversationId to
// the group API.
func TestSendText_ProactiveGroupPush(t *testing.T) {
	stub := &stubAPI{}
	ts := stub.server(t)
	defer ts.Close()

	a := testAdapter(ts)
	if res := a.SendText("cidAbC123==", "task done", ""); !res.OK {
		t.Fatalf("send: %s", res.Error)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.sends) != 1 || stub.sends[0].path != "/v1.0/robot/groupMessages/send" {
		t.Fatalf("sends = %+v, want one group send", stub.sends)
	}
	if stub.sends[0].body["openConversationId"] != "cidAbC123==" {
		t.Errorf("openConversationId = %v", stub.sends[0].body["openConversationId"])
	}
}

// #1116: SendText posted content as a single unbounded payload — a reply
// exceeding maxMessageBytes had no splitting to fall back on and would be
// rejected or truncated outright by the platform.
func TestSendText_SplitsLongMessages(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	wh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		md, _ := body["markdown"].(map[string]any)
		text, _ := md["text"].(string)
		mu.Lock()
		sent = append(sent, text)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0})
	}))
	defer wh.Close()

	stub := &stubAPI{}
	ts := stub.server(t)
	defer ts.Close()

	a := testAdapter(ts)
	a.webhooks["chat1"] = webhookEntry{URL: wh.URL, ExpiresAtMs: time.Now().Add(time.Hour).UnixMilli()}

	long := strings.Repeat("word ", 5000) // ~25000 chars, over maxMessageBytes
	if res := a.SendText("chat1", long, ""); !res.OK {
		t.Fatalf("send: %s", res.Error)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) < 2 {
		t.Fatalf("expected split into >=2 messages, got %d", len(sent))
	}
	for _, text := range sent {
		if len(text) > maxMessageBytes {
			t.Fatalf("chunk exceeds cap: %d bytes", len(text))
		}
	}
}

// TestSendText_PrefersSessionWebhook keeps the reply path on the free
// webhook channel when one is cached and fresh.
func TestSendText_PrefersSessionWebhook(t *testing.T) {
	var webhookHits int
	wh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHits++
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0})
	}))
	defer wh.Close()

	stub := &stubAPI{}
	ts := stub.server(t)
	defer ts.Close()

	a := testAdapter(ts)
	a.webhooks["chat1"] = webhookEntry{URL: wh.URL, ExpiresAtMs: time.Now().Add(time.Hour).UnixMilli()}

	if res := a.SendText("chat1", "hello", ""); !res.OK {
		t.Fatalf("send: %s", res.Error)
	}
	if webhookHits != 1 {
		t.Fatalf("webhook hits = %d, want 1", webhookHits)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.sends) != 0 {
		t.Fatalf("robot api sends = %d, want 0", len(stub.sends))
	}
}

// #1123: an unsupported msgtype (voice, audio, video, …) used to drop at
// extractPayload's default branch with only a server-side log — the user
// got no signal their message was even received. It should now get a text
// acknowledgement instead, and not be forwarded as an agent turn.
func TestHandleInbound_UnsupportedMediaAcknowledged(t *testing.T) {
	stub := &stubAPI{}
	ts := stub.server(t)
	defer ts.Close()

	a := testAdapter(ts)
	a.routes = make(map[string]routeEntry)

	raw := []byte(`{"senderId": "user-1", "msgtype": "audio"}`)

	gotEvent := false
	a.handleInbound(raw, func(ev channel.InboundEvent) {
		gotEvent = true
	})

	if gotEvent {
		t.Fatal("expected no InboundEvent for an unsupported-media-only message")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.sends) != 1 {
		t.Fatalf("expected one acknowledgement to be sent, got %d", len(stub.sends))
	}
}

// #1122: a group message containing a bare "@" character (mentioning someone
// else entirely) used to pass the gate anyway via a loose substring match —
// the bot barged into unrelated group conversations. AtUsers is the real
// signal and should be the only one consulted.
func TestHandleInbound_GroupRequiresRealMention(t *testing.T) {
	stub := &stubAPI{}
	ts := stub.server(t)
	defer ts.Close()

	a := testAdapter(ts)
	a.routes = make(map[string]routeEntry)

	raw := []byte(`{
		"senderId": "user-1",
		"conversationId": "conv-1",
		"conversationType": "2",
		"chatbotUserId": "bot-1",
		"msgtype": "text",
		"text": {"content": "@someone-else 帮我看下"},
		"atUsers": [{"dingtalkId": "someone-else"}]
	}`)

	gotEvent := false
	a.handleInbound(raw, func(ev channel.InboundEvent) {
		gotEvent = true
	})

	if gotEvent {
		t.Fatal("expected the message to be dropped — the bot wasn't actually @-mentioned")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.sends) != 0 {
		t.Fatalf("expected no send for a dropped group message, got %d", len(stub.sends))
	}
}

// A group message that genuinely @-mentions the bot (its ID present in
// atUsers) should still pass through normally.
func TestHandleInbound_GroupRealMentionPasses(t *testing.T) {
	stub := &stubAPI{}
	ts := stub.server(t)
	defer ts.Close()

	a := testAdapter(ts)
	a.routes = make(map[string]routeEntry)

	raw := []byte(`{
		"senderId": "user-1",
		"conversationId": "conv-1",
		"conversationType": "2",
		"chatbotUserId": "bot-1",
		"msgtype": "text",
		"text": {"content": "@octo 帮我看下"},
		"atUsers": [{"dingtalkId": "bot-1"}]
	}`)

	var got channel.InboundEvent
	gotEvent := false
	a.handleInbound(raw, func(ev channel.InboundEvent) {
		gotEvent = true
		got = ev
	})

	if !gotEvent {
		t.Fatal("expected the message to be delivered — the bot was actually @-mentioned")
	}
	if got.ChatType != "group" {
		t.Fatalf("expected group chat type, got %+v", got)
	}
}
