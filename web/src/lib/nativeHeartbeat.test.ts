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

describe('startNativeHeartbeat outside the desktop shell', () => {
  beforeEach(() => {
    mocks.isDesktopShell = false
    mocks.request.mockReset()
    mocks.request.mockResolvedValue({ ok: true })
    vi.useFakeTimers()
  })
  afterEach(() => vi.useRealTimers())

  it('sends no beats and its stop function is safe to call', () => {
    const stop = startNativeHeartbeat()
    vi.advanceTimersByTime(60_000)
    expect(mocks.request).not.toHaveBeenCalled()
    expect(() => stop()).not.toThrow()
  })
})

describe('startNativeHeartbeat inside the desktop shell', () => {
  beforeEach(() => {
    mocks.isDesktopShell = true
    mocks.request.mockReset()
    mocks.request.mockResolvedValue({ ok: true })
    vi.useFakeTimers()
  })
  afterEach(() => vi.useRealTimers())

  it('beats immediately, then on the interval, and stops when told', () => {
    const stop = startNativeHeartbeat()
    expect(mocks.request).toHaveBeenCalledTimes(1) // immediate first beat

    vi.advanceTimersByTime(5_000)
    expect(mocks.request).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(5_000)
    expect(mocks.request).toHaveBeenCalledTimes(3)

    stop()
    vi.advanceTimersByTime(30_000)
    expect(mocks.request).toHaveBeenCalledTimes(3) // no beats after stop
  })

  it('posts a non-negative frame age the shell can read', () => {
    const stop = startNativeHeartbeat()
    const [path, init] = mocks.request.mock.calls[0]
    expect(path).toBe('/api/native/heartbeat')
    expect(init.method).toBe('POST')
    const body = JSON.parse(init.body as string)
    expect(typeof body.frame_age_ms).toBe('number')
    expect(body.frame_age_ms).toBeGreaterThanOrEqual(0)
    stop()
  })

  it('beats on window focus so a shown window proves life promptly', () => {
    const stop = startNativeHeartbeat()
    mocks.request.mockClear()
    window.dispatchEvent(new Event('focus'))
    expect(mocks.request).toHaveBeenCalledTimes(1)
    stop()
  })

  it('survives a failed beat without throwing', async () => {
    mocks.request.mockRejectedValue(new Error('hub unreachable'))
    const stop = startNativeHeartbeat()
    await vi.advanceTimersByTimeAsync(5_000)
    expect(mocks.request).toHaveBeenCalled()
    stop()
  })
})
