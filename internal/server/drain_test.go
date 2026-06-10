package server

import (
	"errors"
	"testing"
	"time"
)

func TestDrainGate_BeginAfterDrainRefused(t *testing.T) {
	var g drainGate

	if err := g.begin(); err != nil {
		t.Fatalf("begin before drain: %v, want nil", err)
	}
	g.end()

	if ok := g.drain(0); !ok {
		t.Fatal("drain with no active turns should report clean")
	}
	if err := g.begin(); !errors.Is(err, errDraining) {
		t.Fatalf("begin after drain = %v, want errDraining", err)
	}
}

func TestDrainGate_DrainWaitsForActiveTurn(t *testing.T) {
	var g drainGate

	if err := g.begin(); err != nil {
		t.Fatal(err)
	}

	done := make(chan bool, 1)
	go func() { done <- g.drain(5 * time.Second) }()

	// The drain must not complete while the turn is active.
	select {
	case <-done:
		t.Fatal("drain returned while a turn was still active")
	case <-time.After(100 * time.Millisecond):
	}

	g.end()

	select {
	case clean := <-done:
		if !clean {
			t.Error("drain = false (timeout), want true (clean)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not return after the active turn ended")
	}
}

func TestDrainGate_TimeoutReportsDirty(t *testing.T) {
	var g drainGate

	if err := g.begin(); err != nil {
		t.Fatal(err)
	}
	defer g.end()

	if clean := g.drain(50 * time.Millisecond); clean {
		t.Error("drain = true, want false when a turn outlives the timeout")
	}
}

func TestDrainGate_MultipleTurns(t *testing.T) {
	var g drainGate

	for i := 0; i < 3; i++ {
		if err := g.begin(); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan bool, 1)
	go func() { done <- g.drain(5 * time.Second) }()

	g.end()
	g.end()
	select {
	case <-done:
		t.Fatal("drain returned with one turn still active")
	case <-time.After(100 * time.Millisecond):
	}

	g.end()
	select {
	case clean := <-done:
		if !clean {
			t.Error("drain = false, want true after all turns ended")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not return after last turn ended")
	}
}
