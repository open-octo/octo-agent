import { describe, it, expect } from 'vitest'
import { anchorBgTasks, BG_ANCHOR_TOLERANCE_MS } from './bgTaskAnchor'

const T0 = Date.parse('2026-08-26T10:00:00Z')

function task(id: string, elapsed?: any) {
  return { handle_id: id, command: `cmd ${id}`, elapsed }
}

describe('anchorBgTasks', () => {
  it('anchors a new task to now minus the server elapsed', () => {
    const [t] = anchorBgTasks([], [task('bg_1', 67)], T0)
    expect(t.startedAt).toBe(T0 - 67_000)
    expect(t.command).toBe('cmd bg_1')
  })

  it('keeps the anchor of a task already on screen', () => {
    const first = anchorBgTasks([], [task('bg_1', 67)], T0)
    // A later broadcast: elapsed advanced, and truncation lost a fraction.
    const second = anchorBgTasks(first, [task('bg_1', 70)], T0 + 3_600)
    expect(second[0].startedAt).toBe(first[0].startedAt)
  })

  it('re-anchors when the server disagrees beyond the tolerance', () => {
    const first = anchorBgTasks([], [task('bg_1', 9_000)], T0)
    // Same handle id, but a freshly started process (server ids restart at 1).
    const second = anchorBgTasks(first, [task('bg_1', 2)], T0 + 1_000)
    expect(second[0].startedAt).toBe(T0 + 1_000 - 2_000)
  })

  it('treats a drift inside the tolerance as the same process', () => {
    const first = anchorBgTasks([], [task('bg_1', 30)], T0)
    const second = anchorBgTasks(first, [task('bg_1', 30)], T0 + BG_ANCHOR_TOLERANCE_MS)
    expect(second[0].startedAt).toBe(first[0].startedAt)
  })

  it('falls back to now for missing, non-numeric or negative elapsed', () => {
    const tasks = anchorBgTasks([], [task('a'), task('b', null), task('c', 'x'), task('d', -5)], T0)
    for (const t of tasks) expect(t.startedAt).toBe(T0)
  })

  it('re-anchors a task that disappeared and came back', () => {
    const first = anchorBgTasks([], [task('bg_1', 300)], T0)
    const gone = anchorBgTasks(first, [], T0 + 1_000)
    expect(gone).toEqual([])
    const back = anchorBgTasks(gone, [task('bg_1', 1)], T0 + 2_000)
    expect(back[0].startedAt).toBe(T0 + 1_000)
  })

  it('handles an empty or missing incoming list', () => {
    expect(anchorBgTasks([], [], T0)).toEqual([])
    expect(anchorBgTasks(undefined as any, undefined as any, T0)).toEqual([])
  })
})
