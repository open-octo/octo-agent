<script lang="ts">
  // Concurrent sub-agents panel
</script>

<div class="subagents">
  <div class="header">
    <iconify-icon icon="ant-design:partition-outlined" width="14" style="color:rgba(0,0,0,0.45)"></iconify-icon>
    <span class="lbl">Sub-agents</span>
    <span class="count">3</span>
    <span class="running-badge">
      <iconify-icon icon="ant-design:loading-outlined" width="12" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
      2 running
    </span>
    <span class="elapsed mono">8.1s</span>
  </div>

  <!-- Explorer 1 — running -->
  <details class="agent-row">
    <summary class="agent-summary">
      <span class="agent-avatar blue">E1<span class="dot green pulse"></span></span>
      <div class="agent-info">
        <span class="agent-name">Explorer 1</span>
        <span class="agent-task">Map every SetReadDeadline call site</span>
      </div>
      <span class="status-running">
        <iconify-icon icon="ant-design:loading-outlined" width="12" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
        <span class="mono">grep · 3 tools</span>
      </span>
      <iconify-icon icon="lucide:chevron-right" width="13" style="color:rgba(0,0,0,0.35);flex:0 0 auto"></iconify-icon>
    </summary>
    <div class="agent-body">
      <div class="step done mono"><iconify-icon icon="ant-design:check-circle-outlined" width="12" style="color:#52C41A"></iconify-icon> glob internal/ws/*.go</div>
      <div class="step done mono"><iconify-icon icon="ant-design:check-circle-outlined" width="12" style="color:#52C41A"></iconify-icon> read_file client.go</div>
      <div class="step active mono"><iconify-icon icon="ant-design:loading-outlined" width="12" style="color:#1677FF;animation:octo-spin 0.8s linear infinite"></iconify-icon> grep "SetReadDeadline"…</div>
    </div>
  </details>

  <!-- Writer — done -->
  <details open class="agent-row">
    <summary class="agent-summary">
      <span class="agent-avatar green-av">W<span class="dot green"></span></span>
      <div class="agent-info">
        <span class="agent-name">Writer</span>
        <span class="agent-task">Draft backoff test cases</span>
      </div>
      <span class="status-done">
        <iconify-icon icon="ant-design:check-circle-outlined" width="13"></iconify-icon>
        done · 5 tools
      </span>
      <iconify-icon icon="lucide:chevron-down" width="13" style="color:rgba(0,0,0,0.35);flex:0 0 auto"></iconify-icon>
    </summary>
    <div class="agent-body">
      <span style="font-size:12px;line-height:1.6;color:rgba(0,0,0,0.65);">Returned 6 table cases covering capped growth, jitter bounds and reset-on-success.</span>
      <span class="mono" style="font-size:11px;color:rgba(0,0,0,0.45);">↑ 1.2k tokens · 4.6s</span>
    </div>
  </details>
</div>

<style>
.subagents { border: 1px solid #F0F0F0; border-radius: 10px; background: #fff; overflow: hidden; }
.header {
  display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; background: #FAFAFA; border-bottom: 1px solid #F0F0F0;
  font-size: 13px; color: rgba(0,0,0,0.65);
}
.lbl { flex: 0 0 auto; }
.count {
  font-size: 11px; font-weight: 600; background: #E6F4FF; color: #1677FF;
  border-radius: 9999px; min-width: 16px; height: 16px; padding: 0 5px;
  display: inline-flex; align-items: center; justify-content: center;
}
.running-badge { margin-left: auto; display: inline-flex; align-items: center; gap: 5px; font-size: 12px; color: #1677FF; }
.elapsed { font-size: 12px; color: rgba(0,0,0,0.45); margin-left: 10px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.agent-row { border-bottom: 1px solid #F0F0F0; }
.agent-row:last-child { border-bottom: none; }
.agent-summary {
  list-style: none; display: flex; align-items: center; gap: 10px;
  padding: 10px 12px; cursor: pointer; user-select: none;
}
.agent-summary:hover { background: rgba(0,0,0,0.02); }
.agent-avatar {
  width: 24px; height: 24px; flex: 0 0 24px;
  border-radius: 7px; display: flex; align-items: center; justify-content: center;
  font-size: 11px; font-weight: 600; position: relative;
}
.agent-avatar.blue { background: #E6F4FF; color: #1677FF; }
.agent-avatar.green-av { background: #F6FFED; color: #389E0D; }
.dot {
  position: absolute; right: -2px; bottom: -2px;
  width: 8px; height: 8px; border-radius: 9999px;
  border: 2px solid #fff;
}
.dot.green { background: #52C41A; }
.dot.pulse { animation: octo-dot 1.4s infinite; }
.agent-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.agent-name { font-size: 13px; font-weight: 600; color: #1F1F1F; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.agent-task { font-size: 12px; color: rgba(0,0,0,0.45); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.status-running { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; color: #1677FF; flex: 0 0 auto; }
.status-done { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; color: #52C41A; flex: 0 0 auto; }
.agent-body {
  border-top: 1px solid #F0F0F0; background: #FBFBFB;
  padding: 8px 14px 8px 46px; display: flex; flex-direction: column; gap: 6px;
}
.step { display: flex; align-items: center; gap: 8px; font-size: 12px; color: rgba(0,0,0,0.65); }
.step.done { color: rgba(0,0,0,0.65); }
.step.active { color: rgba(0,0,0,0.88); }
</style>
