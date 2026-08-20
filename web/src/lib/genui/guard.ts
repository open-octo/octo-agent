// TS-side GenUI spec guard — an independent implementation of the same
// whitelist/cap policy as the Go guard (internal/tools/genui/guard.go), per
// dev-docs/genui-design.md's "Security design". It is the ONLY guard on the
// inline octo-ui-fence path (which never touches Go) and a second,
// independent pass on the render_ui tool-card path (defense in depth — the
// frontend must not trust any JSON blob it renders, tool-sourced or
// inline-fence-sourced).
//
// Cap values here must stay in lockstep with the Go guard's constants.

import type { GenuiNode, GenuiSpec } from './types'

export const MAX_DEPTH = 8
export const MAX_NODES = 200
// Same numeric value as the Go guard's MaxStringLen, but a different unit:
// this clamps UTF-16 code units (clampString below), while the Go side
// clamps bytes. Both are correct clamps for their own string
// representation, but the same input can survive to a different length
// through the two guards (e.g. non-ASCII text) — don't assume "same
// constant value" implies "same character count kept."
export const MAX_STRING_LEN = 500
export const MAX_TABLE_CELL_LEN = 2000
export const MAX_LIST_ITEMS = 200
// Raised from 100 when local filtering arrived: the core case is "here is the
// data set, narrow it down", and 100 rows is below the size where offering a
// filter is worth it. See dev-docs/genui-interactive-panels-design.md.
export const MAX_TABLE_ROWS = 500
export const MAX_TABLE_COLUMNS = 50
// Slice B caps (select/radio options, tabs) — see dev-docs/genui-design.md
// "GenUI spec shape".
export const MAX_OPTIONS = 50
export const MAX_TABS = 8

// Interactive-panel caps — see dev-docs/genui-interactive-panels-design.md.
export const MAX_PANEL_ID_LEN = 64
export const MAX_MERMAID_LEN = 5000
export const MAX_CODE_LEN = 5000
export const MAX_TEXTAREA_LEN = 5000
export const MAX_TEXTAREA_ROWS = 12
export const MIN_TEXTAREA_ROWS = 2
export const MAX_PLOT_POINTS = 100
/** Matches the fixed colour sequence a plot draws from — a ninth series would
 * have no distinct colour left to take. */
export const MAX_PLOT_SERIES = 8
/** Numeric fields (slider/number bounds) are clamped to a finite range rather
 * than trusted, the same posture progress.value already takes. */
export const MAX_NUMERIC = 1e9

/** Panel ids become part of a localStorage key and are compared against ids
 * parsed out of model output, so the character class is restricted to remove
 * any question of escaping. */
const PANEL_ID_RE = /^[A-Za-z0-9_-]+$/

/** Field names beginning with "__" are reserved for internal panel state
 * (tab index, fold state) and must stay unaddressable from a spec. */
function isReservedField(name: string): boolean {
  return name.startsWith('__')
}

/** Node "type" values render_ui / inline octo-ui fences accept in Slice A
 * (read-only components). Interactive types are added by a later slice. */
export const READ_ONLY_NODE_TYPES: ReadonlySet<string> = new Set([
  'text',
  'row',
  'col',
  'card',
  'list',
  'table',
  'keyvalue',
  'stat',
  'badge',
  'progress',
  'callout',
  // These carry no field and fire no action, so a render_ui tool-result card
  // can hold them too. `collapsible` does toggle, but folding is presentation
  // rather than input: it reports nothing back and needs no field.
  'collapsible',
  'code',
  'divider',
  'plot',
  'mermaid',
])

/** Interactive node "type" values, added by Slice B. Inline-octo-ui-fence
 * only — render_ui's tool-card path never accepts these, and the Go guard
 * (internal/tools/genui/guard.go, ReadOnlyNodeTypes) does not mirror them. */
export const INTERACTIVE_NODE_TYPES: ReadonlySet<string> = new Set([
  'button',
  'input',
  'select',
  'checkbox',
  'switch',
  'radio',
  'tabs',
  'slider',
  'number',
  'textarea',
  'quiz',
])

export interface SanitizeResult {
  /** The cleaned spec, or null when the input has no valid "items" array
   * (a hard contract violation) or isn't an object at all (e.g. a
   * still-streaming partial parse that hasn't produced valid JSON yet). */
  spec: GenuiSpec | null
  /** Total number of nodes kept (0 when spec is null). */
  count: number
}

/** Validates and clamps a GenUI spec against the allowed node-type
 * whitelist. Unknown-type nodes (and their subtree) are dropped rather than
 * failing the whole spec; a spec whose node count would exceed MAX_NODES is
 * trimmed, not rejected. Returns { spec: null, count: 0 } only when the
 * top-level shape itself is invalid (no "items" array). */
export function sanitizeSpec(input: unknown, allowed: ReadonlySet<string>): SanitizeResult {
  if (typeof input !== 'object' || input === null) {
    return { spec: null, count: 0 }
  }
  const raw = input as Record<string, unknown>
  const items = raw.items
  if (!Array.isArray(items)) {
    return { spec: null, count: 0 }
  }

  const count = { value: 0 }
  const cleanedItems: GenuiNode[] = []
  for (const item of items) {
    if (count.value >= MAX_NODES) break
    if (typeof item !== 'object' || item === null) continue
    const cleaned = sanitizeNode(item as Record<string, unknown>, allowed, 1, count)
    if (cleaned) cleanedItems.push(cleaned)
  }

  const spec: GenuiSpec = { items: cleanedItems }
  // An id that fails validation is dropped, degrading the panel to anonymous
  // — never an error, consistent with every other guard violation here.
  if (typeof raw.id === 'string' && raw.id.length <= MAX_PANEL_ID_LEN && PANEL_ID_RE.test(raw.id)) {
    spec.id = raw.id
  }
  if (typeof raw.title === 'string') {
    spec.title = clampString(raw.title, MAX_STRING_LEN)
  }
  return { spec, count: count.value }
}

function sanitizeNode(
  node: Record<string, unknown>,
  allowed: ReadonlySet<string>,
  depth: number,
  count: { value: number }
): GenuiNode | null {
  if (depth > MAX_DEPTH || count.value >= MAX_NODES) return null
  const type = node.type
  if (typeof type !== 'string' || !allowed.has(type)) return null
  // Reserve this node's own slot before recursing into any children — see
  // the Go guard's identical comment for why (a chain of containers can
  // otherwise push the total past MAX_NODES by up to MAX_DEPTH-1).
  count.value++

  const out = sanitizeByType(node, allowed, depth, count, type)
  if (out === null) return null
  // Every node type may carry visibleWhen, so it is validated once here
  // rather than repeated in each case below.
  const cond = sanitizeCondition(node.visibleWhen)
  if (cond) (out as unknown as Record<string, unknown>).visibleWhen = cond
  return out
}

function sanitizeByType(
  node: Record<string, unknown>,
  allowed: ReadonlySet<string>,
  depth: number,
  count: { value: number },
  type: string
): GenuiNode | null {
  switch (type) {
    case 'text':
      return withTone({ type: 'text', text: clampString(stringField(node, 'text'), MAX_STRING_LEN) }, node, [
        'default',
        'muted',
        'danger',
      ]) as GenuiNode

    case 'row':
    case 'col': {
      const out: any = { type, children: sanitizeChildren(node, allowed, depth, count) }
      const gap = numberField(node, 'gap')
      if (gap !== undefined) out.gap = clampNumber(gap, 0, 64)
      return out
    }

    case 'card': {
      const out: any = { type: 'card', children: sanitizeChildren(node, allowed, depth, count) }
      if (typeof node.title === 'string') out.title = clampString(node.title, MAX_STRING_LEN)
      return out
    }

    case 'list':
      return { type: 'list', items: sanitizeListItems(node) }

    case 'table': {
      const columns = sanitizeStringArray(node, 'columns', MAX_TABLE_COLUMNS, MAX_STRING_LEN)
      const out: any = { type: 'table', columns, rows: sanitizeTableRows(node) }
      if (typeof node.sortable === 'boolean') out.sortable = node.sortable
      const fb = node.filterBy
      if (typeof fb === 'object' && fb !== null && !Array.isArray(fb)) {
        const rec = fb as Record<string, unknown>
        const field = stringField(rec, 'field')
        const column = stringField(rec, 'column')
        // An unmatched column name is dropped rather than kept as a filter
        // that silently matches nothing.
        if (field !== '' && !isReservedField(field) && columns.includes(column)) {
          out.filterBy = { field: clampString(field, MAX_STRING_LEN), column }
        }
      }
      return out
    }

    case 'keyvalue':
      return { type: 'keyvalue', items: sanitizeKeyValueItems(node) }

    case 'stat': {
      const out: any = withTone(
        {
          type: 'stat',
          label: clampString(stringField(node, 'label'), MAX_STRING_LEN),
          value: clampString(stringField(node, 'value'), MAX_STRING_LEN),
        },
        node,
        ['up', 'down', 'neutral']
      )
      if (typeof node.delta === 'string') out.delta = clampString(node.delta, MAX_STRING_LEN)
      return out
    }

    case 'badge':
      return withTone({ type: 'badge', text: clampString(stringField(node, 'text'), MAX_STRING_LEN) }, node, [
        'default',
        'success',
        'warning',
        'danger',
        'info',
      ]) as GenuiNode

    case 'progress': {
      const out: any = { type: 'progress', value: clampNumber(numberField(node, 'value') ?? 0, 0, 100) }
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      return out
    }

    case 'callout': {
      const out: any = withTone({ type: 'callout' }, node, ['info', 'success', 'warning', 'danger'])
      if (typeof node.title === 'string') out.title = clampString(node.title, MAX_STRING_LEN)
      if (typeof node.text === 'string') out.text = clampString(node.text, MAX_STRING_LEN)
      return out
    }

    case 'button': {
      const out: any = {
        type: 'button',
        label: clampString(stringField(node, 'label'), MAX_STRING_LEN),
        action: clampString(stringField(node, 'action'), MAX_STRING_LEN),
      }
      const variant = node.variant
      if (typeof variant === 'string' && ['primary', 'default', 'danger'].includes(variant)) out.variant = variant
      if (typeof node.payload === 'object' && node.payload !== null && !Array.isArray(node.payload)) {
        out.payload = sanitizePayload(node.payload as Record<string, unknown>)
      }
      return out
    }

    case 'input': {
      // No `inputType` field is ever read or passed through — see the
      // security note on GenuiInputNode in types.ts. Any such field the
      // model sends anyway is silently dropped by construction (this
      // switch only ever assembles the fields listed below).
      const field = fieldName(node)
      if (field === null) return null
      const out: any = { type: 'input', field }
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      if (typeof node.placeholder === 'string') out.placeholder = clampString(node.placeholder, MAX_STRING_LEN)
      if (typeof node.value === 'string') out.value = clampString(node.value, MAX_STRING_LEN)
      return out
    }

    case 'select': {
      const field = fieldName(node)
      if (field === null) return null
      const out: any = { type: 'select', field, options: sanitizeOptions(node) }
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      if (typeof node.value === 'string') out.value = clampString(node.value, MAX_STRING_LEN)
      return out
    }

    case 'checkbox':
    case 'switch': {
      const field = fieldName(node)
      if (field === null) return null
      const out: any = { type, field }
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      if (typeof node.checked === 'boolean') out.checked = node.checked
      return out
    }

    case 'radio': {
      const field = fieldName(node)
      if (field === null) return null
      const out: any = { type: 'radio', field, options: sanitizeOptions(node) }
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      if (typeof node.value === 'string') out.value = clampString(node.value, MAX_STRING_LEN)
      return out
    }

    case 'tabs':
      return { type: 'tabs', tabs: sanitizeTabs(node, allowed, depth, count) }

    case 'slider': {
      const field = fieldName(node)
      if (field === null) return null
      const min = clampNumber(numberField(node, 'min') ?? 0, -MAX_NUMERIC, MAX_NUMERIC)
      const max = clampNumber(numberField(node, 'max') ?? 0, -MAX_NUMERIC, MAX_NUMERIC)
      // A zero- or negative-width slider has no meaningful rendering, so the
      // node is dropped rather than silently repaired into something the
      // model didn't ask for.
      if (!(max > min)) return null
      const out: any = { type: 'slider', field, min, max }
      const step = numberField(node, 'step')
      out.step = step !== undefined && step > 0 ? clampNumber(step, 0, max - min) : (max - min) / 100
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      const value = numberField(node, 'value')
      if (value !== undefined) out.value = clampNumber(value, min, max)
      return out
    }

    case 'number': {
      const field = fieldName(node)
      if (field === null) return null
      const out: any = { type: 'number', field }
      const min = numberField(node, 'min')
      const max = numberField(node, 'max')
      if (min !== undefined) out.min = clampNumber(min, -MAX_NUMERIC, MAX_NUMERIC)
      if (max !== undefined) out.max = clampNumber(max, -MAX_NUMERIC, MAX_NUMERIC)
      const step = numberField(node, 'step')
      if (step !== undefined && step > 0) out.step = clampNumber(step, 0, MAX_NUMERIC)
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      const value = numberField(node, 'value')
      if (value !== undefined) {
        out.value = clampNumber(value, out.min ?? -MAX_NUMERIC, out.max ?? MAX_NUMERIC)
      }
      return out
    }

    case 'textarea': {
      const field = fieldName(node)
      if (field === null) return null
      const out: any = { type: 'textarea', field }
      if (typeof node.label === 'string') out.label = clampString(node.label, MAX_STRING_LEN)
      if (typeof node.placeholder === 'string') out.placeholder = clampString(node.placeholder, MAX_STRING_LEN)
      if (typeof node.value === 'string') out.value = clampString(node.value, MAX_TEXTAREA_LEN)
      const rows = numberField(node, 'rows')
      if (rows !== undefined) out.rows = clampNumber(Math.round(rows), MIN_TEXTAREA_ROWS, MAX_TEXTAREA_ROWS)
      return out
    }

    case 'quiz': {
      const field = fieldName(node)
      if (field === null) return null
      const out: any = {
        type: 'quiz',
        field,
        question: clampString(stringField(node, 'question'), MAX_STRING_LEN),
        options: sanitizeOptions(node),
        correct: clampString(stringField(node, 'correct'), MAX_STRING_LEN),
      }
      if (typeof node.explanation === 'string') out.explanation = clampString(node.explanation, MAX_STRING_LEN)
      return out
    }

    case 'collapsible': {
      const out: any = {
        type: 'collapsible',
        title: clampString(stringField(node, 'title'), MAX_STRING_LEN),
        children: sanitizeChildren(node, allowed, depth, count),
      }
      if (typeof node.open === 'boolean') out.open = node.open
      return out
    }

    case 'code': {
      const out: any = { type: 'code', code: clampString(stringField(node, 'code'), MAX_CODE_LEN) }
      if (typeof node.lang === 'string') out.lang = clampString(node.lang, MAX_STRING_LEN)
      return out
    }

    case 'divider':
      return { type: 'divider' } as GenuiNode

    case 'plot': {
      const kind = node.plot
      if (typeof kind !== 'string' || !['bar', 'line', 'area', 'pie'].includes(kind)) return null
      const series = sanitizePlotSeries(node)
      if (series.length === 0) return null
      const out: any = { type: 'plot', plot: kind, series }
      if (typeof node.stacked === 'boolean') out.stacked = node.stacked
      if (typeof node.legend === 'boolean') out.legend = node.legend
      if (typeof node.xLabel === 'string') out.xLabel = clampString(node.xLabel, MAX_STRING_LEN)
      if (typeof node.yLabel === 'string') out.yLabel = clampString(node.yLabel, MAX_STRING_LEN)
      const height = numberField(node, 'height')
      if (height !== undefined) out.height = clampNumber(Math.round(height), 80, 400)
      return out
    }

    case 'mermaid':
      return { type: 'mermaid', code: clampString(stringField(node, 'code'), MAX_MERMAID_LEN) } as GenuiNode

    default:
      return null
  }
}

/** A node's field name, or null when it is missing, empty, or reserved —
 * in which case the caller drops the node, since an input nothing can read
 * is not worth rendering. */
function fieldName(node: Record<string, unknown>): string | null {
  const raw = stringField(node, 'field')
  if (raw === '' || isReservedField(raw)) return null
  return clampString(raw, MAX_STRING_LEN)
}

/**
 * Validates a visibleWhen condition. The equality family wins as a whole when
 * any of its members is present (first of equals/in/not, others dropped), so
 * the outcome never depends on JSON key ordering; otherwise the range
 * predicates that are present are kept and ANDed at evaluation time.
 * Returns null when there is nothing valid to keep.
 */
function sanitizeCondition(input: unknown): Record<string, unknown> | null {
  if (typeof input !== 'object' || input === null || Array.isArray(input)) return null
  const raw = input as Record<string, unknown>
  const field = stringField(raw, 'field')
  if (field === '') return null
  const out: Record<string, unknown> = { field: clampString(field, MAX_STRING_LEN) }

  if (isScalar(raw.equals)) {
    out.equals = clampScalar(raw.equals)
    return out
  }
  if (Array.isArray(raw.in)) {
    const list = raw.in
      .filter(v => typeof v === 'string' || (typeof v === 'number' && Number.isFinite(v)))
      .slice(0, MAX_OPTIONS)
      .map(v => clampScalar(v) as string | number)
    if (list.length > 0) {
      out.in = list
      return out
    }
  }
  if (isScalar(raw.not)) {
    out.not = clampScalar(raw.not)
    return out
  }

  let hasRange = false
  for (const key of ['gt', 'gte', 'lt', 'lte'] as const) {
    const v = raw[key]
    if (typeof v === 'number' && Number.isFinite(v)) {
      out[key] = clampNumber(v, -MAX_NUMERIC, MAX_NUMERIC)
      hasRange = true
    }
  }
  return hasRange ? out : null
}

function isScalar(v: unknown): boolean {
  return typeof v === 'string' || typeof v === 'boolean' || (typeof v === 'number' && Number.isFinite(v))
}

function clampScalar(v: unknown): string | number | boolean {
  if (typeof v === 'string') return clampString(v, MAX_STRING_LEN)
  if (typeof v === 'number') return clampNumber(v, -MAX_NUMERIC, MAX_NUMERIC)
  return v as boolean
}

function sanitizePlotSeries(node: Record<string, unknown>): { name?: string; points: { label: string; value: number }[] }[] {
  const series = node.series
  if (!Array.isArray(series)) return []
  const out: { name?: string; points: { label: string; value: number }[] }[] = []
  for (const s of series) {
    if (out.length >= MAX_PLOT_SERIES) break
    if (typeof s !== 'object' || s === null || Array.isArray(s)) continue
    const rec = s as Record<string, unknown>
    if (!Array.isArray(rec.points)) continue
    const points: { label: string; value: number }[] = []
    for (const p of rec.points) {
      if (points.length >= MAX_PLOT_POINTS) break
      if (typeof p !== 'object' || p === null || Array.isArray(p)) continue
      const pr = p as Record<string, unknown>
      const value = pr.value
      // Non-finite values are dropped rather than coerced: a NaN would poison
      // every axis calculation downstream.
      if (typeof value !== 'number' || !Number.isFinite(value)) continue
      points.push({
        label: clampString(stringField(pr, 'label'), MAX_STRING_LEN),
        value: clampNumber(value, -MAX_NUMERIC, MAX_NUMERIC),
      })
    }
    if (points.length === 0) continue
    const entry: { name?: string; points: { label: string; value: number }[] } = { points }
    if (typeof rec.name === 'string') entry.name = clampString(rec.name, MAX_STRING_LEN)
    out.push(entry)
  }
  return out
}

function sanitizeChildren(
  node: Record<string, unknown>,
  allowed: ReadonlySet<string>,
  depth: number,
  count: { value: number }
): GenuiNode[] {
  const children = node.children
  if (!Array.isArray(children)) return []
  const out: GenuiNode[] = []
  for (const child of children) {
    if (count.value >= MAX_NODES) break
    if (typeof child !== 'object' || child === null) continue
    const cleaned = sanitizeNode(child as Record<string, unknown>, allowed, depth + 1, count)
    if (cleaned) out.push(cleaned)
  }
  return out
}

function sanitizeListItems(node: Record<string, unknown>): (string | { label: string; value?: string })[] {
  const items = node.items
  if (!Array.isArray(items)) return []
  const out: (string | { label: string; value?: string })[] = []
  for (const item of items) {
    if (out.length >= MAX_LIST_ITEMS) break
    if (typeof item === 'string') {
      out.push(clampString(item, MAX_STRING_LEN))
    } else if (typeof item === 'object' && item !== null) {
      const rec = item as Record<string, unknown>
      const entry: { label: string; value?: string } = { label: clampString(stringField(rec, 'label'), MAX_STRING_LEN) }
      if (typeof rec.value === 'string') entry.value = clampString(rec.value, MAX_STRING_LEN)
      out.push(entry)
    }
  }
  return out
}

function sanitizeKeyValueItems(node: Record<string, unknown>): { label: string; value: string }[] {
  const items = node.items
  if (!Array.isArray(items)) return []
  const out: { label: string; value: string }[] = []
  for (const item of items) {
    if (out.length >= MAX_LIST_ITEMS) break
    if (typeof item !== 'object' || item === null) continue
    const rec = item as Record<string, unknown>
    out.push({
      label: clampString(stringField(rec, 'label'), MAX_STRING_LEN),
      value: clampString(stringField(rec, 'value'), MAX_STRING_LEN),
    })
  }
  return out
}

function sanitizeOptions(node: Record<string, unknown>): { label: string; value: string }[] {
  const raw = node.options
  if (!Array.isArray(raw)) return []
  const out: { label: string; value: string }[] = []
  for (const opt of raw) {
    if (out.length >= MAX_OPTIONS) break
    if (typeof opt !== 'object' || opt === null) continue
    const rec = opt as Record<string, unknown>
    out.push({
      label: clampString(stringField(rec, 'label'), MAX_STRING_LEN),
      value: clampString(stringField(rec, 'value'), MAX_STRING_LEN),
    })
  }
  return out
}

function sanitizeTabs(
  node: Record<string, unknown>,
  allowed: ReadonlySet<string>,
  depth: number,
  count: { value: number }
): { label: string; children: GenuiNode[] }[] {
  const raw = node.tabs
  if (!Array.isArray(raw)) return []
  const out: { label: string; children: GenuiNode[] }[] = []
  for (const tab of raw) {
    if (out.length >= MAX_TABS) break
    if (typeof tab !== 'object' || tab === null) continue
    const rec = tab as Record<string, unknown>
    out.push({
      label: clampString(stringField(rec, 'label'), MAX_STRING_LEN),
      children: sanitizeChildren(rec, allowed, depth, count),
    })
  }
  return out
}

// payload is never rendered as HTML/text (see GenuiButtonNode) — it only
// round-trips through the [octo-ui-action] synthetic message back to the
// model, the same trust boundary an ordinary typed chat message already
// has. This still bounds its size/depth defensively, same spirit as every
// other field here, rather than passing it through completely unchecked.
// sanitizePayload's depth (MAX_DEPTH) and per-level width (reusing
// MAX_OPTIONS as a generic "how many keys/array entries at this level" cap,
// not because it's semantically the select/radio options cap) bound one
// button's payload in isolation. They are NOT counted against the spec-wide
// MAX_NODES node budget — a spec with MAX_NODES button nodes, each carrying
// a near-max-size payload, carries more total JSON than "200 nodes" alone
// suggests. Not a safety gap (this data never renders as HTML/DOM, it only
// ever gets JSON.stringify'd into an [octo-ui-action] reply — see
// ChatView.svelte's sendGenuiAction — and its total size is still bounded by
// the input JSON text size itself), but worth knowing if a future caller
// wants a tighter combined budget.
function sanitizePayload(obj: Record<string, unknown>, depth = 1): Record<string, unknown> {
  if (depth > MAX_DEPTH) return {}
  const out: Record<string, unknown> = {}
  let n = 0
  for (const [k, v] of Object.entries(obj)) {
    if (n >= MAX_OPTIONS) break
    out[clampString(k, MAX_STRING_LEN)] = sanitizePayloadValue(v, depth)
    n++
  }
  return out
}

function sanitizePayloadValue(v: unknown, depth: number): unknown {
  if (typeof v === 'string') return clampString(v, MAX_STRING_LEN)
  if (typeof v === 'number') return Number.isFinite(v) ? v : 0
  if (typeof v === 'boolean' || v === null) return v
  if (Array.isArray(v)) {
    if (depth + 1 > MAX_DEPTH) return []
    return v.slice(0, MAX_OPTIONS).map((item) => sanitizePayloadValue(item, depth + 1))
  }
  if (typeof v === 'object') return sanitizePayload(v as Record<string, unknown>, depth + 1)
  return null
}

function sanitizeStringArray(node: Record<string, unknown>, key: string, maxItems: number, maxLen: number): string[] {
  const raw = node[key]
  if (!Array.isArray(raw)) return []
  const out: string[] = []
  for (const v of raw) {
    if (out.length >= maxItems) break
    if (typeof v === 'string') out.push(clampString(v, maxLen))
  }
  return out
}

// A cell of any other JSON type becomes an empty-string placeholder rather
// than being skipped — skipping would shift every later cell in that row
// left by one, silently misaligning it against the table's "columns"
// header (same reasoning as the Go guard's sanitizeTableRows).
function sanitizeTableRows(node: Record<string, unknown>): (string | number)[][] {
  const rows = node.rows
  if (!Array.isArray(rows)) return []
  const out: (string | number)[][] = []
  for (const row of rows) {
    if (out.length >= MAX_TABLE_ROWS) break
    if (!Array.isArray(row)) continue
    const cleanedRow: (string | number)[] = []
    for (const cell of row) {
      if (cleanedRow.length >= MAX_TABLE_COLUMNS) break
      if (typeof cell === 'string') {
        cleanedRow.push(clampString(cell, MAX_TABLE_CELL_LEN))
      } else if (typeof cell === 'number' && Number.isFinite(cell)) {
        cleanedRow.push(cell)
      } else {
        cleanedRow.push('')
      }
    }
    out.push(cleanedRow)
  }
  return out
}

function withTone<T extends Record<string, unknown>>(out: T, node: Record<string, unknown>, allowed: string[]): T {
  const tone = node.tone
  if (typeof tone === 'string' && allowed.includes(tone)) {
    ;(out as any).tone = tone
  }
  return out
}

function stringField(node: Record<string, unknown>, key: string): string {
  const v = node[key]
  return typeof v === 'string' ? v : ''
}

function numberField(node: Record<string, unknown>, key: string): number | undefined {
  const v = node[key]
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined
}

function clampNumber(v: number, lo: number, hi: number): number {
  return Math.min(Math.max(v, lo), hi)
}

// Truncates s to at most maxLen UTF-16 code units, backing off by one when
// the cut would split a surrogate pair (e.g. an emoji) — a code-unit-blind
// slice can otherwise leave a lone unpaired surrogate, which downstream
// JSON encoding round-trips as U+FFFD.
function clampString(s: string, maxLen: number): string {
  if (s.length <= maxLen) return s
  let cut = maxLen
  const code = s.charCodeAt(cut - 1)
  // A high surrogate (0xD800–0xDBFF) as the last code unit means the cut
  // landed between it and its low surrogate — back off one more unit.
  if (code >= 0xd800 && code <= 0xdbff) cut--
  return s.slice(0, cut)
}
