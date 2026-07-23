// Derives the mobile Sessions feed's three sections from the shared session
// state. Pure derivation over existing stores — no new data source:
//   To-do   — needs-approval (a confirmation is open for the session) or
//             needs-reply (an outstanding ask_user_question, Session.pending_question)
//   Active  — the agent is working
//   Recent  — everything else, newest first, capped
// Within each section, pinned sessions float to the top.
import { derived } from 'svelte/store'
import { sessions } from '../lib/stores'
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
}

// Cap the Recent section; the full history lives behind a second-level page.
const RECENT_CAP = 8

function byPinThenRecent(a: FeedItem, b: FeedItem): number {
  if (!!a.session.pinned !== !!b.session.pinned) return a.session.pinned ? -1 : 1
  return (b.session.updated_at || '').localeCompare(a.session.updated_at || '')
}

export const feedGroups = derived(
  sessions,
  ($sessions): FeedGroups => {
    const todo: FeedItem[] = []
    const active: FeedItem[] = []
    const recent: FeedItem[] = []

    for (const s of $sessions) {
      // pending_confirmation is server-fed (session_activity confirm_pending /
      // session list), so the feed sees it without subscribing to the session.
      if (s.pending_confirmation) todo.push({ session: s, kind: 'approval' })
      else if (s.pending_question) todo.push({ session: s, kind: 'reply' })
      else if (s.status === 'working') active.push({ session: s, kind: 'running' })
      else recent.push({ session: s, kind: 'done' })
    }

    todo.sort(byPinThenRecent)
    active.sort(byPinThenRecent)
    recent.sort(byPinThenRecent)

    return { todo, active, recent: recent.slice(0, RECENT_CAP) }
  },
)
