package server

import (
	"context"
	"testing"
)

// newActivityServer builds the bare maps Activity reads, without standing up a
// whole Server — the aggregate is pure bookkeeping over those two.
func newActivityServer() *Server {
	return &Server{
		interrupts:       make(map[string]context.CancelFunc),
		pendingQuestions: make(map[string]wsEventRequestUserQuestion),
		pendingConfirms:  make(map[string]wsEventRequestConfirmation),
	}
}

func TestActivityIdleWhenNothingOutstanding(t *testing.T) {
	s := newActivityServer()
	if got := s.Activity(); got != ActivityIdle {
		t.Fatalf("Activity() = %q, want %q", got, ActivityIdle)
	}
}

func TestActivityBusyWhileATurnRuns(t *testing.T) {
	s := newActivityServer()
	s.interrupts["sess-1"] = func() {}
	if got := s.Activity(); got != ActivityBusy {
		t.Fatalf("Activity() = %q, want %q", got, ActivityBusy)
	}
}

// A session waiting on the user also has a turn in flight, so ask has to win —
// otherwise the pet would report "busy" for a turn that is actually blocked on
// the person watching it.
func TestActivityAskOutranksBusy(t *testing.T) {
	s := newActivityServer()
	s.interrupts["sess-1"] = func() {}
	s.pendingQuestions["sess-1"] = wsEventRequestUserQuestion{}
	if got := s.Activity(); got != ActivityAsk {
		t.Fatalf("Activity() = %q, want %q", got, ActivityAsk)
	}
}

func TestActivityAskCoversConfirmations(t *testing.T) {
	s := newActivityServer()
	s.pendingConfirms["sess-1"] = wsEventRequestConfirmation{}
	if got := s.Activity(); got != ActivityAsk {
		t.Fatalf("Activity() = %q, want %q", got, ActivityAsk)
	}
}

func TestActivityReturnsToIdleWhenCleared(t *testing.T) {
	s := newActivityServer()
	s.interrupts["sess-1"] = func() {}
	s.pendingQuestions["sess-1"] = wsEventRequestUserQuestion{}
	delete(s.pendingQuestions, "sess-1")
	if got := s.Activity(); got != ActivityBusy {
		t.Fatalf("after clearing the question, Activity() = %q, want %q", got, ActivityBusy)
	}
	delete(s.interrupts, "sess-1")
	if got := s.Activity(); got != ActivityIdle {
		t.Fatalf("after the turn ends, Activity() = %q, want %q", got, ActivityIdle)
	}
}
