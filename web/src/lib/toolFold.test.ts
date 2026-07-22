import { describe, it, expect } from 'vitest'
import { defaultToolOpen, toolOpenState, applyToolToggle, autoCloseAction, type ToolLike } from './toolFold'

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

describe('autoCloseAction', () => {
  const tools = [tool('a'), tool('b')]

  it('starts the animated close of the last card on the running->false edge', () => {
    expect(autoCloseAction(true, false, tools, {}, 0)).toEqual({ kind: 'start', id: 'b' })
  })

  it('does not start on first render (history replay lands already-done tools)', () => {
    expect(autoCloseAction(undefined, false, tools, {}, 0)).toBeNull()
  })

  it('does not re-start while already stopped', () => {
    expect(autoCloseAction(false, false, tools, {}, 0)).toBeNull()
  })

  it('skips an error card — those stay open by default, nothing to animate', () => {
    expect(autoCloseAction(true, false, [tool('a'), tool('b', 'boom')], {}, 0)).toBeNull()
  })

  it('skips a card the user holds an explicit override on', () => {
    expect(autoCloseAction(true, false, tools, { b: false }, 0)).toBeNull()
    expect(autoCloseAction(true, false, tools, { b: true }, 0)).toBeNull()
  })

  // A new tool call arriving before the previous card finished shrinking must
  // land the old card instantly — otherwise its shrink overlaps the new card's
  // arrival and the pair reads as everything animating open at once.
  it('cancels an in-flight close when the group starts running again', () => {
    expect(autoCloseAction(false, true, tools, {}, 1)).toEqual({ kind: 'cancel' })
  })

  it('does nothing while running with no close in flight', () => {
    expect(autoCloseAction(false, true, tools, {}, 0)).toBeNull()
    expect(autoCloseAction(true, true, tools, {}, 0)).toBeNull()
  })

  it('handles an empty tool list', () => {
    expect(autoCloseAction(true, false, [], {}, 0)).toBeNull()
  })
})
