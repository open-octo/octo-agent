// Replay dedup for live history_user_message echoes.
//
// On a mid-turn (re)subscribe the server replays the turn's buffered events —
// including the user message that started the turn (buffered server-side so a
// tab that missed the original broadcast still gets it). A client that was
// already subscribed has long since confirmed that bubble (pending cleared,
// pendingSends entry retired), so the pending-bubble match in the live handler
// can't catch the replayed echo and would append a second, identical bubble —
// the duplicate that vanishes on refresh.
//
// The replay carries the same event verbatim, so the persisted created_at is
// identical across the original broadcast and the replay: (content, createdAt)
// is a natural idempotency key. Two genuinely separate sends of the same text
// land in different turns with different CreatedAt values, so they never
// false-positive.

export interface UserMsgLike {
  type: string
  pending?: boolean
  content: string
  createdAt?: number
}

// Reports whether msgs already holds a confirmed user bubble for this echo,
// i.e. the echo is a replay and must not be appended again.
export function isReplayedUserEcho(msgs: readonly UserMsgLike[], content: string, createdAt: number): boolean {
  return msgs.some(m =>
    m.type === 'user' && !m.pending && m.content === content && m.createdAt === createdAt,
  )
}

// Joins a history fetch with the live messages that arrived while it was in
// flight. A rollback (edit / retry) clears the transcript and refetches while
// the rerun is already streaming: its user echo lands first, and the server
// has also persisted that message below the turn's history watermark, so the
// fetch returns it too. History goes first (it is older), and a live user
// bubble the fetch already holds is dropped rather than rendered twice.
export function appendLiveAfterHistory<T extends UserMsgLike>(history: readonly T[], live: readonly T[]): T[] {
  const fresh = live.filter(m =>
    !(m.type === 'user' && m.createdAt !== undefined && isReplayedUserEcho(history, m.content, m.createdAt)),
  )
  return [...history, ...fresh]
}
