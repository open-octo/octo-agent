package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/open-octo/octo-agent/internal/agent"
)

// fakeAttachment builds a pendingAttachment with a dummy image block for tests.
func fakeAttachment(t *testing.T) pendingAttachment {
	t.Helper()
	return pendingAttachment{
		block: mustPNGBlock(t),
		label: "image (PNG, 4 B)",
	}
}

// TestTUI_SubmitImageOnlyStartsTurn verifies that Enter with no text but a
// pending attachment still starts a turn and consumes the attachment (an
// image-only message is valid).
func TestTUI_SubmitImageOnlyStartsTurn(t *testing.T) {
	m := newTestModel()
	m.pendingAttachments = []pendingAttachment{fakeAttachment(t)}
	setInput(m, "")

	_, _ = m.submit()

	if !m.turnRunning {
		t.Fatal("Enter with an attachment (empty text) should start a turn")
	}
	if len(m.pendingAttachments) != 0 {
		t.Errorf("attachment should be consumed on submit, still have %d", len(m.pendingAttachments))
	}
}

// TestTUI_EscDiscardsAttachments verifies idle Esc clears pending attachments
// through the real key handler.
func TestTUI_EscDiscardsAttachments(t *testing.T) {
	m := newTestModel()
	m.pendingAttachments = []pendingAttachment{fakeAttachment(t)}

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if len(m.pendingAttachments) != 0 {
		t.Errorf("idle Esc should discard attachments, still have %d", len(m.pendingAttachments))
	}
}

// TestTUI_MidTurnSendsAttachments verifies that submitting while a turn runs
// folds pending attachments into the steer message (they ride via Inbox).
func TestTUI_MidTurnSendsAttachments(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.pendingAttachments = []pendingAttachment{fakeAttachment(t)}
	setInput(m, "also look here")

	_, _ = m.submit()

	if len(m.pendingAttachments) != 0 {
		t.Errorf("attachment should be consumed on mid-turn submit, still have %d", len(m.pendingAttachments))
	}
	if !m.a.Inbox.HasPending() {
		t.Fatal("steer text should still be enqueued mid-turn")
	}
	items := m.a.Inbox.Drain()
	if len(items) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(items))
	}
	if items[0].Text != "also look here" {
		t.Errorf("inbox text = %q, want 'also look here'", items[0].Text)
	}
	if len(items[0].Blocks) != 1 {
		t.Errorf("inbox blocks = %d, want 1 (the image)", len(items[0].Blocks))
	}
}

// mustPNGBlock builds an image block from a minimal valid PNG. NewImageBlock
// identifies the format by sniffing the bytes rather than trusting the caller's
// label, so a fixture needs the real 8-byte signature — truncated stand-ins are
// correctly rejected.
func mustPNGBlock(t *testing.T) agent.ContentBlock {
	t.Helper()
	blk, ok := agent.NewImageBlock("image/png", []byte("\x89PNG\r\n\x1a\n"))
	if !ok {
		t.Fatal("fixture PNG was rejected by NewImageBlock")
	}
	return blk
}

// TestTUI_MidTurnImageOnlySteerEchoesChip covers the reported bug: an
// image-only steer mid-turn enqueued an EMPTY display line, so the user saw
// no echo at all and assumed the send failed. The entry must carry the chip.
func TestTUI_MidTurnImageOnlySteerEchoesChip(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.pendingAttachments = []pendingAttachment{fakeAttachment(t)}
	setInput(m, "")

	_, _ = m.submit()

	if len(m.pendingSteer) != 1 {
		t.Fatalf("pendingSteer = %v, want 1 entry", m.pendingSteer)
	}
	entry := m.pendingSteer[0]
	if entry.text != "" {
		t.Errorf("image-only steer text = %q, want empty", entry.text)
	}
	if !strings.Contains(entry.chips, "📎 image") {
		t.Errorf("image-only steer should carry the chip, got %q", entry.chips)
	}
	if line := entry.line(); !strings.Contains(line, "📎 image") {
		t.Errorf("rendered line = %q, want the chip visible", line)
	}
	items := m.a.Inbox.Drain()
	if len(items) != 1 || len(items[0].Blocks) != 1 {
		t.Fatalf("inbox = %+v, want one item carrying the image block", items)
	}
}

// TestTUI_SteerInjectedPrintsImageChips: when the loop drains an image-only
// steer, the scrollback echo must show the chip — Messages carries only texts
// (empty for an image-only steer), so the chips come from the Steer blocks.
func TestTUI_SteerInjectedPrintsImageChips(t *testing.T) {
	m := newTestModel()
	blk := mustPNGBlock(t)
	m.handleEvent(agent.AgentEvent{
		Kind:     agent.EventSteerInjected,
		Messages: []string{""},
		Steer:    []agent.InboxItem{{Text: "", Blocks: []agent.ContentBlock{blk}}},
	})
	joined := stripANSI(strings.Join(m.printlnBuf, "\n"))
	if !strings.Contains(joined, "📎 image") {
		t.Errorf("drained image-only steer should print the chip, got %q", joined)
	}
}
