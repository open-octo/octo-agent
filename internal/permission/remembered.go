package permission

import "sync"

// Remembered holds session-level "always allow this" decisions, keyed by
// (tool, input) signature. It exists as a standalone type because the server
// and IM bridge rebuild their Engine on every turn (to pick up policy/mode
// changes) — decisions remembered on the engine itself would die with the
// turn. The transport keeps one Remembered per session and attaches it to
// each fresh engine; the CLI's single long-lived engine just uses the one it
// was born with.
type Remembered struct {
	mu sync.Mutex
	m  map[string]Decision
}

// NewRemembered returns an empty store.
func NewRemembered() *Remembered {
	return &Remembered{m: map[string]Decision{}}
}

func (r *Remembered) get(sig string) (Decision, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[sig]
	return d, ok
}

func (r *Remembered) set(sig string, d Decision) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[sig] = d
}

// AttachRemembered swaps in a shared decision store, replacing the engine's
// private one. Call before the engine serves checks.
func (e *Engine) AttachRemembered(r *Remembered) {
	if r == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.remember = r
}
