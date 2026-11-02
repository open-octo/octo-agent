import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { settleScroll } from './settleScroll'

// A pane whose target sits `error` px from where it should, as if the rows
// between had rendered at their real heights after the first scroll.
function pane(error: number) {
  const sc = document.createElement('div')
  let top = 0
  Object.defineProperty(sc, 'scrollTop', { get: () => top, set: (v: number) => { top = v } })
  const offset = () => error - top
  return { sc, offset, top: () => top }
}

describe('settleScroll', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => setTimeout(() => cb(0), 16) as unknown as number)
    vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id))
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('corrects the landing once the scroll has ended', () => {
    const p = pane(640)
    settleScroll(p.sc, p.offset)
    expect(p.top()).toBe(0)
    p.sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(100)
    expect(p.offset()).toBe(0)
  })

  it('falls back to a timer when no scrollend arrives', () => {
    const p = pane(-300)
    settleScroll(p.sc, p.offset)
    vi.advanceTimersByTime(1100)
    expect(p.offset()).toBe(0)
  })

  it('stops as soon as the user scrolls themselves', () => {
    const p = pane(500)
    settleScroll(p.sc, p.offset)
    p.sc.dispatchEvent(new Event('wheel'))
    p.sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(2000)
    expect(p.top()).toBe(0)
  })

  it('gives up after a bounded number of frames on an unreachable target', () => {
    const sc = document.createElement('div')
    let writes = 0
    Object.defineProperty(sc, 'scrollTop', { get: () => 0, set: () => { writes++ } })
    settleScroll(sc, () => 400)
    sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(5000)
    expect(writes).toBeGreaterThan(0)
    expect(writes).toBeLessThanOrEqual(30)
  })
})
