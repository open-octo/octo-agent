package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	// No sender in ctx → CLI/TUI/Web turn → clean error, no panic.
	_, err := SendFileTool{}.Execute(context.Background(), "send_file", map[string]any{"path": "/tmp/x.png"})
	if err == nil {
		t.Fatal("expected error when no channel sender is in ctx")
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

// send_file is IM-only: it must never appear in the shared default tool list,
// or CLI/TUI/Web turns would advertise a tool that can only error.
func TestSendFileTool_NotInDefaultTools(t *testing.T) {
	for _, d := range DefaultToolsFor("") {
		if d.Name == "send_file" {
			t.Fatal("send_file must not be advertised in DefaultToolsFor")
		}
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
