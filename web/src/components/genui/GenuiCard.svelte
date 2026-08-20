<script lang="ts">
  import type { Snippet } from 'svelte'
  import type { GenuiCardNode } from '../../lib/genui/types'

  let { node, children }: { node: GenuiCardNode; children?: Snippet } = $props()
</script>

<div class="genui-card">
  {#if node.title}<div class="genui-card-title">{node.title}</div>{/if}
  <div class="genui-card-body">
    {@render children?.()}
  </div>
</div>

<style>
  .genui-card {
    border: 1px solid var(--border);
    border-radius: var(--radius-card, 8px);
    background: var(--bg-container);
    padding: 12px;
  }
  .genui-card-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
    margin-bottom: 8px;
  }
  .genui-card-body {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
