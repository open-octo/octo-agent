package tools

// PendingAsyncWork reports whether the session has async work in flight that a
// turn is legitimately waiting on: a one-shot background process (BgModeAsync)
// or a running async sub-agent. Both push a completion note to the model when
// they finish, so the turn that would answer "still waiting" has nothing to add
// until then.
//
// Transports use it to hold back the goal-continuation loop, whose only other
// guard — zero token progress — cannot catch this: each "still waiting" turn
// bills real tokens, so the loop would spin unbounded until the work finishes.
// The wait is safe because the completion hooks (BackgroundManager.SetOnExit,
// SubAgentManager.SetOnExit) start their own turn, whose end re-enters the
// continuation check.
//
// Interactive background processes are deliberately excluded: a service or REPL
// (octo serve, rails c) may never exit, and parking the goal on one would stall
// it forever. Sub-agents are counted by Busy, not by presence: ListRunning
// reports every agent that hasn't exited, and one that finished its round stays
// there indefinitely so a later turn can Continue it — treating those as
// in-flight would stall the goal just as badly.
//
// Either manager may be nil (a transport with no such manager wired).
func PendingAsyncWork(bg *BackgroundManager, sub *SubAgentManager) bool {
	if bg != nil {
		for _, info := range bg.ListRunning() {
			if info.Mode == BgModeAsync {
				return true
			}
		}
	}
	if sub != nil {
		for _, info := range sub.ListRunning() {
			if info.Busy {
				return true
			}
		}
	}
	return false
}
