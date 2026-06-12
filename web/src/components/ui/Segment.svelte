<script lang="ts">
  let { options = [], value = $bindable(''), onchange }: {
    options?: string[]
    value?: string
    onchange?: (v: string) => void
  } = $props()
</script>

<div class="segment">
  {#each options as opt}
    <button
      class="opt"
      class:active={value === opt}
      onclick={() => { value = opt; onchange?.(opt) }}
    >{opt}</button>
  {/each}
</div>

<style>
.segment {
  display: inline-flex;
  padding: 2px;
  background: #F0F2F5;
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
  color: rgba(0,0,0,0.65);
  transition: background 0.15s, color 0.15s;
  font-family: inherit;
}
.opt:hover { background: rgba(0,0,0,0.04); }
.opt.active { background: #FFFFFF; color: #1677FF; }
</style>
