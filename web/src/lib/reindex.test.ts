import { describe, expect, it } from 'vitest'
import { shiftMessageIndices } from './reindex'

describe('shiftMessageIndices', () => {
  it('drops the index of folded messages and shifts the rest onto the summary', () => {
    const msgs = [
      { id: 'a', messageIndex: 0 },
      { id: 'b', messageIndex: 5 },
      { id: 'tool' },
      { id: 'c', messageIndex: 6 },
      { id: 'd', messageIndex: 9 },
    ]
    // Messages 0..5 fold into the summary at 0: 6 becomes 1, 9 becomes 4.
    expect(shiftMessageIndices(msgs, 6)).toEqual([
      { id: 'a' },
      { id: 'b' },
      { id: 'tool' },
      { id: 'c', messageIndex: 1 },
      { id: 'd', messageIndex: 4 },
    ])
  })

  it('leaves the list alone when nothing folded or nothing is indexed', () => {
    const msgs = [{ id: 'a', messageIndex: 3 }]
    expect(shiftMessageIndices(msgs, 0)).toBe(msgs)
    const unindexed: { id: string; messageIndex?: number }[] = [{ id: 'x' }]
    expect(shiftMessageIndices(unindexed, 4)).toBe(unindexed)
  })
})
