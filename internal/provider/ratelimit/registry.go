package ratelimit

import "sync"

// Registry hands out one shared Limiter per key. Provider clients are built
// per session (and per sub-agent, per workflow step, per background title
// call), so a limiter owned by a client would gate nothing; the registry is
// what makes one endpoint's limits hold across every client pointed at it.
//
// The zero value is ready to use. It is safe for concurrent use.
type Registry struct {
	mu sync.Mutex
	m  map[string]*entry
}

type entry struct {
	limiter        *Limiter
	rpm            int
	maxConcurrency int
}

// For returns the shared limiter for key, creating it on first use. Passing
// limits that differ from the ones the key was created with replaces the
// limiter, so an edited config.yml takes effect on the next client build
// instead of at the next restart; calls already gated by the old limiter run
// to completion under it.
//
// It returns nil when both limits are off — Limiter's methods are nil-safe.
func (r *Registry) For(key string, rpm, maxConcurrency int) *Limiter {
	if rpm <= 0 && maxConcurrency <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.m[key]; ok && e.rpm == rpm && e.maxConcurrency == maxConcurrency {
		return e.limiter
	}
	if r.m == nil {
		r.m = make(map[string]*entry)
	}
	e := &entry{limiter: New(rpm, maxConcurrency), rpm: rpm, maxConcurrency: maxConcurrency}
	r.m[key] = e
	return e.limiter
}
