import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { buildStateForm, acceptArtifactState, dropArtifactState } from './laState'

type Call = { url: string; method: string; body: FormData | undefined }

let calls: Call[]

// One identity used across the behavior tests.
const S = 'sess-1'
const P = '/tmp/work/page.html'
const URL_PUT = `/api/sessions/${S}/artifacts/state?path=${encodeURIComponent(P)}`

const push = (msg: Parameters<typeof acceptArtifactState>[2]) => acceptArtifactState(S, P, msg)
const drop = () => dropArtifactState(S, P)

beforeEach(() => {
  vi.useFakeTimers()
  calls = []
  vi.stubGlobal('fetch', (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? 'GET', body: init?.body as FormData | undefined })
    return Promise.resolve(new Response('{}', { status: 200 }))
  })
  // The throttle keeps per-identity state in module scope, and fake timers
  // restart the clock each case — a leftover "last sent" stamp from the
  // previous test reads as the future and postpones the next send past the
  // window.
  drop()
  dropArtifactState('sess-1', '/tmp/other.html')
  dropArtifactState('sess-9', '/tmp/shared.html')
  calls = []
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('acceptArtifactState', () => {
  it('relays a snapshot to the session-scoped endpoint, path as a query', async () => {
    push({ digest: '2 strokes', summary: { nodes: 2 } })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(1)
    expect(calls[0].url).toBe(URL_PUT)
    expect(calls[0].method).toBe('PUT')
    expect(calls[0].body?.get('digest')).toBe('2 strokes')
    expect(calls[0].body?.get('summary')).toBe('{"nodes":2}')
  })

  // A canvas fires on every stroke. Coalescing is what keeps that from
  // becoming one request per pixel.
  it('coalesces a burst into one request carrying the newest state', async () => {
    push({ digest: 'first' })
    push({ digest: 'second' })
    push({ digest: 'third' })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(1)
    expect(calls[0].body?.get('digest')).toBe('third')
  })

  it('spaces out sustained pushes instead of sending them back to back', async () => {
    // The first push goes out at once — nothing has been sent for this page yet.
    push({ digest: 'a' })
    await vi.advanceTimersByTimeAsync(10)
    expect(calls.length).toBe(1)

    // This one lands inside the window, so it has to wait it out rather than
    // following immediately behind.
    push({ digest: 'b' })
    await vi.advanceTimersByTimeAsync(200)
    expect(calls.length).toBe(1)

    await vi.advanceTimersByTimeAsync(1000)
    expect(calls.length).toBe(2)
    expect(calls[1].body?.get('digest')).toBe('b')
  })

  it('keeps pages independent', async () => {
    acceptArtifactState(S, P, { digest: 'one' })
    acceptArtifactState(S, '/tmp/other.html', { digest: 'two' })
    acceptArtifactState('sess-2', P, { digest: 'three' })
    await vi.advanceTimersByTimeAsync(1100)

    // Same path in another session is another identity; so is another path
    // in the same one.
    expect(calls.length).toBe(3)
  })

  it('ignores a push with nothing in it', async () => {
    push({})
    push({ digest: 42 as unknown as string })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(0)
  })

  it('drops an oversized image but still sends the digest', async () => {
    const huge = new Blob([new Uint8Array(13 * 1024 * 1024)])
    push({ digest: 'big one', image: huge })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(1)
    expect(calls[0].body?.get('digest')).toBe('big one')
    expect(calls[0].body?.get('image')).toBeNull()
  })

  it('survives a failing request without throwing', async () => {
    vi.stubGlobal('fetch', () => Promise.reject(new Error('offline')))
    push({ digest: 'x' })
    await expect(vi.advanceTimersByTimeAsync(1100)).resolves.not.toThrow()
  })
})

describe('dropArtifactState', () => {
  // A request already awaiting fetch cannot be recalled. If it lands after the
  // DELETE, the mirror is left describing a page nobody has open — so the
  // flush checks afterwards and undoes itself.
  it('undoes an in-flight push that raced the drop', async () => {
    let release: (v: Response) => void = () => {}
    vi.stubGlobal('fetch', (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method ?? 'GET', body: init?.body as FormData | undefined })
      if (init?.method === 'DELETE') return Promise.resolve(new Response('{}', { status: 200 }))
      return new Promise<Response>((r) => { release = r })
    })

    push({ digest: 'in flight' })
    await vi.advanceTimersByTimeAsync(1100)
    expect(calls.filter((c) => c.method === 'PUT').length).toBe(1)

    // The frame goes away while the PUT is still out.
    drop()
    release(new Response('{}', { status: 200 }))
    await vi.advanceTimersByTimeAsync(50)

    // Two DELETEs: the one the drop sent, and the one the flush sent when it
    // came back and found the page gone.
    expect(calls.filter((c) => c.method === 'DELETE').length).toBe(2)
  })

  it('does not undo a push that belongs to a reopened page', async () => {
    drop()
    calls = []

    push({ digest: 'reopened' })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.filter((c) => c.method === 'PUT').length).toBe(1)
    expect(calls.filter((c) => c.method === 'DELETE').length).toBe(0)
  })

  it('tells the server the page is gone', () => {
    drop()

    expect(calls.length).toBe(1)
    expect(calls[0].url).toBe(URL_PUT)
    expect(calls[0].method).toBe('DELETE')
  })

  it('cancels a queued push — the frame is already gone', async () => {
    push({ digest: 'never sent' })
    drop()
    await vi.advanceTimersByTimeAsync(2000)

    expect(calls.filter((c) => c.method === 'PUT').length).toBe(0)
  })
})

describe('buildStateForm', () => {
  it('omits the parts that are absent', () => {
    const form = buildStateForm({ digest: 'only this', summary: '', image: null })
    expect(form.get('digest')).toBe('only this')
    expect(form.get('summary')).toBeNull()
    expect(form.get('image')).toBeNull()
  })
})
