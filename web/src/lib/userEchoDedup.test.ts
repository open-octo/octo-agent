import { describe, expect, it } from 'vitest'
import { appendLiveAfterHistory, isReplayedUserEcho } from './userEchoDedup'

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

describe('appendLiveAfterHistory', () => {
  it('drops the live echo of a message the history fetch already returned', () => {
    // Edit of the first message: the rerun's echo lands in the cleared list,
    // then the fetch returns the same persisted message.
    const history = [userMsg('edited', 1000)]
    const live = [userMsg('edited', 1000), { type: 'assistant', content: 'reply', createdAt: 1001 }]
    expect(appendLiveAfterHistory(history, live)).toEqual([history[0], live[1]])
  })

  it('puts older history ahead of live messages that arrived before the fetch', () => {
    const history = [userMsg('first', 1), { type: 'assistant', content: 'a', createdAt: 2 }, userMsg('edited', 3)]
    const live = [userMsg('edited', 3)]
    expect(appendLiveAfterHistory(history, live).map(m => m.content)).toEqual(['first', 'a', 'edited'])
  })

  it('keeps a live echo the fetch did not include', () => {
    const history = [userMsg('first', 1)]
    const live = [userMsg('edited', 3)]
    expect(appendLiveAfterHistory(history, live)).toEqual([history[0], live[0]])
  })

  it('returns the history unchanged when nothing arrived live', () => {
    const history = [userMsg('first', 1)]
    expect(appendLiveAfterHistory(history, [])).toEqual(history)
  })
})
