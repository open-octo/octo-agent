<script lang="ts">
  import { untrack } from 'svelte'
  import type { GenuiSelectNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiSelectNode } = $props()
  const ctx = useGenuiFieldContext()

  // See GenuiInput.svelte's comment: seed once, don't clobber a later
  // user pick on a re-render of the same node.
  let value = $state(untrack(() => ctx?.initialString(node.field, node.value ?? node.options[0]?.value ?? '') ?? ''))

  $effect(() => {
    ctx?.setFieldValue(node.field, value)
  })
</script>

<label class="genui-field">
  {#if node.label}<span class="genui-field-label">{node.label}</span>{/if}
  <select class="genui-select" bind:value disabled={!ctx?.interactive}>
    {#each node.options as opt, i (i)}
      <option value={opt.value}>{opt.label}</option>
    {/each}
  </select>
</label>

<style>
  .genui-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 13px;
  }
  .genui-field-label {
    font-size: 12px;
    color: var(--text-tertiary);
  }
  .genui-select {
    border: 1px solid var(--border);
    border-radius: var(--radius-xs, 6px);
    padding: 6px 10px;
    font-size: 13px;
    color: var(--text);
    background: var(--bg-container);
  }
  .genui-select:focus {
    outline: none;
    border-color: var(--blue-6);
    box-shadow: 0 0 0 3px var(--focus-ring);
  }
  .genui-select:disabled {
    opacity: 0.6;
  }
</style>
