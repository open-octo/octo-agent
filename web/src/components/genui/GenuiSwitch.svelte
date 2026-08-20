<script lang="ts">
  import { untrack } from 'svelte'
  import type { GenuiCheckboxNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiCheckboxNode } = $props()
  const ctx = useGenuiFieldContext()

  // See GenuiInput.svelte's comment: seed once, don't clobber a later
  // user toggle on a re-render of the same node.
  let checked = $state(untrack(() => ctx?.initialBoolean(node.field, node.checked ?? false) ?? node.checked ?? false))

  $effect(() => {
    ctx?.setFieldValue(node.field, checked)
  })
</script>

<label class="genui-switch-row">
  {#if node.label}<span class="genui-switch-label">{node.label}</span>{/if}
  <span class="genui-switch" class:on={checked} class:disabled={!ctx?.interactive}>
    <input type="checkbox" bind:checked disabled={!ctx?.interactive} />
    <span class="genui-switch-knob"></span>
  </span>
</label>

<style>
  .genui-switch-row {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
  }
  .genui-switch {
    position: relative;
    display: inline-block;
    width: 32px;
    height: 18px;
    border-radius: var(--radius-pill, 999px);
    background: var(--control-track, var(--hover-neutral));
    transition: background 0.15s ease;
  }
  .genui-switch.on {
    background: var(--blue-6);
  }
  .genui-switch.disabled {
    opacity: 0.6;
  }
  .genui-switch input {
    position: absolute;
    inset: 0;
    margin: 0;
    opacity: 0;
    cursor: pointer;
  }
  .genui-switch-knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--on-accent);
    box-shadow: var(--card-shadow);
    transition: transform 0.15s ease;
  }
  .genui-switch.on .genui-switch-knob {
    transform: translateX(14px);
  }
</style>
