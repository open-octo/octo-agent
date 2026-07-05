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
        <span class="lbl">{$t(tasks.length === 1 ? 'bgtask.n_process' : 'bgtask.n_processes').replace('{n}', String(tasks.length))}</span>
        <span style="margin-left:auto"></span>
        <iconify-icon icon="lucide:chevron-up" width="14" style="color:var(--text-tertiary)"></iconify-icon>
      </summary>
      <div class="proc-list">
        {#each tasks as p (p.handle_id)}
        <div class="proc-row">
          <span class="proc-dot"></span>
          <div class="proc-info">
            <span class="proc-cmd mono">{p.command}</span>
          </div>
          <span class="proc-time">{$t('bgtask.running_elapsed').replace('{elapsed}', fmtElapsed(p.elapsed))}</span>
        </div>
        {/each}
      </div>
    </details>
  </div>
</div>

<style>
.bg-tray { flex: 0 0 auto; background: var(--bg-container); border-top: 1px solid var(--border-secondary); }
.tray-summary {
  list-style: none; display: flex; align-items: center; gap: 8px;
  padding: 7px 4px; cursor: pointer; user-select: none; color: var(--text-secondary);
  font-size: 13px;
}
.tray-summary:hover { color: var(--blue-6); }
.dot {
  width: 7px; height: 7px; border-radius: 9999px; background: var(--success);
  animation: octo-dot 1.4s infinite; flex: 0 0 auto;
}
.proc-list { display: flex; flex-direction: column; gap: 6px; padding: 4px 0 8px; }
.proc-row {
  display: flex; align-items: center; gap: 10px;
  padding: 8px 12px; border: 1px solid var(--border-table); border-radius: 8px; background: var(--bg-table-header);
}
.proc-dot {
  width: 7px; height: 7px; flex: 0 0 7px; border-radius: 9999px; background: var(--success);
  animation: octo-dot 1.4s infinite;
}
.proc-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.proc-cmd { font-size: 13px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.proc-time { font-size: 12px; color: var(--success); flex: 0 0 auto; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
