package hooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// staleClaimAge is how long a claimed-but-unfinished pending file must sit
// before another process reclaims it. Set well past any hook's max runtime
// (timeoutCeiling) so a live sibling's in-flight claim is never stolen; only a
// crashed claimer's orphan is recovered.
const staleClaimAge = 2 * timeoutCeiling

// asyncItem is one queued async hook: the command, its per-hook timeout, the
// originating working directory (so a relative command runs where it was
// configured, not in whatever process later claims it), and the stdin payload.
// Serialised to a pending file when spilled.
type asyncItem struct {
	Command string        `json:"command"`
	Timeout time.Duration `json:"timeout"`
	Cwd     string        `json:"cwd,omitempty"`
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
// The closed-check and the send are done under the SAME lock that Drain holds
// while it closes the channel, so offer can never send on a closed channel
// (which would panic). The send is non-blocking (select/default), so holding
// the lock across it can't deadlock; spillToDisk runs after releasing it.
func (q *spillQueue) offer(item asyncItem) {
	q.mu.Lock()
	if !q.closed {
		select {
		case q.ch <- item:
			q.mu.Unlock()
			return
		default: // backlog full
		}
	}
	q.mu.Unlock()
	q.spillToDisk(item) // closed, or backlog full → durable overflow
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
	if _, err := execShellDir(context.Background(), item.Command, mustMarshal(item.Payload), item.Timeout, item.Cwd); err != nil {
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
	q.reclaimStaleClaims()
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
		// Stamp the claim time so a crash mid-run leaves an orphan that
		// reclaimStaleClaims recovers only after it's provably stale.
		now := time.Now()
		_ = os.Chtimes(claimed, now, now)
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

// reclaimStaleClaims renames orphaned "<id>.json.claimed.<pid>" files — left by
// a claimer that crashed between claiming and deleting — back to "<id>.json" so
// they're retried, but only once they're older than staleClaimAge (past any
// hook's max runtime), so a live sibling's in-flight claim is never stolen.
// This closes the "runs zero times" gap in the durability guarantee.
func (q *spillQueue) reclaimStaleClaims() {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		idx := strings.Index(name, ".json.claimed.")
		if e.IsDir() || idx < 0 {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < staleClaimAge {
			continue
		}
		orig := name[:idx] + ".json"
		_ = os.Rename(filepath.Join(q.dir, name), filepath.Join(q.dir, orig))
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
