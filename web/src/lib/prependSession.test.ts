import { describe, it, expect } from 'vitest'
import { get } from 'svelte/store'
import { sessions, prependSession } from './stores'
import type { Session } from './types'

const sess = (id: string, extra: Partial<Session> = {}): Session => ({ id, title: id, ...extra } as Session)

describe('prependSession', () => {
  it('puts a brand new session at the front', () => {
    sessions.set([sess('a'), sess('b')])
    prependSession(sess('new'))
    expect(get(sessions).map(s => s.id)).toEqual(['new', 'a', 'b'])
  })

  // The 'session_created' broadcast's own full sessions.set() refresh can
  // land before this call and already include the new session — without
  // dedup, prepending again would leave the id twice in the list and crash
  // Sidebar's keyed {#each ... (s.id)} with each_key_duplicate.
  it('replaces an existing entry instead of duplicating the id', () => {
    sessions.set([sess('a'), sess('new', { title: 'stale' }), sess('b')])
    prependSession(sess('new', { title: 'fresh' }))
    const list = get(sessions)
    expect(list.map(s => s.id)).toEqual(['new', 'a', 'b'])
    expect(list.filter(s => s.id === 'new')).toHaveLength(1)
    expect(list[0].title).toBe('fresh')
  })
})
