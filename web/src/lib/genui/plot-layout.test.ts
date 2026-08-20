import { describe, it, expect } from 'vitest'
import { unionLabels, alignSeries, valueRange, stackedValues, stackOffsets, pieSlices, PLOT_COLORS } from './plot-layout'
import { MAX_PLOT_SERIES } from './guard'

const s = (name: string, pts: [string, number][]) => ({ name, points: pts.map(([label, value]) => ({ label, value })) })

describe('unionLabels', () => {
  it('is the union in first-appearance order', () => {
    const labels = unionLabels([s('a', [['jan', 1], ['feb', 2]]), s('b', [['feb', 3], ['mar', 4]])])
    expect(labels).toEqual(['jan', 'feb', 'mar'])
  })

  it('tolerates series of differing lengths', () => {
    expect(unionLabels([s('a', [['x', 1]]), s('b', [])])).toEqual(['x'])
  })
})

describe('alignSeries', () => {
  it('marks a missing label as a gap rather than a zero', () => {
    const labels = ['jan', 'feb', 'mar']
    const rows = alignSeries([s('a', [['jan', 1], ['mar', 3]])], labels)
    // The distinction matters: a line breaks here, a bar draws zero height.
    expect(rows[0]).toEqual([1, null, 3])
  })

  it('keeps the first value for a duplicated label, matching the axis', () => {
    const labels = unionLabels([s('a', [['x', 1], ['x', 9]])])
    expect(alignSeries([s('a', [['x', 1], ['x', 9]])], labels)[0]).toEqual([1])
  })
})

describe('valueRange', () => {
  it('always includes zero so bars have a baseline', () => {
    expect(valueRange([[5, 10]], false)).toEqual({ min: 0, max: 10 })
    expect(valueRange([[-5, -1]], false)).toEqual({ min: -5, max: 0 })
  })

  it('never returns a zero-width span', () => {
    const r = valueRange([[0, 0]], false)
    expect(r.max).toBeGreaterThan(r.min)
  })

  it('sums columns when stacked', () => {
    expect(valueRange([[3, 1], [4, 1]], true).max).toBe(7)
  })

  it('ignores gaps', () => {
    expect(valueRange([[null, 4]], false).max).toBe(4)
  })
})

describe('stacking', () => {
  it('clamps negatives and gaps to zero so segments stay additive', () => {
    expect(stackedValues([[-3, null, 2]])).toEqual([[0, 0, 2]])
  })

  it('offsets each series by the running total beneath it', () => {
    const stack = stackedValues([[2, 1], [3, 4]])
    expect(stackOffsets(stack)).toEqual([[0, 0], [2, 1]])
  })
})

describe('pieSlices', () => {
  it('normalises positive values into fractions', () => {
    const slices = pieSlices(s('a', [['x', 1], ['y', 3]]))
    expect(slices.map(x => x.fraction)).toEqual([0.25, 0.75])
  })

  it('drops non-positive values and returns nothing when there is no total', () => {
    expect(pieSlices(s('a', [['x', -1], ['y', 0]]))).toEqual([])
  })
})

describe('PLOT_COLORS', () => {
  it('has exactly one colour per allowed series', () => {
    // The guard stops at MAX_PLOT_SERIES because a further series would have
    // no distinct colour to take — the two must stay in step.
    expect(PLOT_COLORS.length).toBe(MAX_PLOT_SERIES)
  })
})
