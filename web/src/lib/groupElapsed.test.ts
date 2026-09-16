import { describe, it, expect } from 'vitest'
import { groupElapsed } from './groupElapsed'

// startedAt in ms, elapsed in s — mirrors the tool objects built by
// addToolCallToGroup / updateToolResult.
const tool = (startedAt?: number, elapsed?: number) => ({ startedAt, elapsed })

describe('groupElapsed', () => {
  it('is empty when no tool carries timing', () => {
    expect(groupElapsed([])).toBe('')
    expect(groupElapsed([tool(), tool(undefined, 1.5)])).toBe('')
  })

  it('renders a single tool duration', () => {
    expect(groupElapsed([tool(1000, 0.7)])).toBe('0.7s')
  })

  it('spans first start to last finish, including gaps between sequential tools', () => {
    // t0 runs 1s→3s, t1 runs 4s→5.5s: span 1s→5.5s = 4.5s (sum would be 3.5s)
    expect(groupElapsed([tool(1000, 2), tool(4000, 1.5)])).toBe('4.5s')
  })

  it('does not multiply-count parallel batches', () => {
    // Three tools starting together, longest 0.7s: span is 0.7s (sum: 1.8s)
    expect(groupElapsed([tool(1000, 0.5), tool(1000, 0.7), tool(1000, 0.6)])).toBe('0.7s')
  })

  it('treats a tool closed without a result-derived elapsed as zero-width', () => {
    // The elapsed-less tool (start 2s) must not extend the span beyond the
    // timed tool's finish (1s→3s): span is 2.0s, not 3.0s.
    expect(groupElapsed([tool(1000, 2), tool(2000)])).toBe('2.0s')
    // …but it still anchors the start when it is the earliest.
    expect(groupElapsed([tool(500), tool(1000, 2)])).toBe('2.5s')
  })

  it('carries rounding overflow instead of rendering 1m 60s', () => {
    // 119.96s must read 2m, not "1m 60s".
    expect(groupElapsed([tool(1000, 119.96)])).toBe('2m')
    expect(groupElapsed([tool(1000, 65.4)])).toBe('1m 5s')
    expect(groupElapsed([tool(1000, 60)])).toBe('1m')
  })

  it('omits sub-100ms spans that would render as 0.0s', () => {
    expect(groupElapsed([tool(1000, 0.05)])).toBe('')
  })
})
