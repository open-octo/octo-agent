package weixin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	var mu sync.Mutex
	var received []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
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
		client:  ilink.NewClient(),
		bot: &ilinkBot{
			client: ilink.NewClient(),
			creds:  &ilink.Credentials{Token: "tok", BaseURL: ts.URL},
		},
	}
	a.bot.contextTokens.Store("user1", "ctx123")
	a.sendQ = newSendQueue(a.client, ts.URL, "tok")

	res := a.SendText("user1", "hello world", "")
	if !res.OK {
		t.Fatalf("expected send OK, got error: %s", res.Error)
	}

	// Flush the queue so the message is sent immediately.
	a.sendQ.flush("user1")

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected at least 1 received request, got 0")
	}
	msg := received[0]["msg"].(map[string]any)
	if msg["to_user_id"] != "user1" {
		t.Errorf("unexpected to_user_id: %v", msg["to_user_id"])
	}
}

func TestAdapter_SendText_BufferedMerge(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received = append(received, payload)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
	}))
	defer ts.Close()

	a := &Adapter{
		baseURL: ts.URL,
		client:  ilink.NewClient(),
		bot: &ilinkBot{
			client: ilink.NewClient(),
			creds:  &ilink.Credentials{Token: "tok", BaseURL: ts.URL},
		},
	}
	a.bot.contextTokens.Store("user1", "ctx123")
	a.sendQ = newSendQueue(a.client, ts.URL, "tok")
	defer a.sendQ.stop()

	for _, text := range []string{"hello", "world", "from weixin"} {
		res := a.SendText("user1", text, "")
		if !res.OK {
			t.Fatalf("expected send OK for %q, got error: %s", text, res.Error)
		}
	}

	// Wait for the flush interval plus two ticker ticks to avoid flakes.
	time.Sleep(flushInterval + 500*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 batched request, got %d", len(received))
	}
	msg := received[0]["msg"].(map[string]any)
	items := msg["item_list"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 text item, got %d", len(items))
	}
	textItem := items[0].(map[string]any)["text_item"].(map[string]any)
	sent := textItem["text"].(string)
	want := "hello\nworld\nfrom weixin"
	if sent != want {
		t.Errorf("unexpected batched text: got %q, want %q", sent, want)
	}
}

func TestAdapter_SendFile(t *testing.T) {
	// Create a real temp file to upload.
	tmpFile, err := os.CreateTemp(t.TempDir(), "test*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.WriteString("hello world")
	tmpFile.Close()

	var mu sync.Mutex
	var received []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/ilink/bot/getuploadurl":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ret":          0,
				"upload_param": "mock-upload-param",
			})
		case "/ilink/bot/sendmessage":
			var payload map[string]any
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			_ = json.Unmarshal(body, &payload)
			received = append(received, payload)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0})
		case "/upload":
			w.Header().Set("x-encrypted-param", "mock-encrypt-param")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	// Point CDNBaseURL at our test server.
	origCDN := ilink.CDNBaseURL
	ilink.CDNBaseURL = ts.URL
	defer func() { ilink.CDNBaseURL = origCDN }()

	a := &Adapter{
		baseURL: ts.URL,
		client:  ilink.NewClient(),
		bot: &ilinkBot{
			client: ilink.NewClient(),
			creds:  &ilink.Credentials{Token: "tok", BaseURL: ts.URL},
		},
	}
	a.bot.contextTokens.Store("user1", "ctx123")
	a.sendQ = newSendQueue(a.client, ts.URL, "tok")

	res := a.SendFile("user1", tmpFile.Name(), "test.txt", "")
	if !res.OK {
		t.Fatalf("expected send OK, got error: %s", res.Error)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected sendmessage request")
	}
	msg := received[0]["msg"].(map[string]any)
	items := msg["item_list"].([]any)
	if len(items) == 0 {
		t.Fatal("expected at least one item")
	}
	item := items[0].(map[string]any)
	if item["type"] != float64(ilink.ItemFile) {
		t.Errorf("expected file item type, got %v", item["type"])
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

func TestHandleInbound_UserMessage(t *testing.T) {
	var received []channel.InboundEvent
	a := &Adapter{}
	wire := &ilink.WireMessage{
		MessageType:  ilink.MessageTypeUser,
		FromUserID:   "user1",
		ContextToken: "ctx123",
		CreateTimeMs: 1700000000000,
		ItemList: []ilink.MessageItem{
			{Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "hello"}},
		},
	}
	a.handleInbound(wire, func(ev channel.InboundEvent) {
		received = append(received, ev)
	})
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].UserID != "user1" {
		t.Errorf("unexpected userID: %q", received[0].UserID)
	}
	if received[0].Text != "hello" {
		t.Errorf("unexpected text: %q", received[0].Text)
	}
	if received[0].ContextToken != "ctx123" {
		t.Errorf("unexpected contextToken: %q", received[0].ContextToken)
	}
}

func TestHandleInbound_BotMessage(t *testing.T) {
	var received []channel.InboundEvent
	a := &Adapter{}
	wire := &ilink.WireMessage{
		MessageType: ilink.MessageTypeBot,
		FromUserID:  "bot1",
	}
	a.handleInbound(wire, func(ev channel.InboundEvent) {
		received = append(received, ev)
	})
	if len(received) != 0 {
		t.Fatal("expected no events for bot message")
	}
}

func TestExtractText(t *testing.T) {
	items := []ilink.MessageItem{
		{Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "hello"}},
		{Type: ilink.ItemImage, ImageItem: &ilink.ImageItem{URL: "http://img"}},
	}
	text := extractText(items)
	if text != "hello\n[图片]" {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestExtractText_QuotedMessage(t *testing.T) {
	items := []ilink.MessageItem{
		{Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "reply"},
			RefMsg: &ilink.RefMessage{Title: "Original", MessageItem: &ilink.MessageItem{
				Type: ilink.ItemText, TextItem: &ilink.TextItem{Text: "quoted text"},
			}},
		},
	}
	text := extractText(items)
	if !strings.Contains(text, "[引用: Original | quoted text]") {
		t.Errorf("expected quoted message, got: %q", text)
	}
}

func TestSmartChunkText(t *testing.T) {
	chunks := smartChunkText("hello", 10)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}

	chunks = smartChunkText("hello world foo bar", 5)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestMarkdownToPlain(t *testing.T) {
	md := "**bold** and *italic* and [link](http://x)"
	plain := markdownToPlain(md)
	if strings.Contains(plain, "**") {
		t.Errorf("expected no **, got: %q", plain)
	}
	if strings.Contains(plain, "[link]") {
		t.Errorf("expected link text, got: %q", plain)
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
