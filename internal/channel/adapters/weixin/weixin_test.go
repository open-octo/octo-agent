package weixin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/channel"
	"github.com/Leihb/octo-agent/internal/channel/adapters/weixin/ilink"
)

func TestAdapter_ValidateConfig(t *testing.T) {
	a := &Adapter{}
	errs := a.ValidateConfig(channel.PlatformConfig{})
	if len(errs) != 0 {
		t.Fatalf("expected 0 validation errors, got %d: %v", len(errs), errs)
	}
}

func TestAdapter_SendText_NotLoggedIn(t *testing.T) {
	a := &Adapter{}
	res := a.SendText("user1", "hello", "")
	if res.OK {
		t.Fatal("expected failure when not logged in")
	}
}

func TestAdapter_SendText_NoContextToken(t *testing.T) {
	a := &Adapter{
		bot: &ilinkBot{
			creds: &ilink.Credentials{Token: "tok", BaseURL: "http://example.com"},
		},
	}
	res := a.SendText("user1", "hello", "")
	if res.OK {
		t.Fatal("expected failure when no context_token")
	}
}

func TestAdapter_SendText_Success(t *testing.T) {
	var received []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ilink/bot/sendmessage" {
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			received = append(received, payload)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		}
	}))
	defer ts.Close()

	a := &Adapter{
		baseURL: ts.URL,
		bot: &ilinkBot{
			client: ilink.NewClient(),
			creds:  &ilink.Credentials{Token: "tok", BaseURL: ts.URL},
		},
	}
	a.bot.contextTokens.Store("user1", "ctx123")

	res := a.SendText("user1", "hello world", "")
	if !res.OK {
		t.Fatalf("expected send OK, got error: %s", res.Error)
	}

	if len(received) != 1 {
		t.Fatalf("expected 1 received request, got %d", len(received))
	}
	msg := received[0]["msg"].(map[string]any)
	if msg["to_user_id"] != "user1" {
		t.Errorf("unexpected to_user_id: %v", msg["to_user_id"])
	}
}

func TestAdapter_SendFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ilink/bot/sendmessage" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		}
	}))
	defer ts.Close()

	a := &Adapter{
		baseURL: ts.URL,
		bot: &ilinkBot{
			client: ilink.NewClient(),
			creds:  &ilink.Credentials{Token: "tok", BaseURL: ts.URL},
		},
	}
	a.bot.contextTokens.Store("user1", "ctx123")

	res := a.SendFile("user1", "/tmp/test.txt", "test.txt", "")
	if !res.OK {
		t.Fatalf("expected send OK, got error: %s", res.Error)
	}
}

func TestAdapter_StopNotRunning(t *testing.T) {
	a := &Adapter{}
	err := a.Stop()
	if err != nil {
		t.Fatalf("expected nil error when stopping non-running adapter, got: %v", err)
	}
}

func TestAdapter_StartAlreadyRunning(t *testing.T) {
	a := &Adapter{running: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := a.Start(ctx, func(ev channel.InboundEvent) {})
	if err == nil {
		t.Fatal("expected error when starting already-running adapter")
	}
}

func TestAdapter_StartNoCredentials(t *testing.T) {
	// Use a temp dir so credentials don't exist.
	tmpDir := t.TempDir()
	a := &Adapter{
		credPath: filepath.Join(tmpDir, "creds.json"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := a.Start(ctx, func(ev channel.InboundEvent) {})
	if err == nil {
		t.Fatal("expected error when no credentials")
	}
}

func TestAdapter_SupportsMessageUpdates(t *testing.T) {
	a := &Adapter{}
	if a.SupportsMessageUpdates() {
		t.Error("expected SupportsMessageUpdates to be false")
	}
}

func TestAdapter_UpdateMessage(t *testing.T) {
	a := &Adapter{}
	if a.UpdateMessage("c1", "m1", "text") {
		t.Error("expected UpdateMessage to return false")
	}
}

func TestNew(t *testing.T) {
	cfg := channel.PlatformConfig{
		"base_url":    "https://custom.example.com",
		"cred_path":   "/tmp/test-creds.json",
		"timeout_sec": 60,
	}
	ad, err := New(cfg)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	wa := ad.(*Adapter)
	if wa.baseURL != "https://custom.example.com" {
		t.Errorf("unexpected baseURL: %q", wa.baseURL)
	}
	if wa.credPath != "/tmp/test-creds.json" {
		t.Errorf("unexpected credPath: %q", wa.credPath)
	}
	if wa.client.HTTP.Timeout != 60*time.Second {
		t.Errorf("unexpected timeout: %v", wa.client.HTTP.Timeout)
	}
}

func TestNew_Defaults(t *testing.T) {
	ad, err := New(channel.PlatformConfig{})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	wa := ad.(*Adapter)
	if wa.baseURL != "https://ilinkai.weixin.qq.com" {
		t.Errorf("unexpected default baseURL: %q", wa.baseURL)
	}
	if wa.client.HTTP.Timeout != 30*time.Second {
		t.Errorf("unexpected default timeout: %v", wa.client.HTTP.Timeout)
	}
}

func TestParseMessage(t *testing.T) {
	wire := &ilink.WireMessage{
		MessageType:  ilink.MessageTypeUser,
		FromUserID:   "user1",
		ContextToken: "ctx123",
		CreateTimeMs: 1700000000000,
		ItemList: []ilink.MessageItem{
			{Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "hello"}},
		},
	}
	msg := parseMessage(wire)
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.UserID != "user1" {
		t.Errorf("unexpected userID: %q", msg.UserID)
	}
	if msg.Text != "hello" {
		t.Errorf("unexpected text: %q", msg.Text)
	}
	if msg.ContextToken != "ctx123" {
		t.Errorf("unexpected contextToken: %q", msg.ContextToken)
	}
}

func TestParseMessage_BotMessage(t *testing.T) {
	wire := &ilink.WireMessage{
		MessageType: ilink.MessageTypeBot,
		FromUserID:  "bot1",
	}
	msg := parseMessage(wire)
	if msg != nil {
		t.Fatal("expected nil for bot message")
	}
}

func TestExtractText(t *testing.T) {
	items := []ilink.MessageItem{
		{Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "hello"}},
		{Type: ilink.ItemImage, ImageItem: &ilink.ImageItem{URL: "http://img"}},
	}
	text := extractText(items)
	if text != "hello http://img" {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestChunkText(t *testing.T) {
	chunks := chunkText("hello", 10)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}

	chunks = chunkText("hello world foo bar", 5)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}
}

func TestMin(t *testing.T) {
	if min(1*time.Second, 2*time.Second) != 1*time.Second {
		t.Error("unexpected min")
	}
	if min(3*time.Second, 2*time.Second) != 2*time.Second {
		t.Error("unexpected min")
	}
}

func TestAdapter_StartWithCredentials(t *testing.T) {
	// Create a mock iLink server.
	var mu sync.Mutex
	callCount := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		callCount++

		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			// Return one message then empty.
			if callCount == 1 {
				resp := map[string]any{
					"ret":             0,
					"get_updates_buf": "buf1",
					"msgs": []json.RawMessage{
						mustJSON(ilink.WireMessage{
							MessageType:  ilink.MessageTypeUser,
							FromUserID:   "user1",
							ContextToken: "ctx123",
							CreateTimeMs: time.Now().UnixMilli(),
							ItemList: []ilink.MessageItem{
								{Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "hi"}},
							},
						}),
					},
				}
				json.NewEncoder(w).Encode(resp)
			} else {
				// Empty response to avoid timeout.
				json.NewEncoder(w).Encode(map[string]any{"ret": 0, "get_updates_buf": "buf1"})
			}
		}
	}))
	defer ts.Close()

	// Write fake credentials.
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "creds.json")
	creds := &ilink.Credentials{
		Token:   "test-token",
		BaseURL: ts.URL,
		UserID:  "bot1",
	}
	data, _ := json.Marshal(creds)
	os.WriteFile(credPath, data, 0600)

	a := &Adapter{
		baseURL:  ts.URL,
		credPath: credPath,
		client:   ilink.NewClient(),
	}

	var received []channel.InboundEvent
	onMessage := func(ev channel.InboundEvent) {
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := a.Start(ctx, onMessage)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("expected 1 received event, got %d", len(received))
	}
	if received[0].Text != "hi" {
		t.Errorf("unexpected text: %q", received[0].Text)
	}
	mu.Unlock()
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
