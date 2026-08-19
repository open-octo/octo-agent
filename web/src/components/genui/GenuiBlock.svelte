<script lang="ts">
  // Root entry point for a GenUI spec. Callers pass an already-guarded
  // GenuiSpec (see web/src/lib/genui/guard.ts) — this component does not
  // re-run the guard itself; both the render_ui tool-card path
  // (ToolGroup.svelte) and the future inline octo-ui-fence path are
  // responsible for calling sanitizeSpec before mounting this.
  import type { GenuiSpec } from '../../lib/genui/types'
  import GenuiNode from './GenuiNode.svelte'

  let { spec }: { spec: GenuiSpec } = $props()
</script>

<div class="genui-block">
  {#if spec.title}
    <div class="genui-block-title">{spec.title}</div>
  {/if}
  <div class="genui-block-items">
    {#each spec.items as item, i (i)}
      <GenuiNode node={item} />
    {/each}
  </div>
</div>

<style>
  .genui-block {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .genui-block-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text);
  }
  .genui-block-items {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
</style>
