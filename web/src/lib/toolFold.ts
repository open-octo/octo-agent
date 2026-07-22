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
