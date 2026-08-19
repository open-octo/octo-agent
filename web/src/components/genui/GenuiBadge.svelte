<script lang="ts">
  import type { GenuiBadgeNode } from '../../lib/genui/types'

  let { node }: { node: GenuiBadgeNode } = $props()
</script>

<span class="genui-badge tone-{node.tone ?? 'default'}">{node.text}</span>

<style>
  .genui-badge {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: var(--radius-pill, 999px);
    font-size: 12px;
    font-weight: 500;
    line-height: 1.4;
    background: var(--hover-neutral);
    color: var(--text-secondary);
  }
  .genui-badge.tone-success {
    background: var(--success-bg);
    color: var(--success-text, var(--success));
  }
  .genui-badge.tone-warning {
    background: var(--warning-bg);
    color: var(--warning-text, var(--warning));
  }
  .genui-badge.tone-danger {
    background: var(--error-bg);
    color: var(--error-dark, var(--error));
  }
  .genui-badge.tone-info {
    background: var(--info-bg);
    color: var(--info-text);
  }
</style>
