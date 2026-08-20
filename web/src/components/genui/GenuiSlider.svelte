<script lang="ts">
  // A range control whose value feeds visibleWhen's range predicates and
  // table filters. Dragging it costs nothing — no message, no model turn —
  // which is the entire reason this node type exists.
  import { untrack } from 'svelte'
  import type { GenuiSliderNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiSliderNode } = $props()
  const ctx = useGenuiFieldContext()

  // See GenuiInput.svelte: seed once, don't clobber a later drag on a
  // re-render of the same node.
  let value = $state(untrack(() => ctx?.initialNumber(node.field, node.value ?? node.min) ?? node.value ?? node.min))

  $effect(() => {
    ctx?.setFieldValue(node.field, value)
  })
</script>

<label class="genui-field">
  {#if node.label}
    <span class="genui-field-label">
      {node.label}
      <span class="genui-slider-value">{value}</span>
    </span>
  {/if}
  <input
    type="range"
    class="genui-slider"
    min={node.min}
    max={node.max}
    step={node.step}
    bind:value
    disabled={!ctx?.interactive}
  />
</label>

<style>
  .genui-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .genui-field-label {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    font-size: 12px;
    color: var(--text-secondary);
  }
  .genui-slider-value {
    font-variant-numeric: tabular-nums;
    color: var(--text);
    font-weight: 600;
  }
  .genui-slider {
    width: 100%;
    accent-color: var(--blue-6);
  }
  .genui-slider:disabled {
    opacity: 0.6;
  }
</style>
