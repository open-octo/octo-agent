package hooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Async hooks (Stop / PostToolUse / SubagentStop / PreCompact marked async in
// hooks.yml) run off the turn's critical path so a slow retention script never
// blocks the next prompt — the defect the synchronous post-turn hook had. But
// retention must not be lost either, so the queue is durable at its edges: an
// item that can't be handed to a worker in time (overflow, or a not-yet-run
// backlog at process exit) is written to ~/.octo/hooks-pending/ and re-enqueued
// by the next process to start. The common path stays in-memory; disk is only
// touched under backpressure or shutdown.
//
// The queue is process-level (shared by every Engine, like the SeenSet): one
// worker set, one pending dir. Multi-process safety rests on atomic-rename
// claiming of pending files — at worst a retention hook runs twice, never zero
// times.

const (
	spillWorkers   = 2  // concurrent async hook executions
	spillChanDepth = 64 // in-memory backlog before overflow spills to disk
)

// asyncItem is one queued async hook: the command, its per-hook timeout, and
// the stdin payload. Serialised to a pending file when spilled.
type asyncItem struct {
	Command string        `json:"command"`
	Timeout time.Duration `json:"timeout"`
	Payload Payload       `json:"payload"`
}

type spillQueue struct {
	mu      sync.Mutex
	ch      chan asyncItem
	wg      sync.WaitGroup
	started bool
	closed  bool
	dir     string
	notify  func(string)
}

// sharedSpill is the one process-level async queue.
var sharedSpill = &spillQueue{ch: make(chan asyncItem, spillChanDepth)}

// SetSpillNotify sets the sink for async-hook errors/traces (process-global).
// Nil-safe; last writer wins. Callers wire it to the same place a synchronous
// hook's notice would go.
func SetSpillNotify(fn func(string)) {
	sharedSpill.mu.Lock()
	sharedSpill.notify = fn
	sharedSpill.mu.Unlock()
}

// enqueueAsync hands an async hook to the queue: in-memory when there's room,
// otherwise spilled to disk (overflow never blocks the caller). Lazily starts
// the workers and redelivers any pending files left by a prior process.
func enqueueAsync(item asyncItem) {
	sharedSpill.ensureStarted()
	sharedSpill.offer(item)
}

// offer hands item to a worker if the in-memory backlog has room, else spills
// it to disk — overflow (and post-Drain submission) never blocks the caller.
func (q *spillQueue) offer(item asyncItem) {
	q.mu.Lock()
	closed := q.closed
	q.mu.Unlock()
	if closed {
		q.spillToDisk(item)
		return
	}
	select {
	case q.ch <- item:
	default:
		q.spillToDisk(item) // backlog full → durable overflow
	}
}

func (q *spillQueue) ensureStarted() {
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return
	}
	q.started = true
	if q.dir == "" {
		q.dir = pendingDir()
	}
	q.mu.Unlock()

	for i := 0; i < spillWorkers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	// Redeliver anything a prior process spilled, in the background so startup
	// isn't blocked on it.
	go q.redeliverPending()
}

func (q *spillQueue) worker() {
	defer q.wg.Done()
	for item := range q.ch {
		q.run(item)
	}
}

// run executes one async hook. Errors are surfaced via notify but never
// retried — a deterministically failing script shouldn't loop forever; the
// durability guarantee is against crashes and overflow, not against a bad hook.
func (q *spillQueue) run(item asyncItem) {
	if _, err := execShell(context.Background(), item.Command, mustMarshal(item.Payload), item.Timeout); err != nil {
		q.notifyMsg(err.Error())
	}
}

func (q *spillQueue) notifyMsg(msg string) {
	q.mu.Lock()
	fn := q.notify
	q.mu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

// Drain stops accepting new async work and waits up to deadline for the workers
// to finish the in-memory backlog. Anything still queued when the deadline hits
// is spilled to disk for the next process. Called from the CLI exit path and
// the server's shutdown. Safe to call once.
func (q *spillQueue) Drain(deadline time.Duration) {
	q.mu.Lock()
	if !q.started || q.closed {
		q.closed = true
		q.mu.Unlock()
		return
	}
	q.closed = true
	close(q.ch)
	q.mu.Unlock()

	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(deadline):
	}
	// Spill whatever the workers didn't pick up so it survives process exit.
	// On the done path the workers already drained the channel (this loop finds
	// it empty); on timeout, or when no workers were running, it flushes the
	// backlog to disk. The channel is closed, so a receive never blocks: it
	// yields buffered items, then (zero, false) to end the loop.
	for {
		item, ok := <-q.ch
		if !ok {
			return
		}
		q.spillToDisk(item)
	}
}

// DrainSpill drains the process-level async queue. Entry points call it on the
// way out so queued retention isn't dropped.
func DrainSpill(deadline time.Duration) { sharedSpill.Drain(deadline) }

func pendingDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "hooks-pending")
}

// spillToDisk writes item to a uniquely-named pending file (temp + atomic
// rename). Best-effort: a write failure is surfaced but not fatal.
func (q *spillQueue) spillToDisk(item asyncItem) {
	if q.dir == "" {
		q.dir = pendingDir()
	}
	if q.dir == "" {
		q.notifyMsg("hooks: cannot spill async hook (no home dir); dropping")
		return
	}
	if err := os.MkdirAll(q.dir, 0o755); err != nil {
		q.notifyMsg("hooks: spill mkdir: " + err.Error())
		return
	}
	b, err := json.Marshal(item)
	if err != nil {
		q.notifyMsg("hooks: spill marshal: " + err.Error())
		return
	}
	id := randHex()
	tmp := filepath.Join(q.dir, "."+id+".tmp")
	final := filepath.Join(q.dir, id+".json")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		q.notifyMsg("hooks: spill write: " + err.Error())
		return
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		q.notifyMsg("hooks: spill rename: " + err.Error())
	}
}

// redeliverPending re-enqueues pending files left by a prior process (or spilled
// by this one). Each file is claimed by an atomic rename to a per-pid name so two
// processes can't both run it; the claimer runs it and deletes it. A claim that
// loses the race (rename fails) is simply skipped.
func (q *spillQueue) redeliverPending() {
	if q.dir == "" {
		return
	}
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return
	}
	pid := os.Getpid()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		src := filepath.Join(q.dir, name)
		claimed := src + ".claimed." + strconv.Itoa(pid)
		if err := os.Rename(src, claimed); err != nil {
			continue // another process claimed it first
		}
		b, err := os.ReadFile(claimed)
		if err != nil {
			_ = os.Remove(claimed)
			continue
		}
		var item asyncItem
		if err := json.Unmarshal(b, &item); err != nil {
			_ = os.Remove(claimed) // corrupt entry — drop it
			continue
		}
		q.run(item)
		_ = os.Remove(claimed)
	}
}

func mustMarshal(p Payload) []byte {
	b, _ := json.Marshal(p)
	return b
}

func randHex() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
