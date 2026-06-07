package ilink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetUploadURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/getuploadurl" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ret":             0,
			"upload_param":    "test-param",
			"upload_full_url": "",
		})
	}))
	defer ts.Close()

	c := NewClient()
	resp, err := c.GetUploadURL(context.Background(), ts.URL, "tok", GetUploadURLRequest{
		FileKey:    "key123",
		MediaType:  3,
		ToUserID:   "user1",
		RawSize:    100,
		RawFileMD5: "abc123",
		FileSize:   128,
	})
	if err != nil {
		t.Fatalf("GetUploadURL error: %v", err)
	}
	if resp.UploadParam != "test-param" {
		t.Errorf("upload_param = %q, want test-param", resp.UploadParam)
	}
}

func TestUploadToCDN_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("x-encrypted-param", "encrypted123")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewClient()
	param, err := c.UploadToCDN(context.Background(), ts.URL, []byte("encrypted data"))
	if err != nil {
		t.Fatalf("UploadToCDN error: %v", err)
	}
	if param != "encrypted123" {
		t.Errorf("param = %q, want encrypted123", param)
	}
}

func TestUploadToCDN_RetryOn5xx(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("x-encrypted-param", "ok-after-retry")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewClient()
	param, err := c.UploadToCDN(context.Background(), ts.URL, []byte("data"))
	if err != nil {
		t.Fatalf("UploadToCDN error: %v", err)
	}
	if param != "ok-after-retry" {
		t.Errorf("param = %q", param)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestUploadToCDN_ClientErrorNoRetry(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("x-error-message", "bad request")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	c := NewClient()
	_, err := c.UploadToCDN(context.Background(), ts.URL, []byte("data"))
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

func TestBuildCDNUploadURL(t *testing.T) {
	url := BuildCDNUploadURL("https://cdn.example.com/c2c", "param=val", "key123")
	if !strings.Contains(url, "encrypted_query_param=param%3Dval") {
		t.Errorf("missing encrypted_query_param: %s", url)
	}
	if !strings.Contains(url, "filekey=key123") {
		t.Errorf("missing filekey: %s", url)
	}
}

func TestBuildMediaMessage(t *testing.T) {
	items := []map[string]interface{}{
		{"type": 4, "file_item": map[string]string{"file_name": "test.txt"}},
	}
	msg := BuildMediaMessage("user1", "ctx123", items)
	if msg["to_user_id"] != "user1" {
		t.Errorf("to_user_id = %v", msg["to_user_id"])
	}
	if msg["context_token"] != "ctx123" {
		t.Errorf("context_token = %v", msg["context_token"])
	}
	itemList := msg["item_list"].([]map[string]interface{})
	if len(itemList) != 1 {
		t.Fatalf("expected 1 item, got %d", len(itemList))
	}
}

func TestClient_UploadToCDN_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("x-encrypted-param", "too-late")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewClient()
	c.HTTP.Timeout = 50 * time.Millisecond
	_, err := c.UploadToCDN(context.Background(), ts.URL, []byte("data"))
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
