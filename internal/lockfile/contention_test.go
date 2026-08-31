package lockfile

import (
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Contention must queue, not race. This is what stops a lost update in
// practice: a caller that waits out its budget proceeds unlocked, so an
// acquire that keeps losing the race is how the registry silently drops
// another process's write — the failure this package exists to prevent.
//
// The shape: one waiter asks first and then blocks, a second caller hammers
// the lock behind it, and the count of times the latecomer gets in ahead is
// the measurement. Queued, it cannot get in at all — it asked later, so it
// waits later. Retrying a non-blocking lock on a timer has no queue, so every
// release is a fresh coin flip and the latecomer wins its share of them.
//
// Counting who gets in, rather than timing how long a wait took, is what keeps
// this honest on a loaded CI machine: the verdict does not move when every
// sleep in it runs long.
func TestAcquire_ContentionQueuesRatherThanRacing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	held := Acquire(path)
	if held == nil {
		t.Skip("file locking unavailable here")
	}

	// The first waiter. It asks while the lock is held, so it is at the head of
	// the queue before anyone else asks.
	var firstIn atomic.Bool
	asked := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(asked)
		h := acquire(path, 10*time.Second)
		// firstIn records a real acquisition, not just "acquire returned". A
		// waiter that gives up (proceed-unlocked, h == nil) is the failure this
		// package exists to prevent, and must be caught regardless of platform.
		if h != nil {
			firstIn.Store(true)
			h.Release()
		}
	}()
	<-asked
	// The first waiter is only queued once its goroutine is scheduled, runs
	// acquire, and blocks in the kernel's flock wait. A single short sleep is
	// wall-clock and clamps badly when the runner is loaded — the flake this
	// test has hit on macOS CI, where a 50ms sleep let the hammering latecomer
	// barge in front of a waiter that had not actually queued yet. Yielding
	// repeatedly for a generous window is robust to that: the waiter's goroutine
	// gets scheduled and blocks long before we release, so it is at the head of
	// the queue when the lock frees.
	waitDeadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(waitDeadline) {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}

	// The latecomer, asking over and over from behind.
	var jumpedAhead atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(5 * time.Second)
		for !firstIn.Load() && time.Now().Before(deadline) {
			h := acquire(path, 50*time.Millisecond)
			if h == nil {
				continue
			}
			if !firstIn.Load() {
				jumpedAhead.Add(1)
			}
			h.Release()
		}
	}()

	held.Release()
	wg.Wait()

	if !firstIn.Load() {
		t.Fatal("the first waiter never got the lock")
	}
	// One is the benign race: the latecomer can slip in during the window
	// between the release and the queued waiter being scheduled. Repeatedly is
	// the bug — it means asking first bought nothing.
	//
	// That "at most one" bound only holds where flock() wakes waiters in FIFO
	// order (Linux, Windows). macOS's flock does not guarantee FIFO wakeup: a
	// later requester can barge in front of an already-queued waiter, so the
	// count climbs even when acquire blocks correctly (observed repeatedly on
	// the macOS CI runner). Assert the strict ordering only where it is
	// guaranteed; on macOS the non-starvation check above (firstIn) is the
	// property that actually keeps a waiter from giving up and losing a write.
	if runtime.GOOS != "darwin" {
		if n := jumpedAhead.Load(); n > 1 {
			t.Errorf("the latecomer got the lock %d times ahead of the waiter that asked first — waiters are racing, not queueing", n)
		}
	}
}
