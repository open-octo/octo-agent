import { describe, it, expect, beforeEach } from 'vitest'
import 'fake-indexeddb/auto'
import { registerLaIframe, unregisterLaIframe, installLaStorageBridge, LA_DB_NAME, LA_STORE } from './laStorage'

installLaStorageBridge()

type Sent = Record<string, unknown> | null

let nsCounter = 0
function makeWin() {
  // unique namespace per test — the module caches its IndexedDB connection,
  // so a fresh IDBFactory alone doesn't reset data between tests
  const ns = 'app-' + nsCounter++
  const w = { __sent: [] as Sent[], __ns: ns } as unknown as Window & { __sent: Sent[]; __ns: string }
  ;(w as { postMessage: (m: unknown) => void }).postMessage = (m: unknown) => {
    w.__sent.push(m as Sent)
  }
  return w
}

// What the old srcdoc shim left behind: rows keyed `{ns}:{key}` in the host's
// IndexedDB. Written straight into the store, as the shim's host half did.
function seedLegacy(ns: string, entries: Record<string, string>): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(LA_DB_NAME, 1)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(LA_STORE)) db.createObjectStore(LA_STORE)
    }
    req.onerror = () => reject(req.error)
    req.onsuccess = () => {
      const db = req.result
      const t = db.transaction(LA_STORE, 'readwrite')
      const s = t.objectStore(LA_STORE)
      for (const [k, v] of Object.entries(entries)) s.put(v, `${ns}:${k}`)
      t.oncomplete = () => { db.close(); resolve() }
      t.onerror = () => reject(t.error)
    }
  })
}

// Dispatch a message as if from the app's frame and wait for the async IDB
// roundtrip.
async function fromFrame(w: Window & { __sent: Sent[]; __ns: string }, op: string, extra: Record<string, unknown> = {}, ns = w.__ns): Promise<void> {
  window.dispatchEvent(new MessageEvent('message', {
    data: { __laBridge: 1, id: 0, ns, op, ...extra },
    source: w,
    origin: 'http://' + ns + '.apps.localhost:8088',
  }))
  await new Promise((r) => setTimeout(r, 20))
}

// No fresh IDBFactory per test: the module caches its connection, so a new
// factory would leave the seeds and the reads in different databases. The
// unique namespace per test is the isolation.
describe('Light App storage migration', () => {
  it('answers the first migrate-ready with the legacy entries, once', async () => {
    const w = makeWin()
    await seedLegacy(w.__ns, { score: '10', name: 'x' })
    registerLaIframe(w, w.__ns)

    await fromFrame(w, 'migrate-ready')
    expect(w.__sent).toEqual([{ __laBridge: 1, id: 0, res: true, ok: true, op: 'migrate', value: { score: '10', name: 'x' } }])

    // The frame took the data and said so; the next load must get nothing.
    await fromFrame(w, 'migrated', { count: 2 })
    w.__sent = []
    await fromFrame(w, 'migrate-ready')
    expect(w.__sent).toEqual([])
  })

  it('says nothing for an app that never had legacy data', async () => {
    const w = makeWin()
    registerLaIframe(w, w.__ns)
    await fromFrame(w, 'migrate-ready')
    expect(w.__sent).toEqual([])
  })

  it('only hands out the registered namespace, whatever the message claims', async () => {
    const a = makeWin()
    const b = makeWin()
    await seedLegacy(a.__ns, { secret: 'a' })
    await seedLegacy(b.__ns, { secret: 'b' })
    registerLaIframe(b, b.__ns)

    // b's frame claiming to be a is a stale-document (or hostile) message: ignored.
    await fromFrame(b, 'migrate-ready', {}, a.__ns)
    expect(b.__sent).toEqual([])
    await fromFrame(b, 'migrate-ready')
    expect(b.__sent).toEqual([{ __laBridge: 1, id: 0, res: true, ok: true, op: 'migrate', value: { secret: 'b' } }])
  })

  it('ignores windows the panel never registered, and ones it unregistered', async () => {
    const w = makeWin()
    await seedLegacy(w.__ns, { k: 'v' })
    await fromFrame(w, 'migrate-ready')
    expect(w.__sent).toEqual([])

    registerLaIframe(w, w.__ns)
    unregisterLaIframe(w)
    await fromFrame(w, 'migrate-ready')
    expect(w.__sent).toEqual([])
  })

  it('no longer answers the storage ops the old shim sent', async () => {
    const w = makeWin()
    registerLaIframe(w, w.__ns)
    for (const op of ['dump', 'set', 'remove', 'clear']) {
      await fromFrame(w, op, { key: 'k', value: 'v' })
    }
    expect(w.__sent).toEqual([])
  })
})
