package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// stubILink fakes the iLink QR endpoints: one QR issue, then polls that report
// "normal" twice and "confirmed" after, handing out credentials.
func stubILink(t *testing.T) *httptest.Server {
	t.Helper()
	var polls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ilink/bot/get_bot_qrcode":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errcode": 0,
				"qrcode":  "qr-1", "qrcode_img_content": "https://example.invalid/qr.png",
			})
		case r.URL.Path == "/ilink/bot/get_qrcode_status":
			n := polls.Add(1)
			if n < 3 {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "wait"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "confirmed",
				"bot_token": "tok-1", "ilink_bot_id": "bot-1", "ilink_user_id": "user-1",
			})
		default:
			t.Logf("stub ilink: unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestWeixinWebLogin_FullFlow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".octo"), 0700); err != nil {
		t.Fatal(err)
	}

	stub := stubILink(t)
	defer stub.Close()

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.weixinLogin.qrBaseURL = stub.URL

	// Start the flow — the response should already carry the QR URL.
	req := httptest.NewRequest("POST", "/api/channels/weixin/login", nil)
	rec := httptest.NewRecorder()
	srv.handleWeixinLoginStart(rec, req)
	var started map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if started["qr_url"] != "https://example.invalid/qr.png" {
		t.Fatalf("start response = %v, want qr_url", started)
	}

	// Poll the status endpoint until done (stub confirms on the 3rd poll;
	// the library sleeps 2s between polls).
	deadline := time.Now().Add(15 * time.Second)
	for {
		rec := httptest.NewRecorder()
		srv.handleWeixinLoginStatus(rec, httptest.NewRequest("GET", "/api/channels/weixin/login", nil))
		var st map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &st)
		if st["status"] == "done" {
			if st["user_id"] != "user-1" {
				t.Fatalf("user_id = %q, want user-1", st["user_id"])
			}
			break
		}
		if st["status"] == "failed" {
			t.Fatalf("login failed: %s", st["error"])
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out; last status %v", st)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Credentials persisted where the adapter will look for them.
	if _, err := os.Stat(filepath.Join(tmp, ".octo", "weixin-credentials.json")); err != nil {
		t.Fatalf("credentials not saved: %v", err)
	}

	// A second start without force reports already_logged_in.
	rec2 := httptest.NewRecorder()
	srv.handleWeixinLoginStart(rec2, httptest.NewRequest("POST", "/api/channels/weixin/login", nil))
	var again map[string]string
	_ = json.Unmarshal(rec2.Body.Bytes(), &again)
	if again["status"] != "already_logged_in" || again["user_id"] != "user-1" {
		t.Fatalf("second start = %v, want already_logged_in/user-1", again)
	}
}
