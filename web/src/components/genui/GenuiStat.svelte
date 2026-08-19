<script lang="ts">
  import type { GenuiStatNode } from '../../lib/genui/types'

  let { node }: { node: GenuiStatNode } = $props()
</script>

<div class="genui-stat">
  <div class="genui-stat-label">{node.label}</div>
  <div class="genui-stat-value">{node.value}</div>
  {#if node.delta}
    <div class="genui-stat-delta" class:up={node.tone === 'up'} class:down={node.tone === 'down'}>
      {node.delta}
    </div>
  {/if}
</div>

<style>
  .genui-stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 96px;
  }
  .genui-stat-label {
    font-size: 12px;
    color: var(--text-tertiary);
  }
  .genui-stat-value {
    font-size: 18px;
    font-weight: 600;
    color: var(--text);
  }
  .genui-stat-delta {
    font-size: 12px;
    color: var(--text-secondary);
  }
  .genui-stat-delta.up {
    color: var(--success-text, var(--success));
  }
  .genui-stat-delta.down {
    color: var(--error-dark, var(--error));
  }
</style>
