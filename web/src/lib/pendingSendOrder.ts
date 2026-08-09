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

export interface ConfirmableSend extends PendingSendLike {
  pendingId: string
  text: string
}

// Which entry a history_user_message confirms, and which entries it proves
// will never be confirmed.
//
// A plain shift() assumes every send gets exactly one confirmation. One that
// goes missing — a WS gap, a send that raced its own subscription — leaves the
// queue permanently one entry ahead, and from then on every confirmation is
// read against the wrong entry: steers are misread as fresh turns, ghosts are
// never dropped, and bubbles are duplicated instead of de-duped. Matching on
// the text the server echoed back re-anchors the queue, so a single lost
// confirmation costs one stale entry rather than the rest of the session.
//
// Falls back to the head when nothing matches, which is what shift() did.
export function takeConfirmedSend<T extends ConfirmableSend>(
  queue: T[],
  content: string,
): { meta?: T; dropped: T[] } {
  const want = content.trim()
  const found = queue.findIndex(m => m.text.trim() === want)
  const removed = queue.splice(0, (found >= 0 ? found : 0) + 1)
  return { meta: removed[removed.length - 1], dropped: removed.slice(0, -1) }
}
