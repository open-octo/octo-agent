package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/channel"
)

func TestHandleChannelSendText(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	fa := &fullFakeAdapter{}
	srv.runningAdapters.Store("fake", fa)

	req := httptest.NewRequest("POST", "/api/channels/fake/send", strings.NewReader(`{"chat_id":"c1","text":"hi"}`))
	req.SetPathValue("platform", "fake")
	w := httptest.NewRecorder()
	srv.handleChannelSendText(w, req)

	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if got := fa.texts(); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("sent texts = %v", got)
	}
}

func TestHandleChannelSendText_Validation(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest("POST", "/api/channels/fake/send", strings.NewReader(`{"chat_id":"","text":""}`))
	req.SetPathValue("platform", "fake")
	w := httptest.NewRecorder()
	srv.handleChannelSendText(w, req)
	if w.Code != 400 {
		t.Fatalf("want 400 for missing fields, got %d", w.Code)
	}
}

func TestHandleChannelSendFile(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.runningAdapters.Store("fake", &fullFakeAdapter{})

	req := httptest.NewRequest("POST", "/api/channels/fake/send-file", strings.NewReader(`{"chat_id":"c1","path":"/tmp/x.png","name":"x.png"}`))
	req.SetPathValue("platform", "fake")
	w := httptest.NewRecorder()
	srv.handleChannelSendFile(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleChannelRecipients(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := chanServer(t)
	srv.channelMgr.GetOrCreateSession(channel.InboundEvent{Platform: "feishu", ChatID: "c1", UserID: "u1"})

	req := httptest.NewRequest("GET", "/api/channels/recipients", nil)
	w := httptest.NewRecorder()
	srv.handleChannelRecipients(w, req)

	if w.Code != 200 {
		t.Fatalf("code=%d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "feishu") || !strings.Contains(body, "c1") {
		t.Fatalf("recipients body missing live chat: %s", body)
	}
}
