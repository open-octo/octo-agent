import { describe, expect, it } from 'vitest'
import { isReplayedUserEcho } from './userEchoDedup'

const userMsg = (content: string, createdAt: number, pending = false) =>
  ({ type: 'user', content, createdAt, pending })

describe('isReplayedUserEcho', () => {
  it('detects the replay of an already-confirmed echo', () => {
    // The bubble was confirmed by the original broadcast (pending cleared);
    // the replayed event carries the same content and persisted created_at.
    const msgs = [userMsg('hello', 1000)]
    expect(isReplayedUserEcho(msgs, 'hello', 1000)).toBe(true)
  })

  it('does not match a still-pending bubble — that one is the optimistic echo the handler replaces in place', () => {
    const msgs = [userMsg('hello', 1000, true)]
    expect(isReplayedUserEcho(msgs, 'hello', 1000)).toBe(false)
  })

  it('does not match same text from a different turn (different created_at)', () => {
    const msgs = [userMsg('hello', 1000)]
    expect(isReplayedUserEcho(msgs, 'hello', 2000)).toBe(false)
  })

  it('does not match different text at the same instant', () => {
    const msgs = [userMsg('hello', 1000)]
    expect(isReplayedUserEcho(msgs, 'goodbye', 1000)).toBe(false)
  })

  it('ignores assistant messages entirely', () => {
    const msgs = [{ type: 'assistant', content: 'hello', createdAt: 1000 }]
    expect(isReplayedUserEcho(msgs, 'hello', 1000)).toBe(false)
  })

  it('matches anywhere in the transcript, not just the tail', () => {
    const msgs = [
      userMsg('hello', 1000),
      { type: 'assistant', content: 'hi', createdAt: 1001 },
      userMsg('next', 1002),
    ]
    expect(isReplayedUserEcho(msgs, 'hello', 1000)).toBe(true)
  })

  it('returns false on an empty transcript (first echo of a fresh turn)', () => {
    expect(isReplayedUserEcho([], 'hello', 1000)).toBe(false)
  })
})
