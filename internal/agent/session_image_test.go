package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LoadSession must reload persisted image attachments from ImagePath (bytes
// are never serialised into the transcript), and degrade gracefully — the
// anthropic adapter hard-errors on an image block with no data, so a husk
// left in history would fail every later turn of the resumed session.
func TestLoadSession_RehydratesImageBlocks(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	imgPath := filepath.Join(tmp, "pic.png")
	payload := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := os.WriteFile(imgPath, payload, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	sess := NewSession("m", "")
	block := ContentBlock{Type: "image", ImagePath: imgPath}
	sess.Messages = append(sess.Messages, Message{
		Role:   RoleUser,
		Blocks: []ContentBlock{NewTextBlock("look at this"), block},
	})
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	blocks := loaded.Messages[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	img := blocks[1]
	if img.Type != "image" || img.Image == nil {
		t.Fatalf("image block not rehydrated: %+v", img)
	}
	if string(img.Image.Data) != string(payload) {
		t.Errorf("rehydrated bytes mismatch")
	}
	if img.Image.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", img.Image.MIMEType)
	}
}

func TestLoadSession_MissingImageFileBecomesPlaceholder(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := NewSession("m", "")
	sess.Messages = append(sess.Messages, Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{Type: "image", ImagePath: filepath.Join(tmp, "gone.jpg")},
			{Type: "image"}, // legacy husk saved before ImagePath existed
		},
	})
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i, b := range loaded.Messages[0].Blocks {
		if b.Type != "text" {
			t.Errorf("block %d type = %q, want text placeholder", i, b.Type)
		}
		if !strings.Contains(b.Text, "no longer available") {
			t.Errorf("block %d text = %q", i, b.Text)
		}
	}
}

// A description already paid for must survive a session reload even though the
// image bytes don't. Tool-produced images (read_file, browser screenshots, MCP
// results) never carry an ImagePath, so this is the common case for exactly
// the images the vision helper exists to serve.
func TestLoadSession_KeepsDescriptionWhenBytesAreGone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := NewSession("m", "")
	sess.Messages = append(sess.Messages, Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{Type: "image", ImageDescription: "a login form with an email field"},
		},
	})
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	b := loaded.Messages[0].Blocks[0]
	if b.Type != "text" {
		t.Fatalf("block type = %q, want text", b.Type)
	}
	if !strings.Contains(b.Text, "a login form with an email field") {
		t.Errorf("description was dropped on reload: %q", b.Text)
	}
	if strings.Contains(b.Text, "no longer available") {
		t.Errorf("described image should not degrade to the unavailable placeholder: %q", b.Text)
	}
}

// The retry budget is per-session: a helper that was down must be retried
// after a restart, not stay permanently written off.
func TestLoadSession_ResetsImageDescFailures(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	imgPath := filepath.Join(tmp, "pic.png")
	if err := os.WriteFile(imgPath, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	sess := NewSession("m", "")
	sess.Messages = append(sess.Messages, Message{
		Role:   RoleUser,
		Blocks: []ContentBlock{{Type: "image", ImagePath: imgPath, ImageDescFailures: visionHelperMaxFailures}},
	})
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := loaded.Messages[0].Blocks[0].ImageDescFailures; got != 0 {
		t.Errorf("ImageDescFailures = %d after reload, want 0 (fresh budget)", got)
	}
}
