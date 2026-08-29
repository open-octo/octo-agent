// Unread tracking for the sidebar's session list.
//
// A session can finish a turn with nobody watching it: a /loop tick, a cron
// fire, an IM message, or simply a reply that landed while the tab sat on a
// different chat. The row already says whether a session is running NOW (the
// spinner) and when it last changed (the timestamp), but neither answers "is
// there something in here I haven't read". That's what this tracks.
//
// It lives client-side, in localStorage: "have I read this" is a property of a
// reader at a screen, not of the session on disk, and the server has no notion
// of who is looking. Per browser, per device — deliberately.
//
// Two clocks feed the comparison. The server's `updated_at` is authoritative
// and covers everything that happened while this tab was closed (the list is
// refetched on load). Turns that finish while the tab IS open don't refresh
// `updated_at` — no broadcast carries it — so `session_activity`'s turn_ended
// stamps a local time in `sessionTouchedAt` instead. Whichever is later is the
// session's last change.
import { writable, get } from 'svelte/store'
import { sessions } from './stores'
import type { Session } from './types'

const KEY = 'octo.session-seen'

function load(): Record<string, number> {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed as Record<string, number> : {}
  } catch {
    return {}
  }
}

/** Last change (epoch ms) the user has seen, per session id. Persisted. */
export const sessionSeenAt = writable<Record<string, number>>(
  typeof localStorage === 'undefined' ? {} : load(),
)
sessionSeenAt.subscribe(m => {
  try { localStorage.setItem(KEY, JSON.stringify(m)) } catch { /* private mode: in-memory only */ }
})

/**
 * Locally observed turn endings (epoch ms), per session id. In-memory only:
 * across a reload the server's `updated_at` says the same thing, and better.
 */
export const sessionTouchedAt = writable<Record<string, number>>({})

// Parse rather than compare the ISO strings directly: the list mixes the
// server's zone-offset format with the UTC "Z" stamps App.svelte writes on
// turn_ended, and those don't order lexicographically. Exported for the
// sidebar/mobile recency sorts, which face the same mix.
export function updatedAtMs(s: Pick<Session, 'updated_at'>): number {
  const t = Date.parse(s.updated_at ?? '')
  return Number.isNaN(t) ? 0 : t
}

/**
 * The later of the server's updated_at and a turn ending seen in this tab, or
 * 0 for "no idea" — the WS handshake's brief session list carries no
 * updated_at at all (only created_at), and a list without timestamps must not
 * be read as a list of sessions last changed at the epoch.
 */
export function lastChangeAt(s: Pick<Session, 'id' | 'updated_at'>, touched: Record<string, number>): number {
  return Math.max(updatedAtMs(s), touched[s.id] ?? 0)
}

/**
 * Whether a session has changed since the user last looked at it. A running
 * session is never unread — the spinner is already saying more than a dot
 * could, and the dot's turn comes when the turn ends. A session with no seen
 * record yet isn't either: reconcileSeen() baselines those, so an unrecorded
 * one is a session this browser has never listed and there's nothing to claim
 * about it.
 */
export function isUnread(
  s: Pick<Session, 'id' | 'updated_at' | 'status'>,
  seen: Record<string, number>,
  touched: Record<string, number>,
): boolean {
  if (s.status === 'running') return false
  const seenAt = seen[s.id]
  if (seenAt === undefined) return false
  const at = lastChangeAt(s, touched)
  if (at === 0) return false
  return at > seenAt
}

/**
 * Mark a session read as of now. Takes the max of every clock in play so a
 * local clock behind the server's can't leave a dot the user can never clear.
 */
export function markSessionSeen(id: string) {
  if (!id) return
  const s = get(sessions).find(x => x.id === id)
  const at = Math.max(Date.now(), s ? lastChangeAt(s, get(sessionTouchedAt)) : 0)
  sessionSeenAt.update(m => (m[id] >= at ? m : { ...m, [id]: at }))
}

/** Record that a session just finished a turn, making it unread if unwatched. */
export function touchSession(id: string) {
  if (!id) return
  sessionTouchedAt.update(m => ({ ...m, [id]: Date.now() }))
}

/**
 * Mark the on-screen session seen when the page goes away — window/tab close
 * (pagehide) or minimize/OS-suspension of the webview (visibilitychange →
 * hidden).
 *
 * A turn's end-of-turn file writes (context-usage persist, title adoption)
 * land AFTER the reply is fully rendered, and the only event that re-marks
 * the session afterwards is the fire-and-forget turn_ended broadcast: a
 * window gone before that broadcast arrives — or a webview the OS suspended
 * mid-turn — keeps a seen mark older than the file's new mtime, and the next
 * open shows an unread dot on a session the user finished reading. Stamping
 * on the way out closes that gap for the session on screen, the only one the
 * user could have read through.
 *
 * `activeId` returns the session whose transcript is on screen, or null when
 * chat isn't the visible view — the same contract as the marking effect in
 * App.svelte. Returns an unsubscribe.
 */
export function markActiveSessionSeenOnLeave(activeId: () => string | null): () => void {
  const mark = () => {
    const sid = activeId()
    if (sid) markSessionSeen(sid)
  }
  const onVisibility = () => {
    if (document.visibilityState === 'hidden') mark()
  }
  window.addEventListener('pagehide', mark)
  document.addEventListener('visibilitychange', onVisibility)
  return () => {
    window.removeEventListener('pagehide', mark)
    document.removeEventListener('visibilitychange', onVisibility)
  }
}

/**
 * Baseline newly-seen sessions and forget vanished ones.
 *
 * Baselining is what keeps a first visit (or a new browser) from opening onto
 * a screen of dots: a session this browser has never listed counts as read at
 * whatever its current updated_at is, and only changes after that mark it
 * unread. Pruning keeps the map from growing forever as sessions are deleted.
 * An empty list is ignored — that's boot, or a failed fetch, not "every
 * session is gone", and pruning on it would wipe every pending dot.
 *
 * A session the list gives no timestamp for gets no baseline (it keeps an
 * existing one). That's the WS handshake list, which carries created_at and no
 * updated_at: baselining it at 0 would mark every session unread the moment
 * the REST list landed with real timestamps.
 */
export function reconcileSeen(list: Session[]) {
  if (list.length === 0) return
  const touched = get(sessionTouchedAt)
  sessionSeenAt.update(m => {
    const next: Record<string, number> = {}
    let changed = false
    for (const s of list) {
      const prev = m[s.id]
      if (prev !== undefined) {
        next[s.id] = prev
        continue
      }
      const at = lastChangeAt(s, touched)
      if (at === 0) continue
      next[s.id] = at
      changed = true
    }
    for (const id of Object.keys(m)) {
      if (!(id in next)) changed = true
    }
    return changed ? next : m
  })
}

// The session list is replaced wholesale by several paths (the initial fetch,
// every post-broadcast refetch). Subscribing here means each of them baselines
// and prunes without having to remember to.
sessions.subscribe(list => reconcileSeen(list))
