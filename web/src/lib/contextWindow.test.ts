import { describe, it, expect } from 'vitest'
import { parseContextWindow, formatContextWindow } from './contextWindow'

describe('parseContextWindow', () => {
  it('reads plain counts and k/M shorthand', () => {
    expect(parseContextWindow('128000')).toBe(128000)
    expect(parseContextWindow('200k')).toBe(200_000)
    expect(parseContextWindow('1M')).toBe(1_000_000)
    expect(parseContextWindow(' 1.5m ')).toBe(1_500_000)
    expect(parseContextWindow('1,000,000')).toBe(1_000_000)
  })

  it('treats empty as unset', () => {
    expect(parseContextWindow('')).toBe(0)
    expect(parseContextWindow('   ')).toBe(0)
  })

  it('rejects what the server would', () => {
    expect(parseContextWindow('32')).toBeNull() // 32 meaning 32k is the unit mistake
    expect(parseContextWindow('-5')).toBeNull()
    expect(parseContextWindow('1g')).toBeNull()
    expect(parseContextWindow('abc')).toBeNull()
  })
})

describe('formatContextWindow', () => {
  it('uses shorthand only when exact', () => {
    expect(formatContextWindow(1_000_000)).toBe('1M')
    expect(formatContextWindow(200_000)).toBe('200K')
    expect(formatContextWindow(1_500_000)).toBe('1.5M')
    expect(formatContextWindow(131072)).toBe('131072')
    expect(formatContextWindow(0)).toBe('')
  })

  it('round-trips through parse', () => {
    for (const n of [1_000_000, 200_000, 131072, 1_500_000]) {
      expect(parseContextWindow(formatContextWindow(n))).toBe(n)
    }
  })
})
