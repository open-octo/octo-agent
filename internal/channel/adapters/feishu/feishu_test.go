package feishu

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/channel"
)

// fakeFeishu serves the token, upload and message-send REST endpoints.
type fakeFeishu struct {
	mu          sync.Mutex
	sent        []map[string]any
	uploadType  string // "image" or "file" of the last upload
	gotFileType string // file_type form field seen on a file upload
	srv         *httptest.Server
}

func newFakeFeishu(t *testing.T) *fakeFeishu {
	f := &fakeFeishu{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "tok", "expire": 7200,
			})
		case "/open-apis/im/v1/images":
			f.mu.Lock()
			f.uploadType = "image"
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "data": map[string]any{"image_key": "img-key-1"},
			})
		case "/open-apis/im/v1/files":
			r.ParseMultipartForm(1 << 20)
			f.mu.Lock()
			f.uploadType = "file"
			f.gotFileType = r.FormValue("file_type")
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "data": map[string]any{"file_key": "file-key-1"},
			})
		case "/open-apis/im/v1/messages":
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			f.mu.Lock()
			f.sent = append(f.sent, p)
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "data": map[string]any{"message_id": "m-1"},
			})
		default:
			if strings.HasPrefix(r.URL.Path, "/open-apis/docx/v1/documents/") {
				// #1123: docToken "badtoken" simulates a failed doc fetch
				// (deleted doc, no permission, …) — everything else succeeds.
				if strings.Contains(r.URL.Path, "badtoken") {
					json.NewEncoder(w).Encode(map[string]any{"code": 99999})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"code": 0, "data": map[string]any{"content": "doc body"},
				})
				return
			}
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newTestAdapter(t *testing.T, f *fakeFeishu) *Adapter {
	a, err := New(channel.PlatformConfig{
		cfgAppID:     "app",
		cfgAppSecret: "secret",
		cfgDomain:    f.srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a.(*Adapter)
}

// #1116: SendText posted content as a single unbounded payload with no
// split at all; a reply exceeding maxMessageBytes would fail outright with
// no chunking to fall back on.
func TestSendText_SplitsLongMessages(t *testing.T) {
	f := newFakeFeishu(t)
	a := newTestAdapter(t, f)

	long := strings.Repeat("word ", 5000) // ~25000 chars, over maxMessageBytes
	res := a.SendText("chat-1", long, "m-0")
	if !res.OK {
		t.Fatalf("send failed: %+v", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) < 2 {
		t.Fatalf("expected split into >=2 messages, got %d", len(f.sent))
	}
	for _, p := range f.sent {
		// p["content"] is the JSON-marshaled post/card envelope, not the raw
		// chunk — it carries a small, fixed structural overhead (tag/key
		// names) on top of the actual text, so allow slack rather than
		// asserting the exact raw-chunk cap here (that's what
		// chunking_test.go pins directly against SplitForSend).
		content, _ := p["content"].(string)
		if len(content) > maxMessageBytes+200 {
			t.Fatalf("chunk wildly exceeds cap: %d bytes", len(content))
		}
	}
	// replyTo should attach to the first chunk only, not every one.
	if f.sent[0]["reply_to_message_id"] != "m-0" {
		t.Fatalf("expected reply_to_message_id on chunk 0, got: %+v", f.sent[0])
	}
	if _, ok := f.sent[1]["reply_to_message_id"]; ok {
		t.Fatalf("reply_to_message_id should only be set on chunk 0, found on chunk 1: %+v", f.sent[1])
	}
}

func TestSendFile_Image(t *testing.T) {
	f := newFakeFeishu(t)
	a := newTestAdapter(t, f)

	path := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := a.SendFile("chat-1", path, "", "reply-0")
	if !res.OK || res.MessageID != "m-1" {
		t.Fatalf("send failed: %+v", res)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploadType != "image" {
		t.Fatalf("expected image upload, got %q", f.uploadType)
	}
	if len(f.sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(f.sent))
	}
	if f.sent[0]["msg_type"] != "image" {
		t.Errorf("msg_type = %v, want image", f.sent[0]["msg_type"])
	}
	if f.sent[0]["content"] != `{"image_key":"img-key-1"}` {
		t.Errorf("content = %v", f.sent[0]["content"])
	}
	if f.sent[0]["reply_to_message_id"] != "reply-0" {
		t.Errorf("reply_to_message_id = %v, want reply-0", f.sent[0]["reply_to_message_id"])
	}
}

func TestSendFile_File(t *testing.T) {
	f := newFakeFeishu(t)
	a := newTestAdapter(t, f)

	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := a.SendFile("chat-1", path, "report.pdf", "")
	if !res.OK || res.MessageID != "m-1" {
		t.Fatalf("send failed: %+v", res)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploadType != "file" {
		t.Fatalf("expected file upload, got %q", f.uploadType)
	}
	if f.gotFileType != "pdf" {
		t.Errorf("file_type = %q, want pdf", f.gotFileType)
	}
	if f.sent[0]["msg_type"] != "file" {
		t.Errorf("msg_type = %v, want file", f.sent[0]["msg_type"])
	}
	if f.sent[0]["content"] != `{"file_key":"file-key-1"}` {
		t.Errorf("content = %v", f.sent[0]["content"])
	}
}

func TestSendFile_Missing(t *testing.T) {
	f := newFakeFeishu(t)
	a := newTestAdapter(t, f)

	res := a.SendFile("chat-1", "/no/such/file", "", "")
	if res.OK {
		t.Fatal("expected failure for missing file")
	}
}

// #1123: a message of an unhandled type (voice, rich-post/video, …) used to
// fall straight through to the drop with no signal to the user at all. It
// should now get a text acknowledgement instead, and not be forwarded as an
// agent turn.
func TestHandleEvent_UnsupportedMediaAcknowledged(t *testing.T) {
	f := newFakeFeishu(t)
	a := newTestAdapter(t, f)

	raw := []byte(`{
		"schema": "2.0",
		"header": {"event_type": "im.message.receive_v1"},
		"event": {
			"message": {
				"message_id": "m-1",
				"chat_id": "chat-1",
				"chat_type": "p2p",
				"message_type": "audio",
				"content": "{}"
			},
			"sender": {"sender_id": {"open_id": "user-1"}}
		}
	}`)

	gotEvent := false
	a.handleEvent(raw, func(ev channel.InboundEvent) {
		gotEvent = true
	})

	if gotEvent {
		t.Fatal("expected no InboundEvent for an unsupported-media-only message")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 1 {
		t.Fatalf("expected one acknowledgement to be sent, got %d", len(f.sent))
	}
	content, _ := f.sent[0]["content"].(string)
	if !strings.Contains(content, unsupportedMediaNotice) {
		t.Fatalf("expected the ack notice in sent content, got: %s", content)
	}
}

// #1123: a doc-fetch failure used to only log server-side — the user pasted
// a doc link, got an answer that silently ignored it, and no hint why.
func TestEnrichDocContent_FetchFailureAppendsNote(t *testing.T) {
	f := newFakeFeishu(t)
	a := newTestAdapter(t, f)

	text := "check this doc: https://example.feishu.cn/docx/badtoken"
	got := a.enrichDocContent(text)

	if !strings.Contains(got, "Couldn't read the linked doc") {
		t.Fatalf("expected a failure note appended, got: %s", got)
	}
	if !strings.HasPrefix(got, text) {
		t.Fatalf("expected the original text preserved, got: %s", got)
	}
}

// A successful doc fetch should still just append the content, unaffected
// by the failure-note path above.
func TestEnrichDocContent_Success(t *testing.T) {
	f := newFakeFeishu(t)
	a := newTestAdapter(t, f)

	text := "check this doc: https://example.feishu.cn/docx/goodtoken"
	got := a.enrichDocContent(text)

	if !strings.Contains(got, "doc body") {
		t.Fatalf("expected fetched doc content appended, got: %s", got)
	}
}

func TestFeishuFileType(t *testing.T) {
	cases := map[string]string{
		"a.mp4": "mp4", "a.pdf": "pdf", "a.docx": "doc",
		"a.xlsx": "xls", "a.pptx": "ppt", "a.bin": "stream", "noext": "stream",
	}
	for name, want := range cases {
		if got := feishuFileType(name); got != want {
			t.Errorf("feishuFileType(%q) = %q, want %q", name, got, want)
		}
	}
}
