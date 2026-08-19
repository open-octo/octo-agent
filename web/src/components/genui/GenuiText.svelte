<script lang="ts">
  import type { GenuiTextNode } from '../../lib/genui/types'

  let { node }: { node: GenuiTextNode } = $props()
</script>

<p class="genui-text" class:muted={node.tone === 'muted'} class:danger={node.tone === 'danger'}>{node.text}</p>

<style>
  .genui-text {
    margin: 0;
    color: var(--text);
    font-size: 13px;
    line-height: 1.5;
  }
  .genui-text.muted {
    color: var(--text-tertiary);
  }
  .genui-text.danger {
    color: var(--error);
  }
</style>
