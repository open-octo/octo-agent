import { describe, it, expect } from 'vitest'
import { splitSections, swapWithinSection, parseSectionFold } from './sidebarSections'
import type { Session, SessionGroup } from './types'

const sess = (id: string): Session => ({ id }) as Session
const group = (id: string, session_ids: string[] = [], working_dir?: string): SessionGroup =>
  ({ id, name: id, session_ids, ...(working_dir ? { working_dir } : {}) }) as SessionGroup

describe('splitSections', () => {
  it('sorts groups into projects and tasks by working dir', () => {
    const s = splitSections(
      [sess('a'), sess('b')],
      [group('p', ['a'], '/work/app'), group('t', ['b'])],
      [], [],
    )
    expect(s.projects.map(g => g.group.id)).toEqual(['p'])
    expect(s.taskGroups.map(g => g.group.id)).toEqual(['t'])
  })

  it('files a session with no group under ungrouped', () => {
    const s = splitSections([sess('a'), sess('b')], [group('t', ['a'])], [], [])
    expect(s.taskGroups[0].items.map(x => x.id)).toEqual(['a'])
    expect(s.ungrouped.map(x => x.id)).toEqual(['b'])
  })

  // The regression this module was extracted for: an empty group has no
  // sessions, so a session count says the section is empty — but the group is
  // still a row the user has to see to rename, configure, or delete it. A
  // freshly created group is always empty and opens its inline rename box.
  it('reports the Tasks section as non-empty for an empty group', () => {
    const s = splitSections([], [group('fresh')], [], [])
    expect(s.taskCount).toBe(0)
    expect(s.hasTasks).toBe(true)
  })

  it('reports the Tasks section as empty only when there is nothing at all', () => {
    expect(splitSections([], [], [], []).hasTasks).toBe(false)
    // Every session lives in a project, and no plain group exists: Tasks really
    // has nothing to show.
    const s = splitSections([sess('a')], [group('p', ['a'], '/work/app')], [], [])
    expect(s.hasTasks).toBe(false)
    expect(s.taskCount).toBe(0)
  })

  it('counts sessions across task groups and ungrouped', () => {
    const s = splitSections(
      [sess('a'), sess('b'), sess('c'), sess('d')],
      [group('t1', ['a', 'b']), group('t2', ['c']), group('p', [], '/work/app')],
      [], [],
    )
    expect(s.taskCount).toBe(4)
  })

  it('claims a pinned session once, ahead of its group', () => {
    const s = splitSections([sess('a')], [group('t', ['a'])], ['a'], [])
    expect(s.pinned.map(x => x.id)).toEqual(['a'])
    expect(s.taskGroups[0].items).toEqual([])
    expect(s.ungrouped).toEqual([])
  })

  it('resolves a pin+collapse overlap in favour of the pin', () => {
    const s = splitSections([sess('a')], [], ['a'], ['a'])
    expect(s.pinned.map(x => x.id)).toEqual(['a'])
    expect(s.folded).toEqual([])
  })

  it('drops group members that no longer resolve to a session', () => {
    const s = splitSections([sess('a')], [group('t', ['a', 'gone'])], [], [])
    expect(s.taskGroups[0].items.map(x => x.id)).toEqual(['a'])
  })

  it('renders a session claimed by two groups only once', () => {
    const s = splitSections([sess('a')], [group('t1', ['a']), group('t2', ['a'])], [], [])
    expect(s.taskGroups[0].items.map(x => x.id)).toEqual(['a'])
    expect(s.taskGroups[1].items).toEqual([])
  })

  it('collapses a duplicate id inside one group', () => {
    const s = splitSections([sess('a')], [group('t', ['a', 'a'])], [], [])
    expect(s.taskGroups[0].items.map(x => x.id)).toEqual(['a'])
  })

  it('tolerates a group with no session_ids field', () => {
    const s = splitSections([sess('a')], [{ id: 't', name: 't' } as SessionGroup], [], [])
    expect(s.taskGroups[0].items).toEqual([])
    expect(s.ungrouped.map(x => x.id)).toEqual(['a'])
  })
})

describe('swapWithinSection', () => {
  // Registry order interleaves the sections, which is the case that made
  // section-relative reordering necessary: moving a project up must swap it
  // with the previous PROJECT, leaving the task group between them alone.
  const registry = [
    group('p1', [], '/a'),
    group('t1'),
    group('p2', [], '/b'),
    group('t2'),
  ]
  const projectIds = ['p1', 'p2']
  const taskIds = ['t1', 't2']

  it('swaps a project past an intervening task group', () => {
    const next = swapWithinSection(registry, 'p2', -1, projectIds)
    expect(next?.map(g => g.id)).toEqual(['p2', 't1', 'p1', 't2'])
  })

  it('leaves the other section in the same relative order', () => {
    const next = swapWithinSection(registry, 'p2', -1, projectIds)!
    const tasks = next.filter(g => !g.working_dir).map(g => g.id)
    expect(tasks).toEqual(['t1', 't2'])
  })

  it('moves a task group down within its own section', () => {
    const next = swapWithinSection(registry, 't1', 1, taskIds)
    expect(next?.map(g => g.id)).toEqual(['p1', 't2', 'p2', 't1'])
  })

  it('refuses to move past either end of the section', () => {
    expect(swapWithinSection(registry, 'p1', -1, projectIds)).toBeNull()
    expect(swapWithinSection(registry, 'p2', 1, projectIds)).toBeNull()
    expect(swapWithinSection(registry, 't1', -1, taskIds)).toBeNull()
    expect(swapWithinSection(registry, 't2', 1, taskIds)).toBeNull()
  })

  it('refuses when the sibling has disappeared from the registry', () => {
    // Another client deleted p1 between render and click.
    const stale = registry.filter(g => g.id !== 'p1')
    expect(swapWithinSection(stale, 'p2', -1, projectIds)).toBeNull()
  })

  it('refuses for a group that is not in the section at all', () => {
    expect(swapWithinSection(registry, 't1', -1, projectIds)).toBeNull()
  })

  it('does not mutate the registry it was given', () => {
    const before = registry.map(g => g.id)
    swapWithinSection(registry, 'p2', -1, projectIds)
    expect(registry.map(g => g.id)).toEqual(before)
  })
})

describe('parseSectionFold', () => {
  it('defaults both sections open with nothing stored', () => {
    expect(parseSectionFold(null)).toEqual({ tasks: true, projects: true })
    expect(parseSectionFold('')).toEqual({ tasks: true, projects: true })
  })

  it('round-trips a stored preference', () => {
    expect(parseSectionFold('{"tasks":false,"projects":true}')).toEqual({ tasks: false, projects: true })
  })

  it('treats a missing field as open', () => {
    expect(parseSectionFold('{"tasks":false}')).toEqual({ tasks: false, projects: true })
  })

  it('falls back to open on corrupt or wrongly-shaped values', () => {
    expect(parseSectionFold('{')).toEqual({ tasks: true, projects: true })
    expect(parseSectionFold('null')).toEqual({ tasks: true, projects: true })
    expect(parseSectionFold('"nope"')).toEqual({ tasks: true, projects: true })
    expect(parseSectionFold('[]')).toEqual({ tasks: true, projects: true })
  })
})
