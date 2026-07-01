package feishu

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
