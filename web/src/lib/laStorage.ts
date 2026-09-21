// Light App host-side bridge: the message router for frames on the Light App
// origin, and the one-time migration of the storage the host used to keep.
//
// A Light App renders from its own origin (`<slug>.apps.localhost`, see
// internal/server/lightapp_origin.go), so its `localStorage` is real and
// persistent and nothing here shims it. What remains on the host is:
//
//   - the frame registry: only iframes this panel registered are listened to,
//     each under the namespace (slug) it was registered with;
//   - the migration: before the app had an origin, its localStorage lived in
//     this page's IndexedDB (`octo-la-storage`, keys `{slug}:{key}`) behind a
//     shim. The server appends a small script to every Light App
//     (internal/server/frame_bridge.js) that asks once per load; the host
//     answers with the namespace's entries the first time, the app writes them
//     into its real storage, and the namespace is marked migrated. The old rows
//     stay put for now.
//   - the download channel, handled in laDownload.ts: the desktop webview cannot
//     download, so the bridge script (desktop only) hands the bytes over.
//
// Protocol (frame → host, all fire-and-forget except where noted):
//   { __laBridge: 1, id: 0, ns, op: 'migrate-ready' }      → host may reply
//   { __laBridge: 1, id: 0, res: true, ok: true, op: 'migrate', value: {k: v} }
//   { __laBridge: 1, id: 0, ns, op: 'migrated', count }
//   { __laBridge: 1, id: 0, ns, op: 'download', name, blob }
//   { __laBridge: 1, id: 0, ns, op: 'state', digest, summary?, image? }
//
// Security model:
//   - Only registered windows are heard (event.source must be in the registry),
//     and the message's ns must match the registration: the iframe element is
//     reused across app switches, so a departing document's late message must
//     not act in the incoming app's namespace.
//   - The host reads and marks under the ns it registered, never one the
//     message claims, so an app cannot read another app's legacy data.

import { MAX_DOWNLOAD_BYTES, deliverLaDownload, sanitizeDownloadName } from './laDownload'

export const LA_DB_NAME = 'octo-la-storage'
export const LA_STORE = 'kv'
// Migration markers live under their own prefix so they are invisible to a
// namespace dump (which scans `{ns}:`) and can never collide with an app key.
const MIGRATED_PREFIX = '__octo_migrated__:'

import { acceptLaState, dropLaState, acceptArtifactState, dropArtifactState } from './laState'

// One registered frame: a Light App by slug, or a session artifact by
// (session, path). The ns is the identity the bridge stamps into every
// message — for an artifact it is `session\npath`, the same key the mirror
// uses server-side.
type FrameReg = { ns: string; kind: 'lightapp' | 'artifact'; session?: string; path?: string }

const laFrames = new Map<Window, FrameReg>() // iframe window -> registration
let bridgeInstalled = false

export function registerLaIframe(win: Window | null | undefined, ns: string): void {
  if (!win) return
  laFrames.set(win, { ns, kind: 'lightapp' })
}

// The artifact twin: the panel's preview frame for one HTML artifact of a
// session. Registering it is what lets the page's bridge reach the mirror —
// and lets deliveries find it.
export function registerArtifactFrame(win: Window | null | undefined, session: string, path: string): void {
  if (!win) return
  laFrames.set(win, { ns: session + '\n' + path, kind: 'artifact', session, path })
}

// Reverse lookup for the delivery path: which frame, if any, is currently
// showing this page. Both hosts (the panel and the mounted full page) register
// here, so either one can receive.
export function laFrameFor(ns: string): Window | null {
  for (const [win, r] of laFrames) {
    if (r.ns === ns) return win
  }
  return null
}

export function unregisterLaIframe(win: Window | null | undefined): void {
  if (!win) return
  const reg = laFrames.get(win)
  laFrames.delete(win)
  // The page is gone from the screen, so it must go from the mirror too: the
  // model should not describe a canvas nobody has open.
  if (!reg) return
  if (reg.kind === 'artifact' && reg.session !== undefined && reg.path !== undefined) {
    dropArtifactState(reg.session, reg.path)
  } else {
    dropLaState(reg.ns)
  }
}

// ── IndexedDB ───────────────────────────────────────────────────────────────

let dbPromise: Promise<IDBDatabase> | null = null

function openDb(): Promise<IDBDatabase> {
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const req = indexedDB.open(LA_DB_NAME, 1)
      req.onupgradeneeded = () => {
        const db = req.result
        if (!db.objectStoreNames.contains(LA_STORE)) db.createObjectStore(LA_STORE)
      }
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error ?? new Error('IndexedDB open failed'))
    })
  }
  return dbPromise
}

function run<T>(mode: IDBTransactionMode, fn: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openDb().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const t = db.transaction(LA_STORE, mode)
        const req = fn(t.objectStore(LA_STORE))
        req.onsuccess = () => resolve(req.result)
        req.onerror = () => reject(req.error ?? new Error('IndexedDB request failed'))
      }),
  )
}

// dump: all key/values the shim once stored under the namespace.
function laDump(ns: string): Promise<Record<string, string>> {
  return openDb().then(
    (db) =>
      new Promise<Record<string, string>>((resolve, reject) => {
        const t = db.transaction(LA_STORE, 'readonly')
        const store = t.objectStore(LA_STORE)
        const prefix = ns + ':'
        const out: Record<string, string> = {}
        const cur = store.openCursor()
        cur.onsuccess = () => {
          const c = cur.result
          if (c) {
            if (typeof c.key === 'string' && c.key.startsWith(prefix)) {
              out[c.key.slice(prefix.length)] = String(c.value)
            }
            c.continue()
          } else {
            resolve(out)
          }
        }
        cur.onerror = () => reject(cur.error ?? new Error('IndexedDB cursor failed'))
      }),
  )
}

function laIsMigrated(ns: string): Promise<boolean> {
  return run('readonly', (s) => s.get(MIGRATED_PREFIX + ns)).then((v) => v === 1)
}

function laMarkMigrated(ns: string): Promise<unknown> {
  return run('readwrite', (s) => s.put(1, MIGRATED_PREFIX + ns))
}

// ── Message router ──────────────────────────────────────────────────────────

function onLaMessage(ev: MessageEvent): void {
  const reg = laFrames.get(ev.source as Window)
  if (!reg) return
  const d = ev.data as Record<string, unknown> | null
  if (!d || d.__laBridge !== 1) return
  if (d.ns !== reg.ns) return // stale document from before an app switch
  const source = ev.source as Window

  switch (d.op) {
    case 'migrate-ready':
      // Storage migration is a Light App contract; an artifact frame asking
      // for it hears nothing, like any op it has no business sending.
      if (reg.kind !== 'lightapp') break
      // Nothing to say when the app was born on the origin or already took
      // its data: the frame simply hears no reply and carries on.
      laIsMigrated(reg.ns)
        .then(async (done) => {
          if (done) return
          const entries = await laDump(reg.ns)
          if (Object.keys(entries).length === 0) return
          source.postMessage({ __laBridge: 1, id: 0, res: true, ok: true, op: 'migrate', value: entries }, '*')
        })
        .catch(() => {})
      break
    case 'migrated':
      if (reg.kind !== 'lightapp') break
      laMarkMigrated(reg.ns).catch(() => {})
      break
    case 'state':
      // One-way: the page describes itself, the host relays it. Nothing is
      // sent back, and the page gains no read access by pushing.
      if (reg.kind === 'artifact' && reg.session !== undefined && reg.path !== undefined) {
        acceptArtifactState(reg.session, reg.path, d as { digest?: unknown; summary?: unknown; image?: unknown })
      } else {
        acceptLaState(reg.ns, d as { digest?: unknown; summary?: unknown; image?: unknown })
      }
      break
    case 'download':
      // Light Apps only — an artifact's downloads are its own origin's
      // business. A payload that isn't a Blob or is over the cap is dropped
      // outright rather than reported back — the bridge never waits for a
      // reply.
      if (reg.kind !== 'lightapp') return
      if (!(d.blob instanceof Blob) || d.blob.size > MAX_DOWNLOAD_BYTES) return
      void deliverLaDownload(sanitizeDownloadName(d.name), d.blob)
      break
    default:
      // The storage ops the old shim sent (dump/set/remove/clear) are gone
      // with it; a document that still sends them gets no answer.
      break
  }
}

export function installLaStorageBridge(): void {
  if (bridgeInstalled) return
  bridgeInstalled = true
  window.addEventListener('message', onLaMessage)
}
