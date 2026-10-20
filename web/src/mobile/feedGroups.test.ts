import { describe, it, expect, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import { sessions, sessionGroups } from '../lib/stores'
import { feedGroups, allSessions, matchesQuery, projectOf } from './feedGroups'
import type { Session, SessionGroup } from '../lib/types'

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

describe('feedGroups projects', () => {
  beforeEach(() => {
    sessions.set([])
    sessionGroups.set([])
  })

  const proj = (id: string, ids: string[], extra: Partial<SessionGroup> = {}): SessionGroup =>
    ({ id, name: `Project ${id}`, session_ids: ids, working_dir: `/w/${id}`, ...extra })

  it('lists projects in registry order and keeps their done sessions out of Recent', () => {
    sessions.set([mk('a', 1), mk('b', 2), mk('c', 3), mk('d', 4)])
    sessionGroups.set([proj('p2', ['c']), proj('p1', ['a', 'b'])])
    const g = get(feedGroups)
    expect(g.projects.map((p) => p.group.id)).toEqual(['p2', 'p1'])
    expect(g.projects[1].items.map((i) => i.session.id)).toEqual(['a', 'b'])
    expect(g.recent.map((i) => i.session.id)).toEqual(['d'])
    expect(g.recentTotal).toBe(1)
  })

  it('keeps a project session that needs you in To-do and Active too', () => {
    sessions.set([
      mk('ask', 1, { pending_question: true } as Partial<Session>),
      mk('run', 2, { status: 'running' } as Partial<Session>),
    ])
    sessionGroups.set([proj('p', ['ask', 'run'])])
    const g = get(feedGroups)
    expect(g.todo.map((i) => i.session.id)).toEqual(['ask'])
    expect(g.active.map((i) => i.session.id)).toEqual(['run'])
    expect(g.projects[0].items.map((i) => i.session.id)).toEqual(['ask', 'run'])
  })

  it('treats a group without a working dir as no project', () => {
    sessions.set([mk('a', 1)])
    sessionGroups.set([{ id: 'plain', name: 'Plain', session_ids: ['a'] }])
    const g = get(feedGroups)
    expect(g.projects).toEqual([])
    expect(get(projectOf).has('a')).toBe(false)
  })

  it('maps a session to its project name', () => {
    sessionGroups.set([proj('p', ['a'])])
    expect(get(projectOf).get('a')).toBe('Project p')
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
