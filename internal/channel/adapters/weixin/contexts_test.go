package weixin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/channel/adapters/weixin/ilink"
)

func TestContextTokenStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weixin-contexts.json")

	saveContextToken(path, "user1", "ctx-a")
	saveContextToken(path, "user2", "ctx-b")
	saveContextToken(path, "user1", "ctx-a2") // newer token wins

	m := loadContextTokens(path)
	if m["user1"] != "ctx-a2" || m["user2"] != "ctx-b" {
		t.Fatalf("tokens = %v, want user1=ctx-a2 user2=ctx-b", m)
	}
}

func TestContextTokenStore_MissingFileIsEmpty(t *testing.T) {
	m := loadContextTokens(filepath.Join(t.TempDir(), "nope.json"))
	if len(m) != 0 {
		t.Fatalf("tokens = %v, want empty", m)
	}
}

// TestSendText_StandaloneDelivers exercises the no-Start() path used by
// channel.SendOnce: credentials and context token both come from disk and the
// message goes out synchronously.
func TestSendText_StandaloneDelivers(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/ilink/bot/sendmessage" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			received = append(received, payload)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	credPath := filepath.Join(dir, "weixin-credentials.json")
	if err := ilink.SaveCredentials(&ilink.Credentials{Token: "tok", BaseURL: ts.URL}, credPath); err != nil {
		t.Fatalf("save creds: %v", err)
	}
	saveContextToken(contextStorePath(credPath), "user1", "ctx123")

	a := &Adapter{credPath: credPath, client: ilink.NewClient()}

	res := a.SendText("user1", "task done", "")
	if !res.OK {
		t.Fatalf("standalone send: %s", res.Error)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("received %d requests, want 1", len(received))
	}
	msg := received[0]["msg"].(map[string]any)
	if msg["to_user_id"] != "user1" || msg["context_token"] != "ctx123" {
		t.Errorf("msg = %v, want to_user_id=user1 context_token=ctx123", msg)
	}
}

func TestSendText_StandaloneNoToken(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "weixin-credentials.json")
	if err := ilink.SaveCredentials(&ilink.Credentials{Token: "tok", BaseURL: "http://example.invalid"}, credPath); err != nil {
		t.Fatalf("save creds: %v", err)
	}

	a := &Adapter{credPath: credPath, client: ilink.NewClient()}
	if res := a.SendText("stranger", "hi", ""); res.OK {
		t.Fatal("want failure when no stored context_token")
	}
}

// TestSendText_RunningFallsBackToStore covers a process restart: the receive
// loop is up (bot != nil) but the in-memory token map is empty, so the send
// falls back to the on-disk store.
func TestSendText_RunningFallsBackToStore(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "weixin-credentials.json")
	saveContextToken(contextStorePath(credPath), "user1", "ctx-disk")

	a := &Adapter{
		credPath: credPath,
		client:   ilink.NewClient(),
		bot: &ilinkBot{
			client: ilink.NewClient(),
			creds:  &ilink.Credentials{Token: "tok", BaseURL: "http://example.invalid"},
		},
	}
	a.sendQ = newSendQueue(a.client, "http://example.invalid", "tok")

	// Enqueue-only path: OK means the token lookup succeeded.
	if res := a.SendText("user1", "hello", ""); !res.OK {
		t.Fatalf("want OK via disk fallback, got: %s", res.Error)
	}
}

func TestHandleInbound_PersistsContextToken(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "weixin-credentials.json")
	a := &Adapter{credPath: credPath, client: ilink.NewClient(), bot: &ilinkBot{}}

	a.handleInbound(&ilink.WireMessage{
		MessageType:  ilink.MessageTypeUser,
		FromUserID:   "user9",
		ContextToken: "ctx-9",
		ItemList:     []ilink.MessageItem{{Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "hi"}}},
	}, func(channel.InboundEvent) {})

	if got := loadContextTokens(contextStorePath(credPath))["user9"]; got != "ctx-9" {
		t.Fatalf("persisted token = %q, want ctx-9", got)
	}
}

// ensure the temp-file rename leaves no .tmp behind
func TestContextTokenStore_NoTempLeftover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weixin-contexts.json")
	saveContextToken(path, "u", "t")
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}
