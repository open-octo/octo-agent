import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { settleScroll } from './settleScroll'

// A pane whose target sits `error` px from where it should, as if the rows
// between had rendered at their real heights after the first scroll.
function pane(error: number) {
  const sc = document.createElement('div')
  const target = document.createElement('div')
  sc.appendChild(target)
  document.body.appendChild(sc)
  let top = 0
  Object.defineProperty(sc, 'scrollTop', { configurable: true, get: () => top, set: (v: number) => { top = v } })
  const offset = () => error - top
  return { sc, target, offset, top: () => top }
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
    settleScroll(p.sc, p.target, p.offset)
    expect(p.top()).toBe(0)
    p.sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(100)
    expect(p.offset()).toBe(0)
  })

  it('falls back to a timer when no scrollend arrives', () => {
    const p = pane(-300)
    settleScroll(p.sc, p.target, p.offset)
    vi.advanceTimersByTime(1100)
    expect(p.offset()).toBe(0)
  })

  it('stops as soon as the user scrolls themselves', () => {
    const p = pane(500)
    settleScroll(p.sc, p.target, p.offset)
    p.sc.dispatchEvent(new Event('wheel'))
    p.sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(2000)
    expect(p.top()).toBe(0)
  })

  it('gives up after a bounded number of frames on an unreachable target', () => {
    const { sc, target } = pane(0)
    let writes = 0
    Object.defineProperty(sc, 'scrollTop', { get: () => 0, set: () => { writes++ } })
    settleScroll(sc, target, () => 400)
    sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(5000)
    expect(writes).toBeGreaterThan(0)
    expect(writes).toBeLessThanOrEqual(30)
  })

  // A session switch reuses the pane but clears the transcript; the target
  // measures at 0 once detached and would drag the new session upward.
  it('stops once the target leaves the DOM', () => {
    const p = pane(500)
    settleScroll(p.sc, p.target, p.offset)
    p.target.remove()
    p.sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(2000)
    expect(p.top()).toBe(0)
  })

  it('settles once when both scrollend and the fallback timer fire', () => {
    const p = pane(0)
    let writes = 0
    Object.defineProperty(p.sc, 'scrollTop', { get: () => 0, set: () => { writes++ } })
    settleScroll(p.sc, p.target, () => 400)
    p.sc.dispatchEvent(new Event('scrollend'))
    vi.advanceTimersByTime(5000)
    expect(writes).toBeLessThanOrEqual(30)
  })
})
