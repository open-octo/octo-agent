import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

const setSessionPinned = vi.fn()
vi.mock('../lib/api', () => ({
  setSessionPinned: (id: string, pinned: boolean) => setSessionPinned(id, pinned),
}))

import { pinnedSessions, toasts } from '../lib/stores'
import { togglePin } from './sessionActions'

beforeEach(() => {
  setSessionPinned.mockReset()
  pinnedSessions.set(['a'])
  toasts.set([])
})

describe('togglePin', () => {
  it('updates the registry before the write lands, appending a new pin', async () => {
    let release!: () => void
    setSessionPinned.mockReturnValue(new Promise<void>((r) => (release = r)))
    const done = togglePin('b', true)
    expect(get(pinnedSessions)).toEqual(['a', 'b'])
    release()
    await done
    expect(setSessionPinned).toHaveBeenCalledWith('b', true)
    expect(get(pinnedSessions)).toEqual(['a', 'b'])
  })

  it('removes an unpinned session', async () => {
    setSessionPinned.mockResolvedValue(undefined)
    await togglePin('a', false)
    expect(get(pinnedSessions)).toEqual([])
  })

  it('reverts and reports when the write fails', async () => {
    setSessionPinned.mockRejectedValue(new Error('boom'))
    await togglePin('b', true)
    expect(get(pinnedSessions)).toEqual(['a'])
    expect(get(toasts).map((t) => t.type)).toEqual(['error'])
  })
})
