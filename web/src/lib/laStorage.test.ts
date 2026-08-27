import { describe, it, expect, beforeEach } from 'vitest'
import 'fake-indexeddb/auto'
import {
  registerLaIframe,
  unregisterLaIframe,
  installLaStorageBridge,
  withLaBridge,
  buildLaBridgeScript,
} from './laStorage'

installLaStorageBridge()

type Reply = { ok: boolean; value?: unknown; err?: string } | null

let nsCounter = 0
function makeWin() {
  // unique namespace per test — the module caches its IndexedDB connection,
  // so a fresh IDBFactory alone doesn't reset data between tests
  const ns = 'app-' + nsCounter++
  const w = { __reply: null as Reply, __ns: ns } as unknown as Window & { __reply: Reply; __ns: string }
  ;(w as { postMessage: (m: unknown) => void }).postMessage = (m: unknown) => {
    w.__reply = m as Reply
  }
  return w
}

// Dispatch a request as if from the iframe, wait for the async IDB roundtrip,
// and return the reply the host posted back.
async function bridgeCall(
  w: Window & { __reply: Reply; __ns: string },
  op: string,
  key?: string,
  value?: string,
  autoRegister = true,
): Promise<Reply> {
  w.__reply = null
  if (autoRegister) registerLaIframe(w, w.__ns)
  const ev = new MessageEvent('message', {
    data: { __laBridge: 1, id: 1, ns: w.__ns, op, key, value },
    source: w,
    origin: 'null',
  })
  window.dispatchEvent(ev)
  await new Promise((r) => setTimeout(r, 15))
  return w.__reply
}

beforeEach(() => {
  indexedDB = new IDBFactory()
})

describe('laStorage bridge', () => {
  it('set then dump returns the namespace payload', async () => {
    const w = makeWin()
    // registered inside bridgeCall
    await bridgeCall(w, 'set', 'k1', 'v1')
    await bridgeCall(w, 'set', 'k2', 'v2')
    const r = await bridgeCall(w, 'dump')
    expect(r?.ok).toBe(true)
    expect(r?.value).toEqual({ k1: 'v1', k2: 'v2' })
  })

  it('dump of an empty namespace returns {}', async () => {
    const w = makeWin()
    // registered inside bridgeCall
    const r = await bridgeCall(w, 'dump')
    expect(r?.ok).toBe(true)
    expect(r?.value).toEqual({})
  })

  it('namespaces isolate same keys', async () => {
    const wa = makeWin()
    const wb = makeWin()
    // registered inside bridgeCall
    // registered inside bridgeCall
    await bridgeCall(wa, 'set', 'score', '10')
    await bridgeCall(wb, 'set', 'score', '99')
    const ra = await bridgeCall(wa, 'dump')
    const rb = await bridgeCall(wb, 'dump')
    expect(ra?.value).toEqual({ score: '10' })
    expect(rb?.value).toEqual({ score: '99' })
  })

  it('remove deletes a key; clear only clears the caller namespace', async () => {
    const wa = makeWin()
    const wb = makeWin()
    // registered inside bridgeCall
    // registered inside bridgeCall
    await bridgeCall(wa, 'set', 'k', 'a')
    await bridgeCall(wa, 'set', 'junk', 'x')
    await bridgeCall(wb, 'set', 'k', 'b')
    await bridgeCall(wa, 'remove', 'junk')
    await bridgeCall(wa, 'clear')
    expect((await bridgeCall(wa, 'dump'))?.value).toEqual({})
    expect((await bridgeCall(wb, 'dump'))?.value).toEqual({ k: 'b' })
  })

  it('rejects unregistered iframes', async () => {
    const w = makeWin()
    expect(await bridgeCall(w, 'set', 'k', 'v', false)).toBe(null) // no reply at all
  })

  it('rejects bad keys', async () => {
    const w = makeWin()
    // registered inside bridgeCall
    const tooLong = 'x'.repeat(600)
    expect((await bridgeCall(w, 'set', tooLong, 'v'))?.ok).toBe(false)
    expect((await bridgeCall(w, 'remove', ''))?.ok).toBe(false)
  })

  it('unregister stops the bridge', async () => {
    const w = makeWin()
    await bridgeCall(w, 'set', 'k', 'v') // registers
    unregisterLaIframe(w)
    expect(await bridgeCall(w, 'dump', undefined, undefined, false)).toBe(null)
  })

  it('withLaBridge injects the script before </body>', () => {
    const html = '<html><body><p>hi</p></body></html>'
    const out = withLaBridge(html, 'app-x')
    expect(out).toContain('localStorage')
    expect(out).toContain('<\\/script>') // escaped close tag
    expect(out.indexOf('localStorage')).toBeGreaterThan(out.indexOf('<p>hi</p>'))
    expect(out.endsWith('</body></html>')).toBe(true)
  })

  it('bridge script shims window.localStorage (synchronous, cache-backed)', () => {
    const script = buildLaBridgeScript('app-x')
    // The shim replaces window.localStorage with a synchronous API
    expect(script).toContain("Object.defineProperty(window, 'localStorage'")
    expect(script).toContain('window.parent.postMessage')
    expect(script).toContain('__laStorageReady')
    // Prefetch via dump
    expect(script).toContain("call('dump')")
    // No special app-facing API — plain localStorage calls work
    expect(script).not.toContain('window.__laStorage =')
  })
})

// ── End-to-end: run the real injected script against the real host handler ──
//
// The string assertions above can't see a protocol mismatch between the two
// sides — a reply shape the shim drops still contains all the right substrings.
// These tests execute buildLaBridgeScript's output in a fake iframe window
// whose `parent` posts into the actual host listener, so the whole roundtrip
// (shim -> postMessage -> IndexedDB -> reply -> cache) has to work.

type ShimWin = {
  __ns: string
  localStorage: Storage
  __laStorageReady: Promise<void>
  parent: { postMessage: (data: unknown) => void }
}

function makeShimWin(ns: string): ShimWin {
  const listeners: ((ev: { data: unknown }) => void)[] = []
  const fake = {
    __ns: ns,
    addEventListener: (t: string, fn: (ev: { data: unknown }) => void) => {
      if (t === 'message') listeners.push(fn)
    },
    // The host replies through ev.source.postMessage.
    postMessage: (data: unknown) => listeners.forEach((fn) => fn({ data })),
    parent: {
      postMessage: (data: unknown) => {
        window.dispatchEvent(new MessageEvent('message', { data, source: fake as never, origin: 'null' }))
      },
    },
  }
  return fake as unknown as ShimWin
}

// Boot a Light App document: register the frame, then run the bridge script
// with a `localStorage` that throws like a sandboxed origin's does.
function boot(ns: string): ShimWin {
  const win = makeShimWin(ns)
  registerLaIframe(win as unknown as Window, ns)
  const opaque = new Proxy(
    {},
    {
      get() {
        throw new Error('SecurityError: opaque origin')
      },
    },
  )
  new Function('window', 'localStorage', buildLaBridgeScript(ns))(win, opaque)
  return win
}

describe('injected bridge script (end-to-end)', () => {
  it('persists across reloads: write, reboot, read it back', async () => {
    const ns = 'e2e-' + nsCounter++
    const first = boot(ns)
    await first.__laStorageReady
    first.localStorage.setItem('score', '42')
    await new Promise((r) => setTimeout(r, 15))

    // Fresh document, same app — the prefetch must warm the cache.
    const second = boot(ns)
    await second.__laStorageReady
    expect(second.localStorage.getItem('score')).toBe('42')
    expect(second.localStorage.length).toBe(1)
    expect(second.localStorage.key(0)).toBe('score')
  })

  it('getItem returns null for keys never written', async () => {
    const win = boot('e2e-' + nsCounter++)
    await win.__laStorageReady
    expect(win.localStorage.getItem('nope')).toBe(null)
    expect(win.localStorage.length).toBe(0)
  })

  it('a startup write survives the prefetch that lands after it', async () => {
    const ns = 'e2e-' + nsCounter++
    const seed = boot(ns)
    await seed.__laStorageReady
    seed.localStorage.setItem('score', '42')
    await new Promise((r) => setTimeout(r, 15))

    // Apps commonly write a default synchronously at startup, which always
    // happens before the async dump lands. The dump must not revert it.
    const win = boot(ns)
    win.localStorage.setItem('score', 'DEFAULT-0')
    await win.__laStorageReady
    expect(win.localStorage.getItem('score')).toBe('DEFAULT-0')

    // ...and the cache agrees with what actually got persisted.
    const reload = boot(ns)
    await reload.__laStorageReady
    expect(reload.localStorage.getItem('score')).toBe('DEFAULT-0')
  })

  it('clear() before the prefetch lands is not undone by it', async () => {
    const ns = 'e2e-' + nsCounter++
    const seed = boot(ns)
    await seed.__laStorageReady
    seed.localStorage.setItem('a', '1')
    seed.localStorage.setItem('b', '2')
    await new Promise((r) => setTimeout(r, 15))

    const win = boot(ns)
    win.localStorage.clear()
    await win.__laStorageReady
    expect(win.localStorage.length).toBe(0)
    expect(win.localStorage.getItem('a')).toBe(null)
  })

  it('removeItem is not resurrected by the prefetch', async () => {
    const ns = 'e2e-' + nsCounter++
    const seed = boot(ns)
    await seed.__laStorageReady
    seed.localStorage.setItem('a', '1')
    seed.localStorage.setItem('b', '2')
    await new Promise((r) => setTimeout(r, 15))

    const win = boot(ns)
    win.localStorage.removeItem('a')
    await win.__laStorageReady
    expect(win.localStorage.getItem('a')).toBe(null)
    expect(win.localStorage.getItem('b')).toBe('2')
  })

  it('throws QuotaExceededError instead of caching a write the host would reject', async () => {
    const win = boot('e2e-' + nsCounter++)
    await win.__laStorageReady
    expect(() => win.localStorage.setItem('x', 'v'.repeat(1_000_001))).toThrow(
      expect.objectContaining({ name: 'QuotaExceededError' }),
    )
    expect(() => win.localStorage.setItem('k'.repeat(600), 'v')).toThrow(
      expect.objectContaining({ name: 'QuotaExceededError' }),
    )
    // Nothing was cached, so the cache still matches the store.
    expect(win.localStorage.getItem('x')).toBe(null)
    expect(win.localStorage.length).toBe(0)
  })

  it('a stale document cannot write into the app that replaced it', async () => {
    const ns = 'e2e-' + nsCounter++
    const stale = boot(ns)
    await stale.__laStorageReady

    // The iframe element is reused across app switches: same window, new slug.
    const next = 'e2e-' + nsCounter++
    registerLaIframe(stale as unknown as Window, next)
    stale.localStorage.setItem('leak', 'from-old-app')
    await new Promise((r) => setTimeout(r, 15))

    const victim = boot(next)
    await victim.__laStorageReady
    expect(victim.localStorage.getItem('leak')).toBe(null)
  })

  it('does nothing when localStorage natively works', () => {
    const win = makeShimWin('e2e-native')
    const native = { getItem: () => null } as unknown as Storage
    new Function('window', 'localStorage', buildLaBridgeScript('e2e-native'))(win, native)
    expect(win.localStorage).toBe(undefined) // no shim installed
    expect(win.__laStorageReady).toBe(undefined)
  })

  it('embeds the namespace inertly, even a hostile slug', () => {
    const script = buildLaBridgeScript('a</script><script>alert(1)//')
    expect(script).not.toContain('</script>')
    expect(script).toContain('\\u003c/script\\u003e')
  })
})
