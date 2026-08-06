import { describe, it, expect } from 'vitest'
import { globalKeyIntent } from './globalKeys'

describe('globalKeyIntent', () => {
  const on = { shell: true }
  const off = { shell: false }

  it('Cmd/Ctrl+K opens the palette regardless of shell', () => {
    expect(globalKeyIntent({ key: 'k', metaKey: true }, on)).toBe('palette')
    expect(globalKeyIntent({ key: 'k', ctrlKey: true }, on)).toBe('palette')
    expect(globalKeyIntent({ key: 'k', metaKey: true }, off)).toBe('palette')
  })

  it('accepts uppercase keys (Shift not held)', () => {
    expect(globalKeyIntent({ key: 'K', metaKey: true }, on)).toBe('palette')
  })

  it('Cmd/Ctrl+N opens a new session only in the desktop shell', () => {
    expect(globalKeyIntent({ key: 'n', metaKey: true }, on)).toBe('new-session')
    expect(globalKeyIntent({ key: 'n', ctrlKey: true }, on)).toBe('new-session')
    expect(globalKeyIntent({ key: 'n', metaKey: true }, off)).toBeNull()
  })

  it('a modifier is required — bare keys fall through', () => {
    for (const key of ['k', 'K', 'n', 'a', 'Enter']) {
      expect(globalKeyIntent({ key }, on)).toBeNull()
    }
  })

  it('an Alt or Shift modifier disables both shortcuts', () => {
    expect(globalKeyIntent({ key: 'k', metaKey: true, altKey: true }, on)).toBeNull()
    expect(globalKeyIntent({ key: 'k', metaKey: true, shiftKey: true }, on)).toBeNull()
    expect(globalKeyIntent({ key: 'n', metaKey: true, altKey: true }, on)).toBeNull()
    expect(globalKeyIntent({ key: 'n', metaKey: true, shiftKey: true }, on)).toBeNull()
  })

  it('other keys are not shortcuts', () => {
    for (const key of ['a', 'Enter', 'Tab', ' ']) {
      expect(globalKeyIntent({ key, metaKey: true }, on)).toBeNull()
    }
  })
})
