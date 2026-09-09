package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testWindow keeps the RPM tests in the millisecond range instead of waiting
// out a real minute.
const testWindow = 60 * time.Millisecond

func TestNilLimiterIsNoOp(t *testing.T) {
	var l *Limiter
	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire on nil limiter: %v", err)
	}
	release()
	release() // must stay safe when called twice
}

func TestNewBothLimitsOffReturnsNil(t *testing.T) {
	if l := New(0, 0); l != nil {
		t.Errorf("New(0, 0) = %v, want nil", l)
	}
	if l := New(-1, -1); l != nil {
		t.Errorf("New(-1, -1) = %v, want nil", l)
	}
}

// TestMaxConcurrencyBoundsInFlight is the gate's whole point: however many
// callers pile in, only maxConcurrency of them are inside at once.
func TestMaxConcurrencyBoundsInFlight(t *testing.T) {
	l := New(0, 2)
	var (
		inFlight atomic.Int32
		peak     atomic.Int32
		wg       sync.WaitGroup
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(context.Background())
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			defer release()
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
		}()
	}
	wg.Wait()
	if got := peak.Load(); got > 2 {
		t.Errorf("peak in-flight = %d, want <= 2", got)
	}
}

// TestReleaseIsIdempotent guards the slot accounting: a double release would
// hand out a permit nobody acquired and let an extra call past the cap.
func TestReleaseIsIdempotent(t *testing.T) {
	l := New(0, 1)
	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release()

	// The slot is free exactly once: one Acquire succeeds immediately, and a
	// second must block.
	if _, err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx); err == nil {
		t.Error("third Acquire succeeded — the double release freed a slot twice")
	}
}

// TestRPMWaitsForTheWindow: with rpm=2, the third call may not start until
// the first one's slot ages out of the window.
func TestRPMWaitsForTheWindow(t *testing.T) {
	l := newWithWindow(2, 0, testWindow)
	start := time.Now()
	for i := range 2 {
		if _, err := l.Acquire(context.Background()); err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
	}
	if _, err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("third Acquire: %v", err)
	}
	if elapsed := time.Since(start); elapsed < testWindow {
		t.Errorf("third call started after %v, want at least the %v window", elapsed, testWindow)
	}
}

// TestRPMDoesNotOverIssueUnderConcurrency: concurrent Acquires must not race
// past the cap by all reading the same free permit. Asserted one-sided — at
// most rpm calls inside the window — so a slow machine that drags every call
// past the window can't fail it.
func TestRPMDoesNotOverIssueUnderConcurrency(t *testing.T) {
	l := newWithWindow(3, 0, testWindow)
	start := time.Now()
	elapsed := make([]time.Duration, 6)
	var wg sync.WaitGroup
	for i := range elapsed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Acquire(context.Background()); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			elapsed[i] = time.Since(start)
		}()
	}
	wg.Wait()
	within := 0
	for _, e := range elapsed {
		if e < testWindow {
			within++
		}
	}
	if within > 3 {
		t.Errorf("%d calls started within the window, want at most the rpm of 3", within)
	}
}

// TestAcquireReleasesSlotWhenTheWindowWaitIsCancelled: a caller interrupted
// while queued on the RPM window must not walk off with a concurrency slot.
func TestAcquireReleasesSlotWhenTheWindowWaitIsCancelled(t *testing.T) {
	l := newWithWindow(1, 1, testWindow)
	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	release()

	// The RPM permit is spent, so this one queues on the window; cancel it
	// mid-wait.
	ctx, cancel := context.WithTimeout(context.Background(), testWindow/4)
	defer cancel()
	if _, err := l.Acquire(ctx); err == nil {
		t.Fatal("Acquire succeeded, want the cancelled window wait to fail")
	}

	// If the cancelled call had kept its slot, this would block forever.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*testWindow)
	defer cancel2()
	if _, err := l.Acquire(ctx2); err != nil {
		t.Errorf("Acquire after a cancelled wait: %v — the slot was never given back", err)
	}
}

func TestAcquireHonoursAnAlreadyCancelledContext(t *testing.T) {
	l := New(0, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Acquire(ctx); err != context.Canceled {
		t.Errorf("Acquire error = %v, want context.Canceled", err)
	}
	// The slot must still be free.
	if _, err := l.Acquire(context.Background()); err != nil {
		t.Errorf("Acquire after cancelled call: %v", err)
	}
}

func TestRegistrySharesOneLimiterPerKey(t *testing.T) {
	var r Registry
	a := r.For("anthropic|https://api.example", 8, 1)
	b := r.For("anthropic|https://api.example", 8, 1)
	if a != b {
		t.Error("same key + same limits returned different limiters — clients would each get their own quota")
	}
	if c := r.For("openai|https://other.example", 8, 1); c == a {
		t.Error("different keys share a limiter")
	}
	if d := r.For("anthropic|https://api.example", 4, 1); d == a {
		t.Error("changed limits reused the old limiter — an edited config would not take effect")
	}
	if n := r.For("anthropic|https://api.example", 0, 0); n != nil {
		t.Errorf("For with both limits off = %v, want nil", n)
	}
}
