<script lang="ts">
  // Root entry point for a GenUI spec. Callers pass an already-guarded
  // GenuiSpec (see web/src/lib/genui/guard.ts) — this component does not
  // re-run the guard itself; both the render_ui tool-card path
  // (ToolGroup.svelte) and the inline octo-ui-fence path (ChatView.svelte)
  // are responsible for calling sanitizeSpec before mounting this.
  //
  // Owns the field-value map and the action dispatcher (see
  // lib/genui/context.ts) so interactive descendants — however deeply
  // nested inside row/col/card/tabs — can report field changes and fire
  // button actions without any intermediate container knowing about it.
  //
  // When the spec carries an id and a session is known, the field map is
  // seeded from and written back to localStorage, so what the user set
  // survives a reload. An anonymous panel keeps everything in memory, exactly
  // as it did before addressable panels existed.
  import { untrack } from 'svelte'
  import type { GenuiSpec, GenuiFieldValue } from '../../lib/genui/types'
  import GenuiNode from './GenuiNode.svelte'
  import { provideGenuiFieldContext, type GenuiActionEvent } from '../../lib/genui/context'
  import { loadPanelFields, savePanelField } from '../../lib/genui/panel-state'

  let {
    spec,
    interactive = true,
    pending = false,
    sessionId = '',
    onaction,
  }: {
    spec: GenuiSpec
    /** False while the fence this spec came from is still streaming in —
     * per the design, a fence that has already closed earlier in a
     * still-streaming message is interactive immediately. */
    interactive?: boolean
    /** True while a silent turn fired from this panel is in flight. */
    pending?: boolean
    /** Session the panel belongs to; required for state to persist. */
    sessionId?: string
    onaction?: (event: GenuiActionEvent) => void
  } = $props()

  // Read once at construction: the panel's identity does not change under a
  // mounted instance (a new version of the same panel keeps its id, and a
  // different panel renders at a different anchor), so capturing the initial
  // value is the intent rather than a missed reactive dependency.
  const persistKey = untrack(() => (spec.id && sessionId ? { sessionId, panelId: spec.id } : null))

  const fields = $state<Record<string, GenuiFieldValue>>(
    persistKey ? loadPanelFields(persistKey.sessionId, persistKey.panelId) : {}
  )

  // Every control reports its value once on mount, before the user has done
  // anything. That first report is a seed, not a change, and persisting it
  // would both fill storage with values nobody set and — worse — pin the
  // field to today's default, so a later version of the panel could never
  // introduce a new one. Remember the seed, persist everything after it.
  const seeded = new Map<string, true>()

  function persist(field: string, value: GenuiFieldValue) {
    if (!persistKey) return
    if (!seeded.has(field)) {
      seeded.set(field, true)
      return
    }
    savePanelField(persistKey.sessionId, persistKey.panelId, field, value)
  }

  provideGenuiFieldContext({
    get interactive() {
      return interactive
    },
    get pending() {
      return pending
    },
    get fields() {
      return fields
    },
    setFieldValue(field, value) {
      fields[field] = value
      persist(field, value)
    },
    initialString(field, declared) {
      const v = fields[field]
      return typeof v === 'string' ? v : declared
    },
    initialBoolean(field, declared) {
      const v = fields[field]
      return typeof v === 'boolean' ? v : declared
    },
    initialNumber(field, declared) {
      const v = fields[field]
      return typeof v === 'number' ? v : declared
    },
    dispatchAction(action, payload) {
      if (!interactive || pending) return
      onaction?.({ action, fields: { ...fields }, payload })
    },
  })
</script>

<div class="genui-block" class:pending>
  {#if spec.title}
    <div class="genui-block-title">{spec.title}</div>
  {/if}
  <div class="genui-block-items">
    {#each spec.items as item, i (i)}
      <GenuiNode node={item} path={String(i)} />
    {/each}
  </div>
</div>

<style>
  .genui-block {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  /* Subdued while a silent turn is in flight. Local interaction stays live —
     only actions are refused — so the panel must still look usable. */
  .genui-block.pending {
    opacity: 0.6;
    transition: opacity 120ms ease;
  }
  .genui-block-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text);
  }
  .genui-block-items {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
