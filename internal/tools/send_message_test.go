package tools

import (
	"context"
	"strings"
	"testing"
)

type sentMsg struct{ platform, chatID, text string }
type sentFile struct{ platform, chatID, path, name string }

type fakeMessenger struct {
	chats     []KnownRecipient
	sent      []sentMsg
	sentFiles []sentFile
	err       error
}

func (f *fakeMessenger) SendMessage(platform, chatID, text string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentMsg{platform, chatID, text})
	return nil
}

func (f *fakeMessenger) SendFile(platform, chatID, path, name string) error {
	if f.err != nil {
		return f.err
	}
	f.sentFiles = append(f.sentFiles, sentFile{platform, chatID, path, name})
	return nil
}

func (f *fakeMessenger) KnownChats() []KnownRecipient { return f.chats }

func runSendMessage(t *testing.T, input map[string]any) (string, error) {
	t.Helper()
	res, err := SendMessageTool{}.Execute(context.Background(), "send_message", input)
	return res.Text, err
}

func TestSendMessage_NoMessenger(t *testing.T) {
	SetMessenger(nil)
	if messengerEnabled() {
		t.Fatal("messenger should be disabled")
	}
	if _, err := runSendMessage(t, map[string]any{"platform": "weixin", "chat_id": "c1", "text": "hi"}); err == nil {
		t.Fatal("want error with no messenger registered")
	}
	// And the tool must not be advertised.
	for _, d := range DefaultToolsFor("") {
		if d.Name == "send_message" {
			t.Fatal("send_message must not be advertised without a messenger")
		}
	}
}

func TestSendMessage_AdvertisedWhenEnabled(t *testing.T) {
	SetMessenger(&fakeMessenger{})
	defer SetMessenger(nil)
	var found bool
	for _, d := range DefaultToolsFor("") {
		if d.Name == "send_message" {
			found = true
		}
	}
	if !found {
		t.Fatal("send_message should be advertised when a messenger is registered")
	}
}

func TestSendMessage_ExplicitSend(t *testing.T) {
	fm := &fakeMessenger{}
	SetMessenger(fm)
	defer SetMessenger(nil)

	out, err := runSendMessage(t, map[string]any{"platform": "weixin", "chat_id": "c1", "text": "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(fm.sent) != 1 || fm.sent[0] != (sentMsg{"weixin", "c1", "hello"}) {
		t.Fatalf("sent = %+v", fm.sent)
	}
	if !strings.Contains(out, "weixin") || !strings.Contains(out, "c1") {
		t.Fatalf("result text = %q", out)
	}
}

func TestSendMessage_SingleCandidateAutoSends(t *testing.T) {
	fm := &fakeMessenger{chats: []KnownRecipient{{Platform: "weixin", ChatID: "c1", Label: "active"}}}
	SetMessenger(fm)
	defer SetMessenger(nil)

	// No chat_id, but exactly one weixin chat known → send straight to it.
	if _, err := runSendMessage(t, map[string]any{"platform": "weixin", "text": "hi"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(fm.sent) != 1 || fm.sent[0].chatID != "c1" {
		t.Fatalf("sent = %+v", fm.sent)
	}
}

func TestSendMessage_MultipleCandidatesList(t *testing.T) {
	fm := &fakeMessenger{chats: []KnownRecipient{
		{Platform: "weixin", ChatID: "c1"},
		{Platform: "weixin", ChatID: "c2"},
	}}
	SetMessenger(fm)
	defer SetMessenger(nil)

	out, err := runSendMessage(t, map[string]any{"platform": "weixin", "text": "hi"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(fm.sent) != 0 {
		t.Fatalf("must not send when the recipient is ambiguous; sent = %+v", fm.sent)
	}
	if !strings.Contains(out, "c1") || !strings.Contains(out, "c2") {
		t.Fatalf("expected both candidates listed, got %q", out)
	}
}

func TestSendMessage_DiscoveryNoArgs(t *testing.T) {
	fm := &fakeMessenger{chats: []KnownRecipient{{Platform: "telegram", ChatID: "42"}}}
	SetMessenger(fm)
	defer SetMessenger(nil)

	out, err := runSendMessage(t, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "telegram") || !strings.Contains(out, "42") {
		t.Fatalf("discovery should list known chats, got %q", out)
	}
}

func TestSendMessage_NoReachableChats(t *testing.T) {
	SetMessenger(&fakeMessenger{})
	defer SetMessenger(nil)

	out, err := runSendMessage(t, map[string]any{"platform": "weixin", "text": "hi"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "no reachable chats") {
		t.Fatalf("expected a no-recipient hint, got %q", out)
	}
}
