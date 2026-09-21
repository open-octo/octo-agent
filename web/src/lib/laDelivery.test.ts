import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { deliverToFrame } from './laDelivery'
import { laFrameFor } from './laStorage'
import { blobResponse, errorResponse } from '../test/fetchStub'

vi.mock('./laStorage', () => ({ laFrameFor: vi.fn() }))

type Posted = { data: Record<string, unknown>; origin: string }

let posted: Posted[]
let fetched: string[]

// The registry hands back the window and the origin it was registered with;
// the bytes may only be posted to that origin.
const ART_ORIGIN = 'http://tok-abc.artifacts.localhost:8088'

function fakeFrame(origin = ART_ORIGIN): { win: Window; origin: string } {
  return {
    win: {
      postMessage: (data: Record<string, unknown>, target: string) => {
        posted.push({ data, origin: target })
      },
    } as unknown as Window,
    origin,
  }
}

beforeEach(() => {
  posted = []
  fetched = []
  vi.mocked(laFrameFor).mockReset()
  vi.stubGlobal('fetch', (url: string) => {
    fetched.push(url)
    return Promise.resolve(blobResponse('PNGBYTES'))
  })
})

afterEach(() => vi.unstubAllGlobals())

describe('deliverToFrame', () => {
  it('redeems the ticket and hands the bytes to the frame showing the artifact', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())

    const out = await deliverToFrame({ session: 's1', path: '/tmp/page.html', id: 'abc123', name: 'gen.png', note: 'from the brief' })

    expect(out).toBe('delivered')
    expect(fetched).toEqual(['/api/sessions/s1/artifacts/delivery/abc123'])
    // The frame lookup is by the same composite identity the bridge stamps.
    expect(laFrameFor).toHaveBeenCalledWith('s1\n/tmp/page.html')
    expect(posted.length).toBe(1)
    expect(posted[0].data.op).toBe('delivery')
    expect(posted[0].data.ns).toBe('s1\n/tmp/page.html')
    expect(posted[0].data.name).toBe('gen.png')
    expect(posted[0].data.note).toBe('from the brief')
    expect(posted[0].data.blob).toBeInstanceOf(Blob)
    // Never '*': the frame may have navigated itself away since it registered.
    expect(posted[0].origin).toBe(ART_ORIGIN)
  })

  // A registration with no origin to post to is not a target. Nothing is
  // fetched either — the ticket is better left to expire than spent on a
  // frame the bytes cannot safely reach.
  it('does not deliver to a frame with no pinned origin', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame(''))

    expect(await deliverToFrame({ session: 's1', path: '/tmp/p.html', id: 'abc' })).toBe('no-frame')
    expect(fetched.length).toBe(0)
    expect(posted.length).toBe(0)
  })

  it('escapes the session, the path and the ticket', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())

    await deliverToFrame({ session: 'a/b', path: '/tmp/x y.html', id: 'x y' })

    expect(fetched[0]).toBe('/api/sessions/a%2Fb/artifacts/delivery/x%20y')
  })

  // The user may have closed the panel between the tool call and the event.
  // That is not an error worth surfacing — the ticket simply expires.
  it('does nothing when the artifact is not open', async () => {
    vi.mocked(laFrameFor).mockReturnValue(null)

    expect(await deliverToFrame({ session: 's1', path: '/tmp/p.html', id: 'abc' })).toBe('no-frame')
    expect(fetched.length).toBe(0)
    expect(posted.length).toBe(0)
  })

  it('does not post anything when the ticket is refused', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())
    vi.stubGlobal('fetch', () => Promise.resolve(errorResponse(404)))

    expect(await deliverToFrame({ session: 's1', path: '/tmp/p.html', id: 'gone' })).toBe('failed')
    expect(posted.length).toBe(0)
  })

  it('survives a network failure', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())
    vi.stubGlobal('fetch', () => Promise.reject(new Error('offline')))

    expect(await deliverToFrame({ session: 's1', path: '/tmp/p.html', id: 'abc' })).toBe('failed')
    expect(posted.length).toBe(0)
  })

  it('ignores a malformed announcement', async () => {
    vi.mocked(laFrameFor).mockReturnValue(fakeFrame())

    expect(await deliverToFrame({ session: 's1', path: '/tmp/p.html' })).toBe('failed')
    expect(await deliverToFrame({ id: 'abc' })).toBe('failed')
    expect(await deliverToFrame({ session: 42, path: '/tmp/p.html', id: 'abc' })).toBe('failed')
    expect(fetched.length).toBe(0)
  })
})
