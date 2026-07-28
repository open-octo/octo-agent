// Insertion order for the per-session pendingSends FIFO in ChatView.
//
// That FIFO is retired one entry per history_user_message, so it has to be kept
// in the order the SERVER confirms messages — not the order the user sent them.
// The two differ once queueing exists: a steer is injected into the turn already
// running, while a queued message waits for the turn AFTER it. So a steer sent
// later is confirmed first, and appending it would retire the queued entry
// instead — dropping the wrong ghost bubble and stranding the other one.
//
// Only a steer can jump: it goes ahead of the first queued entry. Queued entries
// keep their relative order (each runs as its own turn, in order), and a steer
// never overtakes another steer (they fold into one injection anyway).

export interface PendingSendLike {
  queued?: boolean
}

// Index at which `isQueued` should be inserted into `queue`.
export function pendingSendInsertIndex(queue: readonly PendingSendLike[], isQueued: boolean): number {
  if (isQueued) return queue.length
  const firstQueued = queue.findIndex(m => m.queued)
  return firstQueued >= 0 ? firstQueued : queue.length
}

// Insert into `queue` in place, at the position above.
export function insertPendingSend<T extends PendingSendLike>(queue: T[], entry: T): T[] {
  queue.splice(pendingSendInsertIndex(queue, entry.queued === true), 0, entry)
  return queue
}
