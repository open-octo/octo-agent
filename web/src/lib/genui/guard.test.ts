import { describe, it, expect } from 'vitest'
import { sanitizeSpec, READ_ONLY_NODE_TYPES, MAX_NODES, MAX_DEPTH, MAX_STRING_LEN, MAX_TABLE_ROWS } from './guard'

describe('sanitizeSpec: valid input', () => {
  it('passes a well-formed spec through unchanged', () => {
    const spec = {
      title: 'Order status',
      items: [
        { type: 'text', text: 'hello' },
        { type: 'stat', label: 'Revenue', value: '$128,430', tone: 'up' },
      ],
    }
    const { spec: out, count } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    expect(count).toBe(2)
    expect(out?.items).toHaveLength(2)
    expect(out?.items[0]).toEqual({ type: 'text', text: 'hello' })
  })

  it('returns null when items is missing', () => {
    const { spec: out, count } = sanitizeSpec({ title: 'x' }, READ_ONLY_NODE_TYPES)
    expect(out).toBeNull()
    expect(count).toBe(0)
  })

  it('returns null for non-object input (e.g. a still-streaming partial parse)', () => {
    const { spec: out } = sanitizeSpec(null, READ_ONLY_NODE_TYPES)
    expect(out).toBeNull()
  })
})

describe('sanitizeSpec: unknown types', () => {
  it('drops an unknown-type node (and its subtree) without failing the whole spec', () => {
    const spec = {
      items: [
        { type: 'text', text: 'kept' },
        { type: 'iframe', src: 'https://evil.example' },
      ],
    }
    const { spec: out, count } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    expect(count).toBe(1)
    expect(out?.items).toHaveLength(1)
    expect(out?.items[0]).toMatchObject({ type: 'text' })
  })
})

describe('sanitizeSpec: unlisted fields stripped', () => {
  it('strips any field the schema does not know about', () => {
    const spec = {
      items: [{ type: 'text', text: 'hi', onclick: 'evil()', style: 'position:fixed' }],
    }
    const { spec: out } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    const node = out?.items[0] as any
    expect(node.onclick).toBeUndefined()
    expect(node.style).toBeUndefined()
    expect(Object.keys(node).sort()).toEqual(['text', 'type'])
  })
})

describe('sanitizeSpec: node count cap', () => {
  it('trims (does not error) once the total exceeds MAX_NODES', () => {
    const items = Array.from({ length: MAX_NODES + 50 }, () => ({ type: 'text', text: 'x' }))
    const { spec: out, count } = sanitizeSpec({ items }, READ_ONLY_NODE_TYPES)
    expect(count).toBe(MAX_NODES)
    expect(out?.items).toHaveLength(MAX_NODES)
  })

  it('keeps exactly MAX_NODES flat siblings untrimmed', () => {
    const items = Array.from({ length: MAX_NODES }, () => ({ type: 'text', text: 'x' }))
    const { count } = sanitizeSpec({ items }, READ_ONLY_NODE_TYPES)
    expect(count).toBe(MAX_NODES)
  })

  it('reserves a container node its own budget slot before recursing', () => {
    // Same shape as the bug the Go guard review caught: MAX_NODES leaves
    // nested one level down inside a single "row" must not push the total
    // past MAX_NODES.
    const leaves = Array.from({ length: MAX_NODES }, () => ({ type: 'text', text: 'x' }))
    const spec = { items: [{ type: 'row', children: leaves }] }
    const { count } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    expect(count).toBeLessThanOrEqual(MAX_NODES)
  })
})

describe('sanitizeSpec: depth cap', () => {
  function nestedRow(depth: number): any {
    if (depth === 0) return { type: 'text', text: 'leaf' }
    return { type: 'row', children: [nestedRow(depth - 1)] }
  }

  it('drops a leaf past MAX_DEPTH', () => {
    const spec = { items: [nestedRow(10)] } // leaf lands at depth 11
    const { spec: out } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    let node: any = out?.items[0]
    let depth = 1
    while (node.children?.length) {
      node = node.children[0]
      depth++
    }
    expect(node.type).toBe('row') // the leaf itself never survived
    expect(depth).toBeLessThanOrEqual(MAX_DEPTH)
  })

  it('keeps a leaf exactly at MAX_DEPTH', () => {
    const spec = { items: [nestedRow(MAX_DEPTH - 1)] } // leaf lands at depth MAX_DEPTH
    const { spec: out } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    let node: any = out?.items[0]
    while (node.children?.length) node = node.children[0]
    expect(node.type).toBe('text')
  })
})

describe('sanitizeSpec: string clamping', () => {
  it('clamps an over-length string field', () => {
    const long = 'a'.repeat(MAX_STRING_LEN + 100)
    const { spec: out } = sanitizeSpec({ items: [{ type: 'text', text: long }] }, READ_ONLY_NODE_TYPES)
    expect((out?.items[0] as any).text).toHaveLength(MAX_STRING_LEN)
  })

  it('leaves a string exactly at the cap untouched', () => {
    const exact = 'a'.repeat(MAX_STRING_LEN)
    const { spec: out } = sanitizeSpec({ items: [{ type: 'text', text: exact }] }, READ_ONLY_NODE_TYPES)
    expect((out?.items[0] as any).text).toBe(exact)
  })

  it('does not split a surrogate pair (emoji) when clamping', () => {
    const emoji = '🎉'.repeat(MAX_STRING_LEN) // each 🎉 is 2 UTF-16 code units
    const { spec: out } = sanitizeSpec({ items: [{ type: 'text', text: emoji }] }, READ_ONLY_NODE_TYPES)
    const text = (out?.items[0] as any).text as string
    // A valid clamp never leaves a lone leading low surrogate.
    expect(text.charCodeAt(0)).not.toBeGreaterThanOrEqual(0xdc00)
    expect(Array.from(text).every((ch) => ch === '🎉')).toBe(true)
  })
})

describe('sanitizeSpec: enum fields', () => {
  it('drops an invalid tone rather than passing it through', () => {
    const { spec: out } = sanitizeSpec({ items: [{ type: 'badge', text: 'x', tone: 'rainbow' }] }, READ_ONLY_NODE_TYPES)
    expect((out?.items[0] as any).tone).toBeUndefined()
  })

  it('keeps a valid tone', () => {
    const { spec: out } = sanitizeSpec({ items: [{ type: 'badge', text: 'x', tone: 'success' }] }, READ_ONLY_NODE_TYPES)
    expect((out?.items[0] as any).tone).toBe('success')
  })
})

describe('sanitizeSpec: progress value clamping', () => {
  it('clamps out-of-range values to [0, 100]', () => {
    const { spec: out } = sanitizeSpec(
      { items: [{ type: 'progress', value: 150 }, { type: 'progress', value: -20 }] },
      READ_ONLY_NODE_TYPES
    )
    expect((out?.items[0] as any).value).toBe(100)
    expect((out?.items[1] as any).value).toBe(0)
  })
})

describe('sanitizeSpec: table caps', () => {
  it('caps rows at MAX_TABLE_ROWS', () => {
    const rows = Array.from({ length: MAX_TABLE_ROWS + 20 }, () => ['x', 'y'])
    const { spec: out } = sanitizeSpec({ items: [{ type: 'table', columns: ['a', 'b'], rows }] }, READ_ONLY_NODE_TYPES)
    expect((out?.items[0] as any).rows).toHaveLength(MAX_TABLE_ROWS)
  })

  it('replaces a non-string/number cell with a placeholder to preserve column alignment', () => {
    const spec = { items: [{ type: 'table', columns: ['Name', 'Active', 'City'], rows: [['Alice', true, 'NYC']] }] }
    const { spec: out } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    const row = (out?.items[0] as any).rows[0]
    expect(row).toHaveLength(3)
    expect(row[0]).toBe('Alice')
    expect(row[2]).toBe('NYC')
  })
})
