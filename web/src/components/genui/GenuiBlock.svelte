<script lang="ts">
  // Root entry point for a GenUI spec. Callers pass an already-guarded
  // GenuiSpec (see web/src/lib/genui/guard.ts) — this component does not
  // re-run the guard itself; both the render_ui tool-card path
  // (ToolGroup.svelte) and the inline octo-ui-fence path (ChatView.svelte)
  // are responsible for calling sanitizeSpec before mounting this.
  //
  // Owns the Slice B field-value map and action dispatcher (see
  // lib/genui/context.ts) so interactive descendants — however deeply
  // nested inside row/col/card/tabs — can report field changes and fire
  // button actions without any intermediate container knowing about it.
  import type { GenuiSpec } from '../../lib/genui/types'
  import GenuiNode from './GenuiNode.svelte'
  import { provideGenuiFieldContext, type GenuiActionEvent } from '../../lib/genui/context'

  let {
    spec,
    interactive = true,
    onaction,
  }: {
    spec: GenuiSpec
    /** False while the fence this spec came from is still streaming in —
     * per the design, a fence that has already closed earlier in a
     * still-streaming message is interactive immediately. */
    interactive?: boolean
    onaction?: (event: GenuiActionEvent) => void
  } = $props()

  const fields = $state<Record<string, string | boolean>>({})

  provideGenuiFieldContext({
    get interactive() {
      return interactive
    },
    setFieldValue(field, value) {
      fields[field] = value
    },
    dispatchAction(action, payload) {
      if (!interactive) return
      onaction?.({ action, fields: { ...fields }, payload })
    },
  })
</script>

<div class="genui-block">
  {#if spec.title}
    <div class="genui-block-title">{spec.title}</div>
  {/if}
  <div class="genui-block-items">
    {#each spec.items as item, i (i)}
      <GenuiNode node={item} />
    {/each}
  </div>
</div>

<style>
  .genui-block {
    display: flex;
    flex-direction: column;
    gap: 8px;
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
