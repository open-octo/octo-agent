<script lang="ts">
  let { checked = $bindable(false), onchange }: { checked?: boolean, onchange?: (v: boolean) => void } = $props()
</script>

<button
  role="switch"
  aria-checked={checked}
  class="switch"
  class:on={checked}
  onclick={() => { checked = !checked; onchange?.(checked) }}
>
  <span class="thumb"></span>
</button>

<style>
.switch {
  display: inline-flex;
  align-items: center;
  width: 28px;
  height: 16px;
  border-radius: 999px;
  background: rgba(0,0,0,0.25);
  border: none;
  cursor: pointer;
  padding: 0;
  transition: background 0.2s;
  position: relative;
}
.switch.on { background: #1677FF; }
.thumb {
  position: absolute;
  left: 2px;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: #fff;
  transition: left 0.2s;
  box-shadow: 0 1px 4px rgba(0,0,0,0.2);
}
.on .thumb { left: 14px; }
</style>
