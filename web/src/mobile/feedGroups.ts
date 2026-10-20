// Derives the mobile Sessions feed's sections from the shared session and
// group state. Pure derivation over existing stores — no new data source:
//   To-do    — needs-approval (a confirmation is open for the session) or
//              needs-reply (an outstanding ask_user_question, Session.pending_question)
//   Active   — the agent is working
//   Pinned   — the rest of the pinned sessions, in the desktop's pin order
//   Projects — one row per project, in the desktop sidebar's order
//   Recent   — everything else outside a project, newest first, capped
// To-do and Active span projects and pins: they are what needs you, wherever
// it runs. Pinned and project membership come from the desktop sidebar's own
// registry (pinnedSessions, splitSections), so a session sits in the same
// place on both. Within each section, pinned sessions float to the top. The
// full list, for the history / search page, is allSessions.
import { derived } from 'svelte/store'
import { sessions, sessionGroups, pinnedSessions } from '../lib/stores'
import { updatedAtMs } from '../lib/unread'
import { splitSections } from '../lib/sidebarSections'
import type { Session, SessionGroup } from '../lib/types'

export type FeedKind = 'approval' | 'reply' | 'running' | 'done'
export interface FeedItem {
  session: Session
  kind: FeedKind
}
export interface ProjectView {
  group: SessionGroup
  /** Every session in the project, any state, pinned first then newest. */
  items: FeedItem[]
}
export interface FeedGroups {
  todo: FeedItem[]
  active: FeedItem[]
  pinned: FeedItem[]
  projects: ProjectView[]
  recent: FeedItem[]
  /** Size of Recent before the cap, so the feed knows there is more. */
  recentTotal: number
}

// Cap the Recent section; the full history lives behind a second-level page.
const RECENT_CAP = 8

// Pinned-ness reads the pinnedSessions registry, not Session.pinned: the
// registry is what a pin toggle updates (optimistically, then via the
// session_groups_changed refetch), while the session list's flag stays stale
// until the next list fetch.
function byPinThenRecent(pinned: Set<string>) {
  return (a: FeedItem, b: FeedItem): number => {
    const pa = pinned.has(a.session.id)
    const pb = pinned.has(b.session.id)
    if (pa !== pb) return pa ? -1 : 1
    // Numeric, not lexicographic: updated_at mixes the server's zone-offset
    // format with the UTC "Z" stamps App.svelte writes on turn_ended.
    return updatedAtMs(b.session) - updatedAtMs(a.session)
  }
}

function kindOf(s: Session): FeedKind {
  // pending_confirmation is server-fed (session_activity confirm_pending /
  // session list), so the feed sees it without subscribing to the session.
  if (s.pending_confirmation) return 'approval'
  if (s.pending_question) return 'reply'
  if (s.status === 'running') return 'running'
  return 'done'
}

const toItem = (s: Session): FeedItem => ({ session: s, kind: kindOf(s) })

export const feedGroups = derived(
  [sessions, sessionGroups, pinnedSessions],
  ([$sessions, $groups, $pinned]): FeedGroups => {
    const pinnedSet = new Set($pinned)
    const order = byPinThenRecent(pinnedSet)
    const todo: FeedItem[] = []
    const active: FeedItem[] = []
    const recent: FeedItem[] = []

    // Pinned ids go in so the partition claims them first, exactly as the
    // desktop sidebar does: a pinned session leaves Recent for Pinned. Projects
    // come from a second, pin-free partition so a project's page still lists
    // every member, pinned or not.
    const split = splitSections($sessions, $groups, $pinned, [])
    const { projects } = splitSections($sessions, $groups, [], [])
    const loose = new Set(split.ungrouped.map((s) => s.id))
    const needsYou = (it: FeedItem) => it.kind !== 'done'

    for (const s of $sessions) {
      const item = toItem(s)
      if (item.kind === 'approval' || item.kind === 'reply') todo.push(item)
      else if (item.kind === 'running') active.push(item)
      else if (loose.has(s.id)) recent.push(item)
    }

    todo.sort(order)
    active.sort(order)
    recent.sort(order)

    return {
      todo,
      active,
      pinned: split.pinned.map(toItem).filter((it) => !needsYou(it)),
      projects: projects.map((p) => ({ group: p.group, items: p.items.map(toItem).sort(order) })),
      recent: recent.slice(0, RECENT_CAP),
      recentTotal: recent.length,
    }
  },
)

// Project name by session id, for the project label on a session card.
export const projectOf = derived(sessionGroups, ($groups): Map<string, string> => {
  const m = new Map<string, string>()
  for (const g of $groups) {
    if (!g.working_dir) continue
    for (const id of g.session_ids ?? []) if (!m.has(id)) m.set(id, g.name)
  }
  return m
})

// Every session, pinned first then newest — the history / search page.
export const allSessions = derived([sessions, pinnedSessions], ([$sessions, $pinned]): FeedItem[] =>
  $sessions.map(toItem).sort(byPinThenRecent(new Set($pinned))),
)

// matchesQuery is the search filter: a case-insensitive substring of the title
// (or name, for an untitled session). An empty query matches everything.
export function matchesQuery(s: Session, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return (s.title || s.name || '').toLowerCase().includes(q)
}
