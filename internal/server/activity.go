package server

// Activity levels, ordered by how much they want the user's attention.
const (
	// ActivityIdle: nothing is running and nothing is waiting.
	ActivityIdle = "idle"
	// ActivityBusy: at least one session has a turn in flight.
	ActivityBusy = "busy"
	// ActivityAsk: at least one session is blocked on the user answering a
	// question or a confirmation.
	ActivityAsk = "ask"
)

// Activity reports what the hub is doing right now, aggregated across every
// session — not per session, because the callers of this are whole-app surfaces
// (the desktop pet today) that show one thing at a time.
//
// Ask outranks busy: a turn that is blocked on the user is also "running", and
// of the two facts the one worth surfacing is that octo is waiting on them.
//
// Read-only and cheap (two mutexes, two length checks), so a caller can poll it
// on a timer instead of the server having to push activity changes into the
// hot path of every turn.
func (s *Server) Activity() string {
	s.pendingPromptMu.Lock()
	waiting := len(s.pendingQuestions) > 0 || len(s.pendingConfirms) > 0
	s.pendingPromptMu.Unlock()
	if waiting {
		return ActivityAsk
	}

	// An entry exists in interrupts exactly for the duration of a turn — the
	// same signal sessionStatus reports per session.
	s.interruptMu.Lock()
	running := len(s.interrupts) > 0
	s.interruptMu.Unlock()
	if running {
		return ActivityBusy
	}

	return ActivityIdle
}
