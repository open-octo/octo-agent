// Package ratelimit throttles outbound provider calls per endpoint, so a
// free-tier quota ("8 requests per minute", "1 concurrent request") holds no
// matter how many callers share the endpoint — the main loop, sub-agents,
// workflows, background title generation and the vision helper all build
// their own provider client but pass through the same Limiter.
//
// Two independent gates, either of which may be off:
//
//   - MaxConcurrency bounds calls in flight. A call holds its slot for the
//     whole provider call, streaming body included, and releases on return.
//   - RPM bounds calls *started* per rolling 60-second window. A call that
//     would exceed it waits for the window to free up rather than failing.
//
// Retries inside a provider call don't re-enter the gate: the call holds its
// concurrency slot across them and spends one RPM permit for the lot. The
// retry layer's own backoff (see package retry) is what spaces those out.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Window is the RPM accounting period.
const Window = time.Minute

// Limiter gates one endpoint. The zero value is not usable — build one with
// New. A nil *Limiter is a working no-op, so callers with no limits
// configured need no nil checks.
type Limiter struct {
	sem chan struct{} // concurrency slots; nil when unlimited

	window time.Duration
	mu     sync.Mutex
	// starts is a ring of the last cap(starts) permitted start times, in
	// issue order. Empty when RPM is unlimited. Entries record the time a
	// call was *cleared* to start, which is what bounds the window — writing
	// the reserved time on the way in (rather than the actual send time on
	// the way out) keeps concurrent Acquires from over-issuing.
	starts []time.Time
	idx    int

	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// New returns a Limiter enforcing rpm requests per minute and maxConcurrency
// simultaneous requests. Zero or negative disables that gate; with both off,
// New returns nil — a no-op limiter.
func New(rpm, maxConcurrency int) *Limiter {
	return newWithWindow(rpm, maxConcurrency, Window)
}

// newWithWindow is New with the RPM period as a parameter, so tests can use a
// window measured in milliseconds instead of waiting out a real minute.
func newWithWindow(rpm, maxConcurrency int, window time.Duration) *Limiter {
	if rpm <= 0 && maxConcurrency <= 0 {
		return nil
	}
	l := &Limiter{window: window, now: time.Now, sleep: sleepCtx}
	if maxConcurrency > 0 {
		l.sem = make(chan struct{}, maxConcurrency)
	}
	if rpm > 0 {
		l.starts = make([]time.Time, rpm)
	}
	return l
}

// Acquire blocks until the call may proceed, then returns the release
// function that gives back its concurrency slot. The returned function is
// safe to call more than once and is never nil, so `defer release()` is
// always correct — including on the error path, where the caller has no slot
// and release does nothing.
//
// It returns ctx.Err() if the context ends while waiting; a caller that gets
// an error has acquired nothing.
func (l *Limiter) Acquire(ctx context.Context) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return func() {}, err
	}
	if l.sem != nil {
		select {
		case l.sem <- struct{}{}:
		case <-ctx.Done():
			return func() {}, ctx.Err()
		}
	}
	release = l.releaser()
	// The RPM wait happens while holding the concurrency slot: the slot bounds
	// what is in flight, and a call queued on the window is on its way in.
	if wait := l.reserve(); wait > 0 {
		if err := l.sleep(ctx, wait); err != nil {
			release()
			return func() {}, err
		}
	}
	return release, nil
}

// releaser returns a one-shot function giving back this call's concurrency
// slot. Extra calls are ignored so a defer plus an explicit release can't
// free a slot twice and let an extra call through the gate.
func (l *Limiter) releaser() func() {
	if l.sem == nil {
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() { <-l.sem })
	}
}

// reserve claims the next RPM permit and reports how long the caller must
// wait before using it. It returns 0 when RPM is unlimited or the window has
// room right now.
func (l *Limiter) reserve() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.starts) == 0 {
		return 0
	}
	now := l.now()
	at := now
	// The permit being reused is the oldest of the last len(starts) — the
	// window may not hold another until it expires.
	if oldest := l.starts[l.idx]; !oldest.IsZero() {
		if earliest := oldest.Add(l.window); earliest.After(at) {
			at = earliest
		}
	}
	l.starts[l.idx] = at
	l.idx = (l.idx + 1) % len(l.starts)
	return at.Sub(now)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
