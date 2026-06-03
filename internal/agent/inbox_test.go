package agent

import (
	"testing"
)

func TestInbox_EnqueueDrain(t *testing.T) {
	ib := &Inbox{}

	if ib.HasPending() {
		t.Fatal("fresh inbox should have no pending messages")
	}
	if got := ib.Drain(); got != nil {
		t.Errorf("Drain on empty = %v, want nil", got)
	}

	// Whitespace-only messages are ignored.
	ib.Enqueue("   ")
	ib.Enqueue("\n\t")
	if ib.HasPending() {
		t.Fatal("whitespace-only messages should be ignored")
	}

	ib.Enqueue("first")
	ib.Enqueue("second")
	if !ib.HasPending() {
		t.Fatal("expected pending messages after two enqueues")
	}

	items := ib.Drain()
	if len(items) != 2 {
		t.Fatalf("Drain len = %d, want 2", len(items))
	}
	if items[0].Text != "first" || items[1].Text != "second" {
		t.Errorf("Drain = %v, want [first second]", items)
	}

	// Drain must clear.
	if ib.HasPending() || ib.Drain() != nil {
		t.Error("Drain did not clear the inbox")
	}
}

func TestInbox_EnqueueWithBlocks(t *testing.T) {
	var ib Inbox
	img := NewImageBlock("image/png", []byte{0x89, 'P', 'N', 'G'})
	ib.EnqueueWithBlocks("look at this", []ContentBlock{img})
	ib.Enqueue("plain text")

	items := ib.Drain()
	if len(items) != 2 {
		t.Fatalf("Drain len = %d, want 2", len(items))
	}
	if items[0].Text != "look at this" || len(items[0].Blocks) != 1 {
		t.Errorf("item[0] = %+v, want text + 1 block", items[0])
	}
	if items[1].Text != "plain text" || len(items[1].Blocks) != 0 {
		t.Errorf("item[1] = %+v, want text + 0 blocks", items[1])
	}
}

func TestInbox_EnqueueWithBlocks_ImageOnly(t *testing.T) {
	var ib Inbox
	img := NewImageBlock("image/png", []byte{1, 2, 3})
	ib.EnqueueWithBlocks("", []ContentBlock{img})

	items := ib.Drain()
	if len(items) != 1 {
		t.Fatalf("Drain len = %d, want 1", len(items))
	}
	if items[0].Text != "" || len(items[0].Blocks) != 1 {
		t.Errorf("item[0] = %+v, want empty text + 1 block", items[0])
	}
}

func TestInbox_Remove(t *testing.T) {
	var ib Inbox
	ib.Enqueue("a")
	ib.Enqueue("b")
	ib.Enqueue("a") // duplicate

	// Removes the LAST matching entry.
	if !ib.Remove("a") {
		t.Fatal("Remove(a) = false, want true")
	}
	items := ib.Drain()
	if len(items) != 2 || items[0].Text != "a" || items[1].Text != "b" {
		t.Errorf("after Remove(a) drain = %v, want [a b]", items)
	}
}

func TestInbox_Remove_NotFound(t *testing.T) {
	var ib Inbox
	ib.Enqueue("x")
	if ib.Remove("nope") {
		t.Error("Remove of absent message returned true")
	}
	if !ib.HasPending() {
		t.Error("Remove of absent message should leave the queue intact")
	}
}
