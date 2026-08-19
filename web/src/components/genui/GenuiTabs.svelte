<script lang="ts">
  import type { GenuiTabsNode } from '../../lib/genui/types'
  import GenuiNode from './GenuiNode.svelte'

  let { node }: { node: GenuiTabsNode } = $props()

  let active = $state(0)
</script>

<div class="genui-tabs">
  <div class="genui-tabs-bar" role="tablist">
    {#each node.tabs as tab, i (i)}
      <button
        type="button"
        role="tab"
        class="genui-tab"
        class:active={i === active}
        aria-selected={i === active}
        onclick={() => (active = i)}
      >
        {tab.label}
      </button>
    {/each}
  </div>
  <div class="genui-tabs-body">
    {#each node.tabs[active]?.children ?? [] as child, i (i)}
      <GenuiNode node={child} />
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
