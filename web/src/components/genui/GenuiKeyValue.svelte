<script lang="ts">
  import type { GenuiKeyValueNode } from '../../lib/genui/types'

  let { node }: { node: GenuiKeyValueNode } = $props()
</script>

<dl class="genui-keyvalue">
  {#each node.items as item, i (i)}
    <div class="genui-kv-row">
      <dt>{item.label}</dt>
      <dd>{item.value}</dd>
    </div>
  {/each}
</dl>

<style>
  .genui-keyvalue {
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 13px;
  }
  .genui-kv-row {
    display: flex;
    justify-content: space-between;
    gap: 12px;
  }
  dt {
    color: var(--text-tertiary);
  }
  dd {
    margin: 0;
    color: var(--text);
    font-weight: 500;
    text-align: right;
  }
</style>
