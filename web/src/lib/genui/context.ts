// Shared Svelte context for the action-feedback loop and local interaction.
// GenuiBlock.svelte (the root of one octo-ui fence's component tree) owns the
// field-value map and the action dispatcher; every interactive leaf component
// (GenuiInput, GenuiSelect, GenuiCheckbox, GenuiSwitch, GenuiRadio,
// GenuiSlider, GenuiNumber, GenuiTextarea, GenuiQuiz, GenuiButton) reads and
// writes through this context instead of prop-drilling a callback through
// GenuiNode/GenuiRow/GenuiCard/GenuiTabs — those container components stay
// unaware that interaction exists at all, so adding a node type never
// requires touching every container in between.
//
// GenuiNode also reads `fields` here to evaluate a node's visibleWhen, which
// is why the map is exposed rather than kept private to the dispatcher.
import { getContext, setContext } from 'svelte'
import type { GenuiFieldValue } from './types'

export interface GenuiActionEvent {
  action: string
  fields: Record<string, GenuiFieldValue>
  payload?: Record<string, unknown>
}

export interface GenuiFieldContext {
  /** Whether this tree currently accepts interaction (mirrors GenuiBlock's
   * `interactive` prop — false while the fence is still streaming in). */
  readonly interactive: boolean
  /** True while a silent turn fired from this panel is in flight. Local
   * interaction stays live; only actions are refused. */
  readonly pending: boolean
  /** Current field values. Read reactively — GenuiNode evaluates visibleWhen
   * against it, and interactive leaves seed themselves from it. */
  readonly fields: Record<string, GenuiFieldValue>
  /** Records a field's current value; called on mount and on every change
   * so a button fired without touching a field still sees its default. */
  setFieldValue(field: string, value: GenuiFieldValue): void
  /**
   * The value a control should start at: a persisted value when the panel is
   * addressable and one was stored for this field, otherwise the spec's
   * declared default. Split by type so each control gets the type it renders
   * without casting — a persisted value of the wrong type is ignored rather
   * than coerced, since a spec that changed a field from text to a switch
   * means the old value no longer describes anything.
   */
  initialString(field: string, declared: string): string
  initialBoolean(field: string, declared: boolean): boolean
  initialNumber(field: string, declared: number): number
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
