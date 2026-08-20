import { describe, it, expect } from 'vitest'
import { splitSections, swapWithinSection, parseSectionFold } from './sidebarSections'
import type { Session, SessionGroup } from './types'

const sess = (id: string): Session => ({ id }) as Session
const runs = (id: string): Session => ({ id, status: 'running' }) as Session
const group = (id: string, session_ids: string[] = [], working_dir?: string): SessionGroup =>
  ({ id, name: id, session_ids, ...(working_dir ? { working_dir } : {}) }) as SessionGroup
// A scheduled task's project: the scheduler gives it a directory named after the
// task, so it is a project like any other and needs no section of its own.
const cron = (id: string, session_ids: string[] = [], working_dir = '/Octo/' + id): SessionGroup =>
  ({ id, name: id, session_ids, task_id: 'task-' + id, working_dir }) as SessionGroup

describe('splitSections', () => {
  it('files every group with a working dir under projects', () => {
    const s = splitSections(
      [sess('a'), sess('b')],
      [group('p', ['a'], '/work/app'), cron('c', ['b'])],
      [], [],
    )
    expect(s.projects.map(g => g.group.id)).toEqual(['p', 'c'])
  })

  it('files a session with no group under ungrouped', () => {
    const s = splitSections([sess('a'), sess('b')], [cron('c', ['a'])], [], [])
    expect(s.projects[0].items.map(x => x.id)).toEqual(['a'])
    expect(s.ungrouped.map(x => x.id)).toEqual(['b'])
  })

  // A plain group — no directory, no task — is the retired concept. The server
  // dissolves those at startup; until it has, its sessions must not be rendered
  // twice, and there is no row to render them under.
  it('drops a plain group entirely', () => {
    const s = splitSections([sess('a'), sess('b')], [group('leftover', ['a'])], [], [])
    expect(s.projects).toEqual([])
    expect(s.ungrouped.map(x => x.id)).toEqual(['b'])
  })

  it('counts tasks by loose sessions only', () => {
    const s = splitSections(
      [sess('a'), sess('b'), sess('c'), sess('d')],
      [cron('c1', ['a', 'b']), group('p', ['c'], '/work/app')],
      [], [],
    )
    expect(s.taskCount).toBe(1) // only 'd'
  })

  it('claims a pinned session once, ahead of its group', () => {
    const s = splitSections([sess('a')], [cron('c', ['a'])], ['a'], [])
    expect(s.pinned.map(x => x.id)).toEqual(['a'])
    expect(s.projects[0].items).toEqual([])
    expect(s.ungrouped).toEqual([])
  })

  // The collapsed-project running badge counts gv.items, so precedence decides
  // its number: a running session the Pinned section already claimed must not be
  // counted again under the project, where it also has a row of its own that is
  // always visible.
  it('leaves a running pinned session out of its project items', () => {
    const s = splitSections([runs('a'), runs('b')], [cron('c', ['a', 'b'])], ['a'], [])
    expect(s.pinned.map(x => x.id)).toEqual(['a'])
    expect(s.projects[0].items.map(x => x.id)).toEqual(['b'])
  })

  it('resolves a pin+collapse overlap in favour of the pin', () => {
    const s = splitSections([sess('a')], [], ['a'], ['a'])
    expect(s.pinned.map(x => x.id)).toEqual(['a'])
    expect(s.folded).toEqual([])
  })

  it('drops group members that no longer resolve to a session', () => {
    const s = splitSections([sess('a')], [cron('c', ['a', 'gone'])], [], [])
    expect(s.projects[0].items.map(x => x.id)).toEqual(['a'])
  })

  it('renders a session claimed by two groups only once', () => {
    const s = splitSections([sess('a')], [cron('c1', ['a']), cron('c2', ['a'])], [], [])
    expect(s.projects[0].items.map(x => x.id)).toEqual(['a'])
    expect(s.projects[1].items).toEqual([])
  })

  it('collapses a duplicate id inside one group', () => {
    const s = splitSections([sess('a')], [cron('c', ['a', 'a'])], [], [])
    expect(s.projects[0].items.map(x => x.id)).toEqual(['a'])
  })

  it('tolerates a group with no session_ids field', () => {
    const s = splitSections([sess('a')], [{ id: 'p', name: 'p', working_dir: '/w' } as SessionGroup], [], [])
    expect(s.projects[0].items).toEqual([])
    expect(s.ungrouped.map(x => x.id)).toEqual(['a'])
  })
})

describe('swapWithinSection', () => {
  // The registry can hold rows the sidebar does not render — a plain group left
  // by an older version, until the server's startup pass dissolves it. Reorder
  // has to move a project past those rather than swapping with whatever is
  // adjacent in the registry, which would make the row vanish from under the
  // cursor.
  const registry = [
    group('p1', [], '/a'),
    group('leftover1'),
    group('p2', [], '/b'),
    group('leftover2'),
  ]
  const projectIds = ['p1', 'p2']

  it('swaps a project past an unrendered row', () => {
    const next = swapWithinSection(registry, 'p2', -1, projectIds)
    expect(next?.map(g => g.id)).toEqual(['p2', 'leftover1', 'p1', 'leftover2'])
  })

  it('leaves the unrendered rows in the same relative order', () => {
    const next = swapWithinSection(registry, 'p2', -1, projectIds)!
    const rest = next.filter(g => !g.working_dir).map(g => g.id)
    expect(rest).toEqual(['leftover1', 'leftover2'])
  })

  it('refuses to move past either end of the section', () => {
    expect(swapWithinSection(registry, 'p1', -1, projectIds)).toBeNull()
    expect(swapWithinSection(registry, 'p2', 1, projectIds)).toBeNull()
  })

  it('refuses when the sibling has disappeared from the registry', () => {
    // Another client deleted p1 between render and click.
    const stale = registry.filter(g => g.id !== 'p1')
    expect(swapWithinSection(stale, 'p2', -1, projectIds)).toBeNull()
  })

  it('refuses for a group that is not in the section at all', () => {
    expect(swapWithinSection(registry, 'leftover1', -1, projectIds)).toBeNull()
  })

  it('does not mutate the registry it was given', () => {
    const before = registry.map(g => g.id)
    swapWithinSection(registry, 'p2', -1, projectIds)
    expect(registry.map(g => g.id)).toEqual(before)
  })
})

describe('parseSectionFold', () => {
  const allOpen = { tasks: true, projects: true }

  it('defaults every section open with nothing stored', () => {
    expect(parseSectionFold(null)).toEqual(allOpen)
    expect(parseSectionFold('')).toEqual(allOpen)
  })

  it('round-trips a stored preference', () => {
    expect(parseSectionFold('{"tasks":false,"projects":true}'))
      .toEqual({ tasks: false, projects: true })
  })

  it('treats a missing field as open', () => {
    expect(parseSectionFold('{"tasks":false}')).toEqual({ tasks: false, projects: true })
  })

  it('falls back to open on corrupt or wrongly-shaped values', () => {
    expect(parseSectionFold('{')).toEqual(allOpen)
    expect(parseSectionFold('null')).toEqual(allOpen)
    expect(parseSectionFold('"nope"')).toEqual(allOpen)
    expect(parseSectionFold('[]')).toEqual(allOpen)
  })
})
