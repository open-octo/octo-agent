import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

const setSessionPinned = vi.fn()
const updateSession = vi.fn()
const deleteSessionApi = vi.fn()
vi.mock('../lib/api', () => ({
  setSessionPinned: (id: string, pinned: boolean) => setSessionPinned(id, pinned),
  updateSession: (id: string, patch: unknown) => updateSession(id, patch),
  deleteSession: (id: string) => deleteSessionApi(id),
}))

import { activeSessionId, pinnedSessions, sessions, toasts } from '../lib/stores'
import { togglePin, renameSession, deleteSession } from './sessionActions'

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

describe('renameSession / deleteSession', () => {
  beforeEach(() => {
    updateSession.mockReset()
    deleteSessionApi.mockReset()
    toasts.set([])
    sessions.set([{ id: 'a', name: 'old', title: 'old' }, { id: 'b', name: 'other' }] as any)
    activeSessionId.set('a')
  })

  it('renames on the server, then in the store', async () => {
    updateSession.mockResolvedValue({})
    await renameSession('a', '  new name  ')
    expect(updateSession).toHaveBeenCalledWith('a', { name: 'new name' })
    expect(get(sessions).find((s: any) => s.id === 'a')).toMatchObject({ name: 'new name', title: 'new name' })
  })

  it('ignores a blank name', async () => {
    await renameSession('a', '   ')
    expect(updateSession).not.toHaveBeenCalled()
  })

  it('keeps the old name and reports when the rename fails', async () => {
    updateSession.mockRejectedValue(new Error('nope'))
    await renameSession('a', 'new')
    expect(get(sessions).find((s: any) => s.id === 'a')).toMatchObject({ name: 'old' })
    expect(get(toasts)[0]?.msg).toBe('nope')
  })

  it('deletes and drops the session, clearing it as the active one', async () => {
    deleteSessionApi.mockResolvedValue(undefined)
    await deleteSession('a')
    expect(get(sessions).map((s: any) => s.id)).toEqual(['b'])
    expect(get(activeSessionId)).toBeNull()
  })

  it('keeps the session when the delete fails', async () => {
    deleteSessionApi.mockRejectedValue(new Error('locked'))
    await deleteSession('a')
    expect(get(sessions).map((s: any) => s.id)).toEqual(['a', 'b'])
    expect(get(toasts)[0]?.msg).toBe('locked')
  })
})
