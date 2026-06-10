package dingtalk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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
