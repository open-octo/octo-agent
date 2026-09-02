package lockfile

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Contention must not starve a waiting caller. This is what stops a lost
// update in practice: a caller that waits out its budget proceeds unlocked,
// so an acquire that keeps losing the race is how the registry silently drops
// another process's write — the failure this package exists to prevent.
//
// The shape: one waiter asks first and then blocks, a second caller hammers
// the lock behind it. The first waiter must end up HOLDING the lock — not give
// up and proceed unlocked — even while the latecomer keeps getting in ahead.
// Given that a correct acquire blocks in the kernel (and the latecomer stops
// after a bounded deadline), the first waiter is guaranteed to acquire within
// its generous budget.
//
// We deliberately do NOT assert strict FIFO ordering (fairest first). flock()
// does not guarantee FIFO wakeup under load: macOS and, at high contention,
// Linux both let a later requester barge in front of an already-queued waiter,
// so a count of how often the latecomer "jumps ahead" is not a stable signal
// and made this test flaky on CI. What is stable, and is the actual
// lost-update-prevention property, is that the first waiter is not starved
// into giving up.
func TestAcquire_ContentionQueuesRatherThanRacing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	held := Acquire(path)
	if held == nil {
		t.Skip("file locking unavailable here")
	}

	// The first waiter, asking while the lock is held. It must acquire a real
	// handle — firstIn stays false if acquire gives up (proceed-unlocked).
	var firstIn atomic.Bool
	asked := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(asked)
		if h := acquire(path, 10*time.Second); h != nil {
			firstIn.Store(true)
			h.Release()
		}
	}()
	<-asked
	time.Sleep(50 * time.Millisecond) // let it reach the wait

	// The latecomer, hammering the lock from behind for a bounded window. It
	// stops once the first waiter is in, so it cannot run the first waiter's
	// budget out.
	deadline := time.Now().Add(5 * time.Second)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !firstIn.Load() && time.Now().Before(deadline) {
			if h := acquire(path, 50*time.Millisecond); h != nil {
				h.Release()
			}
		}
	}()

	held.Release()
	wg.Wait()

	if !firstIn.Load() {
		t.Fatal("the first waiter gave up (proceeded unlocked) instead of acquiring the lock")
	}
}
