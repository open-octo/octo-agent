package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/channel"
)

// 1x1 transparent PNG, base64.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pdkAAAAAElFTkSuQmCC"

func newAttachSession() *channel.Session {
	return &channel.Session{Agent: agent.New(&stubSender{}, "stub-model")}
}

func TestAttachInboundFiles_TextOnly(t *testing.T) {
	s := &Server{}
	sess := newAttachSession()
	ev := channel.InboundEvent{Platform: "weixin", Text: "hello"}
	if got := s.attachInboundFiles(sess, ev); got != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}

func TestAttachInboundFiles_DocumentNote(t *testing.T) {
	s := &Server{}
	sess := newAttachSession()
	doc := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(doc, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := channel.InboundEvent{
		Platform: "feishu",
		Text:     "see attached",
		Files:    []channel.FileAttachment{{Type: "file", Name: "report.pdf", Path: doc}},
	}
	got := s.attachInboundFiles(sess, ev)
	if !strings.Contains(got, "see attached") {
		t.Errorf("content lost the original text: %q", got)
	}
	if !strings.Contains(got, doc) {
		t.Errorf("content missing the file path note: %q", got)
	}
}

func TestAttachInboundFiles_Image(t *testing.T) {
	// Redirect ~/.octo/uploads into a temp HOME so the test doesn't touch the
	// real home dir (saveImageAttachment persists the decoded bytes there).
	t.Setenv("HOME", t.TempDir())

	s := &Server{}
	sess := newAttachSession()
	dataURL := "data:image/png;base64," + tinyPNG
	ev := channel.InboundEvent{
		Platform: "telegram",
		Text:     "what is this",
		Files:    []channel.FileAttachment{{Type: "image", Name: "pic.png", MimeType: "image/png", DataURL: dataURL}},
	}
	// Image rides as a vision block, not in the text — content stays the caption.
	if got := s.attachInboundFiles(sess, ev); got != "what is this" {
		t.Errorf("content = %q, want %q", got, "what is this")
	}
	// A copy of the image should have been persisted under ~/.octo/uploads.
	dir := filepath.Join(os.Getenv("HOME"), ".octo", "uploads")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("uploads dir not created: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected the inbound image to be persisted under uploads")
	}
}
