<script lang="ts">
  import type { SubAgentState } from '../../lib/stores'

  // Live concurrent sub-agents for the current turn. Fed from chatSubAgents.
  let { agents = [], elapsed = 0 }: { agents?: SubAgentState[]; elapsed?: number } = $props()

  let runningCount = $derived(agents.filter(a => a.status === 'running').length)

  // Avatar initials: first letters of the description words, capped at 2 chars.
  function initials(a: SubAgentState): string {
    const words = (a.description || a.id).split(/[\s_-]+/).filter(Boolean)
    if (words.length === 0) return '?'
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase()
    return (words[0][0] + words[1][0]).toUpperCase()
  }

  function fmtElapsed(sec: number): string {
    if (!sec || sec < 0) return '0s'
    return sec < 60 ? `${sec.toFixed(1)}s` : `${Math.floor(sec / 60)}m ${Math.floor(sec % 60)}s`
  }
</script>

<div class="subagents">
  <div class="header">
    <iconify-icon icon="ant-design:partition-outlined" width="14" style="color:rgba(0,0,0,0.45)"></iconify-icon>
    <span class="lbl">Sub-agents</span>
    <span class="count">{agents.length}</span>
    {#if runningCount > 0}
      <span class="running-badge">
        <iconify-icon icon="ant-design:loading-outlined" width="12" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
        {runningCount} running
      </span>
    {:else}
      <span class="running-badge done">
        <iconify-icon icon="ant-design:check-circle-outlined" width="12"></iconify-icon>
        all done
      </span>
    {/if}
    {#if elapsed > 0}
      <span class="elapsed mono">{fmtElapsed(elapsed)}</span>
    {/if}
  </div>

  {#each agents as a (a.id)}
    <details class="agent-row" open={a.status === 'done'}>
      <summary class="agent-summary">
        <span class="agent-avatar" class:blue={a.status === 'running'} class:green-av={a.status === 'done'}>
          {initials(a)}
          <span class="dot green" class:pulse={a.status === 'running'}></span>
        </span>
        <div class="agent-info">
          <span class="agent-name">{a.description}</span>
          <span class="agent-task mono">{a.id}</span>
        </div>
        {#if a.status === 'running'}
          <span class="status-running">
            <iconify-icon icon="ant-design:loading-outlined" width="12" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
            <span class="mono">{a.lastTool || 'working'} · {a.tools.length} tool{a.tools.length === 1 ? '' : 's'}</span>
          </span>
        {:else}
          <span class="status-done">
            <iconify-icon icon="ant-design:check-circle-outlined" width="13"></iconify-icon>
            done · {a.tools.length} tool{a.tools.length === 1 ? '' : 's'}
          </span>
        {/if}
        <iconify-icon icon="lucide:chevron-right" width="13" style="color:rgba(0,0,0,0.35);flex:0 0 auto"></iconify-icon>
      </summary>
      <div class="agent-body">
        {#if a.tools.length === 0}
          <span class="step mono" style="color:rgba(0,0,0,0.35)">No tool calls yet…</span>
        {:else}
          {#each a.tools as tool, i}
            <div class="step mono" class:err={tool.error}>
              {#if tool.error}
                <iconify-icon icon="ant-design:close-circle-outlined" width="12" style="color:#FF4D4F"></iconify-icon>
              {:else if a.status === 'running' && i === a.tools.length - 1}
                <iconify-icon icon="ant-design:loading-outlined" width="12" style="color:#1677FF;animation:octo-spin 0.8s linear infinite"></iconify-icon>
              {:else}
                <iconify-icon icon="ant-design:check-circle-outlined" width="12" style="color:#52C41A"></iconify-icon>
              {/if}
              {tool.name}
            </div>
          {/each}
        {/if}
      </div>
    </details>
  {/each}
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
.running-badge.done { color: #52C41A; }
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
.step.err { color: #CF1322; }
</style>
