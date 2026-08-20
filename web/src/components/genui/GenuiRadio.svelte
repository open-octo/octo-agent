<script lang="ts">
  import { untrack } from 'svelte'
  import type { GenuiRadioNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiRadioNode } = $props()
  const ctx = useGenuiFieldContext()

  // See GenuiInput.svelte's comment: seed once, don't clobber a later
  // user pick on a re-render of the same node.
  let value = $state(untrack(() => ctx?.initialString(node.field, node.value ?? '') ?? node.value ?? ''))

  $effect(() => {
    ctx?.setFieldValue(node.field, value)
  })
</script>

<div class="genui-radio-group">
  {#if node.label}<span class="genui-field-label">{node.label}</span>{/if}
  {#each node.options as opt, i (i)}
    <label class="genui-radio-option">
      <input type="radio" name={node.field} value={opt.value} bind:group={value} disabled={!ctx?.interactive} />
      {opt.label}
    </label>
  {/each}
</div>

<style>
  .genui-radio-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 13px;
  }
  .genui-field-label {
    font-size: 12px;
    color: var(--text-tertiary);
  }
  .genui-radio-option {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--text);
    cursor: pointer;
  }
</style>
