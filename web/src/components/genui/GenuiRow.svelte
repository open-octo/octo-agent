<script lang="ts">
  import type { Snippet } from 'svelte'
  import type { GenuiRowNode } from '../../lib/genui/types'

  let { node, children }: { node: GenuiRowNode; children?: Snippet } = $props()
</script>

<div
  class="genui-flex"
  class:genui-col={node.type === 'col'}
  style={node.gap != null ? `gap: ${node.gap}px` : undefined}
>
  {@render children?.()}
</div>

<style>
  .genui-flex {
    display: flex;
    flex-direction: row;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 8px;
  }
  .genui-flex.genui-col {
    flex-direction: column;
    align-items: stretch;
  }
</style>
