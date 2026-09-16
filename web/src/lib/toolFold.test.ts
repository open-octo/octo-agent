import { describe, it, expect, beforeEach } from 'vitest'
import { defaultToolOpen, toolOpenState, applyToolToggle, keepOpenAction, foldSuppressed, type ToolLike } from './toolFold'

const tool = (id: string, error?: unknown): ToolLike => ({ id, error })

describe('defaultToolOpen', () => {
  it('opens the last tool while streaming, closes the rest', () => {
    expect(defaultToolOpen(tool('b'), 'b', true)).toBe(true)
    expect(defaultToolOpen(tool('a'), 'b', true)).toBe(false)
  })

  it('closes everything once streaming stops', () => {
    expect(defaultToolOpen(tool('b'), 'b', false)).toBe(false)
  })

  it('always opens error cards', () => {
    expect(defaultToolOpen(tool('a', 'boom'), 'b', false)).toBe(true)
  })
})

describe('toolOpenState + applyToolToggle', () => {
  it('falls back to the default when there is no override', () => {
    const ov: Record<string, boolean> = {}
    expect(toolOpenState(ov, tool('b'), 'b', true)).toBe(true)
    expect(toolOpenState(ov, tool('a'), 'b', true)).toBe(false)
  })

  // The core regression: the last tool is auto-open, the user collapses it, and
  // a later streaming re-render must NOT re-open it.
  it('keeps a user collapse of the streaming last tool sticky across re-renders', () => {
    const ov: Record<string, boolean> = {}
    const last = tool('b')
    // auto-open
    expect(toolOpenState(ov, last, 'b', true)).toBe(true)
    // user collapses -> diverges from default(true) -> override recorded
    applyToolToggle(ov, last, 'b', true, false)
    expect(ov).toEqual({ b: false })
    // re-render(s) while still streaming: stays collapsed
    expect(toolOpenState(ov, last, 'b', true)).toBe(false)
    expect(toolOpenState(ov, last, 'b', true)).toBe(false)
  })

  it('lets the user re-open a collapsed tool and that sticks too', () => {
    const ov: Record<string, boolean> = { b: false }
    applyToolToggle(ov, tool('b'), 'b', true, true) // back to default(true) -> override cleared
    expect(ov).toEqual({})
    expect(toolOpenState(ov, tool('b'), 'b', true)).toBe(true)
  })

  it('does not record an override when a programmatic open matches the default (no loop)', () => {
    const ov: Record<string, boolean> = {}
    applyToolToggle(ov, tool('b'), 'b', true, true) // auto-open toggle fires, open===default
    expect(ov).toEqual({})
  })

  it('lets the user open an earlier finished tool while streaming', () => {
    const ov: Record<string, boolean> = {}
    const earlier = tool('a') // not last -> default closed
    applyToolToggle(ov, earlier, 'b', true, true)
    expect(ov).toEqual({ a: true })
    expect(toolOpenState(ov, earlier, 'b', true)).toBe(true)
  })

  it('lets the user collapse an error card and keeps it collapsed', () => {
    const ov: Record<string, boolean> = {}
    const errored = tool('a', 'boom') // default open
    applyToolToggle(ov, errored, 'b', true, false)
    expect(ov).toEqual({ a: false })
    expect(toolOpenState(ov, errored, 'b', true)).toBe(false)
  })
})

describe('keepOpenAction', () => {
  const tools = [tool('a'), tool('b')]

  it('pins the last card open on the running->false edge', () => {
    expect(keepOpenAction(true, false, tools, {}, 0)).toEqual({ kind: 'pin', id: 'b' })
  })

  it('does not pin on first render (history replay lands already-done tools)', () => {
    expect(keepOpenAction(undefined, false, tools, {}, 0)).toBeNull()
  })

  it('does not re-pin while already stopped', () => {
    expect(keepOpenAction(false, false, tools, {}, 0)).toBeNull()
  })

  it('skips an error card — those stay open by default, nothing to pin', () => {
    expect(keepOpenAction(true, false, [tool('a'), tool('b', 'boom')], {}, 0)).toBeNull()
  })

  it('skips a card the user holds an explicit override on', () => {
    expect(keepOpenAction(true, false, tools, { b: false }, 0)).toBeNull()
    expect(keepOpenAction(true, false, tools, { b: true }, 0)).toBeNull()
  })

  // A new tool round arriving must release the pinned card so it collapses as
  // the new last tool opens — the same handoff as between mid-turn tools.
  it('clears the pins when the group starts running again', () => {
    expect(keepOpenAction(false, true, tools, {}, 1)).toEqual({ kind: 'clear' })
  })

  it('does nothing while running with no pins held', () => {
    expect(keepOpenAction(false, true, tools, {}, 0)).toBeNull()
    expect(keepOpenAction(true, true, tools, {}, 0)).toBeNull()
  })

  it('handles an empty tool list', () => {
    expect(keepOpenAction(true, false, [], {}, 0)).toBeNull()
  })
})

describe('foldSuppressed', () => {
  // <details><summary>[chev] <span.tool-arg>/some/path</span></summary></details>
  function header() {
    const details = document.createElement('details')
    details.innerHTML = '<summary><span class="chev"></span><span class="tool-arg">/some/path</span></summary>'
    document.body.append(details)
    const summary = details.querySelector('summary')!
    return { summary, arg: summary.querySelector('.tool-arg') as HTMLElement, chev: summary.querySelector('.chev') as HTMLElement }
  }

  beforeEach(() => { document.body.replaceChildren() })

  it('suppresses a click on the argument text', () => {
    const { arg } = header()
    expect(foldSuppressed(arg, null, 1)).toBe(true)
    // Keyboard activation never targets the argument span, but the branch must
    // not depend on detail either.
    expect(foldSuppressed(arg, null, 0)).toBe(true)
  })

  it('suppresses a click that ended a drag-selection started in the header', () => {
    const { summary, arg, chev } = header()
    const sel = document.getSelection()!
    sel.selectAllChildren(arg)
    // The mouseup can land anywhere in the summary, not just on the arg span.
    expect(foldSuppressed(chev, sel, 1)).toBe(true)
    expect(foldSuppressed(summary, sel, 1)).toBe(true)
  })

  // Dragging upwards out of the header leaves the anchor outside it.
  it('suppresses a backwards drag whose focus end is in the header', () => {
    const { summary, arg, chev } = header()
    const outside = document.createElement('p')
    outside.textContent = 'above the card'
    document.body.prepend(outside)
    const sel = document.getSelection()!
    const range = document.createRange()
    range.setStart(outside.firstChild!, 0)
    range.setEnd(arg.firstChild!, 3)
    sel.removeAllRanges()
    sel.addRange(range)
    expect(summary.contains(sel.anchorNode)).toBe(false)
    expect(foldSuppressed(chev, sel, 1)).toBe(true)
  })

  // A leftover selection must not swallow Enter/Space on the focused summary,
  // which fires a click with detail 0.
  it('lets keyboard activation fold the card despite a live selection', () => {
    const { summary, arg } = header()
    const sel = document.getSelection()!
    sel.selectAllChildren(arg)
    expect(foldSuppressed(summary, sel, 0)).toBe(false)
  })

  it('lets a plain click elsewhere in the header fold the card', () => {
    const { summary, chev } = header()
    const sel = document.getSelection()!
    sel.removeAllRanges()
    expect(foldSuppressed(chev, sel, 1)).toBe(false)
    expect(foldSuppressed(summary, sel, 1)).toBe(false)
  })

  it('ignores a selection living in another card header', () => {
    const { chev } = header()
    const other = header()
    const sel = document.getSelection()!
    sel.selectAllChildren(other.arg)
    expect(foldSuppressed(chev, sel, 1)).toBe(false)
  })

  it('ignores a collapsed caret and a non-element target', () => {
    const { chev, arg } = header()
    const sel = document.getSelection()!
    sel.collapse(arg.firstChild!, 1)
    expect(foldSuppressed(chev, sel, 1)).toBe(false)
    expect(foldSuppressed(document, sel, 1)).toBe(false)
    expect(foldSuppressed(null, sel, 1)).toBe(false)
  })
})
