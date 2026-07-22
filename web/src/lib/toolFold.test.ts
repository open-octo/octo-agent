import { describe, it, expect } from 'vitest'
import { defaultToolOpen, toolOpenState, applyToolToggle, type ToolLike } from './toolFold'

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
