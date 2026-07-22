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

// What the auto-close animation should do on a render where the group's
// running state may have changed.
//
// 'start' — the group just went running->not-running: begin the animated close
// of the auto-opened last card (unless it's an error card, which stays open by
// default, or the user holds an explicit override on it).
//
// 'cancel' — the group is running again while a close animation is still in
// flight: a new tool call arrived before the previous card finished shrinking.
// Land the old card instantly, otherwise its shrink overlaps the new card's
// arrival and the pair reads as everything animating open at once.
//
// null — nothing to do. Note prevRunning === undefined (first render, e.g. a
// replayed history transcript whose tools are already done) is not an edge.
export type AutoCloseAction = { kind: 'start'; id: string } | { kind: 'cancel' } | null

export function autoCloseAction(
  prevRunning: boolean | undefined,
  running: boolean,
  tools: ToolLike[],
  overrides: Record<string, boolean>,
  closingCount: number,
): AutoCloseAction {
  if (running) return closingCount > 0 ? { kind: 'cancel' } : null
  if (prevRunning !== true) return null
  const last = tools[tools.length - 1]
  if (!last || last.error || overrides[last.id] !== undefined) return null
  return { kind: 'start', id: last.id }
}
