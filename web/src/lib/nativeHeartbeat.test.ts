import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// isDesktopShell is read at module scope by the heartbeat, so it's mocked per
// suite: the whole point of the first suite is that a plain browser sends
// nothing at all (the endpoint only exists in the desktop shell).
const mocks = vi.hoisted(() => ({ isDesktopShell: false, request: vi.fn() }))
vi.mock('./stores', () => ({
  get isDesktopShell() {
    return mocks.isDesktopShell
  },
}))
vi.mock('./api', () => ({ request: mocks.request }))

import { startNativeHeartbeat } from './nativeHeartbeat'

// The shell contract: frame_age_ms < 0 means "not rendering, by design"
// (hidden, or no frame observed yet); >= 0 is the measured age of a real frame.
const NO_FRAMES = -1

function lastBody() {
  const calls = mocks.request.mock.calls
  return JSON.parse(calls[calls.length - 1][1].body as string)
}

// rAF is driven manually so the tests can model the three states that matter:
// frames flowing, frames stalled (black window), and page hidden.
let rafCallbacks: Map<number, FrameRequestCallback>
let nextRafId: number
// Every start() is torn down in afterEach: a test that fails before its own
// stop() would otherwise leave listeners on the shared jsdom window, and the
// next test's focus/visibility beats would be counted several times over.
let stops: Array<() => void>

function start() {
  const stop = startNativeHeartbeat()
  stops.push(stop)
  return stop
}

function flushFrame() {
  const pending = [...rafCallbacks.entries()]
  rafCallbacks.clear()
  for (const [, cb] of pending) cb(performance.now())
}

beforeEach(() => {
  rafCallbacks = new Map()
  nextRafId = 1
  stops = []
  // Fake the clock but NOT requestAnimationFrame: the tests drive frames by
  // hand, since "did a frame land" is the signal under test. performance is
  // faked so the reported frame age advances with the virtual clock.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date', 'performance'] })
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
    const id = nextRafId++
    rafCallbacks.set(id, cb)
    return id
  })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => {
    rafCallbacks.delete(id)
  })
  mocks.request.mockReset()
  mocks.request.mockResolvedValue({ ok: true })
})
afterEach(() => {
  for (const stop of stops) stop()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('startNativeHeartbeat outside the desktop shell', () => {
  beforeEach(() => {
    mocks.isDesktopShell = false
  })

  it('sends no beats and its stop function is safe to call', () => {
    const stop = start()
    vi.advanceTimersByTime(60_000)
    expect(mocks.request).not.toHaveBeenCalled()
    expect(() => stop()).not.toThrow()
  })

  it('requests no animation frames at all', () => {
    const stop = start()
    vi.advanceTimersByTime(60_000)
    expect(rafCallbacks.size).toBe(0)
    stop()
  })
})

describe('startNativeHeartbeat inside the desktop shell', () => {
  beforeEach(() => {
    mocks.isDesktopShell = true
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  })

  it('beats immediately, then on the interval, and stops when told', () => {
    const stop = start()
    expect(mocks.request).toHaveBeenCalledTimes(1) // immediate first beat

    vi.advanceTimersByTime(5_000)
    expect(mocks.request).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(5_000)
    expect(mocks.request).toHaveBeenCalledTimes(3)

    stop()
    vi.advanceTimersByTime(30_000)
    expect(mocks.request).toHaveBeenCalledTimes(3) // no beats after stop
  })

  it('posts to the shell endpoint as JSON', () => {
    const stop = start()
    const [path, init] = mocks.request.mock.calls[0]
    expect(path).toBe('/api/native/heartbeat')
    expect(init.method).toBe('POST')
    stop()
  })

  it('reports no-frames until a frame is actually observed, then its age', () => {
    const stop = start()
    // First beat: no frame has been seen yet, so the shell must not read the
    // absence as a black window.
    expect(lastBody().frame_age_ms).toBe(NO_FRAMES)

    // A frame lands, then the next beat reports a real (small) age.
    flushFrame()
    vi.advanceTimersByTime(5_000)
    const age = lastBody().frame_age_ms
    expect(age).toBeGreaterThanOrEqual(0)
    expect(age).toBeLessThan(5_000 + 1_000)
    stop()
  })

  it('lets the reported frame age grow while frames are stalled (black window)', () => {
    const stop = start()
    flushFrame() // one good frame, then the compositor dies
    vi.advanceTimersByTime(5_000)
    const first = lastBody().frame_age_ms
    // No further frames are flushed: each beat should report an older frame.
    vi.advanceTimersByTime(5_000)
    const second = lastBody().frame_age_ms
    vi.advanceTimersByTime(5_000)
    const third = lastBody().frame_age_ms
    expect(second).toBeGreaterThan(first)
    expect(third).toBeGreaterThan(second)
    stop()
  })

  it('reports no-frames while the page is hidden, whatever the frame history', () => {
    const stop = start()
    flushFrame()
    vi.advanceTimersByTime(5_000)
    expect(lastBody().frame_age_ms).toBeGreaterThanOrEqual(0)

    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    vi.advanceTimersByTime(5_000)
    expect(lastBody().frame_age_ms).toBe(NO_FRAMES)
    stop()
  })

  it('samples one frame per beat rather than running a continuous rAF loop', () => {
    const stop = start()
    // Exactly one request outstanding after a beat; flushing it doesn't chain
    // another (that would be the per-vsync loop this design avoids).
    expect(rafCallbacks.size).toBe(1)
    flushFrame()
    expect(rafCallbacks.size).toBe(0)
    vi.advanceTimersByTime(5_000)
    expect(rafCallbacks.size).toBe(1)
    stop()
  })

  it('beats on window focus and on becoming visible', () => {
    const stop = start()
    mocks.request.mockClear()

    window.dispatchEvent(new Event('focus'))
    expect(mocks.request).toHaveBeenCalledTimes(1)

    document.dispatchEvent(new Event('visibilitychange'))
    expect(mocks.request).toHaveBeenCalledTimes(2)

    // Hidden: visibilitychange must not beat (nothing to prove, and the shell
    // treats silence after a show as needing a new window only when the page
    // was asked to be visible).
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(mocks.request).toHaveBeenCalledTimes(2)
    stop()
  })

  it('survives a failed beat without throwing', async () => {
    mocks.request.mockRejectedValue(new Error('hub unreachable'))
    const stop = start()
    await vi.advanceTimersByTimeAsync(5_000)
    expect(mocks.request).toHaveBeenCalled()
    stop()
  })

  it('cancels the outstanding frame request on stop', () => {
    const stop = start()
    expect(rafCallbacks.size).toBe(1)
    stop()
    expect(rafCallbacks.size).toBe(0)
  })
})
