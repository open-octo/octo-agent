import { describe, it, expect } from 'vitest'
import {
  sanitizeSpec,
  READ_ONLY_NODE_TYPES,
  INTERACTIVE_NODE_TYPES,
  MAX_NODES,
  MAX_DEPTH,
  MAX_STRING_LEN,
  MAX_TABLE_ROWS,
  MAX_OPTIONS,
  MAX_TABS,
} from './guard'

// Slice B's inline-fence path is the only path that ever passes the
// interactive types in — render_ui's tool-card path never does (see
// INTERACTIVE_NODE_TYPES's doc comment). Union the two whitelists here to
// mirror fence-split.ts's ALLOWED_TYPES.
const ALL_TYPES: ReadonlySet<string> = new Set([...READ_ONLY_NODE_TYPES, ...INTERACTIVE_NODE_TYPES])

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

describe('sanitizeSpec: interactive nodes are rejected on the read-only whitelist', () => {
  it('render_ui tool-card path (READ_ONLY_NODE_TYPES) drops a button node', () => {
    const spec = { items: [{ type: 'button', label: 'Go', action: 'go' }] }
    const { spec: out, count } = sanitizeSpec(spec, READ_ONLY_NODE_TYPES)
    expect(count).toBe(0)
    expect(out?.items).toHaveLength(0)
  })
})

describe('sanitizeSpec: button node', () => {
  it('passes a valid button through with fields clamped', () => {
    const spec = { items: [{ type: 'button', label: 'Refresh', action: 'refresh', variant: 'primary', payload: { range: '7d' } }] }
    const { spec: out, count } = sanitizeSpec(spec, ALL_TYPES)
    expect(count).toBe(1)
    expect(out?.items[0]).toEqual({
      type: 'button',
      label: 'Refresh',
      action: 'refresh',
      variant: 'primary',
      payload: { range: '7d' },
    })
  })

  it('drops an invalid variant rather than passing it through', () => {
    const spec = { items: [{ type: 'button', label: 'Go', action: 'go', variant: 'rainbow' }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect((out?.items[0] as any).variant).toBeUndefined()
  })

  it('clamps over-length label/action strings', () => {
    const long = 'a'.repeat(MAX_STRING_LEN + 50)
    const spec = { items: [{ type: 'button', label: long, action: long }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    const node = out?.items[0] as any
    expect(node.label).toHaveLength(MAX_STRING_LEN)
    expect(node.action).toHaveLength(MAX_STRING_LEN)
  })

  it('truncates a payload nested past MAX_DEPTH to an empty object', () => {
    let payload: any = { leaf: 'x' }
    for (let i = 0; i < MAX_DEPTH + 3; i++) payload = { nested: payload }
    const spec = { items: [{ type: 'button', label: 'Go', action: 'go', payload }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    const node = out?.items[0] as any
    // Walk down the surviving payload until a level truncates to {}.
    let level = node.payload
    let depth = 1
    while (level && level.nested !== undefined && Object.keys(level).length > 0) {
      level = level.nested
      depth++
    }
    // sanitizePayload(obj, depth=1) truncates once depth > MAX_DEPTH, i.e.
    // at the call for depth MAX_DEPTH+1 — one further in than the node-tree
    // MAX_DEPTH convention (which counts a top-level item as depth 1 and
    // truncates children past MAX_DEPTH itself). Different function, its
    // own off-by-one convention — asserted here rather than assumed.
    expect(depth).toBeLessThanOrEqual(MAX_DEPTH + 1)
    expect(level).toEqual({})
  })

  it('caps a payload object with more than MAX_OPTIONS keys at one level', () => {
    const wide: Record<string, string> = {}
    for (let i = 0; i < MAX_OPTIONS + 20; i++) wide[`k${i}`] = 'v'
    const spec = { items: [{ type: 'button', label: 'Go', action: 'go', payload: wide }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    const node = out?.items[0] as any
    expect(Object.keys(node.payload)).toHaveLength(MAX_OPTIONS)
  })

  it('caps a payload array with more than MAX_OPTIONS entries', () => {
    const spec = {
      items: [{ type: 'button', label: 'Go', action: 'go', payload: { list: Array.from({ length: MAX_OPTIONS + 20 }, (_, i) => i) } }],
    }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    const node = out?.items[0] as any
    expect(node.payload.list).toHaveLength(MAX_OPTIONS)
  })
})

describe('sanitizeSpec: input node', () => {
  it('passes a valid input through with fields clamped', () => {
    const spec = { items: [{ type: 'input', field: 'name', label: 'Name', placeholder: 'Enter name', value: 'Alice' }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect(out?.items[0]).toEqual({ type: 'input', field: 'name', label: 'Name', placeholder: 'Enter name', value: 'Alice' })
  })

  it('silently drops an inputType field even if the model sends one', () => {
    const spec = { items: [{ type: 'input', field: 'password', inputType: 'password' }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    const node = out?.items[0] as any
    expect(node.inputType).toBeUndefined()
    expect(Object.keys(node).sort()).toEqual(['field', 'type'])
  })
})

describe('sanitizeSpec: select node', () => {
  it('passes a valid select through with options clamped', () => {
    const spec = {
      items: [
        {
          type: 'select',
          field: 'range',
          label: 'Range',
          value: '7d',
          options: [{ label: '7 days', value: '7d' }, { label: '30 days', value: '30d' }],
        },
      ],
    }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect(out?.items[0]).toEqual({
      type: 'select',
      field: 'range',
      label: 'Range',
      value: '7d',
      options: [{ label: '7 days', value: '7d' }, { label: '30 days', value: '30d' }],
    })
  })

  it('caps options at MAX_OPTIONS', () => {
    const options = Array.from({ length: MAX_OPTIONS + 10 }, (_, i) => ({ label: `o${i}`, value: `${i}` }))
    const spec = { items: [{ type: 'select', field: 'x', options }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect((out?.items[0] as any).options).toHaveLength(MAX_OPTIONS)
  })
})

describe('sanitizeSpec: checkbox / switch nodes', () => {
  it('passes a valid checkbox through', () => {
    const spec = { items: [{ type: 'checkbox', field: 'agree', label: 'I agree', checked: true }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect(out?.items[0]).toEqual({ type: 'checkbox', field: 'agree', label: 'I agree', checked: true })
  })

  it('passes a valid switch through', () => {
    const spec = { items: [{ type: 'switch', field: 'notify', checked: false }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect(out?.items[0]).toEqual({ type: 'switch', field: 'notify', checked: false })
  })

  it('drops a non-boolean checked value', () => {
    const spec = { items: [{ type: 'checkbox', field: 'agree', checked: 'yes' }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect((out?.items[0] as any).checked).toBeUndefined()
  })
})

describe('sanitizeSpec: radio node', () => {
  it('passes a valid radio through with options clamped', () => {
    const spec = {
      items: [
        {
          type: 'radio',
          field: 'plan',
          value: 'pro',
          options: [{ label: 'Free', value: 'free' }, { label: 'Pro', value: 'pro' }],
        },
      ],
    }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect(out?.items[0]).toEqual({
      type: 'radio',
      field: 'plan',
      value: 'pro',
      options: [{ label: 'Free', value: 'free' }, { label: 'Pro', value: 'pro' }],
    })
  })
})

describe('sanitizeSpec: tabs node', () => {
  it('passes valid tabs through with children sanitized', () => {
    const spec = {
      items: [
        {
          type: 'tabs',
          tabs: [
            { label: 'Overview', children: [{ type: 'text', text: 'hi' }] },
            { label: 'Details', children: [{ type: 'stat', label: 'Revenue', value: '$1' }] },
          ],
        },
      ],
    }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    const node = out?.items[0] as any
    expect(node.tabs).toHaveLength(2)
    expect(node.tabs[0]).toEqual({ label: 'Overview', children: [{ type: 'text', text: 'hi' }] })
  })

  it('caps tabs at MAX_TABS', () => {
    const tabs = Array.from({ length: MAX_TABS + 3 }, (_, i) => ({ label: `t${i}`, children: [] }))
    const spec = { items: [{ type: 'tabs', tabs }] }
    const { spec: out } = sanitizeSpec(spec, ALL_TYPES)
    expect((out?.items[0] as any).tabs).toHaveLength(MAX_TABS)
  })
})
