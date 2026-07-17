package tools

import (
	"context"
	"fmt"
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

// TestRecordEventCaps verifies that retained sub-agent events are bounded in
// FIFO order so a long-running agent can't grow memory without bound.
func TestRecordEventCaps(t *testing.T) {
	a := &asyncSubAgent{id: "agent_1"}
	for i := 0; i < maxSubAgentEvents+10; i++ {
		a.recordEvent(SubAgentEvent{Kind: "tool", ToolName: fmt.Sprintf("tool_%d", i)})
	}

	events := a.listEvents()
	if len(events) != maxSubAgentEvents {
		t.Fatalf("len(events) = %d, want %d", len(events), maxSubAgentEvents)
	}

	// The oldest 10 events should have been dropped.
	if events[0].ToolName != "tool_10" {
		t.Errorf("first retained event = %q, want tool_10", events[0].ToolName)
	}
	if events[len(events)-1].ToolName != fmt.Sprintf("tool_%d", maxSubAgentEvents+10-1) {
		t.Errorf("last retained event = %q, want tool_%d", events[len(events)-1].ToolName, maxSubAgentEvents+10-1)
	}
}

// TestEventSinkRecordsWithoutOnEvent verifies that the sink stamped into a
// sub-agent context records events even when no live onEvent hook is set,
// enabling late-joining subscribers to replay the tool trail.
func TestEventSinkRecordsWithoutOnEvent(t *testing.T) {
	m := NewSubAgentManager(&fakeSpawner{})

	a := &asyncSubAgent{id: "agent_1"}
	m.agents[a.id] = a

	sink := m.eventSink(a.id, "test", "explore")
	if sink == nil {
		t.Fatal("eventSink returned nil for a tracked agent")
	}

	sink(SubAgentEvent{Kind: "started"})
	sink(SubAgentEvent{Kind: "tool", ToolName: "read_file"})

	events := a.listEvents()
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Kind != "started" || events[1].Kind != "tool" {
		t.Errorf("events = %v, want [started tool]", events)
	}
}

// TestEventSinkDoneCarriesStopReason verifies that a "done" event carries the
// agent's final disposition: normal completion reports the model stop reason,
// a user kill reports "killed", and other error exits report "error".
func TestEventSinkDoneCarriesStopReason(t *testing.T) {
	// Normal completion via Start: stop reason is "end_turn".
	m := NewSubAgentManager(&fixedSpawner{result: SpawnResult{Reply: "ok", AgentID: "child-1", StopReason: "end_turn"}})

	var mu sync.Mutex
	var events []SubAgentEvent
	m.SetOnEvent(func(ev SubAgentEvent) { mu.Lock(); events = append(events, ev); mu.Unlock() })

	id, err := m.Start(SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = id
	exited := make(chan struct{})
	m.SetOnExit(func(SubAgentNotification) { close(exited) })
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("onExit never fired")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := len(events) >= 2 && events[len(events)-1].Kind == "done"
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			evs := events
			mu.Unlock()
			t.Fatalf("events = %v, want trailing done", evs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	last := events[len(events)-1]
	mu.Unlock()
	if last.StopReason != "end_turn" {
		t.Errorf("done StopReason = %q, want end_turn", last.StopReason)
	}

	// Kill a running agent: the done event should report killed.
	sp := &blockingPromoteSpawner{unblock: make(chan struct{}), spawnCh: make(chan SpawnRequest, 1)}
	m2 := NewSubAgentManager(sp)
	var mu2 sync.Mutex
	var events2 []SubAgentEvent
	m2.SetOnEvent(func(ev SubAgentEvent) { mu2.Lock(); events2 = append(events2, ev); mu2.Unlock() })
	id2, err := m2.Start(SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-sp.spawnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn did not start")
	}
	m2.Kill(id2)
	sp.Unblock(SpawnResult{Reply: "ok", AgentID: "child-1", StopReason: "end_turn"}, nil)

	deadline = time.Now().Add(2 * time.Second)
	var killedEvent SubAgentEvent
	for {
		mu2.Lock()
		for _, ev := range events2 {
			if ev.AgentID == id2 && ev.Kind == "done" {
				killedEvent = ev
			}
		}
		mu2.Unlock()
		if killedEvent.Kind == "done" {
			break
		}
		if time.Now().After(deadline) {
			mu2.Lock()
			evs := events2
			mu2.Unlock()
			t.Fatalf("no killed done event for %q; events = %v", id2, evs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if killedEvent.StopReason != "killed" {
		t.Errorf("killed done StopReason = %q, want killed", killedEvent.StopReason)
	}
}

// fixedSpawner is a Spawner that returns a fixed result or error.
type fixedSpawner struct {
	result SpawnResult
	err    error
}

func (s *fixedSpawner) Spawn(_ context.Context, _ SpawnRequest) (SpawnResult, error) {
	return s.result, s.err
}

func (s *fixedSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return s.result, s.err
}

// TestEventSinkDoneErrorExit verifies that a Spawn error without a stop reason
// is surfaced as StopReason="error".
func TestEventSinkDoneErrorExit(t *testing.T) {
	m := NewSubAgentManager(&fixedSpawner{err: fmt.Errorf("spawn failed")})

	var events []SubAgentEvent
	m.SetOnEvent(func(ev SubAgentEvent) { events = append(events, ev) })

	if _, err := m.RunSync(context.Background(), SpawnRequest{Description: "d", Prompt: "p"}); err == nil {
		t.Fatal("RunSync: expected error")
	}

	if len(events) != 2 || events[1].Kind != "done" {
		t.Fatalf("events = %v, want [started done]", events)
	}
	if events[1].StopReason != "error" {
		t.Errorf("done StopReason = %q, want error", events[1].StopReason)
	}
}

// TestKillAfterAgentExited verifies that Kill() emits a "done" event even when
// the sub-agent's goroutine has already completed, so the frontend always
// receives the update immediately.
func TestKillAfterAgentExited(t *testing.T) {
	m := NewSubAgentManager(&fixedSpawner{result: SpawnResult{Reply: "ok", AgentID: "child-1", StopReason: "end_turn"}})

	var mu sync.Mutex
	var events []SubAgentEvent
	m.SetOnEvent(func(ev SubAgentEvent) { mu.Lock(); events = append(events, ev); mu.Unlock() })

	id, err := m.Start(SpawnRequest{Description: "d", Prompt: "p"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the agent to complete (onExit fires).
	exited := make(chan struct{})
	m.SetOnExit(func(SubAgentNotification) { close(exited) })
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("onExit never fired")
	}

	// Verify the first "done" event arrived with the original stop reason.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := len(events) >= 2 && events[len(events)-1].Kind == "done"
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			evs := events
			mu.Unlock()
			t.Fatalf("events = %v, want trailing done", evs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	firstDone := events[len(events)-1]
	mu.Unlock()
	if firstDone.StopReason != "end_turn" {
		t.Errorf("first done StopReason = %q, want end_turn", firstDone.StopReason)
	}

	// Now kill the already-completed agent. The fix should emit a second "done"
	// event with StopReason = "killed".
	eventsBefore := len(events)
	m.Kill(id)

	deadline = time.Now().Add(2 * time.Second)
	var killEvent SubAgentEvent
	for {
		mu.Lock()
		for _, ev := range events[eventsBefore:] {
			if ev.AgentID == id && ev.Kind == "done" {
				killEvent = ev
			}
		}
		mu.Unlock()
		if killEvent.Kind == "done" {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			evs := events[eventsBefore:]
			mu.Unlock()
			t.Fatalf("no kill done event for %q; events after kill = %v", id, evs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if killEvent.StopReason != "killed" {
		t.Errorf("kill done StopReason = %q, want killed", killEvent.StopReason)
	}
}
