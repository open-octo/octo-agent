// Pure logic behind the sidebar's session list: which section each entry
// belongs to, and how reordering inside a section maps onto the flat group
// registry. Extracted from Sidebar.svelte so both can be tested — the
// partition in particular decides whether a row is rendered at all, and a
// group that exists but never renders is unreachable rather than merely
// misplaced.
//
// Two sections: Tasks (sessions in no project) and Projects (a directory plus
// the sessions working in it). A scheduled task's runs are a project too — the
// scheduler creates one per task, named after it — so they need no section of
// their own. A group with no working_dir is a plain group, a concept that no
// longer exists; the server dissolves those at startup, and anything left in a
// registry this frontend is handed is dropped rather than rendered as a third
// kind of row.

import type { Session, SessionGroup } from './types'

export interface GroupView {
  group: SessionGroup
  items: Session[]
}

export interface SidebarSections {
  /** Pinned sessions, in registry order, above every section. */
  pinned: Session[]
  /** Groups carrying a working directory — projects, scheduled ones included. */
  projects: GroupView[]
  /** Sessions in no group at all. Tasks by definition. */
  ungrouped: Session[]
  /** Collapsed sessions, in registry order, in the folded panel at the bottom. */
  folded: Session[]
  /** How many loose sessions the Tasks section holds. */
  taskCount: number
}

/**
 * Split the session list into the sections the sidebar renders.
 *
 * Each session is claimed exactly once, in precedence order: pinned, then
 * collapsed, then group membership, then whatever is left. Claiming is what
 * keeps a stale registry overlap (the same id pinned AND in a group) from
 * rendering one session twice, which throws on Svelte's keyed `{#each}`.
 * Pinned claims first so a pin+collapse overlap — reachable only through a
 * cross-process last-writer-wins race — resolves the way the server does.
 *
 * Group member ids that no longer resolve to a live session are dropped, so a
 * deleted session leaves no ghost row, and duplicate ids within one group's
 * membership are collapsed for the same keyed-each reason.
 */
export function splitSections(
  sessions: Session[],
  groups: SessionGroup[],
  pinnedIds: string[],
  collapsedIds: string[],
): SidebarSections {
  const byId = new Map(sessions.map(s => [s.id, s] as const))
  const claimed = new Set<string>()

  const take = (ids: string[]): Session[] => {
    const out: Session[] = []
    for (const id of [...new Set(ids)]) {
      const s = byId.get(id)
      if (!s || claimed.has(s.id)) continue
      claimed.add(s.id)
      out.push(s)
    }
    return out
  }

  const pinned = take(pinnedIds)
  const folded = take(collapsedIds)
  const all = groups.map(group => ({ group, items: take(group.session_ids ?? []) }))

  const projects = all.filter(gv => !!gv.group.working_dir)
  // Route through byId (already deduped) rather than the input array, for the
  // same keyed-each reason as a group's own membership. A session in a dissolved
  // plain group was claimed by it above and so does NOT resurface here — that
  // group is gone from the registry the moment the server has run, and until
  // then showing its sessions twice would be worse than showing them nowhere.
  const ungrouped = [...byId.values()].filter(s => !claimed.has(s.id))

  return {
    pinned,
    projects,
    ungrouped,
    folded,
    taskCount: ungrouped.length,
  }
}

/**
 * Move one group up or down WITHIN ITS OWN SECTION, returning the full new
 * registry order to persist, or null when the move isn't possible (either end
 * of the section, or a sibling that has since disappeared).
 *
 * `siblings` is the id list of the section the group renders in, so a project
 * swaps with the previous project rather than with whichever group happens to be
 * adjacent in the registry, which would make the row vanish from under the
 * cursor into the other section.
 * Only the two groups exchange places in the registry; every other group keeps
 * its position, so the other section's order is untouched.
 */
export function swapWithinSection(
  registry: SessionGroup[],
  id: string,
  dir: -1 | 1,
  siblings: string[],
): SessionGroup[] | null {
  const si = siblings.indexOf(id)
  const sj = si + dir
  if (si < 0 || sj < 0 || sj >= siblings.length) return null
  const i = registry.findIndex(g => g.id === id)
  const j = registry.findIndex(g => g.id === siblings[sj])
  if (i < 0 || j < 0) return null
  const next = [...registry]
  ;[next[i], next[j]] = [next[j], next[i]]
  return next
}

export interface SectionFold {
  tasks: boolean
  projects: boolean
}

/**
 * Parse the persisted section-fold preference. Anything unreadable — absent,
 * corrupt, the wrong shape, a storage that throws — leaves both sections open,
 * since a section the user cannot see is worse than one they must re-fold.
 */
export function parseSectionFold(raw: string | null): SectionFold {
  if (!raw) return { tasks: true, projects: true }
  try {
    const v = JSON.parse(raw)
    return { tasks: v?.tasks !== false, projects: v?.projects !== false }
  } catch {
    return { tasks: true, projects: true }
  }
}
