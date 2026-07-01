package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSender records the last SendFile call and can be made to fail.
type fakeSender struct {
	gotPath string
	gotName string
	err     error
	calls   int
}

func (f *fakeSender) SendFile(path, name string) error {
	f.calls++
	f.gotPath = path
	f.gotName = name
	return f.err
}

func TestSendFileTool_RequiresSender(t *testing.T) {
	// No sender in ctx and no messenger (CLI/TUI) → clean error, no panic.
	SetMessenger(nil)
	_, err := SendFileTool{}.Execute(context.Background(), "send_file", map[string]any{"path": "/tmp/x.png"})
	if err == nil {
		t.Fatal("expected error when no channel sender and no messenger")
	}
}

func TestSendFileTool_SendsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "chart.png")
	if err := os.WriteFile(p, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSender{}
	ctx := WithChannelSender(context.Background(), fs)

	res, err := SendFileTool{}.Execute(ctx, "send_file", map[string]any{"path": p})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fs.calls != 1 {
		t.Fatalf("expected SendFile called once, got %d", fs.calls)
	}
	if fs.gotPath != p {
		t.Errorf("path = %q, want %q", fs.gotPath, p)
	}
	// Display name defaults to the basename.
	if fs.gotName != "chart.png" {
		t.Errorf("name = %q, want %q", fs.gotName, "chart.png")
	}
	if res.Text == "" {
		t.Error("expected a non-empty result text")
	}
}

func TestSendFileTool_CustomName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tmp123.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSender{}
	ctx := WithChannelSender(context.Background(), fs)

	if _, err := (SendFileTool{}).Execute(ctx, "send_file", map[string]any{"path": p, "name": "report.png"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fs.gotName != "report.png" {
		t.Errorf("name = %q, want %q", fs.gotName, "report.png")
	}
}

func TestSendFileTool_MissingFile(t *testing.T) {
	fs := &fakeSender{}
	ctx := WithChannelSender(context.Background(), fs)
	if _, err := (SendFileTool{}).Execute(ctx, "send_file", map[string]any{"path": "/no/such/file.png"}); err == nil {
		t.Fatal("expected error for missing file")
	}
	if fs.calls != 0 {
		t.Error("SendFile must not be called for a missing file")
	}
}

func TestSendFileTool_Directory(t *testing.T) {
	fs := &fakeSender{}
	ctx := WithChannelSender(context.Background(), fs)
	if _, err := (SendFileTool{}).Execute(ctx, "send_file", map[string]any{"path": t.TempDir()}); err == nil {
		t.Fatal("expected error for a directory")
	}
	if fs.calls != 0 {
		t.Error("SendFile must not be called for a directory")
	}
}

func TestSendFileTool_EmptyPath(t *testing.T) {
	ctx := WithChannelSender(context.Background(), &fakeSender{})
	if _, err := (SendFileTool{}).Execute(ctx, "send_file", map[string]any{"path": "  "}); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSendFileTool_SenderError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.pdf")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSender{err: errors.New("not logged in")}
	ctx := WithChannelSender(context.Background(), fs)
	_, err := SendFileTool{}.Execute(ctx, "send_file", map[string]any{"path": p})
	if err == nil {
		t.Fatal("expected the sender's error to propagate")
	}
}

// send_file needs a chat to push to (server only): hidden without a messenger
// (CLI/TUI), advertised with one (web + IM).
func TestSendFileTool_DefaultToolsGating(t *testing.T) {
	SetMessenger(nil)
	for _, d := range DefaultToolsFor("") {
		if d.Name == "send_file" {
			t.Fatal("send_file must not be advertised without a messenger")
		}
	}

	SetMessenger(&fakeMessenger{})
	defer SetMessenger(nil)
	var found bool
	for _, d := range DefaultToolsFor("") {
		if d.Name == "send_file" {
			found = true
		}
	}
	if !found {
		t.Fatal("send_file should be advertised when a messenger is registered")
	}
}

// Cross-chat mode: explicit platform + chat_id routes through the messenger,
// not the current-chat sender.
func TestSendFileTool_CrossChatSend(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "chart.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fm := &fakeMessenger{}
	SetMessenger(fm)
	defer SetMessenger(nil)

	res, err := SendFileTool{}.Execute(context.Background(), "send_file",
		map[string]any{"path": p, "platform": "weixin", "chat_id": "c1"})
	if err != nil {
		t.Fatalf("cross-chat send: %v", err)
	}
	if len(fm.sentFiles) != 1 || fm.sentFiles[0].platform != "weixin" || fm.sentFiles[0].chatID != "c1" {
		t.Fatalf("sentFiles = %+v", fm.sentFiles)
	}
	if fm.sentFiles[0].name != "chart.png" {
		t.Errorf("name = %q, want chart.png", fm.sentFiles[0].name)
	}
	if res.Text == "" {
		t.Error("expected result text")
	}
}

// Single known candidate on the platform + no chat_id → auto-targets it.
func TestSendFileTool_CrossChatSingleCandidate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fm := &fakeMessenger{chats: []KnownRecipient{{Platform: "weixin", ChatID: "only"}}}
	SetMessenger(fm)
	defer SetMessenger(nil)

	if _, err := (SendFileTool{}).Execute(context.Background(), "send_file",
		map[string]any{"path": p, "platform": "weixin"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(fm.sentFiles) != 1 || fm.sentFiles[0].chatID != "only" {
		t.Fatalf("sentFiles = %+v", fm.sentFiles)
	}
}

// Ambiguous platform (multiple candidates, no chat_id) → lists, does not send.
func TestSendFileTool_CrossChatAmbiguousLists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fm := &fakeMessenger{chats: []KnownRecipient{
		{Platform: "weixin", ChatID: "c1"},
		{Platform: "weixin", ChatID: "c2"},
	}}
	SetMessenger(fm)
	defer SetMessenger(nil)

	out, err := SendFileTool{}.Execute(context.Background(), "send_file",
		map[string]any{"path": p, "platform": "weixin"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(fm.sentFiles) != 0 {
		t.Fatalf("must not send when ambiguous; sentFiles = %+v", fm.sentFiles)
	}
	if !strings.Contains(out.Text, "c1") || !strings.Contains(out.Text, "c2") {
		t.Fatalf("expected candidates listed, got %q", out.Text)
	}
}

// Web turn (messenger, no ctx sender) with no target → lists reachable chats.
func TestSendFileTool_WebNoTargetLists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fm := &fakeMessenger{chats: []KnownRecipient{{Platform: "telegram", ChatID: "42"}}}
	SetMessenger(fm)
	defer SetMessenger(nil)

	out, err := SendFileTool{}.Execute(context.Background(), "send_file", map[string]any{"path": p})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(fm.sentFiles) != 0 {
		t.Fatal("must not send without a target")
	}
	if !strings.Contains(out.Text, "telegram") || !strings.Contains(out.Text, "42") {
		t.Fatalf("expected discovery listing, got %q", out.Text)
	}
}

// But DefaultRegistry must still be able to dispatch it (runChannelTurns adds
// the def per IM turn; the executor scans allTools to run it).
func TestSendFileTool_DispatchableViaRegistry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSender{}
	ctx := WithChannelSender(context.Background(), fs)
	if _, err := NewDefaultRegistry().Execute(ctx, "send_file", map[string]any{"path": p}); err != nil {
		t.Fatalf("registry could not dispatch send_file: %v", err)
	}
	if fs.calls != 1 {
		t.Fatalf("expected the registry to route to SendFileTool, calls=%d", fs.calls)
	}
}
