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
export const MAX_STRING_LEN = 500
export const MAX_TABLE_CELL_LEN = 2000
export const MAX_LIST_ITEMS = 200
export const MAX_TABLE_ROWS = 100
export const MAX_TABLE_COLUMNS = 50

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

    case 'table':
      return {
        type: 'table',
        columns: sanitizeStringArray(node, 'columns', MAX_TABLE_COLUMNS, MAX_STRING_LEN),
        rows: sanitizeTableRows(node),
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

    default:
      return null
  }
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
