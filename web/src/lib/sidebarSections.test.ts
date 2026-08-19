import { describe, it, expect } from 'vitest'
import { splitSections, swapWithinSection, parseSectionFold } from './sidebarSections'
import type { Session, SessionGroup } from './types'

const sess = (id: string): Session => ({ id }) as Session
const group = (id: string, session_ids: string[] = [], working_dir?: string): SessionGroup =>
  ({ id, name: id, session_ids, ...(working_dir ? { working_dir } : {}) }) as SessionGroup
const cron = (id: string, session_ids: string[] = [], working_dir?: string): SessionGroup =>
  ({ id, name: id, session_ids, task_id: 'task-' + id, ...(working_dir ? { working_dir } : {}) }) as SessionGroup

describe('splitSections', () => {
  it('sorts groups into projects and scheduled clusters', () => {
    const s = splitSections(
      [sess('a'), sess('b')],
      [group('p', ['a'], '/work/app'), cron('c', ['b'])],
      [], [],
    )
    expect(s.projects.map(g => g.group.id)).toEqual(['p'])
    expect(s.cronGroups.map(g => g.group.id)).toEqual(['c'])
  })

  // A cron cluster with a directory is still the scheduler's row: rendering it
  // as a project would offer rename / delete / new-session actions that belong
  // to the task, and that the server refuses.
  it('keeps a cron cluster out of Projects even when it has a working dir', () => {
    const s = splitSections([sess('a')], [cron('c', ['a'], '/work/app')], [], [])
    expect(s.projects).toEqual([])
    expect(s.cronGroups.map(g => g.group.id)).toEqual(['c'])
  })

  it('files a session with no group under ungrouped', () => {
    const s = splitSections([sess('a'), sess('b')], [cron('c', ['a'])], [], [])
    expect(s.cronGroups[0].items.map(x => x.id)).toEqual(['a'])
    expect(s.ungrouped.map(x => x.id)).toEqual(['b'])
  })

  // A plain group — no directory, no task — is the retired concept. The server
  // dissolves those at startup; until it has, its sessions must not be rendered
  // twice, and there is no row to render them under.
  it('drops a plain group entirely', () => {
    const s = splitSections([sess('a'), sess('b')], [group('leftover', ['a'])], [], [])
    expect(s.projects).toEqual([])
    expect(s.cronGroups).toEqual([])
    expect(s.ungrouped.map(x => x.id)).toEqual(['b'])
  })

  it('counts tasks by loose sessions and scheduled by runs', () => {
    const s = splitSections(
      [sess('a'), sess('b'), sess('c'), sess('d')],
      [cron('c1', ['a', 'b']), group('p', ['c'], '/work/app')],
      [], [],
    )
    expect(s.cronCount).toBe(2)
    expect(s.taskCount).toBe(1) // only 'd'
  })

  it('claims a pinned session once, ahead of its group', () => {
    const s = splitSections([sess('a')], [cron('c', ['a'])], ['a'], [])
    expect(s.pinned.map(x => x.id)).toEqual(['a'])
    expect(s.cronGroups[0].items).toEqual([])
    expect(s.ungrouped).toEqual([])
  })

  it('resolves a pin+collapse overlap in favour of the pin', () => {
    const s = splitSections([sess('a')], [], ['a'], ['a'])
    expect(s.pinned.map(x => x.id)).toEqual(['a'])
    expect(s.folded).toEqual([])
  })

  it('drops group members that no longer resolve to a session', () => {
    const s = splitSections([sess('a')], [cron('c', ['a', 'gone'])], [], [])
    expect(s.cronGroups[0].items.map(x => x.id)).toEqual(['a'])
  })

  it('renders a session claimed by two groups only once', () => {
    const s = splitSections([sess('a')], [cron('c1', ['a']), cron('c2', ['a'])], [], [])
    expect(s.cronGroups[0].items.map(x => x.id)).toEqual(['a'])
    expect(s.cronGroups[1].items).toEqual([])
  })

  it('collapses a duplicate id inside one group', () => {
    const s = splitSections([sess('a')], [cron('c', ['a', 'a'])], [], [])
    expect(s.cronGroups[0].items.map(x => x.id)).toEqual(['a'])
  })

  it('tolerates a group with no session_ids field', () => {
    const s = splitSections([sess('a')], [{ id: 'p', name: 'p', working_dir: '/w' } as SessionGroup], [], [])
    expect(s.projects[0].items).toEqual([])
    expect(s.ungrouped.map(x => x.id)).toEqual(['a'])
  })
})

describe('swapWithinSection', () => {
  // Registry order interleaves the sections, which is the case that made
  // section-relative reordering necessary: moving a project up must swap it
  // with the previous PROJECT, leaving the cron cluster between them alone.
  const registry = [
    group('p1', [], '/a'),
    cron('c1'),
    group('p2', [], '/b'),
    cron('c2'),
  ]
  const projectIds = ['p1', 'p2']
  const cronIds = ['c1', 'c2']

  it('swaps a project past an intervening cron cluster', () => {
    const next = swapWithinSection(registry, 'p2', -1, projectIds)
    expect(next?.map(g => g.id)).toEqual(['p2', 'c1', 'p1', 'c2'])
  })

  it('leaves the other section in the same relative order', () => {
    const next = swapWithinSection(registry, 'p2', -1, projectIds)!
    const crons = next.filter(g => !!g.task_id).map(g => g.id)
    expect(crons).toEqual(['c1', 'c2'])
  })

  it('refuses to move past either end of the section', () => {
    expect(swapWithinSection(registry, 'p1', -1, projectIds)).toBeNull()
    expect(swapWithinSection(registry, 'p2', 1, projectIds)).toBeNull()
    expect(swapWithinSection(registry, 'c1', -1, cronIds)).toBeNull()
    expect(swapWithinSection(registry, 'c2', 1, cronIds)).toBeNull()
  })

  it('refuses when the sibling has disappeared from the registry', () => {
    // Another client deleted p1 between render and click.
    const stale = registry.filter(g => g.id !== 'p1')
    expect(swapWithinSection(stale, 'p2', -1, projectIds)).toBeNull()
  })

  it('refuses for a group that is not in the section at all', () => {
    expect(swapWithinSection(registry, 'c1', -1, projectIds)).toBeNull()
  })

  it('does not mutate the registry it was given', () => {
    const before = registry.map(g => g.id)
    swapWithinSection(registry, 'p2', -1, projectIds)
    expect(registry.map(g => g.id)).toEqual(before)
  })
})

describe('parseSectionFold', () => {
  const allOpen = { tasks: true, scheduled: true, projects: true }

  it('defaults every section open with nothing stored', () => {
    expect(parseSectionFold(null)).toEqual(allOpen)
    expect(parseSectionFold('')).toEqual(allOpen)
  })

  it('round-trips a stored preference', () => {
    expect(parseSectionFold('{"tasks":false,"scheduled":false,"projects":true}'))
      .toEqual({ tasks: false, scheduled: false, projects: true })
  })

  it('treats a missing field as open', () => {
    expect(parseSectionFold('{"tasks":false}')).toEqual({ tasks: false, scheduled: true, projects: true })
  })

  it('falls back to open on corrupt or wrongly-shaped values', () => {
    expect(parseSectionFold('{')).toEqual(allOpen)
    expect(parseSectionFold('null')).toEqual(allOpen)
    expect(parseSectionFold('"nope"')).toEqual(allOpen)
    expect(parseSectionFold('[]')).toEqual(allOpen)
  })
})
