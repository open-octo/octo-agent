<script lang="ts">
  // A numeric input. Deliberately a node type of its own rather than an
  // `inputType` on `input` — see the security note on GenuiNumberNode in
  // types.ts: re-adding that field would leave the guard as the only thing
  // standing between a model and type="password".
  import { untrack } from 'svelte'
  import type { GenuiNumberNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiNumberNode } = $props()
  const ctx = useGenuiFieldContext()

  // See GenuiInput.svelte: seed once, don't clobber later typing.
  let value = $state(untrack(() => ctx?.initialNumber(node.field, node.value ?? 0) ?? node.value ?? 0))

  $effect(() => {
    // An emptied input binds as null/NaN; report 0 rather than propagating a
    // non-number into the field map, which range predicates would then have
    // to defend against.
    ctx?.setFieldValue(node.field, typeof value === 'number' && Number.isFinite(value) ? value : 0)
  })
</script>

<label class="genui-field">
  {#if node.label}<span class="genui-field-label">{node.label}</span>{/if}
  <input
    type="number"
    class="genui-number"
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
    font-size: 12px;
    color: var(--text-secondary);
  }
  .genui-number {
    padding: 6px 8px;
    font-size: 13px;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    font-variant-numeric: tabular-nums;
  }
  .genui-number:disabled {
    opacity: 0.6;
  }
</style>
