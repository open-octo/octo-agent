<script lang="ts">
  // Tab switching was the first purely local interaction in GenUI. Its
  // selected index now persists like every other piece of panel state, under
  // a reserved "__tab:<path>" field the guard rejects for model-supplied
  // field names — so a spec cannot address or overwrite it.
  import { untrack } from 'svelte'
  import type { GenuiTabsNode } from '../../lib/genui/types'
  import GenuiNode from './GenuiNode.svelte'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node, path = '' }: { node: GenuiTabsNode; path?: string } = $props()
  const ctx = useGenuiFieldContext()

  // A node's position in the tree is fixed for the life of the component, so
  // reading it once is the intent rather than a missed reactive dependency.
  const stateField = untrack(() => `__tab:${path}`)

  let active = $state(untrack(() => ctx?.initialNumber(stateField, 0) ?? 0))

  // A persisted index can outlive the tab it pointed at, if the model sends a
  // new version of the panel with fewer tabs. Fall back to the first tab
  // rather than rendering an empty body.
  const safeActive = $derived(active >= 0 && active < node.tabs.length ? active : 0)

  $effect(() => {
    ctx?.setFieldValue(stateField, safeActive)
  })
</script>

<div class="genui-tabs">
  <div class="genui-tabs-bar" role="tablist">
    {#each node.tabs as tab, i (i)}
      <button
        type="button"
        role="tab"
        class="genui-tab"
        class:active={i === safeActive}
        aria-selected={i === safeActive}
        onclick={() => (active = i)}
      >
        {tab.label}
      </button>
    {/each}
  </div>
  <div class="genui-tabs-body">
    {#each node.tabs[safeActive]?.children ?? [] as child, i (i)}
      <GenuiNode node={child} path={`${path}.t${safeActive}.${i}`} />
    {/each}
  </div>
</div>

<style>
  .genui-tabs {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .genui-tabs-bar {
    display: flex;
    gap: 4px;
    border-bottom: 1px solid var(--border);
  }
  .genui-tab {
    appearance: none;
    border: none;
    background: none;
    padding: 6px 10px;
    font-size: 13px;
    color: var(--text-secondary);
    cursor: pointer;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
  }
  .genui-tab.active {
    color: var(--text);
    border-bottom-color: var(--blue-6);
    font-weight: 600;
  }
  .genui-tabs-body {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
