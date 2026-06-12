<script lang="ts">
  import { cmdkOpen, view } from '../../lib/stores'

  function close() { cmdkOpen.set(false) }
  function goTo(v: string) { view.set(v as any); close() }
</script>

{#if $cmdkOpen}
<div class="backdrop" onclick={close}>
  <div class="palette" onclick={(e) => e.stopPropagation()}>
    <div class="search-row">
      <iconify-icon icon="ant-design:search-outlined" width="16" style="color:rgba(0,0,0,0.45)"></iconify-icon>
      <span class="placeholder">Search sessions, skills, commands…</span>
      <kbd>esc</kbd>
    </div>
    <div class="results">
      <div class="group-label">SESSIONS</div>
      <div class="result-row active" onclick={close}>
        <iconify-icon icon="ant-design:message-outlined" width="14" style="color:#1677FF"></iconify-icon>
        <span class="result-title">Fix WS reconnect bug</span>
        <iconify-icon icon="lucide:corner-down-left" width="13" style="color:rgba(0,0,0,0.35)"></iconify-icon>
      </div>
      <div class="result-row" onclick={close}>
        <iconify-icon icon="ant-design:clock-circle-outlined" width="14" style="color:rgba(0,0,0,0.45)"></iconify-icon>
        <span class="result-title dim">Weekly Metrics Report</span>
      </div>
      <div class="group-label">ACTIONS</div>
      <div class="result-row" onclick={() => goTo('chat')}>
        <iconify-icon icon="ant-design:plus-outlined" width="14" style="color:rgba(0,0,0,0.45)"></iconify-icon>
        <span class="result-title dim">New session</span>
        <span class="shortcut mono">⌘N</span>
      </div>
      <div class="result-row" onclick={() => goTo('skills')}>
        <iconify-icon icon="ant-design:thunderbolt-outlined" width="14" style="color:rgba(0,0,0,0.45)"></iconify-icon>
        <span class="result-title dim">Open Skills</span>
      </div>
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1000;
  background: rgba(0,0,0,0.35);
  display: flex; align-items: flex-start; justify-content: center; padding-top: 12vh;
}
.palette {
  width: 92%; max-width: 520px; background: #fff;
  border-radius: 12px; overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.search-row {
  display: flex; align-items: center; gap: 10px;
  padding: 12px 16px; border-bottom: 1px solid #F0F0F0;
}
.placeholder { font-size: 14px; color: rgba(0,0,0,0.45); flex: 1; }
kbd {
  font-size: 11px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: #FAFAFA; border: 1px solid #EEEFF1; border-radius: 4px;
  padding: 1px 6px; color: rgba(0,0,0,0.45);
}
.results { padding: 8px; max-height: 360px; overflow-y: auto; }
.group-label { font-size: 11px; font-weight: 600; letter-spacing: 0.4px; color: rgba(0,0,0,0.35); padding: 6px 8px; }
.result-row {
  display: flex; align-items: center; gap: 10px;
  padding: 8px; border-radius: 6px; cursor: pointer;
}
.result-row:hover { background: rgba(0,0,0,0.04); }
.result-row.active { background: rgba(22,119,255,0.06); }
.result-title { font-size: 13px; color: rgba(0,0,0,0.88); flex: 1; }
.result-title.dim { color: rgba(0,0,0,0.65); }
.shortcut { font-size: 11px; color: rgba(0,0,0,0.35); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
