import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { longpress } from './longpress'

const press = (el: HTMLElement, type: string, x = 0, y = 0) =>
  el.dispatchEvent(new PointerEvent(type, { clientX: x, clientY: y, bubbles: true }))

describe('longpress', () => {
  let el: HTMLElement
  let fn: ReturnType<typeof vi.fn<() => void>>
  let action: ReturnType<typeof longpress>

  beforeEach(() => {
    vi.useFakeTimers()
    el = document.createElement('div')
    fn = vi.fn<() => void>()
    action = longpress(el, fn)
  })
  afterEach(() => {
    action.destroy()
    vi.useRealTimers()
  })

  it('fires once after a held press, even when Android adds a contextmenu', () => {
    press(el, 'pointerdown')
    vi.advanceTimersByTime(499)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    el.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    press(el, 'pointerup')
    expect(fn).toHaveBeenCalledOnce()
    // The next gesture fires again.
    el.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('does not fire for a short tap or a drag', () => {
    press(el, 'pointerdown')
    press(el, 'pointerup')
    vi.advanceTimersByTime(600)
    press(el, 'pointerdown', 0, 0)
    press(el, 'pointermove', 0, 30)
    vi.advanceTimersByTime(600)
    expect(fn).not.toHaveBeenCalled()
  })

  it('fires on contextmenu and suppresses the native menu', () => {
    const ev = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    el.dispatchEvent(ev)
    expect(fn).toHaveBeenCalledOnce()
    expect(ev.defaultPrevented).toBe(true)
  })

  it('leaves the element alone with a null handler', () => {
    action.update(null)
    press(el, 'pointerdown')
    vi.advanceTimersByTime(600)
    const ev = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    el.dispatchEvent(ev)
    expect(fn).not.toHaveBeenCalled()
    expect(ev.defaultPrevented).toBe(false)
  })

  it('stops listening once destroyed', () => {
    action.destroy()
    press(el, 'pointerdown')
    vi.advanceTimersByTime(600)
    expect(fn).not.toHaveBeenCalled()
  })
})
