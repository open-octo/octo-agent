<script lang="ts">
  // A foldable section. Load-bearing rather than cosmetic: local interaction
  // requires the model to ship the data the user might look at (MAX_TABLE_ROWS
  // is 500 for that reason), so panels get taller and folding is the lever
  // that keeps them scannable.
  //
  // The fold state persists under a reserved "__open:<path>" field, which the
  // guard rejects for model-supplied field names — a spec cannot address it.
  import { untrack, type Snippet } from 'svelte'
  import type { GenuiCollapsibleNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let {
    node,
    path = '',
    children,
  }: { node: GenuiCollapsibleNode; path?: string; children?: Snippet } = $props()
  const ctx = useGenuiFieldContext()

  // A node's position in the tree is fixed for the life of the component, so
  // reading it once is the intent rather than a missed reactive dependency.
  const stateField = untrack(() => `__open:${path}`)

  // `open` seeds the first render only; after that the user's toggle wins,
  // including across a reload.
  let open = $state(untrack(() => ctx?.initialBoolean(stateField, node.open ?? false) ?? node.open ?? false))

  $effect(() => {
    ctx?.setFieldValue(stateField, open)
  })
</script>

<div class="genui-collapsible">
  <button type="button" class="genui-collapsible-head" aria-expanded={open} onclick={() => (open = !open)}>
    <span class="genui-collapsible-caret" class:open>›</span>
    <span class="genui-collapsible-title">{node.title}</span>
  </button>
  {#if open}
    <div class="genui-collapsible-body">
      {@render children?.()}
    </div>
  {/if}
</div>

<style>
  .genui-collapsible {
    display: flex;
    flex-direction: column;
    gap: 6px;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 8px 10px;
  }
  .genui-collapsible-head {
    display: flex;
    align-items: center;
    gap: 6px;
    appearance: none;
    border: none;
    background: none;
    padding: 0;
    cursor: pointer;
    text-align: left;
    color: var(--text);
    font-size: 13px;
    font-weight: 600;
  }
  .genui-collapsible-caret {
    display: inline-block;
    transition: transform 120ms ease;
    color: var(--text-secondary);
  }
  .genui-collapsible-caret.open {
    transform: rotate(90deg);
  }
  .genui-collapsible-body {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
