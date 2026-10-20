import { describe, it, expect, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import { sessions } from '../lib/stores'
import { feedGroups, allSessions, matchesQuery } from './feedGroups'
import type { Session } from '../lib/types'

const mk = (id: string, minutesAgo: number, extra: Partial<Session> = {}): Session =>
  ({
    id,
    title: `Session ${id}`,
    updated_at: new Date(Date.now() - minutesAgo * 60_000).toISOString(),
    ...extra,
  }) as Session

describe('feedGroups', () => {
  beforeEach(() => sessions.set([]))

  it('caps Recent and reports how many there are in total', () => {
    sessions.set(Array.from({ length: 12 }, (_, i) => mk(String(i), i)))
    const g = get(feedGroups)
    expect(g.recent).toHaveLength(8)
    expect(g.recentTotal).toBe(12)
    expect(g.recent[0].session.id).toBe('0')
  })

  it('does not count to-do or running sessions as Recent', () => {
    sessions.set([
      mk('a', 1, { pending_question: true } as Partial<Session>),
      mk('b', 2, { status: 'running' } as Partial<Session>),
      mk('c', 3),
    ])
    const g = get(feedGroups)
    expect(g.todo.map((i) => i.session.id)).toEqual(['a'])
    expect(g.active.map((i) => i.session.id)).toEqual(['b'])
    expect(g.recentTotal).toBe(1)
  })
})

describe('allSessions', () => {
  beforeEach(() => sessions.set([]))

  it('lists every session, pinned first then newest, with its kind', () => {
    sessions.set([
      mk('old', 30),
      mk('new', 1),
      mk('pinned', 60, { pinned: true } as Partial<Session>),
      mk('ask', 5, { pending_confirmation: true } as Partial<Session>),
    ])
    const all = get(allSessions)
    expect(all.map((i) => i.session.id)).toEqual(['pinned', 'new', 'ask', 'old'])
    expect(all.find((i) => i.session.id === 'ask')?.kind).toBe('approval')
  })
})

describe('matchesQuery', () => {
  it('matches a case-insensitive title substring', () => {
    expect(matchesQuery(mk('1', 0, { title: 'Spring Festival trip' }), 'festival')).toBe(true)
    expect(matchesQuery(mk('1', 0, { title: '春节北京家庭游' }), '北京')).toBe(true)
    expect(matchesQuery(mk('1', 0, { title: 'Spring' }), 'autumn')).toBe(false)
  })

  it('falls back to the name and matches everything on an empty query', () => {
    expect(matchesQuery(mk('1', 0, { title: '', name: 'scratch' }), 'SCR')).toBe(true)
    expect(matchesQuery(mk('1', 0), '   ')).toBe(true)
  })
})
