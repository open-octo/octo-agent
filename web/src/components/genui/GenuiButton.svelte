<script lang="ts">
  import type { GenuiButtonNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiButtonNode } = $props()
  const ctx = useGenuiFieldContext()

  function onClick() {
    ctx?.dispatchAction(node.action, node.payload)
  }
</script>

<button
  type="button"
  class="genui-button variant-{node.variant ?? 'default'}"
  disabled={!ctx?.interactive}
  onclick={onClick}
>
  {node.label}
</button>

<style>
  .genui-button {
    appearance: none;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm, 8px);
    padding: 6px 14px;
    font-size: 13px;
    font-weight: 500;
    background: var(--bg-container);
    color: var(--text);
    cursor: pointer;
  }
  .genui-button:hover:not(:disabled) {
    background: var(--hover-neutral);
  }
  .genui-button:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .genui-button.variant-primary {
    background: var(--blue-6);
    border-color: var(--blue-6);
    color: var(--on-accent);
  }
  .genui-button.variant-primary:hover:not(:disabled) {
    background: var(--blue-7);
  }
  .genui-button.variant-danger {
    background: var(--error);
    border-color: var(--error);
    color: var(--on-accent);
  }
  .genui-button.variant-danger:hover:not(:disabled) {
    background: var(--error-dark, var(--error));
  }
</style>
