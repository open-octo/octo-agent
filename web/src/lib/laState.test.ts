import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { acceptLaState, dropLaState, buildStateForm, acceptArtifactState, dropArtifactState } from './laState'

type Call = { url: string; method: string; body: FormData | undefined }

let calls: Call[]

beforeEach(() => {
  vi.useFakeTimers()
  calls = []
  vi.stubGlobal('fetch', (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? 'GET', body: init?.body as FormData | undefined })
    return Promise.resolve(new Response('{}', { status: 200 }))
  })
  // The throttle keeps per-app state in module scope, and fake timers restart
  // the clock each case — a leftover "last sent" stamp from the previous test
  // reads as the future and postpones the next send past the window.
  dropLaState('sketch')
  dropLaState('board')
  calls = []
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('acceptLaState', () => {
  it('relays a snapshot to the app\'s own state endpoint', async () => {
    acceptLaState('sketch', { digest: '2 strokes', summary: { nodes: 2 } })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(1)
    expect(calls[0].url).toBe('/api/light-apps/sketch/state')
    expect(calls[0].method).toBe('PUT')
    expect(calls[0].body?.get('digest')).toBe('2 strokes')
    expect(calls[0].body?.get('summary')).toBe('{"nodes":2}')
  })

  it('escapes a slug that would otherwise reshape the URL', async () => {
    acceptLaState('a/b', { digest: 'x' })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls[0].url).toBe('/api/light-apps/a%2Fb/state')
  })

  // A canvas fires on every stroke. Coalescing is what keeps that from
  // becoming one request per pixel.
  it('coalesces a burst into one request carrying the newest state', async () => {
    acceptLaState('sketch', { digest: 'first' })
    acceptLaState('sketch', { digest: 'second' })
    acceptLaState('sketch', { digest: 'third' })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(1)
    expect(calls[0].body?.get('digest')).toBe('third')
  })

  it('spaces out sustained pushes instead of sending them back to back', async () => {
    // The first push goes out at once — nothing has been sent for this app yet.
    acceptLaState('sketch', { digest: 'a' })
    await vi.advanceTimersByTimeAsync(10)
    expect(calls.length).toBe(1)

    // This one lands inside the window, so it has to wait it out rather than
    // following immediately behind.
    acceptLaState('sketch', { digest: 'b' })
    await vi.advanceTimersByTimeAsync(200)
    expect(calls.length).toBe(1)

    await vi.advanceTimersByTimeAsync(1000)
    expect(calls.length).toBe(2)
    expect(calls[1].body?.get('digest')).toBe('b')
  })

  it('keeps apps independent', async () => {
    acceptLaState('sketch', { digest: 'from sketch' })
    acceptLaState('board', { digest: 'from board' })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.map((c) => c.url).sort()).toEqual([
      '/api/light-apps/board/state',
      '/api/light-apps/sketch/state',
    ])
  })

  it('ignores a push with nothing in it', async () => {
    acceptLaState('sketch', {})
    acceptLaState('sketch', { digest: 42 as unknown as string })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(0)
  })

  it('drops an oversized image but still sends the digest', async () => {
    const huge = new Blob([new Uint8Array(13 * 1024 * 1024)])
    acceptLaState('sketch', { digest: 'big one', image: huge })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(1)
    expect(calls[0].body?.get('digest')).toBe('big one')
    expect(calls[0].body?.get('image')).toBeNull()
  })

  it('survives a failing request without throwing', async () => {
    vi.stubGlobal('fetch', () => Promise.reject(new Error('offline')))
    acceptLaState('sketch', { digest: 'x' })
    await expect(vi.advanceTimersByTimeAsync(1100)).resolves.not.toThrow()
  })
})

describe('dropLaState', () => {
  // A request already awaiting fetch cannot be recalled. If it lands after the
  // DELETE, the mirror is left describing an app nobody has open — so the
  // flush checks afterwards and undoes itself.
  it('undoes an in-flight push that raced the drop', async () => {
    let release: (v: Response) => void = () => {}
    vi.stubGlobal('fetch', (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method ?? 'GET', body: init?.body as FormData | undefined })
      if (init?.method === 'DELETE') return Promise.resolve(new Response('{}', { status: 200 }))
      return new Promise<Response>((r) => { release = r })
    })

    acceptLaState('sketch', { digest: 'in flight' })
    await vi.advanceTimersByTimeAsync(1100)
    expect(calls.filter((c) => c.method === 'PUT').length).toBe(1)

    // The frame goes away while the PUT is still out.
    dropLaState('sketch')
    release(new Response('{}', { status: 200 }))
    await vi.advanceTimersByTimeAsync(50)

    // Two DELETEs: the one the drop sent, and the one the flush sent when it
    // came back and found the app gone.
    expect(calls.filter((c) => c.method === 'DELETE').length).toBe(2)
  })

  it('does not undo a push that belongs to a reopened app', async () => {
    dropLaState('sketch')
    calls = []

    acceptLaState('sketch', { digest: 'reopened' })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.filter((c) => c.method === 'PUT').length).toBe(1)
    expect(calls.filter((c) => c.method === 'DELETE').length).toBe(0)
  })

  it('tells the server the app is gone', () => {
    dropLaState('sketch')

    expect(calls.length).toBe(1)
    expect(calls[0].url).toBe('/api/light-apps/sketch/state')
    expect(calls[0].method).toBe('DELETE')
  })

  it('cancels a queued push — the frame is already gone', async () => {
    acceptLaState('sketch', { digest: 'never sent' })
    dropLaState('sketch')
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

describe('acceptArtifactState', () => {
  it('relays to the session-scoped artifact endpoint, path as a query', async () => {
    acceptArtifactState('sess-1', '/tmp/work/dir/a page.html', { digest: 'a chart' })
    await vi.advanceTimersByTimeAsync(1100)

    expect(calls.length).toBe(1)
    expect(calls[0].url).toBe('/api/sessions/sess-1/artifacts/state?path=' + encodeURIComponent('/tmp/work/dir/a page.html'))
    expect(calls[0].method).toBe('PUT')
    expect(calls[0].body?.get('digest')).toBe('a chart')
  })

  it('shares the throttle with light apps but not the bucket', async () => {
    acceptLaState('sketch', { digest: 'app' })
    acceptArtifactState('sess-1', '/tmp/a.html', { digest: 'page' })
    await vi.advanceTimersByTimeAsync(1100)

    // Different identities, different queues: both go out.
    expect(calls.length).toBe(2)
    const urls = calls.map((c) => c.url).sort()
    expect(urls[0]).toBe('/api/light-apps/sketch/state')
    expect(urls[1]).toContain('/api/sessions/sess-1/artifacts/state')
  })

  it('drop sends the session-scoped DELETE', async () => {
    acceptArtifactState('sess-1', '/tmp/a.html', { digest: 'x' })
    await vi.advanceTimersByTimeAsync(1100)
    dropArtifactState('sess-1', '/tmp/a.html')

    const del = calls.find((c) => c.method === 'DELETE')
    expect(del?.url).toBe('/api/sessions/sess-1/artifacts/state?path=' + encodeURIComponent('/tmp/a.html'))
  })
})
