package tools

import "time"

// Resumable-child inspection. A synchronous sub_agent call returns its reply
// inline, and RunSync reaps the manager entry the moment it does — so from
// SubAgentManager's point of view the child no longer exists. It does: the
// Spawner keeps it in a live registry (see Spawner.Continue) so a later
// sub_agent_send can resume it with its history intact. These types let
// sub_agent_status read that registry, instead of reporting a still-resumable
// child as an unknown id.

// ChildSnapshot is the state of one resumable sub-agent held by a Spawner.
type ChildSnapshot struct {
	// ID is the spawner-side id — what a synchronous sub-agent's "[agent …]"
	// tag carries, and what sub_agent_send expects for that child.
	ID string
	// StopReason is why the child's most recent round ended (a provider
	// sentinel, or a budget sentinel such as "max_turns"/"stuck"). Empty when
	// the child hasn't completed a round.
	StopReason string
	// Err is why the child's most recent round failed outright, when it did.
	// A round that failed leaves StopReason and Reply empty.
	Err string
	// Reply is the child's most recent reply, capped by the spawner at the same
	// size the manager retains per async sub-agent. Callers rendering it for the
	// model still clip it to something a tool result can carry. Listings leave
	// it empty — they render only the header fields.
	Reply string
	// Busy reports that a round is running right now. Such a child is not
	// resumable: a follow-up would queue behind the round in flight.
	Busy bool
	// Turns is how many provider round-trips the last round took.
	Turns int
	// Idle is how long since the child last ran. It expires from the registry
	// once this passes the spawner's idle TTL.
	Idle time.Duration
}

// ChildInspector is implemented by Spawners that keep spawned children
// resumable and can report their state. Optional: a Spawner without it (test
// fakes, spawners that don't retain children) simply has nothing to report,
// and sub_agent_status falls back to its unknown-id error.
type ChildInspector interface {
	// InspectChild reports the child for id, or false when no live child has
	// that id.
	InspectChild(id string) (ChildSnapshot, bool)
	// ListChildren reports every live child, most recently used first.
	ListChildren() []ChildSnapshot
}
