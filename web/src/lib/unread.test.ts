import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { sessions } from './stores'
import { isUnread, markSessionSeen, touchSession, reconcileSeen, sessionSeenAt, sessionTouchedAt } from './unread'

// jsdom under Node 26 exposes no localStorage (the built-in one needs
// --localstorage-file, and jsdom's own doesn't get installed over it), so the
// persistence path needs a stand-in. Installed after the import above, which
// means the module's initial load() already ran against nothing — that's the
// private-mode path, and it degrades to in-memory exactly as intended.
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

const T0 = Date.parse('2026-08-20T10:00:00Z')

function sess(id: string, updatedAt: number, status = 'idle') {
  return { id, name: id, title: id, updated_at: new Date(updatedAt).toISOString(), status } as any
}

beforeEach(() => {
  localStorage.clear()
  sessionSeenAt.set({})
  sessionTouchedAt.set({})
  sessions.set([])
})

describe('reconcileSeen', () => {
  it('baselines a session it has never seen, so a first visit shows no dots', () => {
    const s = sess('a', T0)
    reconcileSeen([s])
    expect(get(sessionSeenAt).a).toBe(T0)
    expect(isUnread(s, get(sessionSeenAt), {})).toBe(false)
  })

  it('keeps an existing mark instead of re-baselining over a pending dot', () => {
    sessionSeenAt.set({ a: T0 })
    reconcileSeen([sess('a', T0 + 60_000)])
    expect(get(sessionSeenAt).a).toBe(T0)
  })

  it('prunes sessions that no longer exist', () => {
    sessionSeenAt.set({ a: T0, gone: T0 })
    reconcileSeen([sess('a', T0)])
    expect(get(sessionSeenAt)).toEqual({ a: T0 })
  })

  it('ignores an empty list rather than wiping every mark', () => {
    sessionSeenAt.set({ a: T0 })
    reconcileSeen([])
    expect(get(sessionSeenAt)).toEqual({ a: T0 })
  })

  // The WS handshake's brief list has created_at and no updated_at. Baselining
  // it at 0 would light up every row the moment the REST list arrived with real
  // timestamps.
  it('does not baseline a session the list gives no updated_at for', () => {
    reconcileSeen([{ id: 'a', status: 'idle' } as any])
    expect(get(sessionSeenAt)).toEqual({})
  })

  it('keeps an existing mark through a list with no updated_at', () => {
    sessionSeenAt.set({ a: T0 })
    reconcileSeen([{ id: 'a', status: 'idle' } as any])
    expect(get(sessionSeenAt)).toEqual({ a: T0 })
  })
})

describe('isUnread', () => {
  it('marks a session whose server updated_at moved past the seen mark', () => {
    expect(isUnread(sess('a', T0 + 60_000), { a: T0 }, {})).toBe(true)
  })

  it('leaves an unchanged session alone', () => {
    expect(isUnread(sess('a', T0), { a: T0 }, {})).toBe(false)
  })

  it('never marks a running session — the spinner owns that row', () => {
    expect(isUnread(sess('a', T0 + 60_000, 'running'), { a: T0 }, {})).toBe(false)
  })

  it('says nothing about a session with no seen mark yet', () => {
    expect(isUnread(sess('a', T0 + 60_000), {}, {})).toBe(false)
  })

  it('says nothing about a session the list gives no updated_at for', () => {
    expect(isUnread({ id: 'a', status: 'idle' } as any, { a: T0 }, {})).toBe(false)
  })

  it('marks on a locally observed turn ending, with updated_at still stale', () => {
    // The open-tab case: no broadcast refreshes updated_at, so turn_ended's
    // local stamp is the only evidence the session changed.
    expect(isUnread(sess('a', T0), { a: T0 }, { a: T0 + 60_000 })).toBe(true)
  })
})

describe('markSessionSeen', () => {
  it('clears a dot raised by a locally observed turn ending', () => {
    const s = sess('a', T0)
    sessions.set([s])
    sessionSeenAt.set({ a: T0 })
    touchSession('a')
    expect(isUnread(s, get(sessionSeenAt), get(sessionTouchedAt))).toBe(true)
    markSessionSeen('a')
    expect(isUnread(s, get(sessionSeenAt), get(sessionTouchedAt))).toBe(false)
  })

  it('clears a dot whose updated_at is ahead of the local clock', () => {
    const future = Date.now() + 60 * 60_000
    const s = sess('a', future)
    sessions.set([s])
    sessionSeenAt.set({ a: T0 })
    markSessionSeen('a')
    expect(isUnread(s, get(sessionSeenAt), get(sessionTouchedAt))).toBe(false)
  })

  it('never moves a mark backwards', () => {
    const far = Date.now() + 60 * 60_000
    sessionSeenAt.set({ a: far })
    markSessionSeen('a')
    expect(get(sessionSeenAt).a).toBe(far)
  })
})

describe('persistence', () => {
  it('writes marks to localStorage so a reload keeps its dots', () => {
    sessionSeenAt.set({ a: T0 })
    expect(JSON.parse(localStorage.getItem('octo.session-seen')!)).toEqual({ a: T0 })
  })
})
