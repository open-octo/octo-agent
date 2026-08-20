// Per-panel interaction state (form values, selected tab, fold state),
// persisted client-side so a page reload doesn't undo what the user set.
// See dev-docs/genui-interactive-panels-design.md, "State persistence".
//
// It lives in localStorage, per browser and per device, for the same reason
// unread.ts keeps read-tracking there: "which tab did I open, what did I type
// into this filter" is a property of a reader at a screen, not of the session
// on disk, and the server has no notion of who is looking.
//
// Only panels with an id persist. An anonymous panel keeps its values in
// memory exactly as it did before this feature.

import type { GenuiFieldValue } from './types'

const KEY = 'octo.genui-panel-state'

/** How long to sit on writes. A slider drag changes its field on every
 * pointer move; the in-memory map follows synchronously (that's what the UI
 * renders from) while the durable copy lands once the user settles. */
const WRITE_DEBOUNCE_MS = 200

/** Sessions kept before evicting the least-recently-written. A backstop for a
 * client that never loads a session list — pruneSessions() is the primary GC. */
const MAX_SESSIONS = 50

type PanelFields = Record<string, GenuiFieldValue>

interface SessionEntry {
  /** Last write, epoch ms — the LRU key. */
  at: number
  panels: Record<string, PanelFields>
}

type Store = Record<string, SessionEntry>

function hasStorage(): boolean {
  return typeof localStorage !== 'undefined'
}

/** Storage is shared, long-lived, and hand-editable, so a stored value can be
 * any shape at all — an older format, another tool's key collision, a partial
 * write. Every entry is checked rather than assumed, because the alternative
 * is a TypeError thrown from the first interaction with any panel. */
function load(): Store {
  if (!hasStorage()) return {}
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    if (!isPlainObject(parsed)) return {}
    const out: Store = {}
    for (const [sessionId, entry] of Object.entries(parsed)) {
      if (!isPlainObject(entry) || !isPlainObject((entry as Record<string, unknown>).panels)) continue
      const rec = entry as Record<string, unknown>
      const panels: Record<string, PanelFields> = {}
      for (const [panelId, fields] of Object.entries(rec.panels as Record<string, unknown>)) {
        if (isPlainObject(fields)) panels[panelId] = fields as PanelFields
      }
      out[sessionId] = { at: typeof rec.at === 'number' && Number.isFinite(rec.at) ? rec.at : 0, panels }
    }
    return out
  } catch {
    return {}
  }
}

function isPlainObject(v: unknown): boolean {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

// The in-memory copy is authoritative for reads within a page's lifetime;
// localStorage is where it is mirrored.
let store: Store = load()
let flushTimer: ReturnType<typeof setTimeout> | null = null

function flush(): void {
  if (flushTimer !== null) {
    clearTimeout(flushTimer)
    flushTimer = null
  }
  if (!hasStorage()) return
  try {
    localStorage.setItem(KEY, JSON.stringify(store))
  } catch {
    /* private mode or quota: keep the in-memory copy only */
  }
}

function scheduleFlush(): void {
  if (flushTimer !== null) return
  flushTimer = setTimeout(() => {
    flushTimer = null
    flush()
  }, WRITE_DEBOUNCE_MS)
}

// A tab closed or backgrounded mid-drag must not lose the value the user just
// set, so the pending write is forced out at the last moment the page is
// guaranteed to still be running.
if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden' && flushTimer !== null) flush()
  })
}

function evictIfNeeded(): void {
  const ids = Object.keys(store)
  if (ids.length <= MAX_SESSIONS) return
  ids
    .sort((a, b) => (store[a].at ?? 0) - (store[b].at ?? 0))
    .slice(0, ids.length - MAX_SESSIONS)
    .forEach(id => delete store[id])
}

/** Every persisted field for one panel. Empty when nothing was stored. */
export function loadPanelFields(sessionId: string, panelId: string): PanelFields {
  const entry = store[sessionId]
  const fields = entry?.panels?.[panelId]
  return fields ? { ...fields } : {}
}

/** Records one field's value. Writes are debounced; the returned state is
 * visible to loadPanelFields immediately regardless. */
export function savePanelField(
  sessionId: string,
  panelId: string,
  field: string,
  value: GenuiFieldValue
): void {
  const entry = store[sessionId] ?? { at: 0, panels: {} }
  const panel = entry.panels[panelId] ?? {}
  panel[field] = value
  entry.panels[panelId] = panel
  entry.at = Date.now()
  store[sessionId] = entry
  evictIfNeeded()
  scheduleFlush()
}

/**
 * Drops state for sessions that no longer exist. Called when the session list
 * loads — the natural GC point, since that is when a deletion elsewhere first
 * becomes visible to this client.
 */
export function pruneSessions(liveSessionIds: Iterable<string>): void {
  const live = new Set(liveSessionIds)
  // An empty list means "not loaded yet" far more often than it means "this
  // account has no sessions": a Svelte store subscription fires immediately
  // with its initial [] before any fetch has completed, so treating that as
  // "delete everything" wiped all persisted panel state on every page load.
  // A genuinely empty account has nothing worth collecting, and MAX_SESSIONS
  // remains the backstop, so refusing to act on an empty list costs nothing.
  if (live.size === 0) return
  let changed = false
  for (const id of Object.keys(store)) {
    if (!live.has(id)) {
      delete store[id]
      changed = true
    }
  }
  if (changed) scheduleFlush()
}

/** Test seam: drop everything, in memory and on disk. */
export function resetPanelState(): void {
  store = {}
  flush()
}

/**
 * Test seam: re-read from storage the way a fresh page load would.
 *
 * Distinct from resetPanelState, which *writes* an empty store — using that
 * to set up a "what's already in storage" case silently overwrites the very
 * value under test.
 */
export function reloadPanelState(): void {
  if (flushTimer !== null) {
    clearTimeout(flushTimer)
    flushTimer = null
  }
  store = load()
}
