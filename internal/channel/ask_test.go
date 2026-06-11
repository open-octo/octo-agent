package channel

import (
	"testing"
	"time"
)

func TestSession_BeginAskDeliverReply(t *testing.T) {
	sess := &Session{}

	replyCh, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatalf("BeginAsk: %v", err)
	}
	defer release()

	if !sess.DeliverAskReply("c1", "u1", "yes") {
		t.Fatal("DeliverAskReply = false with a pending ask, want true")
	}
	select {
	case got := <-replyCh:
		if got != "yes" {
			t.Errorf("reply = %q, want %q", got, "yes")
		}
	case <-time.After(time.Second):
		t.Fatal("reply not delivered to the ask channel")
	}
}

func TestSession_DeliverWithoutPendingAsk(t *testing.T) {
	sess := &Session{}
	if sess.DeliverAskReply("c1", "u1", "hello") {
		t.Error("DeliverAskReply = true with no pending ask; the message must flow to a normal turn")
	}
}

func TestSession_SecondBeginAskRefused(t *testing.T) {
	sess := &Session{}
	_, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, _, err := sess.BeginAsk("c1", "u1"); err == nil {
		t.Error("second BeginAsk should be refused while one is pending")
	}
}

func TestSession_ReleaseClearsPending(t *testing.T) {
	sess := &Session{}
	_, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	release()

	if sess.DeliverAskReply("c1", "u1", "late") {
		t.Error("a reply after release must not be consumed")
	}
	// The slot is reusable after release.
	if _, release2, err := sess.BeginAsk("c1", "u1"); err != nil {
		t.Errorf("BeginAsk after release: %v", err)
	} else {
		release2()
	}
}

func TestSession_OneReplyOnly(t *testing.T) {
	sess := &Session{}
	_, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if !sess.DeliverAskReply("c1", "u1", "yes") {
		t.Fatal("first reply should be consumed")
	}
	if sess.DeliverAskReply("c1", "u1", "again") {
		t.Error("second reply must not be consumed — the ask is already answered")
	}
}

func TestSession_WrongUserOrChatNotConsumed(t *testing.T) {
	sess := &Session{}
	replyCh, release, err := sess.BeginAsk("c1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if sess.DeliverAskReply("c1", "u2", "yes") {
		t.Error("another user's reply must not answer the ask")
	}
	if sess.DeliverAskReply("c2", "u1", "yes") {
		t.Error("a reply from another chat must not answer the ask")
	}
	// The rightful answer still lands afterwards.
	if !sess.DeliverAskReply("c1", "u1", "yes") {
		t.Fatal("the requester's reply should be consumed")
	}
	select {
	case got := <-replyCh:
		if got != "yes" {
			t.Errorf("reply = %q, want yes", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reply not delivered")
	}
}
