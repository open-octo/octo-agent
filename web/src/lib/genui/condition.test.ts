import { describe, it, expect } from 'vitest'
import { evaluateCondition } from './condition'

describe('evaluateCondition', () => {
  it('renders when there is no condition', () => {
    expect(evaluateCondition(undefined, {})).toBe(true)
  })

  describe('equality family', () => {
    it('compares equals', () => {
      expect(evaluateCondition({ field: 'mode', equals: 'advanced' }, { mode: 'advanced' })).toBe(true)
      expect(evaluateCondition({ field: 'mode', equals: 'advanced' }, { mode: 'basic' })).toBe(false)
    })

    it('treats an unset field as the empty string', () => {
      expect(evaluateCondition({ field: 'mode', equals: 'advanced' }, {})).toBe(false)
      expect(evaluateCondition({ field: 'mode', equals: '' }, {})).toBe(true)
    })

    it('compares across types by string form', () => {
      // A select stores "30"; a model writing equals: 30 means the obvious thing.
      expect(evaluateCondition({ field: 'n', equals: 30 }, { n: '30' })).toBe(true)
      expect(evaluateCondition({ field: 'n', equals: '30' }, { n: 30 })).toBe(true)
    })

    it('handles in and not', () => {
      expect(evaluateCondition({ field: 'r', in: ['7d', '30d'] }, { r: '30d' })).toBe(true)
      expect(evaluateCondition({ field: 'r', in: ['7d', '30d'] }, { r: '90d' })).toBe(false)
      expect(evaluateCondition({ field: 'r', not: 'off' }, { r: 'on' })).toBe(true)
      expect(evaluateCondition({ field: 'r', not: 'off' }, { r: 'off' })).toBe(false)
    })

    it('works with booleans', () => {
      expect(evaluateCondition({ field: 'adv', equals: true }, { adv: true })).toBe(true)
      expect(evaluateCondition({ field: 'adv', equals: true }, { adv: false })).toBe(false)
    })

    it('wins over range predicates when both are present', () => {
      // The guard drops the losers, but the evaluator must not depend on that.
      expect(evaluateCondition({ field: 'n', equals: 5, gt: 100 }, { n: 5 })).toBe(true)
    })
  })

  describe('range family', () => {
    it('ANDs every present predicate', () => {
      const half = { field: 'n', gte: 10, lt: 100 }
      expect(evaluateCondition(half, { n: 10 })).toBe(true)
      expect(evaluateCondition(half, { n: 99 })).toBe(true)
      expect(evaluateCondition(half, { n: 100 })).toBe(false)
      expect(evaluateCondition(half, { n: 9 })).toBe(false)
    })

    it('handles each predicate on its own', () => {
      expect(evaluateCondition({ field: 'n', gt: 5 }, { n: 6 })).toBe(true)
      expect(evaluateCondition({ field: 'n', gt: 5 }, { n: 5 })).toBe(false)
      expect(evaluateCondition({ field: 'n', lte: 5 }, { n: 5 })).toBe(true)
    })

    it('coerces numeric strings', () => {
      expect(evaluateCondition({ field: 'n', gt: 5 }, { n: '6' })).toBe(true)
    })

    it('fails closed on an untouched slider', () => {
      // The node stays hidden until the control that drives it is moved.
      expect(evaluateCondition({ field: 'n', gt: 0 }, {})).toBe(false)
      expect(evaluateCondition({ field: 'n', lt: 1e9 }, {})).toBe(false)
    })

    it('fails closed on values with no numeric meaning', () => {
      expect(evaluateCondition({ field: 'n', gt: 0 }, { n: 'abc' })).toBe(false)
      expect(evaluateCondition({ field: 'n', gt: 0 }, { n: '' })).toBe(false)
      // Booleans are excluded deliberately: Number(true) is 1, but "is this
      // switch greater than zero" is not a question worth answering.
      expect(evaluateCondition({ field: 'n', gt: 0 }, { n: true })).toBe(false)
    })
  })
})
