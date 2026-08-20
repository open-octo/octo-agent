<script lang="ts">
  import { untrack } from 'svelte'
  import type { GenuiCheckboxNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiCheckboxNode } = $props()
  const ctx = useGenuiFieldContext()

  // See GenuiInput.svelte's comment: seed once, don't clobber a later
  // user toggle on a re-render of the same node.
  let checked = $state(untrack(() => node.checked ?? false))

  $effect(() => {
    ctx?.setFieldValue(node.field, checked)
  })
</script>

<label class="genui-checkbox">
  <input type="checkbox" bind:checked disabled={!ctx?.interactive} />
  {#if node.label}<span>{node.label}</span>{/if}
</label>

<style>
  .genui-checkbox {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
  }
</style>
