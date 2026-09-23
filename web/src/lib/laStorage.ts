// Light App host-side bridge: the message router for Light App frames, and
// the one-time move of an app's storage into its namespace.
//
// A Light App renders from /_apps/<slug>/ on this page's own origin
// (internal/server/lightapp_pages.go). The script the server splices into it
// (internal/server/page_shim.js) keeps the app's localStorage under
// `octo.page.<slug>:` in the storage this page shares, so what remains here is:
//
//   - the frame registry: only iframes a host registered are listened to,
//     each under the namespace (slug) it was registered with;
//   - the download channel, handled in laDownload.ts: the desktop webview
//     cannot download, so the shim (desktop only) hands the bytes over;
//   - the migration: an app's data may still sit where it lived before —
//     in the localStorage of `<slug>.apps.localhost` (the retired per-app
//     origin), or in this page's IndexedDB (`octo-la-storage`, keys
//     `{slug}:{key}`, from the srcdoc shim before that). ensureLightAppStorage
//     copies both into the namespace once, before the app's frame loads, and
//     never overwrites a key the namespace already has.
//
// Protocol (frame → host, fire-and-forget):
//   { __laBridge: 1, id: 0, ns, op: 'download', name, blob }
//   { __laBridge: 1, op: 'export', ns, value: {k: v} }   (retired origin's export page)
//
// Only registered windows are heard (event.source must be in the registry),
// and the message's ns must match the registration: the iframe element is
// reused across app switches, so a departing document's late message must not
// act in the incoming app's namespace.

import { writable } from 'svelte/store'
import { MAX_DOWNLOAD_BYTES, deliverLaDownload, sanitizeDownloadName } from './laDownload'

export const LA_DB_NAME = 'octo-la-storage'
export const LA_STORE = 'kv'
// The IndexedDB marker the retired origin's migration set: those rows already
// moved to `<slug>.apps.localhost`, and copying them again would resurrect keys
// the app has deleted since.
const IDB_MIGRATED_PREFIX = '__octo_migrated__:'

// The namespace page_shim.js keeps an app's keys under (encoded the same way
// there), and the marker that the app's data has been moved into it.
export const pageKeyPrefix = (ns: string) => `octo.page.${encodeURIComponent(ns)}:`
export const migratedKey = (slug: string) => `octo.page.migrated.${slug}`

// How long the retired origin's export page gets to answer before the app
// opens without it.
export const EXPORT_TIMEOUT_MS = 5000

const laFrames = new Map<Window, string>() // iframe window -> namespace
let bridgeInstalled = false

export function registerLaIframe(win: Window | null | undefined, ns: string): void {
  if (!win) return
  laFrames.set(win, ns)
}

export function unregisterLaIframe(win: Window | null | undefined): void {
  if (!win) return
  laFrames.delete(win)
}

// ── IndexedDB (legacy rows) ─────────────────────────────────────────────────

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

// All key/values the srcdoc shim once stored under the namespace.
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

function laIdbMigrated(ns: string): Promise<boolean> {
  return openDb().then(
    (db) =>
      new Promise<boolean>((resolve, reject) => {
        const req = db.transaction(LA_STORE, 'readonly').objectStore(LA_STORE).get(IDB_MIGRATED_PREFIX + ns)
        req.onsuccess = () => resolve(req.result === 1)
        req.onerror = () => reject(req.error ?? new Error('IndexedDB request failed'))
      }),
  )
}

// ── Retired origin export ───────────────────────────────────────────────────

// Only a UI on this machine can reach `<slug>.apps.localhost` — and only one
// on localhost or 127.0.0.1 may frame the export page (its frame-ancestors).
// A browser elsewhere never had that origin's data to begin with.
function onLocalUI(): boolean {
  return location.hostname === 'localhost' || location.hostname === '127.0.0.1'
}

function exportFromRetiredOrigin(slug: string): Promise<Record<string, string>> {
  if (!onLocalUI()) return Promise.resolve({})
  const port = location.port ? `:${location.port}` : ''
  // The same scheme as this page: an https UI could not frame an http page
  // anyway (mixed content), and a local serve is plain http either way.
  const origin = `${location.protocol}//${slug.toLowerCase()}.apps.localhost${port}`
  return new Promise((resolve) => {
    const frame = document.createElement('iframe')
    frame.style.display = 'none'
    let done = false
    const finish = (value: Record<string, string>) => {
      if (done) return
      done = true
      window.removeEventListener('message', onMessage)
      clearTimeout(timer)
      frame.remove()
      resolve(value)
    }
    const onMessage = (ev: MessageEvent) => {
      if (ev.source !== frame.contentWindow || ev.origin !== origin) return
      const d = ev.data as Record<string, unknown> | null
      // The server names the namespace after the Host, which it lowercases.
      if (!d || d.__laBridge !== 1 || d.op !== 'export' || d.ns !== slug.toLowerCase()) return
      finish(d.value && typeof d.value === 'object' ? (d.value as Record<string, string>) : {})
    }
    window.addEventListener('message', onMessage)
    const timer = setTimeout(() => finish({}), EXPORT_TIMEOUT_MS)
    frame.src = `${origin}/__octo_export`
    document.body.appendChild(frame)
  })
}

// ── Migration ───────────────────────────────────────────────────────────────

// Slugs whose storage is ready — migrated, or found to need nothing. A host
// holds its frame's src back until its slug is in here, so the app never boots
// against storage that is about to change under it.
export const lightappStorageReady = writable<Set<string>>(new Set())

const migrations = new Map<string, Promise<void>>()

// Kick off (once per page) and await the move of an app's old data into its
// namespace. Never rejects: a migration that fails leaves the app to start
// from whatever the namespace holds.
export function ensureLightAppStorage(slug: string): Promise<void> {
  let p = migrations.get(slug)
  if (!p) {
    p = migrateLightAppStorage(slug)
      .catch(() => {})
      .finally(() => lightappStorageReady.update((s) => new Set(s).add(slug)))
    migrations.set(slug, p)
  }
  return p
}

async function migrateLightAppStorage(slug: string): Promise<void> {
  let store: Storage
  try {
    store = localStorage
  } catch {
    return
  }
  if (store.getItem(migratedKey(slug)) !== null) return
  const prefix = pageKeyPrefix(slug)
  const put = (entries: Record<string, string>) => {
    for (const [k, v] of Object.entries(entries)) {
      if (store.getItem(prefix + k) === null) store.setItem(prefix + k, String(v))
    }
  }
  // The retired origin first: it is the newer of the two homes.
  put(await exportFromRetiredOrigin(slug))
  if (!(await laIdbMigrated(slug).catch(() => true))) put(await laDump(slug).catch(() => ({})))
  store.setItem(migratedKey(slug), '1')
}

// ── Message router ──────────────────────────────────────────────────────────

function onLaMessage(ev: MessageEvent): void {
  const ns = laFrames.get(ev.source as Window)
  if (!ns) return
  const d = ev.data as Record<string, unknown> | null
  if (!d || d.__laBridge !== 1) return
  if (d.ns !== ns) return // stale document from before an app switch
  if (d.op !== 'download') return
  // A payload that isn't a Blob or is over the cap is dropped outright rather
  // than reported back — the shim never waits for a reply.
  if (!(d.blob instanceof Blob) || d.blob.size > MAX_DOWNLOAD_BYTES) return
  void deliverLaDownload(sanitizeDownloadName(d.name), d.blob)
}

export function installLaStorageBridge(): void {
  if (bridgeInstalled) return
  bridgeInstalled = true
  window.addEventListener('message', onLaMessage)
}
