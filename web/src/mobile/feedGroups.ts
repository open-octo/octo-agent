// Derives the mobile Sessions feed's three sections from the shared session
// state. Pure derivation over existing stores — no new data source:
//   To-do   — needs-approval (a confirmation is open for the session) or
//             needs-reply (an outstanding ask_user_question, Session.pending_question)
//   Active  — the agent is working
//   Recent  — everything else, newest first, capped
// Within each section, pinned sessions float to the top. The full list, for
// the history / search page, is allSessions.
import { derived } from 'svelte/store'
import { sessions } from '../lib/stores'
import { updatedAtMs } from '../lib/unread'
import type { Session } from '../lib/types'

export type FeedKind = 'approval' | 'reply' | 'running' | 'done'
export interface FeedItem {
  session: Session
  kind: FeedKind
}
export interface FeedGroups {
  todo: FeedItem[]
  active: FeedItem[]
  recent: FeedItem[]
  /** Size of Recent before the cap, so the feed knows there is more. */
  recentTotal: number
}

// Cap the Recent section; the full history lives behind a second-level page.
const RECENT_CAP = 8

function byPinThenRecent(a: FeedItem, b: FeedItem): number {
  if (!!a.session.pinned !== !!b.session.pinned) return a.session.pinned ? -1 : 1
  // Numeric, not lexicographic: updated_at mixes the server's zone-offset
  // format with the UTC "Z" stamps App.svelte writes on turn_ended.
  return updatedAtMs(b.session) - updatedAtMs(a.session)
}

function kindOf(s: Session): FeedKind {
  // pending_confirmation is server-fed (session_activity confirm_pending /
  // session list), so the feed sees it without subscribing to the session.
  if (s.pending_confirmation) return 'approval'
  if (s.pending_question) return 'reply'
  if (s.status === 'running') return 'running'
  return 'done'
}

export const feedGroups = derived(
  sessions,
  ($sessions): FeedGroups => {
    const todo: FeedItem[] = []
    const active: FeedItem[] = []
    const recent: FeedItem[] = []

    for (const s of $sessions) {
      const item = { session: s, kind: kindOf(s) }
      if (item.kind === 'approval' || item.kind === 'reply') todo.push(item)
      else if (item.kind === 'running') active.push(item)
      else recent.push(item)
    }

    todo.sort(byPinThenRecent)
    active.sort(byPinThenRecent)
    recent.sort(byPinThenRecent)

    return { todo, active, recent: recent.slice(0, RECENT_CAP), recentTotal: recent.length }
  },
)

// Every session, pinned first then newest — the history / search page.
export const allSessions = derived(sessions, ($sessions): FeedItem[] =>
  $sessions.map((s) => ({ session: s, kind: kindOf(s) })).sort(byPinThenRecent),
)

// matchesQuery is the search filter: a case-insensitive substring of the title
// (or name, for an untitled session). An empty query matches everything.
export function matchesQuery(s: Session, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return (s.title || s.name || '').toLowerCase().includes(q)
}
