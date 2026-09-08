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
//     (internal/server/lightapp_bridge.js) that asks once per load; the host
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

const laFrames = new Map<Window, string>() // iframe window -> namespace
let bridgeInstalled = false

export function registerLaIframe(win: Window | null | undefined, ns: string): void {
  if (!win) return
  laFrames.set(win, ns)
}

export function unregisterLaIframe(win: Window | null | undefined): void {
  if (win) laFrames.delete(win)
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
  const ns = laFrames.get(ev.source as Window)
  if (!ns) return
  const d = ev.data as Record<string, unknown> | null
  if (!d || d.__laBridge !== 1) return
  if (d.ns !== ns) return // stale document from before an app switch
  const source = ev.source as Window

  switch (d.op) {
    case 'migrate-ready':
      // Nothing to say when the app was born on the origin or already took
      // its data: the frame simply hears no reply and carries on.
      laIsMigrated(ns)
        .then(async (done) => {
          if (done) return
          const entries = await laDump(ns)
          if (Object.keys(entries).length === 0) return
          source.postMessage({ __laBridge: 1, id: 0, res: true, ok: true, op: 'migrate', value: entries }, '*')
        })
        .catch(() => {})
      break
    case 'migrated':
      laMarkMigrated(ns).catch(() => {})
      break
    case 'download':
      // A payload that isn't a Blob or is over the cap is dropped outright
      // rather than reported back — the bridge never waits for a reply.
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
