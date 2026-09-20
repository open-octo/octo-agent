import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { deliverToFrame } from './laDelivery'
import { laFrameFor } from './laStorage'

vi.mock('./laStorage', () => ({ laFrameFor: vi.fn() }))

type Posted = { data: Record<string, unknown>; origin: string }

let posted: Posted[]
let fetched: string[]

// Explicit stand-ins rather than `new Response(blob)`: only two members are
// read here, and the real Response differs enough between Node majors that
// building one made the suite pass locally and fail on CI.
function okResponse(body: Blob): Response {
  return { ok: true, status: 200, blob: () => Promise.resolve(body) } as unknown as Response
}

function errorResponse(status: number): Response {
  return {
    ok: false,
    status,
    blob: () => Promise.reject(new Error('no body')),
  } as unknown as Response
}

function fakeFrame(): Window {
  return {
    postMessage: (data: Record<string, unknown>, origin: string) => {
      posted.push({ data, origin })
    },
  } as unknown as Window
}

beforeEach(() => {
  posted = []
  fetched = []
  vi.mocked(laFrameFor).mockReset()
  vi.stubGlobal('fetch', (url: string) => {
    fetched.push(url)
    return Promise.resolve(okResponse(new Blob(['PNGBYTES'])))
  })
})

afterEach(() => vi.unstubAllGlobals())

describe('deliverToFrame', () => {
  it('redeems the ticket and hands the bytes to the app', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())

    const out = await deliverToFrame({ slug: 'sketch', id: 'abc123', name: 'gen.png', note: 'from your sketch' })

    expect(out).toBe('delivered')
    expect(fetched).toEqual(['/api/light-apps/sketch/delivery/abc123'])
    expect(posted.length).toBe(1)
    expect(posted[0].data.op).toBe('delivery')
    expect(posted[0].data.ns).toBe('sketch')
    expect(posted[0].data.name).toBe('gen.png')
    expect(posted[0].data.note).toBe('from your sketch')
    expect(posted[0].data.blob).toBeInstanceOf(Blob)
  })

  it('escapes both path segments', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())

    await deliverToFrame({ slug: 'a/b', id: 'x y' })

    expect(fetched[0]).toBe('/api/light-apps/a%2Fb/delivery/x%20y')
  })

  // The user may have closed the app between the tool call and the event. That
  // is not an error worth surfacing — the ticket simply expires.
  it('does nothing when the app is not open', async () => {
    vi.mocked(laFrameFor).mockReturnValue(null)

    expect(await deliverToFrame({ slug: 'sketch', id: 'abc' })).toBe('no-frame')
    expect(fetched.length).toBe(0)
    expect(posted.length).toBe(0)
  })

  it('does not post anything when the ticket is refused', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())
    vi.stubGlobal('fetch', () => Promise.resolve(errorResponse(404)))

    expect(await deliverToFrame({ slug: 'sketch', id: 'gone' })).toBe('failed')
    expect(posted.length).toBe(0)
  })

  it('survives a network failure', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())
    vi.stubGlobal('fetch', () => Promise.reject(new Error('offline')))

    expect(await deliverToFrame({ slug: 'sketch', id: 'abc' })).toBe('failed')
    expect(posted.length).toBe(0)
  })

  it('ignores a malformed announcement', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())

    expect(await deliverToFrame({ slug: 'sketch' })).toBe('failed')
    expect(await deliverToFrame({ id: 'abc' })).toBe('failed')
    expect(await deliverToFrame({ slug: 42, id: 'abc' })).toBe('failed')
    expect(fetched.length).toBe(0)
  })
})
