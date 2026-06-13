<script lang="ts">
  let { options = [], value = $bindable(''), onchange, labels }: {
    options?: string[]
    value?: string
    onchange?: (v: string) => void
    // Optional display labels keyed by option value. The bound `value` stays
    // the stable English option (handlers depend on it); only the rendered
    // text is localized when a label is provided.
    labels?: Record<string, string>
  } = $props()
</script>

<div class="segment">
  {#each options as opt}
    <button
      class="opt"
      class:active={value === opt}
      onclick={() => { value = opt; onchange?.(opt) }}
    >{labels?.[opt] ?? opt}</button>
  {/each}
</div>

<style>
.segment {
  display: inline-flex;
  padding: 2px;
  background: var(--control-track);
  border-radius: 8px;
  gap: 2px;
}
.opt {
  height: 28px;
  padding: 0 14px;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  background: transparent;
  color: var(--text-secondary);
  transition: background 0.15s, color 0.15s;
  font-family: inherit;
}
.opt:hover { background: var(--hover-neutral); }
.opt.active { background: var(--bg-container); color: var(--blue-6); }
</style>
