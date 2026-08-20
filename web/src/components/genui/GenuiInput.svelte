<script lang="ts">
  // Always a plain <input type="text"> — GenuiInputNode has no `inputType`
  // field to switch on (see the security note in types.ts), so there is
  // nothing here that could render type="password" or any other variant.
  import { untrack } from 'svelte'
  import type { GenuiInputNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'

  let { node }: { node: GenuiInputNode } = $props()
  const ctx = useGenuiFieldContext()

  // Deliberately read node.value only once: this seeds the field's initial
  // value, but once the user starts typing, a later re-render of the same
  // node (e.g. the model's partial-parse prefix reshuffling as more of the
  // fence streams in) must not clobber what they've already typed.
  let value = $state(untrack(() => node.value ?? ''))

  $effect(() => {
    ctx?.setFieldValue(node.field, value)
  })
</script>

<label class="genui-field">
  {#if node.label}<span class="genui-field-label">{node.label}</span>{/if}
  <input type="text" class="genui-input" placeholder={node.placeholder} bind:value disabled={!ctx?.interactive} />
</label>

<style>
  .genui-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 13px;
  }
  .genui-field-label {
    font-size: 12px;
    color: var(--text-tertiary);
  }
  .genui-input {
    border: 1px solid var(--border);
    border-radius: var(--radius-xs, 6px);
    padding: 6px 10px;
    font-size: 13px;
    color: var(--text);
    background: var(--bg-container);
  }
  .genui-input:focus {
    outline: none;
    border-color: var(--blue-6);
    box-shadow: 0 0 0 3px var(--focus-ring);
  }
  .genui-input:disabled {
    opacity: 0.6;
  }
</style>
