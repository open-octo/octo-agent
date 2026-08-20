<script lang="ts">
  import type { GenuiCalloutNode } from '../../lib/genui/types'

  let { node }: { node: GenuiCalloutNode } = $props()
</script>

<div class="genui-callout tone-{node.tone ?? 'info'}">
  {#if node.title}<div class="genui-callout-title">{node.title}</div>{/if}
  {#if node.text}<div class="genui-callout-text">{node.text}</div>{/if}
</div>

<style>
  .genui-callout {
    border-radius: var(--radius-sm, 6px);
    padding: 8px 12px;
    font-size: 13px;
    border: 1px solid var(--border);
    background: var(--bg-container);
  }
  .genui-callout-title {
    font-weight: 600;
    margin-bottom: 2px;
    color: var(--text);
  }
  .genui-callout-text {
    color: var(--text-secondary);
  }
  .genui-callout.tone-info {
    background: var(--info-bg);
    border-color: var(--info-border);
  }
  .genui-callout.tone-info .genui-callout-title {
    color: var(--info-text);
  }
  .genui-callout.tone-success {
    background: var(--success-bg);
  }
  .genui-callout.tone-success .genui-callout-title {
    color: var(--success-text, var(--success));
  }
  .genui-callout.tone-warning {
    background: var(--warning-bg);
  }
  .genui-callout.tone-warning .genui-callout-title {
    color: var(--warning-text, var(--warning));
  }
  .genui-callout.tone-danger {
    background: var(--error-bg);
  }
  .genui-callout.tone-danger .genui-callout-title {
    color: var(--error-dark, var(--error));
  }
</style>
