// Open/closed decision for a tool card in ToolGroup.
//
// The card is a native <details>. Its `open` can't bind straight to the default
// below: the default is a derived value the {#each} re-asserts on every streaming
// re-render, so a manual collapse of the auto-opened last tool (or an error card)
// would be reverted on the next tick and the fold would appear not to work. So we
// keep a per-tool override, seeded by the default, that wins once the user
// diverges from it.

export interface ToolLike {
  id: string
  error?: unknown
}

// Default open state: errors always open (they need attention); while the turn
// is streaming the latest tool stays open so its output is readable until the
// next tool replaces it; everything else is closed.
export function defaultToolOpen(tool: ToolLike, lastId: string | undefined, streaming: boolean): boolean {
  if (tool.error) return true
  if (!streaming) return false
  return tool.id === lastId
}

// Effective open state given the user's per-tool overrides: use the override when
// present, otherwise fall back to the default.
export function toolOpenState(
  overrides: Record<string, boolean>,
  tool: ToolLike,
  lastId: string | undefined,
  streaming: boolean,
): boolean {
  const ov = overrides[tool.id]
  return ov === undefined ? defaultToolOpen(tool, lastId, streaming) : ov
}

// Fold a <details> toggle into the overrides map (mutates in place). Record the
// choice only while it diverges from the current default; when it realigns, drop
// the override so auto behaviour (last opens, previous collapses when the next
// tool arrives) resumes. This also keeps auto-open idempotent — the toggle event
// a programmatic open fires matches the default and clears nothing new, so there
// is no feedback loop with the reactive `open` binding.
export function applyToolToggle(
  overrides: Record<string, boolean>,
  tool: ToolLike,
  lastId: string | undefined,
  streaming: boolean,
  open: boolean,
): void {
  if (open === defaultToolOpen(tool, lastId, streaming)) delete overrides[tool.id]
  else overrides[tool.id] = open
}

// What the end-of-run keep-open handling should do on a render where the
// group's running state may have changed.
//
// 'pin' — the group just went running->not-running: pin the auto-opened last
// card open so the default (closed once streaming stops) doesn't collapse it.
// This collapse used to be animated shut, but the shrink shifted the page
// under the user right as they were reading the output — the card now simply
// stays expanded. Skipped for an error card (open by default anyway) or one
// the user holds an explicit override on.
//
// 'clear' — the group is running again: a new tool round arrived, so drop the
// pins and let the pinned card follow the default again — it collapses as the
// new last tool opens, exactly like the mid-turn handoff between tools.
//
// null — nothing to do. Note prevRunning === undefined (first render, e.g. a
// replayed history transcript whose tools are already done) is not an edge —
// replayed groups stay fully collapsed.
export type KeepOpenAction = { kind: 'pin'; id: string } | { kind: 'clear' } | null

export function keepOpenAction(
  prevRunning: boolean | undefined,
  running: boolean,
  tools: ToolLike[],
  overrides: Record<string, boolean>,
  pinnedCount: number,
): KeepOpenAction {
  if (running) return pinnedCount > 0 ? { kind: 'clear' } : null
  if (prevRunning !== true) return null
  const last = tools[tools.length - 1]
  if (!last || last.error || overrides[last.id] !== undefined) return null
  return { kind: 'pin', id: last.id }
}

// Whether a click inside a tool card's <summary> is a text interaction rather
// than a fold, in which case the caller cancels it so the card stays put.
//
// Two cases: the click landed on the selectable argument text, or it ended a
// drag that selected something in this header (the mouseup can land anywhere in
// the summary, so the click target alone doesn't tell us). Note stopPropagation
// on the argument span cannot do this job — a summary's activation behaviour
// runs unless the click is *cancelled*, propagation stopped or not.
//
// `detail` is the click's MouseEvent.detail: 0 for the synthetic click a
// keyboard Enter/Space on the focused summary fires. Selecting the argument
// text leaves both the selection and the focus in place, so without that guard
// a leftover selection would silently swallow every keyboard fold afterwards.
export function foldSuppressed(target: EventTarget | null, selection: Selection | null, detail: number): boolean {
  const el = target instanceof Element ? target : null
  if (!el) return false
  if (el.closest('.tool-arg')) return true
  if (detail === 0 || !selection || selection.isCollapsed) return false
  const summary = el.closest('summary')
  if (!summary) return false
  // Either end, since a drag that started above the card and ended in the
  // header anchors outside it.
  return summary.contains(selection.anchorNode) || summary.contains(selection.focusNode)
}
