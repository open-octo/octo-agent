// GenUI spec types — the JSON tree a model emits either as the render_ui
// tool's "spec" argument or inline in reply text inside a ```octo-ui fence.
// See dev-docs/genui-design.md, "GenUI spec shape". Field names here must
// stay in lockstep with the Go guard (internal/tools/genui/guard.go) and the
// genui skill's teaching content.

/** Read-only node types shipped by both the render_ui tool card and inline
 * octo-ui fences. Interactive node types (button/input/select/…) are added
 * by a later slice and are octo-ui-fence-only — render_ui never accepts
 * them. */
export type GenuiReadOnlyType =
  | 'text'
  | 'row'
  | 'col'
  | 'card'
  | 'list'
  | 'table'
  | 'keyvalue'
  | 'stat'
  | 'badge'
  | 'progress'
  | 'callout'

export interface GenuiTextNode {
  type: 'text'
  text: string
  tone?: 'default' | 'muted' | 'danger'
}

export interface GenuiRowNode {
  type: 'row' | 'col'
  gap?: number
  children: GenuiNode[]
}

export interface GenuiCardNode {
  type: 'card'
  title?: string
  children: GenuiNode[]
}

export type GenuiListItem = string | { label: string; value?: string }

export interface GenuiListNode {
  type: 'list'
  items: GenuiListItem[]
}

export interface GenuiTableNode {
  type: 'table'
  columns: string[]
  rows: (string | number)[][]
  /** Case-insensitive substring match of `field`'s value against `column`.
   * Inline-fence only — a render_ui card has no field to read. */
  filterBy?: { field: string; column: string }
  /** Clickable headers cycling none → ascending → descending, locally. */
  sortable?: boolean
}

export interface GenuiKeyValueNode {
  type: 'keyvalue'
  items: { label: string; value: string }[]
}

export interface GenuiStatNode {
  type: 'stat'
  label: string
  value: string
  delta?: string
  tone?: 'up' | 'down' | 'neutral'
}

export interface GenuiBadgeNode {
  type: 'badge'
  text: string
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'info'
}

export interface GenuiProgressNode {
  type: 'progress'
  value: number
  label?: string
}

export interface GenuiCalloutNode {
  type: 'callout'
  tone?: 'info' | 'success' | 'warning' | 'danger'
  title?: string
  text?: string
}

// ─── Slice B — interactive node types (inline octo-ui-fence only) ──────────
// render_ui's tool-card path never accepts these — see guard.ts's
// INTERACTIVE_NODE_TYPES / the Go guard's ReadOnlyNodeTypes, which does not
// mirror them.
export type GenuiInteractiveType = 'button' | 'input' | 'select' | 'checkbox' | 'switch' | 'radio' | 'tabs'

export interface GenuiButtonNode {
  type: 'button'
  label: string
  action: string
  payload?: Record<string, unknown>
  variant?: 'primary' | 'default' | 'danger'
}

// Deliberately no `inputType` field — always a plain <input type="text">.
// See dev-docs/genui-design.md "Security design": no password-style input,
// by construction rather than by validation.
export interface GenuiInputNode {
  type: 'input'
  field: string
  label?: string
  placeholder?: string
  value?: string
}

export interface GenuiOption {
  label: string
  value: string
}

export interface GenuiSelectNode {
  type: 'select'
  field: string
  label?: string
  options: GenuiOption[]
  value?: string
}

export interface GenuiCheckboxNode {
  type: 'checkbox' | 'switch'
  field: string
  label?: string
  checked?: boolean
}

export interface GenuiRadioNode {
  type: 'radio'
  field: string
  label?: string
  options: GenuiOption[]
  value?: string
}

export interface GenuiTabsNode {
  type: 'tabs'
  tabs: { label: string; children: GenuiNode[] }[]
}

// ─── Local interaction — conditions and the nodes that feed them ───────────
// See dev-docs/genui-interactive-panels-design.md, "Local interaction".

/**
 * A declarative condition over one field's current value. Deliberately a
 * structured object rather than an expression string: the guard can validate
 * it field by field, and there is no parser or evaluator anywhere in the
 * render path (dev-docs/genui-design.md's "no eval/new Function" property).
 *
 * Two families, resolved by one rule:
 *  - If any of equals/in/not is present, that family decides — first present
 *    in the order equals, in, not; the rest are dropped by the guard so
 *    semantics never depend on JSON key order.
 *  - Otherwise every present range predicate must hold, ANDed, so
 *    {gte: 10, lt: 100} is the half-open interval a range filter means.
 */
export interface GenuiCondition {
  field: string
  equals?: string | number | boolean
  in?: (string | number)[]
  not?: string | number | boolean
  gt?: number
  gte?: number
  lt?: number
  lte?: number
}

/** Mixed into every node type: any node may be conditionally shown. */
export interface GenuiVisibility {
  visibleWhen?: GenuiCondition
}

export interface GenuiSliderNode {
  type: 'slider'
  field: string
  min: number
  max: number
  step?: number
  label?: string
  value?: number
}

// A distinct node rather than an `inputType` on `input`: re-adding that field
// — even restricted to "number" — would reopen exactly the hole the comment
// on GenuiInputNode closes, leaving the guard as the only thing between a
// model and type="password". Keeping them separate keeps that structural.
export interface GenuiNumberNode {
  type: 'number'
  field: string
  min?: number
  max?: number
  step?: number
  label?: string
  value?: number
}

export interface GenuiTextareaNode {
  type: 'textarea'
  field: string
  label?: string
  placeholder?: string
  value?: string
  rows?: number
}

export interface GenuiQuizNode {
  type: 'quiz'
  field: string
  question: string
  options: GenuiOption[]
  correct: string
  explanation?: string
}

// ─── Structure and content ─────────────────────────────────────────────────

export interface GenuiCollapsibleNode {
  type: 'collapsible'
  title: string
  children: GenuiNode[]
  /** Initial state only; the user's toggle is what persists afterwards. */
  open?: boolean
}

export interface GenuiCodeNode {
  type: 'code'
  code: string
  lang?: string
}

export interface GenuiDividerNode {
  type: 'divider'
}

export interface GenuiPlotSeries {
  name?: string
  points: { label: string; value: number }[]
}

export interface GenuiPlotNode {
  type: 'plot'
  plot: 'bar' | 'line' | 'area' | 'pie'
  series: GenuiPlotSeries[]
  stacked?: boolean
  legend?: boolean
  xLabel?: string
  yLabel?: string
  height?: number
}

export interface GenuiMermaidNode {
  type: 'mermaid'
  code: string
}

type GenuiNodeVariant =
  | GenuiTextNode
  | GenuiRowNode
  | GenuiCardNode
  | GenuiListNode
  | GenuiTableNode
  | GenuiKeyValueNode
  | GenuiStatNode
  | GenuiBadgeNode
  | GenuiProgressNode
  | GenuiCalloutNode
  | GenuiButtonNode
  | GenuiInputNode
  | GenuiSelectNode
  | GenuiCheckboxNode
  | GenuiRadioNode
  | GenuiTabsNode
  | GenuiSliderNode
  | GenuiNumberNode
  | GenuiTextareaNode
  | GenuiQuizNode
  | GenuiCollapsibleNode
  | GenuiCodeNode
  | GenuiDividerNode
  | GenuiPlotNode
  | GenuiMermaidNode

// Intersecting the union with GenuiVisibility distributes over the members,
// so `node.type === 'text'` still narrows while every variant gains the
// optional visibleWhen.
export type GenuiNode = GenuiNodeVariant & GenuiVisibility

/** A field's current value. Numbers joined string|boolean when slider/number
 * arrived — storing them as strings would push a Number() coercion into every
 * range comparison and hand the model "30" where it wrote 30. */
export type GenuiFieldValue = string | number | boolean

export interface GenuiSpec {
  /**
   * Panel identity, scoped to one session. A spec with an id is addressable:
   * it participates in projection, its interaction state persists, and it can
   * be the target of a silent turn. Without one, the panel behaves exactly as
   * it did before this feature — rendered where it appears, state in memory.
   */
  id?: string
  title?: string
  items: GenuiNode[]
}
