import { describe, it, expect } from 'vitest'
import { parsePartialGenuiJson } from './partial-parse'

describe('parsePartialGenuiJson: recoverable prefix', () => {
  it('recovers the first 2 complete items when a 3rd is half-written', () => {
    const buffer = '{"items":[{"type":"text","text":"a"},{"type":"text","text":"b"},{"type":"text","text":"c"'
    const result = parsePartialGenuiJson(buffer) as any
    expect(result).not.toBeNull()
    expect(result.items).toHaveLength(2)
    expect(result.items[0]).toEqual({ type: 'text', text: 'a' })
    expect(result.items[1]).toEqual({ type: 'text', text: 'b' })
  })

  it('recovers a spec that is already fully valid JSON (fence closes the same instant the body does)', () => {
    const buffer = '{"items":[{"type":"text","text":"a"}]}'
    const result = parsePartialGenuiJson(buffer) as any
    expect(result).toEqual({ items: [{ type: 'text', text: 'a' }] })
  })

  it('does not mistake a brace/bracket inside a quoted string for structure', () => {
    const buffer = '{"items":[{"type":"text","text":"a { b [ c"}'
    const result = parsePartialGenuiJson(buffer) as any
    expect(result).toEqual({ items: [{ type: 'text', text: 'a { b [ c' }] })
  })

  it('handles an escaped quote inside a string without ending the string early', () => {
    const buffer = '{"items":[{"type":"text","text":"say \\"hi\\""}]}'
    const result = parsePartialGenuiJson(buffer) as any
    expect(result).toEqual({ items: [{ type: 'text', text: 'say "hi"' }] })
  })
})

describe('parsePartialGenuiJson: nothing safe to render yet', () => {
  it('returns null (not a throw) when no node has closed at all', () => {
    const buffer = '{"items":[{"type":"text","text":"a"'
    expect(() => parsePartialGenuiJson(buffer)).not.toThrow()
    expect(parsePartialGenuiJson(buffer)).toBeNull()
  })

  it('returns null for an empty buffer', () => {
    expect(parsePartialGenuiJson('')).toBeNull()
  })

  it('returns null when only opening brackets have streamed in so far', () => {
    expect(parsePartialGenuiJson('{"items":[')).toBeNull()
  })
})

describe('parsePartialGenuiJson: pathological input performance', () => {
  it('returns in well under 100ms for a large, never-closing nested buffer', () => {
    // 5000 chars of ever-deeper, never-closed nesting: no candidate is ever
    // recorded, so this must be a cheap single pass, not an accidental
    // O(n^2) rescan.
    const buffer = '{"items":['.repeat(500) // 5000 chars, all unmatched opens
    const start = Date.now()
    const result = parsePartialGenuiJson(buffer)
    const elapsed = Date.now() - start
    expect(result).toBeNull()
    expect(elapsed).toBeLessThan(100)
  })

  it('returns in well under 100ms for a large buffer with many closed candidates', () => {
    // Many complete sibling items followed by one half-written one — many
    // candidates recorded over a single long scan.
    const items = Array.from({ length: 2000 }, (_, i) => `{"type":"text","text":"item ${i}"}`).join(',')
    const buffer = `{"items":[${items},{"type":"text","text":"tail`
    const start = Date.now()
    const result = parsePartialGenuiJson(buffer) as any
    const elapsed = Date.now() - start
    expect(result).not.toBeNull()
    expect(result.items).toHaveLength(2000)
    expect(elapsed).toBeLessThan(100)
  })
})
