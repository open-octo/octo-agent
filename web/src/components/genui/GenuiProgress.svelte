<script lang="ts">
  import type { GenuiProgressNode } from '../../lib/genui/types'

  let { node }: { node: GenuiProgressNode } = $props()
</script>

<div class="genui-progress">
  {#if node.label}<div class="genui-progress-label">{node.label}</div>{/if}
  <div class="genui-progress-track" role="progressbar" aria-valuenow={node.value} aria-valuemin={0} aria-valuemax={100}>
    <div class="genui-progress-fill" style={`width: ${node.value}%`}></div>
  </div>
</div>

<style>
  .genui-progress {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .genui-progress-label {
    font-size: 12px;
    color: var(--text-tertiary);
  }
  .genui-progress-track {
    height: 6px;
    border-radius: var(--radius-xs, 3px);
    background: var(--hover-neutral);
    overflow: hidden;
  }
  .genui-progress-fill {
    height: 100%;
    background: var(--blue-6);
    border-radius: var(--radius-xs, 3px);
  }
</style>
