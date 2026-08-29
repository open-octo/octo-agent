import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import 'fake-indexeddb/auto'
import { registerLaIframe, installLaStorageBridge, withLaBridge } from './laStorage'
import { buildLaDownloadScript, sanitizeDownloadName, MAX_DOWNLOAD_BYTES } from './laDownload'
import { nativeShell, toasts } from './stores'
import { tr } from './i18n'
import { get } from 'svelte/store'
import * as api from './api'

installLaStorageBridge()

let nsCounter = 0

// Post a bridge message as if from a registered Light App iframe.
function fromApp(data: Record<string, unknown>): void {
  const ns = 'dl-' + nsCounter++
  const w = { postMessage: () => {} } as unknown as Window
  registerLaIframe(w, ns)
  window.dispatchEvent(new MessageEvent('message', { data: { __laBridge: 1, id: 0, ns, ...data }, source: w, origin: 'null' }))
}

const tick = () => new Promise((r) => setTimeout(r, 15))

// jsdom has no object URLs; the browser path only needs them to be callable.
beforeEach(() => {
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:host/x', revokeObjectURL: () => {} }))
  nativeShell.set(false)
})
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('sanitizeDownloadName', () => {
  it('strips path separators and control chars, falls back when empty', () => {
    expect(sanitizeDownloadName('../../etc/passwd')).toBe('.._.._etc_passwd')
    expect(sanitizeDownloadName('a\\b\u0000c.txt')).toBe('a_b_c.txt')
    expect(sanitizeDownloadName('  报表.xlsx ')).toBe('报表.xlsx')
    expect(sanitizeDownloadName('')).toBe('download')
    expect(sanitizeDownloadName('..')).toBe('download')
    expect(sanitizeDownloadName(42)).toBe('download')
    expect(sanitizeDownloadName('x'.repeat(300))).toHaveLength(255)
  })
})

describe('host download handler', () => {
  // Record the host's anchor clicks instead of letting jsdom act on them.
  // Scoped to this block: the injected-script tests below need the real
  // click() so an in-document anchor's event actually reaches the document.
  let clicked: HTMLAnchorElement[] = []
  beforeEach(() => {
    clicked = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this)
    })
  })

  it('browser: saves through a top-level anchor named after the request', async () => {
    fromApp({ op: 'download', name: 'out/report.csv', blob: new Blob(['a,b']) })
    await tick()
    expect(clicked).toHaveLength(1)
    expect(clicked[0].download).toBe('out_report.csv')
    expect(clicked[0].isConnected).toBe(false) // appended for the click, removed after
  })

  it('desktop: routes the bytes base64-encoded through the native save dialog', async () => {
    nativeShell.set(true)
    const save = vi.spyOn(api, 'nativeSaveBinary').mockResolvedValue({ path: '/tmp/x.png', cancelled: false })
    fromApp({ op: 'download', name: 'x.png', blob: new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47])]) })
    await tick()
    expect(save).toHaveBeenCalledWith('x.png', 'iVBORw==')
    expect(clicked).toHaveLength(0)
  })

  it('drops non-Blob payloads and oversize blobs', async () => {
    fromApp({ op: 'download', name: 'a.txt', blob: 'not a blob' })
    fromApp({ op: 'download', name: 'a.txt', blob: { size: 1 } })
    const big = { size: MAX_DOWNLOAD_BYTES + 1 }
    Object.setPrototypeOf(big, Blob.prototype)
    fromApp({ op: 'download', name: 'a.txt', blob: big })
    await tick()
    expect(clicked).toHaveLength(0)
  })

  it('ignores downloads from unregistered windows', async () => {
    const w = { postMessage: () => {} } as unknown as Window
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { __laBridge: 1, id: 0, ns: 'nope', op: 'download', name: 'a.txt', blob: new Blob(['x']) },
        source: w,
        origin: 'null',
      }),
    )
    await tick()
    expect(clicked).toHaveLength(0)
  })

  it('desktop: a failed native save toasts an error, a cancelled one toasts nothing', async () => {
    nativeShell.set(true)
    toasts.set([])
    vi.spyOn(api, 'nativeSaveBinary').mockRejectedValueOnce(new Error('boom'))
    fromApp({ op: 'download', name: 'a.txt', blob: new Blob(['x']) })
    await tick()
    expect(get(toasts).map((t) => [t.msg, t.type])).toEqual([[tr('artifacts.save_failed'), 'error']])

    toasts.set([])
    vi.spyOn(api, 'nativeSaveBinary').mockResolvedValueOnce({ path: '', cancelled: true })
    fromApp({ op: 'download', name: 'a.txt', blob: new Blob(['x']) })
    await tick()
    expect(get(toasts)).toEqual([])
  })

  it('desktop: one save dialog at a time — requests during an open one are dropped', async () => {
    nativeShell.set(true)
    const save = vi
      .spyOn(api, 'nativeSaveBinary')
      .mockImplementation(() => new Promise((r) => setTimeout(() => r({ path: '/tmp/a', cancelled: false }), 40)))
    fromApp({ op: 'download', name: 'a.txt', blob: new Blob(['1']) })
    fromApp({ op: 'download', name: 'b.txt', blob: new Blob(['2']) })
    await new Promise((r) => setTimeout(r, 80))
    expect(save).toHaveBeenCalledTimes(1)
    expect(save.mock.calls[0][0]).toBe('a.txt')
    // Once it settles the next one goes through.
    fromApp({ op: 'download', name: 'c.txt', blob: new Blob(['3']) })
    await new Promise((r) => setTimeout(r, 80))
    expect(save).toHaveBeenCalledTimes(2)
  })
})

// ── The injected script, run against the real jsdom document ────────────────

type Sent = { op: string; name: string; blob: Blob; ns: string; id: number }

// Each boot registers a document-level click listener; route registration
// through a proxy so afterEach can remove them, or an earlier app's listener
// would swallow (preventDefault) the clicks of a later test.
let docListeners: Array<[string, EventListener]> = []
const docProxy = {
  addEventListener(type: string, fn: EventListener) {
    document.addEventListener(type, fn)
    docListeners.push([type, fn])
  },
}

function bootShim(ns: string): Sent[] {
  const sent: Sent[] = []
  const fakeWin = { parent: { postMessage: (d: Sent) => sent.push(d) } }
  new Function('window', 'document', buildLaDownloadScript(ns))(fakeWin, docProxy)
  return sent
}

describe('injected download script', () => {
  let origClick: typeof HTMLAnchorElement.prototype.click
  beforeEach(() => {
    origClick = HTMLAnchorElement.prototype.click
    vi.stubGlobal('fetch', async (url: string) => ({ blob: async () => new Blob([`bytes-of:${url}`]) }))
  })
  afterEach(() => {
    HTMLAnchorElement.prototype.click = origClick
    docListeners.forEach(([t, fn]) => document.removeEventListener(t, fn))
    docListeners = []
    document.body.innerHTML = ''
  })

  it('a user click on an in-document <a download> is intercepted and posted', async () => {
    const sent = bootShim('app-a')
    const a = document.createElement('a')
    a.href = 'blob:null/abc'
    a.download = 'report.csv'
    a.textContent = 'Export'
    document.body.appendChild(a)
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true })
    a.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    await tick()
    expect(sent).toHaveLength(1)
    expect(sent[0]).toMatchObject({ __laBridge: 1, id: 0, ns: 'app-a', op: 'download', name: 'report.csv' })
    expect(await sent[0].blob.text()).toBe('bytes-of:blob:null/abc')
  })

  it('a click inside the anchor (nested element) still resolves to the anchor', async () => {
    const sent = bootShim('app-b')
    const a = document.createElement('a')
    a.href = 'data:text/plain,hi'
    a.setAttribute('download', '')
    const span = document.createElement('span')
    a.appendChild(span)
    document.body.appendChild(a)
    span.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    await tick()
    expect(sent).toHaveLength(1)
    expect(sent[0].name).toBe('') // host picks the fallback
  })

  it('programmatic click() on a detached anchor is intercepted too', async () => {
    const sent = bootShim('app-c')
    const a = document.createElement('a')
    a.href = 'blob:null/detached'
    a.download = 'chart.png'
    a.click()
    await tick()
    expect(sent).toHaveLength(1)
    expect(sent[0].name).toBe('chart.png')
  })

  it('leaves plain links and already-handled clicks alone', async () => {
    const sent = bootShim('app-d')
    const plain = document.createElement('a')
    plain.href = '#plain' // in-page, so jsdom's default action is a no-op rather than a navigation
    document.body.appendChild(plain)
    const ev1 = new MouseEvent('click', { bubbles: true, cancelable: true })
    plain.dispatchEvent(ev1)
    expect(ev1.defaultPrevented).toBe(false)

    const handled = document.createElement('a')
    handled.href = 'blob:null/handled'
    handled.download = 'x.txt'
    handled.addEventListener('click', (e) => e.preventDefault())
    document.body.appendChild(handled)
    handled.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))

    await tick()
    expect(sent).toHaveLength(0)
  })

  it('programmatic click() on an in-document anchor posts exactly once (patch defers, listener handles)', async () => {
    const sent = bootShim('app-f')
    const a = document.createElement('a')
    a.href = 'blob:null/attached'
    a.download = 'once.txt'
    document.body.appendChild(a)
    a.click()
    await tick()
    expect(sent).toHaveLength(1)
  })

  it('fetches the href synchronously inside click(), so revoking the blob URL right after is safe', () => {
    const fetched: string[] = []
    vi.stubGlobal('fetch', (url: string) => {
      fetched.push(url)
      return new Promise(() => {}) // never settles; only the call timing matters here
    })
    bootShim('app-g')
    const a = document.createElement('a')
    a.href = 'blob:null/sync'
    a.download = 'x.bin'
    a.click()
    // Nothing awaited yet: the textbook `a.click(); URL.revokeObjectURL(url)`
    // must find the fetch already issued.
    expect(fetched).toEqual(['blob:null/sync'])
  })

  it('a failed fetch posts nothing and throws nothing', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('Failed to fetch')
    })
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const sent = bootShim('app-h')
    const a = document.createElement('a')
    a.href = 'blob:null/gone'
    a.download = 'x.bin'
    expect(() => a.click()).not.toThrow()
    await tick()
    expect(sent).toHaveLength(0)
    expect(warn).toHaveBeenCalled()
  })

  it('an <a download> without href is not intercepted', async () => {
    const sent = bootShim('app-i')
    const a = document.createElement('a')
    a.download = 'nothing.txt'
    document.body.appendChild(a)
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true })
    a.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
    await tick()
    expect(sent).toHaveLength(0)
  })

  it('does nothing when not framed', () => {
    const sent: Sent[] = []
    const top = { parent: null as unknown }
    top.parent = top
    const before = HTMLAnchorElement.prototype.click
    new Function('window', 'document', buildLaDownloadScript('app-e'))(top, docProxy)
    expect(HTMLAnchorElement.prototype.click).toBe(before)
    expect(sent).toHaveLength(0)
  })

  it('withLaBridge injects the download script next to the storage shim', () => {
    const out = withLaBridge('<html><body><p>hi</p></body></html>', 'app-x')
    expect(out).toContain("op: 'download'")
    expect(out).toContain('HTMLAnchorElement.prototype.click')
    expect(out.endsWith('</body></html>')).toBe(true)
  })
})
