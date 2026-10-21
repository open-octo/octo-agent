import { describe, it, expect } from 'vitest'
import { goalChip } from './goalChip'

describe('goalChip', () => {
  it('is empty without a goal', () => {
    expect(goalChip(null)).toBe('')
  })

  it('shows token use against the budget while active', () => {
    expect(goalChip({ status: 'active', token_budget: 2_000_000, tokens_used: 1_500 })).toBe('1.5K/2M')
  })

  it('shows time spent while active and unbudgeted', () => {
    expect(goalChip({ status: 'active', time_used_seconds: 42 })).toBe('42s')
    expect(goalChip({ status: 'active', time_used_seconds: 600 })).toBe('10m')
    expect(goalChip({ status: 'active', time_used_seconds: 3_900 })).toBe('1h 5m')
  })

  it('shows the status otherwise', () => {
    expect(goalChip({ status: 'paused' })).toBe('paused')
    expect(goalChip({ status: 'budget_limited' })).toBe('budget limited')
  })
})
