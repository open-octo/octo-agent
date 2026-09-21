import { describe, it, expect, beforeEach } from 'vitest'
import 'fake-indexeddb/auto'
import { registerLaIframe, unregisterLaIframe, registerArtifactFrame, installLaStorageBridge, LA_DB_NAME, LA_STORE } from './laStorage'
import { acceptArtifactState, dropArtifactState } from './laState'
import { lightappOrigin } from './stores'
import { vi } from 'vitest'

// The relay itself is covered in laState.test.ts; here the question is only
// whether the router hands it the right messages.
vi.mock('./laState', () => ({ acceptArtifactState: vi.fn(), dropArtifactState: vi.fn() }))

installLaStorageBridge()

beforeEach(() => {
  vi.clearAllMocks()
})

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
async function fromFrame(
  w: Window & { __sent: Sent[]; __ns: string },
  op: string,
  extra: Record<string, unknown> = {},
  ns = w.__ns,
  origin = lightappOrigin(w.__ns),
): Promise<void> {
  window.dispatchEvent(new MessageEvent('message', {
    data: { __laBridge: 1, id: 0, ns, op, ...extra },
    source: w,
    origin,
  }))
  await new Promise((r) => setTimeout(r, 20))
}

// The origin an artifact frame is served from — minted per grant, so the host
// takes it from the URL it loaded and the page never gets to assert it.
const ART_ORIGIN = 'http://tok-abc.artifacts.localhost:8088'

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
describe('artifact frame routing', () => {
  it('routes a state push to the artifact relay with its (session, path) identity', async () => {
    const w = makeWin()
    registerArtifactFrame(w, 'sess-9', '/tmp/page.html', ART_ORIGIN)
    await fromFrame(w, 'state', { digest: 'a chart' }, 'sess-9\n/tmp/page.html', ART_ORIGIN)

    expect(acceptArtifactState).toHaveBeenCalledWith('sess-9', '/tmp/page.html', expect.objectContaining({ digest: 'a chart' }))
  })

  it('drops a push that claims another identity than the registration', async () => {
    const w = makeWin()
    registerArtifactFrame(w, 'sess-9', '/tmp/page.html', ART_ORIGIN)
    // A stale document from before the iframe was reused claims a different ns.
    await fromFrame(w, 'state', { digest: 'stale' }, 'sess-9\n/tmp/other.html', ART_ORIGIN)

    expect(acceptArtifactState).not.toHaveBeenCalled()
  })

  it('forgets the artifact server-side when the frame unregisters', async () => {
    const w = makeWin()
    registerArtifactFrame(w, 'sess-9', '/tmp/gone.html', ART_ORIGIN)
    unregisterLaIframe(w)

    expect(dropArtifactState).toHaveBeenCalledWith('sess-9', '/tmp/gone.html')
  })

  it('an artifact frame never triggers the light-app storage migration', async () => {
    const w = makeWin()
    registerArtifactFrame(w, 'sess-9', '/tmp/page.html', ART_ORIGIN)
    await fromFrame(w, 'migrate-ready', {}, 'sess-9\n/tmp/page.html', ART_ORIGIN)

    // No reply at all: migration is a light-app contract.
    expect(w.__sent.length).toBe(0)
  })
})

describe('frame origin pinning', () => {
  // A registered frame can navigate itself elsewhere — the sandbox does not
  // stop it and the WindowProxy survives the trip. The document that lands
  // next must not inherit the registration.
  it('ignores a message from an origin other than the one registered', async () => {
    const w = makeWin()
    registerArtifactFrame(w, 'sess-9', '/tmp/page.html', ART_ORIGIN)
    await fromFrame(w, 'state', { digest: 'from elsewhere' }, 'sess-9\n/tmp/page.html', 'https://evil.example')

    expect(acceptArtifactState).not.toHaveBeenCalled()
  })

  it('ignores a light-app message from an origin other than the app\'s own', async () => {
    const w = makeWin()
    registerLaIframe(w, w.__ns)
    await fromFrame(w, 'migrate-ready', {}, w.__ns, 'https://evil.example')

    expect(w.__sent).toEqual([])
  })
})

describe('double-host frames', () => {
  // The panel and the maximized modal can both host the same artifact at
  // once (ArtifactModal mounts a second ArtifactFrame for the same
  // (session, path)). Closing one must not drop the mirror entry the other
  // is still feeding.
  it('drops the mirror entry only when the LAST frame for an identity unregisters', async () => {
    const panel = makeWin()
    const modal = makeWin()
    registerArtifactFrame(panel, 'sess-9', '/tmp/shared.html', ART_ORIGIN)
    registerArtifactFrame(modal, 'sess-9', '/tmp/shared.html', ART_ORIGIN)

    unregisterLaIframe(modal)
    expect(dropArtifactState).not.toHaveBeenCalled()

    unregisterLaIframe(panel)
    expect(dropArtifactState).toHaveBeenCalledWith('sess-9', '/tmp/shared.html')
  })

})

