import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

const setSessionPinned = vi.fn()
const updateSession = vi.fn()
const deleteSessionApi = vi.fn()
const getSessionMessages = vi.fn()
vi.mock('../lib/api', () => ({
  getSessionMessages: (id: string) => getSessionMessages(id),
  setSessionPinned: (id: string, pinned: boolean) => setSessionPinned(id, pinned),
  updateSession: (id: string, patch: unknown) => updateSession(id, patch),
  deleteSession: (id: string) => deleteSessionApi(id),
}))

import { activeSessionId, pinnedSessions, sessions, toasts } from '../lib/stores'
import { togglePin, renameSession, deleteSession, copySessionMarkdown } from './sessionActions'

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

describe('copySessionMarkdown', () => {
  const writeText = vi.fn()
  beforeEach(() => {
    getSessionMessages.mockReset()
    writeText.mockReset()
    toasts.set([])
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  })

  it('copies the transcript as Markdown, without tool calls', async () => {
    getSessionMessages.mockResolvedValue({ events: [
      { type: 'history_user_message', content: 'hi' },
      { type: 'tool_call', name: 'terminal' },
      { type: 'tool_result', result: 'ok' },
      { type: 'assistant_message', content: 'hello' },
    ] })
    writeText.mockResolvedValue(undefined)
    await copySessionMarkdown('a', 'Greeting')
    expect(writeText).toHaveBeenCalledWith('# Greeting\n\n## You\n\nhi\n\n## Octo\n\nhello\n')
    expect(get(toasts)[0]?.type).toBe('success')
  })

  it('reports an empty session instead of copying', async () => {
    getSessionMessages.mockResolvedValue({ events: [] })
    await copySessionMarkdown('a', 'Empty')
    expect(writeText).not.toHaveBeenCalled()
    expect(get(toasts)[0]?.type).toBe('error')
  })

  it('falls back to writeText when the ClipboardItem write is refused', async () => {
    getSessionMessages.mockResolvedValue({ events: [{ type: 'history_user_message', content: 'hi' }] })
    const write = vi.fn().mockRejectedValue(new DOMException('denied', 'NotAllowedError'))
    writeText.mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { write, writeText }, configurable: true })
    vi.stubGlobal('ClipboardItem', class { constructor(public items: unknown) {} })
    try {
      await copySessionMarkdown('a', 'T')
    } finally {
      vi.unstubAllGlobals()
    }
    expect(write).toHaveBeenCalledOnce()
    expect(writeText).toHaveBeenCalledWith('# T\n\n## You\n\nhi\n')
    expect(get(toasts)[0]?.type).toBe('success')
  })

  it('reports a refused clipboard', async () => {
    getSessionMessages.mockResolvedValue({ events: [{ type: 'history_user_message', content: 'hi' }] })
    writeText.mockRejectedValue(new DOMException('denied', 'NotAllowedError'))
    await copySessionMarkdown('a', 'T')
    expect(get(toasts)[0]).toMatchObject({ type: 'error', msg: 'Copy failed' })
  })
})
