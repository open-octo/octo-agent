import { describe, it, expect } from 'vitest'
import { insertPendingSend, pendingSendInsertIndex } from './pendingSendOrder'

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
