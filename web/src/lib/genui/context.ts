// Shared Svelte context for the Slice B action-feedback loop. GenuiBlock.svelte
// (the root of one octo-ui fence's component tree) owns the field-value map
// and the action dispatcher; every interactive leaf component (GenuiInput,
// GenuiSelect, GenuiCheckbox, GenuiSwitch, GenuiRadio, GenuiButton) reads and
// writes through this context instead of prop-drilling a callback through
// GenuiNode/GenuiRow/GenuiCard/GenuiTabs — those container components stay
// unaware that interaction exists at all, so adding a node type never
// requires touching every container in between.
import { getContext, setContext } from 'svelte'

export interface GenuiActionEvent {
  action: string
  fields: Record<string, string | boolean>
  payload?: Record<string, unknown>
}

export interface GenuiFieldContext {
  /** Whether this tree currently accepts interaction (mirrors GenuiBlock's
   * `interactive` prop — false while the fence is still streaming in). */
  readonly interactive: boolean
  /** Records a field's current value; called on mount and on every change
   * so a button fired without touching a field still sees its default. */
  setFieldValue(field: string, value: string | boolean): void
  /** Fires the onaction callback with a snapshot of every field value
   * collected so far, plus the firing button's own action/payload. */
  dispatchAction(action: string, payload?: Record<string, unknown>): void
}

const KEY = Symbol('genui-field-context')

export function provideGenuiFieldContext(ctx: GenuiFieldContext): void {
  setContext(KEY, ctx)
}

export function useGenuiFieldContext(): GenuiFieldContext | undefined {
  return getContext(KEY)
}
