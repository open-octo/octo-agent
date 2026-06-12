<script lang="ts">
  const procs = [
    { cmd: 'npm run dev', detail: 'VITE ready in 412 ms · http://localhost:5173', time: '4m 12s' },
    { cmd: 'tail -f logs/app.log', detail: '[ws] client reconnected after 2 retries', time: '1m 03s' },
  ]
</script>

<div class="bg-tray">
  <div style="max-width:800px;margin:0 auto;padding:4px 24px;">
    <details>
      <summary class="tray-summary">
        <span class="dot"></span>
        <span class="lbl">2 background processes</span>
        <span style="margin-left:auto"></span>
        <iconify-icon icon="lucide:chevron-up" width="14" style="color:rgba(0,0,0,0.35)"></iconify-icon>
      </summary>
      <div class="proc-list">
        {#each procs as p}
        <div class="proc-row">
          <span class="proc-dot"></span>
          <div class="proc-info">
            <span class="proc-cmd mono">{p.cmd}</span>
            <span class="proc-detail mono">{p.detail}</span>
          </div>
          <span class="proc-time">running · {p.time}</span>
          <button class="proc-btn" title="View output">
            <iconify-icon icon="ant-design:eye-outlined" width="14"></iconify-icon>
          </button>
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
.proc-detail { font-size: 11px; color: rgba(0,0,0,0.45); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
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
