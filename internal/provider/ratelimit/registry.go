package ratelimit

import (
	"strconv"
	"sync"
)

// Registry hands out one shared Limiter per endpoint + limits pair. Provider
// clients are built per session (and per sub-agent, per workflow step, per
// background title call), so a limiter owned by a client would gate nothing;
// the registry is what makes one endpoint's limits hold across every client
// pointed at it.
//
// The zero value is ready to use. It is safe for concurrent use.
type Registry struct {
	mu sync.Mutex
	m  map[string]*Limiter
}

// For returns the shared limiter for key under these limits, creating it on
// first use.
//
// The limits are part of the identity, not just of the value: two endpoints
// may legitimately share a provider and base URL while publishing different
// quotas (a free key and a paid one on one gateway). Keying on the endpoint
// alone would make each build for one of them evict the other's limiter, and
// the senders cached either side would then hold different limiters — no
// shared gate at all. Editing limits in config.yml therefore strands the old
// limiter in the map rather than replacing it; that is one small entry per
// hand edit, and calls still running under it finish gated.
//
// It returns nil when both limits are off — Limiter's methods are nil-safe.
func (r *Registry) For(key string, rpm, maxConcurrency int) *Limiter {
	if rpm <= 0 && maxConcurrency <= 0 {
		return nil
	}
	key = key + "|" + strconv.Itoa(rpm) + "|" + strconv.Itoa(maxConcurrency)
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.m[key]; ok {
		return l
	}
	if r.m == nil {
		r.m = make(map[string]*Limiter)
	}
	l := New(rpm, maxConcurrency)
	r.m[key] = l
	return l
}
