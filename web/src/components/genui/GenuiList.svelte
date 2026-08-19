<script lang="ts">
  import type { GenuiListNode } from '../../lib/genui/types'

  let { node }: { node: GenuiListNode } = $props()
</script>

<ul class="genui-list">
  {#each node.items as item, i (i)}
    <li>
      {#if typeof item === 'string'}
        {item}
      {:else}
        <span class="genui-list-label">{item.label}</span>
        {#if item.value}<span class="genui-list-value">{item.value}</span>{/if}
      {/if}
    </li>
  {/each}
</ul>

<style>
  .genui-list {
    margin: 0;
    padding-left: 18px;
    font-size: 13px;
    color: var(--text);
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .genui-list-value {
    color: var(--text-tertiary);
    margin-left: 6px;
  }
</style>
