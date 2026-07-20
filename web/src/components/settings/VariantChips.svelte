<script lang="ts">
  import { t } from '../../lib/i18n'
  import type { EndpointVariant } from '../../lib/api'

  // Quick-pick chips for a named vendor's alternate endpoints (regional
  // mirrors, protocol variants), shared by the model config form and the
  // endpoint editor. Selection is delegated — callers own whatever state
  // tracks base_url.
  let {
    variants = [],
    value = '',
    disabled = false,
    onselect,
  }: {
    variants?: EndpointVariant[]
    value?: string
    disabled?: boolean
    onselect: (v: EndpointVariant) => void
  } = $props()

  function label(v: EndpointVariant): string {
    if (v.label_key) {
      const tr = $t(v.label_key)
      if (tr && tr !== v.label_key) return tr
    }
    return v.label || v.base_url
  }
</script>

{#if variants.length > 0}
  <div class="variants">
    {#each variants as v (v.base_url)}
      <button
        type="button"
        class="variant-chip"
        class:active={value === v.base_url}
        onclick={() => onselect(v)}
        {disabled}
      >{label(v)}</button>
    {/each}
  </div>
{/if}

<style>
.variants { display: flex; flex-wrap: wrap; gap: 6px; }
.variant-chip {
  height: 24px; padding: 0 10px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 999px; font-size: 12px; color: var(--text-secondary); cursor: pointer;
  font-family: inherit; transition: border-color 0.15s, color 0.15s;
}
.variant-chip:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.variant-chip.active { border-color: var(--blue-6); color: var(--blue-6); background: var(--active-blue-bg); }
</style>
