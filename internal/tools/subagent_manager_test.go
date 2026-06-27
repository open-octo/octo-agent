package tools

import (
	"context"
	"sync"
	"testing"
	"time"
)

// collectEvents registers an onEvent hook that appends every event kind to a
// shared slice, returning an accessor for the collected kinds.
func collectEvents(m *SubAgentManager) func() []string {
	var mu sync.Mutex
	var kinds []string
	m.SetOnEvent(func(ev SubAgentEvent) {
		mu.Lock()
		kinds = append(kinds, ev.Kind)
		mu.Unlock()
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), kinds...)
	}
}

// A synchronous round must bracket its work with started/done events so live
// panels can show and then drop the entry — sync completions never fire the
// onExit hook, so "done" is the only completion signal a panel gets.
func TestRunSync_EmitsStartedAndDone(t *testing.T) {
	m := NewSubAgentManager(&fakeSpawner{})
	kinds := collectEvents(m)

	if _, err := m.RunSync(context.Background(), SpawnRequest{Description: "d", Prompt: "p"}); err != nil {
		t.Fatalf("RunSync: %v", err)
	}

	got := kinds()
	if len(got) != 2 || got[0] != "started" || got[1] != "done" {
		t.Fatalf("events = %v, want [started done]", got)
	}
}

// An async spawn must emit "done" after completion too (alongside the onExit
// notification), so the web live panel clears per-agent.
func TestStart_EmitsDoneOnCompletion(t *testing.T) {
	m := NewSubAgentManager(&fakeSpawner{})
	kinds := collectEvents(m)

	exited := make(chan struct{})
	m.SetOnExit(func(SubAgentNotification) { close(exited) })

	if _, err := m.Start(SpawnRequest{Description: "d", Prompt: "p"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("onExit never fired")
	}
	// "done" is emitted by a deferred call after the hook — give the goroutine
	// a moment to unwind.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := kinds()
		if len(got) >= 2 && got[len(got)-1] == "done" {
			if got[0] != "started" {
				t.Fatalf("events = %v, want started first", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("events = %v, want trailing done", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// cancelRecordingSpawner captures the contexts passed to Spawn/Continue so tests
// can assert they are cancelled at the right boundaries.
type cancelRecordingSpawner struct {
	spawnCtxCh       chan context.Context
	spawnReturnCh    chan SpawnResult
	continueCtxCh    chan context.Context
	continueReturnCh chan SpawnResult
}

func (s *cancelRecordingSpawner) Spawn(ctx context.Context, _ SpawnRequest) (SpawnResult, error) {
	s.spawnCtxCh <- ctx
	return <-s.spawnReturnCh, nil
}

func (s *cancelRecordingSpawner) Continue(ctx context.Context, _, _ string) (SpawnResult, error) {
	s.continueCtxCh <- ctx
	return <-s.continueReturnCh, nil
}

func waitForStatus(t *testing.T, m *SubAgentManager, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, status, found := m.Read(id)
		if !found {
			t.Fatalf("agent %q not found", id)
		}
		if status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent %q status = %q, want %q", id, status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForCancel waits until ctx is cancelled. runContinue cancels the round's
// context via a deferred call that fires when the round goroutine returns —
// which happens slightly after the status flips to "idle" (setDone runs first,
// then the exit hook/sink, then the defer). So a bare ctx.Err() read right
// after waitForStatus races that defer; poll instead.
func waitForCancel(t *testing.T, ctx context.Context, what string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Errorf("%s context was not cancelled", what)
	}
}

// blockingPromoteSpawner blocks in Spawn until Unblock is called, and respects
// the caller's context cancellation so turn-cancel tests don't hang.
type blockingPromoteSpawner struct {
	mu      sync.Mutex
	unblock chan struct{}
	result  SpawnResult
	err     error
	spawnCh chan SpawnRequest
}

func (s *blockingPromoteSpawner) Spawn(ctx context.Context, req SpawnRequest) (SpawnResult, error) {
	s.mu.Lock()
	unblock := s.unblock
	s.mu.Unlock()
	if s.spawnCh != nil {
		select {
		case s.spawnCh <- req:
		case <-ctx.Done():
			return SpawnResult{}, ctx.Err()
		}
	}
	select {
	case <-unblock:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.result, s.err
	case <-ctx.Done():
		return SpawnResult{}, ctx.Err()
	}
}

func (s *blockingPromoteSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

func (s *blockingPromoteSpawner) Unblock(res SpawnResult, err error) {
	s.mu.Lock()
	s.result = res
	s.err = err
	close(s.unblock)
	s.mu.Unlock()
}

func TestRunSync_Promote(t *testing.T) {
	sp := &blockingPromoteSpawner{unblock: make(chan struct{}), spawnCh: make(chan SpawnRequest, 1)}
	m := NewSubAgentManager(sp)

	resCh := make(chan SpawnResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := m.RunSync(context.Background(), SpawnRequest{Description: "d", Prompt: "p"})
		resCh <- res
		errCh <- err
	}()

	select {
	case <-sp.spawnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn did not start")
	}

	if !m.HasSync() {
		t.Error("HasSync() = false, want true before promote")
	}

	m.PromoteSync()

	select {
	case res := <-resCh:
		if res.StopReason != "promoted" {
			t.Errorf("StopReason = %q, want promoted", res.StopReason)
		}
		if res.AgentID == "" {
			t.Error("AgentID is empty")
		}
		waitForStatus(t, m, res.AgentID, "running")

		sp.Unblock(SpawnResult{Reply: "done", AgentID: "child-1"}, nil)
		waitForStatus(t, m, res.AgentID, "idle")
	case <-time.After(2 * time.Second):
		t.Fatal("RunSync did not return after promote")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("RunSync error: %v", err)
	}
}

func TestRunSync_Promote_Notifies(t *testing.T) {
	sp := &blockingPromoteSpawner{unblock: make(chan struct{}), spawnCh: make(chan SpawnRequest, 1)}
	m := NewSubAgentManager(sp)

	notes := make(chan SubAgentNotification, 1)
	m.SetOnExit(func(ev SubAgentNotification) { notes <- ev })

	go func() {
		m.RunSync(context.Background(), SpawnRequest{Description: "d", Prompt: "p"})
	}()

	select {
	case <-sp.spawnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn did not start")
	}
	m.PromoteSync()

	// Wait briefly for RunSync to hand off to the background goroutine.
	select {
	case <-time.After(100 * time.Millisecond):
	}

	sp.Unblock(SpawnResult{Reply: "final", AgentID: "child-1"}, nil)

	select {
	case n := <-notes:
		if n.AgentID == "" {
			t.Error("notification AgentID is empty")
		}
		if n.Result != "final" {
			t.Errorf("notification Result = %q, want final", n.Result)
		}
		if n.Kind != "spawn_done" {
			t.Errorf("notification Kind = %q, want spawn_done", n.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onExit not fired after promoted agent completed")
	}
}

func TestRunSync_Cancel(t *testing.T) {
	sp := &blockingPromoteSpawner{unblock: make(chan struct{}), spawnCh: make(chan SpawnRequest, 1)}
	m := NewSubAgentManager(sp)

	ctx, cancel := context.WithCancel(context.Background())
	resCh := make(chan SpawnResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := m.RunSync(ctx, SpawnRequest{Description: "d", Prompt: "p"})
		resCh <- res
		errCh <- err
	}()

	select {
	case <-sp.spawnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn did not start")
	}
	cancel()

	select {
	case res := <-resCh:
		if res.AgentID != "" || res.StopReason != "" {
			t.Errorf("cancelled RunSync returned non-empty result: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunSync did not return after cancel")
	}

	if err := <-errCh; err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestSend_CancelsSpawnContext verifies that runContinue cancels the previous
// round's context (from Spawn) before starting the Continue round, and cancels
// the Continue context when the round ends. Without this, CancelFuncs pile up
// and the initial Spawn context leaks until garbage collection.
func TestSend_CancelsSpawnContext(t *testing.T) {
	sp := &cancelRecordingSpawner{
		spawnCtxCh:       make(chan context.Context, 1),
		spawnReturnCh:    make(chan SpawnResult, 1),
		continueCtxCh:    make(chan context.Context, 1),
		continueReturnCh: make(chan SpawnResult, 1),
	}
	m := NewSubAgentManager(sp)

	id, err := m.Start(SpawnRequest{Description: "d"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx1 := <-sp.spawnCtxCh
	sp.spawnReturnCh <- SpawnResult{Reply: "spawned", AgentID: "child-1"}
	waitForStatus(t, m, id, "idle")

	go m.Send(id, "continue")

	var ctx2 context.Context
	select {
	case ctx2 = <-sp.continueCtxCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Continue")
	}

	if ctx1.Err() == nil {
		t.Error("Spawn context was not cancelled before Continue started")
	}

	sp.continueReturnCh <- SpawnResult{Reply: "continued"}
	waitForStatus(t, m, id, "idle")

	waitForCancel(t, ctx2, "Continue")
}
