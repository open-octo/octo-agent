<script lang="ts">
  // Real background tasks come from the background_task_update WS event.
  // Each task: { handle_id, command, elapsed } (elapsed in seconds).
  let { tasks = [] }: { tasks?: any[] } = $props()

  function fmtElapsed(sec: number): string {
    if (!sec || sec < 0) return '0s'
    if (sec < 60) return `${Math.floor(sec)}s`
    const m = Math.floor(sec / 60)
    const s = Math.floor(sec % 60)
    return `${m}m ${s.toString().padStart(2, '0')}s`
  }
</script>

<div class="bg-tray">
  <div style="max-width:800px;margin:0 auto;padding:4px 24px;">
    <details>
      <summary class="tray-summary">
        <span class="dot"></span>
        <span class="lbl">{tasks.length} background {tasks.length === 1 ? 'process' : 'processes'}</span>
        <span style="margin-left:auto"></span>
        <iconify-icon icon="lucide:chevron-up" width="14" style="color:rgba(0,0,0,0.35)"></iconify-icon>
      </summary>
      <div class="proc-list">
        {#each tasks as p (p.handle_id)}
        <div class="proc-row">
          <span class="proc-dot"></span>
          <div class="proc-info">
            <span class="proc-cmd mono">{p.command}</span>
          </div>
          <span class="proc-time">running · {fmtElapsed(p.elapsed)}</span>
          <button class="proc-btn stop" title="Stop">
            <iconify-icon icon="ant-design:stop-outlined" width="14"></iconify-icon>
          </button>
        </div>
        {/each}
      </div>
    </details>
  </div>
</div>

<style>
.bg-tray { flex: 0 0 auto; background: #fff; border-top: 1px solid #EEEFF1; }
.tray-summary {
  list-style: none; display: flex; align-items: center; gap: 8px;
  padding: 7px 4px; cursor: pointer; user-select: none; color: rgba(0,0,0,0.65);
  font-size: 13px;
}
.tray-summary:hover { color: #1677FF; }
.dot {
  width: 7px; height: 7px; border-radius: 9999px; background: #52C41A;
  animation: octo-dot 1.4s infinite; flex: 0 0 auto;
}
.proc-list { display: flex; flex-direction: column; gap: 6px; padding: 4px 0 8px; }
.proc-row {
  display: flex; align-items: center; gap: 10px;
  padding: 8px 12px; border: 1px solid #F0F0F0; border-radius: 8px; background: #FAFAFA;
}
.proc-dot {
  width: 7px; height: 7px; flex: 0 0 7px; border-radius: 9999px; background: #52C41A;
  animation: octo-dot 1.4s infinite;
}
.proc-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.proc-cmd { font-size: 13px; color: rgba(0,0,0,0.88); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.proc-time { font-size: 12px; color: #52C41A; flex: 0 0 auto; }
.proc-btn {
  width: 28px; height: 28px; flex: 0 0 28px; border: 1px solid #EEEFF1; background: #fff;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.45);
}
.proc-btn:hover { border-color: #4096FF; color: #4096FF; }
.proc-btn.stop:hover { border-color: #FF4D4F; color: #FF4D4F; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
