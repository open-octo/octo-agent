package tools

import (
	"context"
	"testing"
)

func TestPendingAsyncWork_NothingWired(t *testing.T) {
	if PendingAsyncWork(nil, nil) {
		t.Error("no managers wired must not report pending work")
	}
	if PendingAsyncWork(NewBackgroundManager(), NewSubAgentManager(&fakeSpawner{})) {
		t.Error("idle managers must not report pending work")
	}
}

// An interactive background process is a service or REPL that may never exit.
// Counting one would park the goal-continuation loop forever, so only async
// one-shot tasks hold it back.
func TestPendingAsyncWork_IgnoresInteractive(t *testing.T) {
	m := NewBackgroundManager()
	defer m.KillAll()
	if _, err := m.Start(context.Background(), "sleep 30", BgModeInteractive); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if PendingAsyncWork(m, nil) {
		t.Error("an interactive process must not count as pending async work")
	}
}

func TestPendingAsyncWork_AsyncProcess(t *testing.T) {
	m := NewBackgroundManager()
	defer m.KillAll()
	id, err := m.Start(context.Background(), "sleep 30", BgModeAsync)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !PendingAsyncWork(m, nil) {
		t.Fatal("a running async process must count as pending async work")
	}

	// Once it exits the loop is free again — the completion note is what
	// carries the goal forward from here.
	m.Kill(id)
	waitFor(t, "async process to stop counting", func() bool { return !PendingAsyncWork(m, nil) })
}

// A sub-agent that finished its round stays in ListRunning so a later turn can
// Continue it. Only a busy one is work in flight — counting the idle ones would
// park the goal loop for the rest of the session.
func TestPendingAsyncWork_RunningSubAgent(t *testing.T) {
	sp := &blockingSpawner{release: make(chan struct{})}
	m := NewSubAgentManager(sp)
	if _, err := m.Start(SpawnRequest{Description: "d", Prompt: "p"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !PendingAsyncWork(nil, m) {
		t.Fatal("a busy sub-agent must count as pending async work")
	}

	close(sp.release)
	waitFor(t, "finished sub-agent to stop counting", func() bool { return !PendingAsyncWork(nil, m) })
	if len(m.ListRunning()) == 0 {
		t.Error("test setup: the finished agent should still be listed, so this proves Busy is what's checked")
	}
}
