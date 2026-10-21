import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

// Capture the handlers wireMobileSession registers so a test can fire server
// events by hand, and record what goes out.
const handlers: Record<string, (ev: any) => void> = {}
const sendMessage = vi.fn()
vi.mock('../lib/ws', () => ({
  ws: {
    on: (type: string, fn: (ev: any) => void) => { handlers[type] = fn; return () => delete handlers[type] },
    sendMessage: (...args: unknown[]) => sendMessage(...args),
  },
}))

import { chatContextUsage, chatMessages, chatPermMode, chatStreaming, toasts } from '../lib/stores'
import { echoRefs, insertConfirmedSteer, mobileSteers, sendMobile, wireMobileSession } from './chatWiring'

describe('echoRefs', () => {
  it('maps images to their data URL and other files to a pdf: chip ref', () => {
    expect(echoRefs([
      { name: 'shot.jpg', mime_type: 'image/jpeg', data_url: 'data:image/jpeg;base64,AAAA' },
      { name: 'report.pdf', mime_type: 'application/pdf', path: '/api/uploads/1_report.pdf' },
    ])).toEqual(['data:image/jpeg;base64,AAAA', 'pdf:report.pdf'])
  })

  it('is empty without files', () => {
    expect(echoRefs()).toEqual([])
    expect(echoRefs([])).toEqual([])
  })
})

describe('insertConfirmedSteer', () => {
  it('goes ahead of the reply still streaming', () => {
    const msgs = [{ type: 'user', id: 'u1' }, { type: 'assistant', id: 'a1', streaming: true }]
    expect(insertConfirmedSteer(msgs, { type: 'user', id: 's' }).map(m => m.id)).toEqual(['u1', 's', 'a1'])
  })

  it('appends when nothing is streaming', () => {
    const msgs = [{ type: 'user', id: 'u1' }, { type: 'assistant', id: 'a1', streaming: false }]
    expect(insertConfirmedSteer(msgs, { type: 'user', id: 's' }).map(m => m.id)).toEqual(['u1', 'a1', 's'])
  })
})

describe('sending while a turn runs', () => {
  const sid = 's1'
  let unwire: () => void

  beforeEach(() => {
    unwire?.()
    sendMessage.mockReset()
    toasts.set([])
    mobileSteers.set({})
    chatMessages.set({ [sid]: [
      { id: 'u1', type: 'user', content: 'start', createdAt: 1, streaming: false, pending: false, tools: [], todos: [], images: [] },
      { id: 'a1', type: 'assistant', content: 'working on it', createdAt: 2, streaming: true, tools: [], todos: [] },
    ] })
    chatStreaming.set({ [sid]: true })
    unwire = wireMobileSession(sid)
  })

  const ids = () => (get(chatMessages)[sid] ?? []).map((m: any) => `${m.type}:${m.content}`)

  it('keeps the message out of the transcript until the turn picks it up', () => {
    sendMobile(sid, 'also check the docs')
    expect(sendMessage).toHaveBeenCalledWith(sid, 'also check the docs', undefined)
    expect(get(mobileSteers)[sid]?.map(s => s.content)).toEqual(['also check the docs'])
    expect(ids()).toEqual(['user:start', 'assistant:working on it'])

    handlers.history_user_message({ session_id: sid, content: 'also check the docs', created_at: 3 })
    expect(get(mobileSteers)[sid]).toEqual([])
    expect(ids()).toEqual(['user:start', 'user:also check the docs', 'assistant:working on it'])
  })

  it('drops the steer on rejection and leaves the turn running', () => {
    sendMobile(sid, 'nope')
    handlers.send_rejected({ session_id: sid, message: 'busy' })
    expect(get(mobileSteers)[sid]).toEqual([])
    expect(get(chatStreaming)[sid]).toBe(true)
    expect(ids()).toEqual(['user:start', 'assistant:working on it'])
    expect(get(toasts)[0]?.msg).toBe('busy')
  })

  it('matches a steer whose echo carries document notes', () => {
    sendMobile(sid, 'see attached', [{ name: 'spec.pdf', path: '/api/uploads/1_spec.pdf' }])
    handlers.history_user_message({ session_id: sid, content: 'see attached\n\n[Attached file: /home/me/.octo/uploads/1_spec.pdf]', created_at: 3 })
    expect(get(mobileSteers)[sid]).toEqual([])
    expect(ids()[1]).toMatch(/^user:see attached/)
    expect(ids()[2]).toBe('assistant:working on it')
  })

  it('drops steers the turn ended without picking up', () => {
    sendMobile(sid, 'late')
    handlers.complete({ session_id: sid })
    expect(get(mobileSteers)[sid]).toEqual([])
  })

  it('follows a permission-mode change from another client, ignoring partial pings', () => {
    chatPermMode.set({})
    handlers.session_update({ session_id: sid, permission_mode: 'strict' })
    handlers.session_update({ session_id: sid, status: 'running' })
    expect(get(chatPermMode)[sid]).toBe('strict')
  })

  it('tracks context usage from session_update', () => {
    chatContextUsage.set({})
    handlers.session_update({ session_id: sid, context_usage: 63 })
    handlers.session_update({ session_id: sid, status: 'running' })
    expect(get(chatContextUsage)[sid]).toBe(63)
  })

  it('shifts message indices after a mid-turn compaction', () => {
    chatMessages.update(m => ({ ...m, [sid]: [
      { id: 'u0', type: 'user', content: 'old', messageIndex: 0 },
      { id: 'u5', type: 'user', content: 'recent', messageIndex: 5 },
    ] as any }))
    handlers.history_reindex({ session_id: sid, folded_msgs: 3 })
    const msgs = get(chatMessages)[sid] as any[]
    expect(msgs[0].messageIndex).toBeUndefined()
    expect(msgs[1].messageIndex).toBe(3)
  })

  it('still echoes a send made while idle as a pending bubble', () => {
    chatStreaming.set({ [sid]: false })
    sendMobile(sid, 'next')
    expect(get(mobileSteers)[sid] ?? []).toEqual([])
    expect(ids()).toEqual(['user:start', 'assistant:working on it', 'user:next'])
  })
})
