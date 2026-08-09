import { describe, it, expect } from 'vitest'
import { insertPendingSend, pendingSendInsertIndex, takeConfirmedSend } from './pendingSendOrder'

type Entry = { id: string; queued?: boolean }

const steer = (id: string): Entry => ({ id })
const queued = (id: string): Entry => ({ id, queued: true })

const ids = (q: Entry[]) => q.map(e => e.id)

describe('pendingSendInsertIndex', () => {
  it('appends to an empty queue', () => {
    expect(pendingSendInsertIndex([], false)).toBe(0)
    expect(pendingSendInsertIndex([], true)).toBe(0)
  })

  it('appends a queued entry behind everything', () => {
    expect(pendingSendInsertIndex([queued('q1'), steer('s1')], true)).toBe(2)
  })

  it('puts a steer ahead of the first queued entry', () => {
    expect(pendingSendInsertIndex([steer('s1'), queued('q1'), queued('q2')], false)).toBe(1)
  })

  it('appends a steer when nothing is queued', () => {
    expect(pendingSendInsertIndex([steer('s1'), steer('s2')], false)).toBe(2)
  })
})

describe('insertPendingSend', () => {
  // The case the ordering exists for: queue Q, then steer S. The server injects
  // S into the running turn and confirms it first, so S must be retired first.
  it('confirms a later steer before an earlier queued message', () => {
    const q: Entry[] = []
    insertPendingSend(q, queued('Q'))
    insertPendingSend(q, steer('S'))
    expect(ids(q)).toEqual(['S', 'Q'])
  })

  it('keeps queued messages in the order they were sent', () => {
    const q: Entry[] = []
    insertPendingSend(q, queued('Q1'))
    insertPendingSend(q, queued('Q2'))
    insertPendingSend(q, queued('Q3'))
    expect(ids(q)).toEqual(['Q1', 'Q2', 'Q3'])
  })

  it('keeps steers in the order they were sent', () => {
    const q: Entry[] = []
    insertPendingSend(q, steer('S1'))
    insertPendingSend(q, steer('S2'))
    expect(ids(q)).toEqual(['S1', 'S2'])
  })

  it('interleaves a steer between queued messages without reordering them', () => {
    const q: Entry[] = []
    insertPendingSend(q, queued('Q1'))
    insertPendingSend(q, queued('Q2'))
    insertPendingSend(q, steer('S'))
    expect(ids(q)).toEqual(['S', 'Q1', 'Q2'])
  })

  it('leaves an already-confirmed steer alone (only the pending tail matters)', () => {
    // S1 was sent and retired; the remaining queue starts with a queued entry.
    const q: Entry[] = [queued('Q1')]
    insertPendingSend(q, steer('S2'))
    expect(ids(q)).toEqual(['S2', 'Q1'])
  })
})

describe('takeConfirmedSend', () => {
  type Send = { pendingId: string; text: string; queued?: boolean }
  const send = (id: string, text: string): Send => ({ pendingId: id, text })

  it('retires the head when it is the message confirmed', () => {
    const q = [send('a', 'first'), send('b', 'second')]
    const { meta, dropped } = takeConfirmedSend(q, 'first')
    expect(meta?.pendingId).toBe('a')
    expect(dropped).toEqual([])
    expect(q.map(m => m.pendingId)).toEqual(['b'])
  })

  it('ignores whitespace the server trimmed off', () => {
    const q = [send('a', '  padded  ')]
    expect(takeConfirmedSend(q, 'padded').meta?.pendingId).toBe('a')
  })

  // The desync this exists for: 'a' never got a confirmation, so a plain
  // shift() would hand 'a' back for 'second' and stay one entry ahead forever.
  it('re-anchors past entries whose confirmation never arrived', () => {
    const q = [send('a', 'first'), send('b', 'second'), send('c', 'third')]
    const { meta, dropped } = takeConfirmedSend(q, 'second')
    expect(meta?.pendingId).toBe('b')
    expect(dropped.map(m => m.pendingId)).toEqual(['a'])
    expect(q.map(m => m.pendingId)).toEqual(['c'])
  })

  it('matches the earliest of two identical messages', () => {
    const q = [send('a', 'same'), send('b', 'same')]
    expect(takeConfirmedSend(q, 'same').meta?.pendingId).toBe('a')
    expect(q.map(m => m.pendingId)).toEqual(['b'])
  })

  // Content the client can't match (an attachment-only send the server
  // re-rendered, an older server): behave exactly as the old shift() did.
  it('falls back to the head when nothing matches', () => {
    const q = [send('a', 'first'), send('b', 'second')]
    const { meta, dropped } = takeConfirmedSend(q, 'something else entirely')
    expect(meta?.pendingId).toBe('a')
    expect(dropped).toEqual([])
    expect(q.map(m => m.pendingId)).toEqual(['b'])
  })

  it('reports nothing for an empty queue', () => {
    const q: Send[] = []
    expect(takeConfirmedSend(q, 'anything')).toEqual({ meta: undefined, dropped: [] })
  })
})
