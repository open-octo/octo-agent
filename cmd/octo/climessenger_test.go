package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCliMessenger_DelegatesToServe(t *testing.T) {
	isolateHome(t)

	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"sent"}`))
	}))
	defer ts.Close()
	t.Setenv("OCTO_SERVE_ADDR", strings.TrimPrefix(ts.URL, "http://"))

	if err := (cliMessenger{}).SendMessage("weixin", "c1", "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if gotPath != "/api/channels/weixin/send" {
		t.Errorf("path = %q", gotPath)
	}
	var body map[string]string
	_ = json.Unmarshal([]byte(gotBody), &body)
	if body["chat_id"] != "c1" || body["text"] != "hello" {
		t.Errorf("body = %v", body)
	}
}

func TestCliMessenger_RecipientsFromServe(t *testing.T) {
	isolateHome(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"recipients":[{"platform":"weixin","chat_id":"c1","active":true}]}`))
	}))
	defer ts.Close()
	t.Setenv("OCTO_SERVE_ADDR", strings.TrimPrefix(ts.URL, "http://"))

	recs := (cliMessenger{}).KnownChats()
	if len(recs) != 1 || recs[0].Platform != "weixin" || recs[0].ChatID != "c1" {
		t.Fatalf("recipients = %+v", recs)
	}
	if recs[0].Label != "active" {
		t.Errorf("label = %q, want active", recs[0].Label)
	}
}

// Serve reachable but the send fails (502) → the error must surface, NOT fall
// through to SendOnce. Distinguished by the error text: a serve error mentions
// the HTTP status; a SendOnce fallback would say "not configured".
func TestCliMessenger_ServeErrorDoesNotFallThrough(t *testing.T) {
	isolateHome(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "adapter offline", http.StatusBadGateway)
	}))
	defer ts.Close()
	t.Setenv("OCTO_SERVE_ADDR", strings.TrimPrefix(ts.URL, "http://"))

	err := (cliMessenger{}).SendMessage("weixin", "c1", "hi")
	if err == nil {
		t.Fatal("want the serve error to surface")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("want the serve 502 surfaced, got %v", err)
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Errorf("must NOT fall through to SendOnce, got %v", err)
	}
}

// No serve reachable → falls back to SendOnce, which errors on an unconfigured
// platform (proving the fallback path, not the serve path, was taken).
func TestCliMessenger_FallbackWhenNoServe(t *testing.T) {
	isolateHome(t)
	t.Setenv("OCTO_SERVE_ADDR", "127.0.0.1:1") // nothing listens here

	err := (cliMessenger{}).SendMessage("weixin", "c1", "hi")
	if err == nil {
		t.Fatal("want SendOnce error for unconfigured platform with no serve")
	}
	if recs := (cliMessenger{}).KnownChats(); recs != nil {
		t.Errorf("KnownChats offline should be nil, got %+v", recs)
	}
}
