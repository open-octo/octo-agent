<script lang="ts">
  import type { WorkflowRunState } from '../../lib/stores'
  import { t } from '../../lib/i18n'

  // Background workflow runs for the session. Fed from chatWorkflows; these
  // persist across turns (a workflow runs detached).
  let { runs = [], now = 0 }: { runs?: WorkflowRunState[]; now?: number } = $props()

  let runningCount = $derived(runs.filter(r => r.status === 'running').length)

  function fmtElapsed(startedAt: number): string {
    const sec = now > 0 && startedAt > 0 ? (now - startedAt) / 1000 : 0
    if (!sec || sec < 0) return '0s'
    return sec < 60 ? `${sec.toFixed(0)}s` : `${Math.floor(sec / 60)}m ${Math.floor(sec % 60)}s`
  }

  // Last few progress lines, newest last — the live tail shown under a run.
  function tail(r: WorkflowRunState): string[] {
    return r.progress.slice(-6)
  }
</script>

<div class="workflows">
  <div class="header">
    <iconify-icon icon="ant-design:deployment-unit-outlined" width="14" style="color:var(--text-tertiary)"></iconify-icon>
    <span class="lbl">{$t('workflow.title')}</span>
    <span class="count">{runs.length}</span>
    {#if runningCount > 0}
      <span class="running-badge">
        <iconify-icon icon="ant-design:loading-outlined" width="12" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
        {runningCount} {$t('workflow.running')}
      </span>
    {:else}
      <span class="running-badge done">
        <iconify-icon icon="ant-design:check-circle-outlined" width="12"></iconify-icon>
        {$t('workflow.all_done')}
      </span>
    {/if}
  </div>

  {#each runs as r (r.id)}
    <details class="run-row" open={r.status === 'running'}>
      <summary class="run-summary">
        <span class="run-icon" class:blue={r.status === 'running'} class:green-av={r.status === 'done'} class:red-av={r.status === 'error'}>
          {#if r.status === 'running'}
            <iconify-icon icon="ant-design:loading-outlined" width="13" style="animation:octo-spin 0.8s linear infinite"></iconify-icon>
          {:else if r.status === 'error'}
            <iconify-icon icon="ant-design:close-circle-outlined" width="13"></iconify-icon>
          {:else}
            <iconify-icon icon="ant-design:check-circle-outlined" width="13"></iconify-icon>
          {/if}
        </span>
        <div class="run-info">
          <span class="run-name">{r.description}</span>
          <span class="run-id mono">{r.id}</span>
        </div>
        <span class="status mono"
          class:s-run={r.status === 'running'}
          class:s-done={r.status === 'done'}
          class:s-err={r.status === 'error'}>
          {r.status === 'running' ? $t('workflow.running') : r.status} · {fmtElapsed(r.startedAt)}
        </span>
        <iconify-icon icon="lucide:chevron-right" width="13" style="color:var(--text-tertiary);flex:0 0 auto"></iconify-icon>
      </summary>
      <div class="run-body">
        {#if tail(r).length === 0}
          <span class="step mono" style="color:var(--text-tertiary)">{$t('workflow.no_progress')}</span>
        {:else}
          {#each tail(r) as line}
            <div class="step mono">{line}</div>
          {/each}
        {/if}
        {#if r.status !== 'running'}
          <span class="hint mono">{$t('workflow.collect_hint')} workflow_status("{r.id}")</span>
        {/if}
      </div>
    </details>
  {/each}
</div>

<style>
.workflows { border: 1px solid var(--border-table); border-radius: 10px; background: var(--bg-container); overflow: hidden; }
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
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.run-row { border-bottom: 1px solid var(--border-table); }
.run-row:last-child { border-bottom: none; }
.run-summary {
  list-style: none; display: flex; align-items: center; gap: 10px;
  padding: 10px 12px; cursor: pointer; user-select: none;
}
.run-summary:hover { background: var(--hover-neutral); }
.run-icon {
  width: 24px; height: 24px; flex: 0 0 24px;
  border-radius: 7px; display: flex; align-items: center; justify-content: center;
}
.run-icon.blue { background: var(--blue-1); color: var(--blue-6); }
.run-icon.green-av { background: var(--success-bg); color: var(--success-text); }
.run-icon.red-av { background: var(--error-bg); color: var(--error-dark); }
.run-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.run-name { font-size: 13px; font-weight: 600; color: var(--text-heading); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.run-id { font-size: 12px; color: var(--text-tertiary); }
.status { font-size: 12px; flex: 0 0 auto; }
.status.s-run { color: var(--blue-6); }
.status.s-done { color: var(--success); }
.status.s-err { color: var(--error-dark); }
.run-body {
  border-top: 1px solid var(--border-table); background: var(--bg-sidebar);
  padding: 8px 14px 8px 46px; display: flex; flex-direction: column; gap: 6px;
}
.step { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--text-secondary); word-break: break-word; }
.hint { font-size: 11px; color: var(--text-tertiary); }
</style>
