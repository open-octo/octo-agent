<script lang="ts">
  import type { SubAgentState } from '../../lib/stores'
  import { t } from '../../lib/i18n'

  // Live concurrent sub-agents for the current turn. Fed from chatSubAgents.
  let { agents = [], elapsed = 0 }: { agents?: SubAgentState[]; elapsed?: number } = $props()

  let runningCount = $derived(agents.filter(a => a.status === 'running').length)

  // Track expanded state per agent. Done agents start expanded.
  let expanded = $state<Record<string, boolean>>({})
  $effect(() => {
    // Initialize expanded state for new agents
    const newExpanded: Record<string, boolean> = { ...expanded }
    let changed = false
    for (const a of agents) {
      if (!(a.id in newExpanded)) {
        newExpanded[a.id] = a.status === 'done'
        changed = true
      }
    }
    if (changed) expanded = newExpanded
  })

  function toggleExpand(id: string) {
    expanded = { ...expanded, [id]: !expanded[id] }
  }

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
    <iconify-icon icon="ant-design:partition-outlined" width="14" style="color:var(--text-tertiary)"></iconify-icon>
    <span class="lbl">{$t('agent.sub_agents')}</span>
    <span class="count">{agents.length}</span>
    {#if runningCount > 0}
      <span class="running-badge">
        <iconify-icon icon="ant-design:loading-outlined" width="12" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
        {$t('agent.n_running').replace('{n}', String(runningCount))}
      </span>
    {:else}
      <span class="running-badge done">
        <iconify-icon icon="ant-design:check-circle-outlined" width="12"></iconify-icon>
        {$t('agent.all_done')}
      </span>
    {/if}
    {#if elapsed > 0}
      <span class="elapsed mono">{fmtElapsed(elapsed)}</span>
    {/if}
  </div>

  {#each agents as a (a.id)}
    <details class="agent-row" open={expanded[a.id] ?? (a.status === 'done')}>
      <summary class="agent-summary" onclick={() => toggleExpand(a.id)}>
        <span class="agent-avatar" class:blue={a.status === 'running'} class:green-av={a.status === 'done'}>
          {initials(a)}
          <span class="dot green" class:pulse={a.status === 'running'}></span>
        </span>
        <div class="agent-info">
          <!-- An untyped child is a fork of the parent — label it "fork". -->
          <span class="agent-name"><span class="agent-type">{a.agentType || 'fork'}</span> {a.description}</span>
        </div>
        {#if a.status === 'running'}
          <span class="status-running">
            <iconify-icon icon="ant-design:loading-outlined" width="12" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
            <span class="mono">{a.lastTool || $t('agent.working')} · {$t('agent.n_tools').replace('{n}', String(a.tools.length))}</span>
          </span>
        {:else}
          <span class="status-done">
            <iconify-icon icon="ant-design:check-circle-outlined" width="13"></iconify-icon>
            {$t('agent.done_n_tools').replace('{n}', String(a.tools.length))}
          </span>
        {/if}
        <iconify-icon icon={(expanded[a.id] ?? (a.status === 'done')) ? 'lucide:chevron-down' : 'lucide:chevron-right'} width="13" style="color:var(--text-tertiary);flex:0 0 auto"></iconify-icon>
      </summary>
      <div class="agent-body">
        {#if a.tools.length === 0}
          <span class="step mono" style="color:var(--text-tertiary)">{$t('agent.no_tools_yet')}</span>
        {:else}
          {#each a.tools as tool, i}
            <div class="step mono" class:err={tool.error}>
              {#if tool.error}
                <iconify-icon icon="ant-design:close-circle-outlined" width="12" style="color:var(--error)"></iconify-icon>
              {:else if a.status === 'running' && i === a.tools.length - 1}
                <iconify-icon icon="ant-design:loading-outlined" width="12" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
              {:else}
                <iconify-icon icon="ant-design:check-circle-outlined" width="12" style="color:var(--success)"></iconify-icon>
              {/if}
              <span class="tool-name">{tool.name}</span>
              {#if tool.input && Object.keys(tool.input).length > 0}
                <span class="tool-input mono">({Object.entries(tool.input).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join(', ')})</span>
              {/if}
            </div>
          {/each}
        {/if}
      </div>
    </details>
  {/each}
</div>

<style>
.subagents { border: 1px solid var(--border-table); border-radius: 10px; background: var(--bg-container); overflow: hidden; }
.header {
  display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; background: var(--bg-table-header); border-bottom: 1px solid var(--border-table);
  font-size: 13px; color: var(--text-secondary);
}
.lbl { flex: 0 0 auto; }
.count {
  font-size: 11px; font-weight: 600; background: var(--blue-1); color: var(--blue-6);
  border-radius: 9999px; min-width: 16px; height: 16px; padding: 0 5px;
  display: inline-flex; align-items: center; justify-content: center;
}
.running-badge { margin-left: auto; display: inline-flex; align-items: center; gap: 5px; font-size: 12px; color: var(--blue-6); }
.running-badge.done { color: var(--success); }
.elapsed { font-size: 12px; color: var(--text-tertiary); margin-left: 10px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.agent-row { border-bottom: 1px solid var(--border-table); }
.agent-row:last-child { border-bottom: none; }
.agent-summary {
  list-style: none; display: flex; align-items: center; gap: 10px;
  padding: 10px 12px; cursor: pointer; user-select: none;
}
.agent-summary:hover { background: var(--hover-neutral); }
.agent-avatar {
  width: 24px; height: 24px; flex: 0 0 24px;
  border-radius: 7px; display: flex; align-items: center; justify-content: center;
  font-size: 11px; font-weight: 600; position: relative;
}
.agent-avatar.blue { background: var(--blue-1); color: var(--blue-6); }
.agent-avatar.green-av { background: var(--success-bg); color: var(--success-text); }
.dot {
  position: absolute; right: -2px; bottom: -2px;
  width: 8px; height: 8px; border-radius: 9999px;
  border: 2px solid #fff;
}
.dot.green { background: var(--success); }
.dot.pulse { animation: octo-dot 1.4s infinite; }
.agent-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.agent-name { font-size: 13px; font-weight: 600; color: var(--text-heading); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.agent-type { font-weight: 600; color: var(--blue-6); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
.status-running { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; color: var(--blue-6); flex: 0 0 auto; }
.status-done { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; color: var(--success); flex: 0 0 auto; }
.agent-body {
  border-top: 1px solid var(--border-table); background: var(--bg-sidebar);
  padding: 8px 14px 8px 46px; display: flex; flex-direction: column; gap: 6px;
}
.step { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--text-secondary); flex-wrap: wrap; }
.step.err { color: var(--error-dark); }
.tool-name { font-weight: 500; }
.tool-input {
  font-size: 11px; color: var(--text-tertiary); opacity: 0.8;
  display: inline-block; max-width: 420px; overflow: hidden;
  text-overflow: ellipsis; white-space: nowrap; vertical-align: bottom;
}
</style>
