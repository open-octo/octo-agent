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

export type GenuiNode =
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

export interface GenuiSpec {
  title?: string
  items: GenuiNode[]
}
