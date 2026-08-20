// Guard coverage for the interactive-panel additions: panel identity,
// conditions, the new input/content nodes, and their caps. The pre-existing
// node types are covered in guard.test.ts.
import { describe, it, expect } from 'vitest'
import {
  sanitizeSpec,
  READ_ONLY_NODE_TYPES,
  INTERACTIVE_NODE_TYPES,
  MAX_TABLE_ROWS,
  MAX_MERMAID_LEN,
  MAX_CODE_LEN,
  MAX_TEXTAREA_LEN,
  MAX_PLOT_POINTS,
  MAX_PLOT_SERIES,
} from './guard'

const ALL: ReadonlySet<string> = new Set([...READ_ONLY_NODE_TYPES, ...INTERACTIVE_NODE_TYPES])
const one = (node: unknown) => sanitizeSpec({ items: [node] }, ALL).spec?.items[0] as any
const spec = (extra: Record<string, unknown>) => sanitizeSpec({ items: [], ...extra }, ALL).spec

describe('panel id', () => {
  it('keeps a well-formed id', () => {
    expect(spec({ id: 'sales-2024_q1' })?.id).toBe('sales-2024_q1')
  })

  it('drops an id that could not be used safely, leaving the panel anonymous', () => {
    // Becomes part of a localStorage key and is compared against model output,
    // so anything outside the class is dropped rather than escaped.
    expect(spec({ id: 'has space' })?.id).toBeUndefined()
    expect(spec({ id: 'has/slash' })?.id).toBeUndefined()
    expect(spec({ id: 'a'.repeat(65) })?.id).toBeUndefined()
    expect(spec({ id: '' })?.id).toBeUndefined()
    expect(spec({ id: 42 })?.id).toBeUndefined()
  })

  it('still returns a usable spec when the id is rejected', () => {
    const s = sanitizeSpec({ id: 'bad id', items: [{ type: 'text', text: 'hi' }] }, ALL).spec
    expect(s?.id).toBeUndefined()
    expect(s?.items.length).toBe(1)
  })
})

describe('reserved field names', () => {
  it('drops a node trying to address internal panel state', () => {
    // __tab: / __open: belong to tabs and collapsible; a spec must not reach them.
    expect(one({ type: 'input', field: '__tab:0' })).toBeUndefined()
    expect(one({ type: 'slider', field: '__open:1', min: 0, max: 10 })).toBeUndefined()
    expect(one({ type: 'checkbox', field: '__anything' })).toBeUndefined()
  })

  it('drops a node with no field at all', () => {
    expect(one({ type: 'input' })).toBeUndefined()
    expect(one({ type: 'input', field: '' })).toBeUndefined()
  })

  it('refuses a reserved field as a table filter source', () => {
    const t = one({ type: 'table', columns: ['a'], rows: [], filterBy: { field: '__tab:0', column: 'a' } })
    expect(t.filterBy).toBeUndefined()
  })
})

describe('visibleWhen', () => {
  it('keeps one member of the equality family, in a fixed precedence', () => {
    const n = one({ type: 'text', text: 'x', visibleWhen: { field: 'f', equals: 'a', in: ['b'], not: 'c' } })
    expect(n.visibleWhen).toEqual({ field: 'f', equals: 'a' })
  })

  it('keeps range predicates together', () => {
    const n = one({ type: 'text', text: 'x', visibleWhen: { field: 'f', gte: 10, lt: 100 } })
    expect(n.visibleWhen).toEqual({ field: 'f', gte: 10, lt: 100 })
  })

  it('drops a condition with nothing to compare', () => {
    expect(one({ type: 'text', text: 'x', visibleWhen: { field: 'f' } }).visibleWhen).toBeUndefined()
    expect(one({ type: 'text', text: 'x', visibleWhen: { equals: 'a' } }).visibleWhen).toBeUndefined()
    expect(one({ type: 'text', text: 'x', visibleWhen: 'f == 1' }).visibleWhen).toBeUndefined()
  })

  it('drops non-finite range bounds', () => {
    expect(one({ type: 'text', text: 'x', visibleWhen: { field: 'f', gt: NaN } }).visibleWhen).toBeUndefined()
  })
})

describe('slider', () => {
  it('drops a slider with no width to render', () => {
    expect(one({ type: 'slider', field: 'n', min: 5, max: 5 })).toBeUndefined()
    expect(one({ type: 'slider', field: 'n', min: 10, max: 1 })).toBeUndefined()
  })

  it('defaults step and clamps value into range', () => {
    const n = one({ type: 'slider', field: 'n', min: 0, max: 100, value: 500 })
    expect(n.step).toBe(1)
    expect(n.value).toBe(100)
  })

  it('rejects a non-positive step and falls back to the default', () => {
    expect(one({ type: 'slider', field: 'n', min: 0, max: 10, step: -1 }).step).toBe(0.1)
  })
})

describe('number', () => {
  it('is a distinct node so `input` can keep having no inputType', () => {
    // The security property is structural: an inputType sent to `input` is not
    // validated away, it is simply never read.
    const i = one({ type: 'input', field: 'q', inputType: 'password' })
    expect(i.inputType).toBeUndefined()
    expect(one({ type: 'number', field: 'n' }).type).toBe('number')
  })

  it('clamps a value to its declared bounds', () => {
    expect(one({ type: 'number', field: 'n', min: 0, max: 10, value: 99 }).value).toBe(10)
  })
})

describe('textarea', () => {
  it('clamps rows into a range that fits a chat panel', () => {
    expect(one({ type: 'textarea', field: 't', rows: 99 }).rows).toBe(12)
    expect(one({ type: 'textarea', field: 't', rows: 1 }).rows).toBe(2)
  })

  it('trims an oversized default value', () => {
    const n = one({ type: 'textarea', field: 't', value: 'x'.repeat(MAX_TEXTAREA_LEN + 100) })
    expect(n.value.length).toBe(MAX_TEXTAREA_LEN)
  })
})

describe('content nodes', () => {
  it('trims code and mermaid rather than rejecting them', () => {
    expect(one({ type: 'code', code: 'x'.repeat(MAX_CODE_LEN + 50) }).code.length).toBe(MAX_CODE_LEN)
    expect(one({ type: 'mermaid', code: 'x'.repeat(MAX_MERMAID_LEN + 50) }).code.length).toBe(MAX_MERMAID_LEN)
  })

  it('accepts a divider with no fields', () => {
    expect(one({ type: 'divider' })).toEqual({ type: 'divider' })
  })

  it('keeps collapsible children and its initial state', () => {
    const n = one({ type: 'collapsible', title: 'more', open: true, children: [{ type: 'text', text: 'x' }] })
    expect(n.open).toBe(true)
    expect(n.children.length).toBe(1)
  })
})

describe('plot', () => {
  it('rejects an unknown plot kind', () => {
    expect(one({ type: 'plot', plot: 'sankey', series: [{ points: [{ label: 'a', value: 1 }] }] })).toBeUndefined()
  })

  it('rejects a plot with nothing to draw', () => {
    expect(one({ type: 'plot', plot: 'bar', series: [] })).toBeUndefined()
    expect(one({ type: 'plot', plot: 'bar', series: [{ points: [] }] })).toBeUndefined()
  })

  it('drops non-finite points rather than letting a NaN poison the axis', () => {
    const n = one({
      type: 'plot',
      plot: 'line',
      series: [{ points: [{ label: 'a', value: 1 }, { label: 'b', value: 'x' }, { label: 'c', value: null }] }],
    })
    expect(n.series[0].points.length).toBe(1)
  })

  it('trims to the colour sequence and the point cap', () => {
    const many = Array.from({ length: MAX_PLOT_SERIES + 3 }, (_, i) => ({
      name: `s${i}`,
      points: Array.from({ length: MAX_PLOT_POINTS + 10 }, (_, j) => ({ label: `p${j}`, value: j })),
    }))
    const n = one({ type: 'plot', plot: 'bar', series: many })
    expect(n.series.length).toBe(MAX_PLOT_SERIES)
    expect(n.series[0].points.length).toBe(MAX_PLOT_POINTS)
  })

  it('clamps height to something a chat column can show', () => {
    expect(one({ type: 'plot', plot: 'bar', height: 4000, series: [{ points: [{ label: 'a', value: 1 }] }] }).height).toBe(400)
  })
})

describe('table caps', () => {
  it('trims past the raised row cap instead of rejecting', () => {
    const rows = Array.from({ length: MAX_TABLE_ROWS + 25 }, (_, i) => [String(i)])
    expect(one({ type: 'table', columns: ['n'], rows }).rows.length).toBe(MAX_TABLE_ROWS)
  })

  it('drops a filter naming a column that does not exist', () => {
    const t = one({ type: 'table', columns: ['a'], rows: [], filterBy: { field: 'q', column: 'nope' } })
    expect(t.filterBy).toBeUndefined()
  })
})

describe('whitelist separation', () => {
  it('keeps interactive nodes off the render_ui path', () => {
    const card = sanitizeSpec({ items: [{ type: 'slider', field: 'n', min: 0, max: 1 }] }, READ_ONLY_NODE_TYPES)
    expect(card.spec?.items.length).toBe(0)
  })

  it('allows the new content nodes on the render_ui path', () => {
    const card = sanitizeSpec({ items: [{ type: 'divider' }, { type: 'code', code: 'ls' }] }, READ_ONLY_NODE_TYPES)
    expect(card.spec?.items.length).toBe(2)
  })
})
