<script lang="ts">
  // Multi-line free text. Unlike the other input nodes this one does not
  // serve local interaction — long text is something the user writes *for
  // the model*, so it exists to make a submit-style panel usable (a feedback
  // box, a chunk of text to process) rather than to drive a condition.
  import { untrack } from 'svelte'
  import type { GenuiTextareaNode } from '../../lib/genui/types'
  import { useGenuiFieldContext } from '../../lib/genui/context'
  import { MAX_TEXTAREA_LEN } from '../../lib/genui/guard'

  let { node }: { node: GenuiTextareaNode } = $props()
  const ctx = useGenuiFieldContext()

  // See GenuiInput.svelte: seed once, don't clobber what the user has typed.
  let value = $state(untrack(() => ctx?.initialString(node.field, node.value ?? '') ?? node.value ?? ''))

  $effect(() => {
    ctx?.setFieldValue(node.field, value)
  })
</script>

<label class="genui-field">
  {#if node.label}<span class="genui-field-label">{node.label}</span>{/if}
  <textarea
    class="genui-textarea"
    rows={node.rows ?? 4}
    placeholder={node.placeholder}
    maxlength={MAX_TEXTAREA_LEN}
    bind:value
    disabled={!ctx?.interactive}
  ></textarea>
</label>

<style>
  .genui-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .genui-field-label {
    font-size: 12px;
    color: var(--text-secondary);
  }
  .genui-textarea {
    padding: 6px 8px;
    font-size: 13px;
    font-family: inherit;
    line-height: 1.5;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    resize: vertical;
  }
  .genui-textarea:disabled {
    opacity: 0.6;
  }
</style>
