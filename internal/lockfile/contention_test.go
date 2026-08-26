package lockfile

import (
	"path/filepath"
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
// Every waiter here is behind a bounded queue: at most waiters*hold of work
// stands between it and the lock, and its budget is 25x that. A queued wait
// tracks the queue (measured: ~78ms against an 80ms queue, stable to the
// millisecond). Retrying a non-blocking lock on a timer instead — no queue, so
// each retry re-enters the race from scratch — stretched the same 80ms queue
// to 528-664ms, and tightening the budget made waiters give up outright. The
// ceiling below sits between the two.
func TestAcquire_ContentionQueuesRatherThanStarves(t *testing.T) {
	if h := Acquire(filepath.Join(t.TempDir(), "probe.json")); h == nil {
		t.Skip("file locking unavailable here")
	} else {
		h.Release()
	}

	path := filepath.Join(t.TempDir(), "registry.json")
	const waiters = 8
	const rounds = 10
	const hold = 10 * time.Millisecond
	const queue = waiters * hold // work standing between a waiter and the lock
	const budget = 25 * queue    // generous: the point is that nobody gives up
	const ceiling = 4 * queue    // a queued wait stays well inside this

	var gaveUp atomic.Int64
	var worst atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				start := time.Now()
				h := acquire(path, budget)
				waited := int64(time.Since(start))
				for {
					old := worst.Load()
					if waited <= old || worst.CompareAndSwap(old, waited) {
						break
					}
				}
				if h == nil {
					gaveUp.Add(1)
					continue
				}
				time.Sleep(hold)
				h.Release()
			}
		}()
	}
	wg.Wait()

	acquires := waiters * rounds
	t.Logf("worst wait %s over %d acquires behind a %s queue, %d gave up",
		time.Duration(worst.Load()), acquires, queue, gaveUp.Load())
	if n := gaveUp.Load(); n > 0 {
		t.Errorf("%d of %d acquires gave up inside a %s budget — an unlocked write, and a lost update with it", n, acquires, budget)
	}
	if w := time.Duration(worst.Load()); w > ceiling {
		t.Errorf("worst wait %s exceeds %s for a %s queue — waiters are racing, not queueing", w, ceiling, queue)
	}
}
