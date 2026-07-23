<script lang="ts">
  // Tasks tab: scheduled (cron) tasks as mobile cards — stat chips up top, then
  // one card per task with an enable switch and a run-now action. Management
  // beyond that (create/edit/delete) stays on desktop; the phone is a remote
  // control, so this view is monitor + toggle + fire.
  import { onMount } from 'svelte'
  import { sessions, setActiveSession, showToast } from '../lib/stores'
  import * as api from '../lib/api'

  let { onOpenSession }: { onOpenSession: (id: string) => void } = $props()

  let loading = $state(true)
  let tasks = $state<api.TaskResponse[]>([])
  const activeCount = $derived(tasks.filter(t => t.enabled).length)

  function fmtDate(iso: string): string {
    if (!iso || iso === '0001-01-01T00:00:00Z') return '—'
    try {
      return new Intl.DateTimeFormat('zh-CN', {
        month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
      }).format(new Date(iso))
    } catch {
      return iso
    }
  }

  async function load() {
    try {
      tasks = await api.listTasks()
    } catch (e: any) {
      showToast(e?.message ?? '加载任务失败', 'error')
    } finally {
      loading = false
    }
  }
  onMount(load)

  async function toggle(t: api.TaskResponse) {
    const next = !t.enabled
    try {
      await api.toggleTask(t.id, next)
      tasks = tasks.map(r => (r.id === t.id ? { ...r, enabled: next } : r))
      showToast(next ? '任务已恢复' : '任务已暂停')
    } catch (e: any) {
      showToast(e?.message ?? '更新任务失败', 'error')
    }
  }

  let runningId = $state<string | null>(null)
  async function runNow(t: api.TaskResponse) {
    if (runningId) return
    runningId = t.id
    try {
      const res = await api.runTask(t.id)
      showToast('任务已启动')
      if (res.session_id) {
        // Refresh the session list so the feed knows the new session, then
        // jump straight into its chat detail to watch the run live.
        const data = await api.listSessions().catch(() => null)
        if (data) sessions.set(data.sessions ?? [])
        setActiveSession(res.session_id)
        onOpenSession(res.session_id)
      }
    } catch (e: any) {
      showToast(e?.message ?? '运行任务失败', 'error')
    } finally {
      runningId = null
    }
  }
</script>

<header class="head"><h1>任务</h1></header>

<div class="scroll">
  <div class="stats">
    <div class="stat"><span class="num on">{activeCount}</span><span class="cap">启用</span></div>
    <div class="stat"><span class="num">{tasks.length - activeCount}</span><span class="cap">暂停</span></div>
    <div class="stat"><span class="num">{tasks.length}</span><span class="cap">总数</span></div>
  </div>

  {#if loading}
    <div class="empty">加载中…</div>
  {:else if tasks.length === 0}
    <div class="empty">还没有定时任务 · 在桌面端用 /cron-task-creator 创建</div>
  {:else}
    {#each tasks as t (t.id)}
      <div class="card" class:off={!t.enabled}>
        <div class="top">
          <div class="names">
            <span class="name">{t.name}</span>
            {#if t.agent || t.model}<span class="target">{t.agent || t.model}</span>{/if}
          </div>
          <button
            class="switch"
            class:on={t.enabled}
            role="switch"
            aria-checked={t.enabled}
            aria-label={t.enabled ? '暂停任务' : '恢复任务'}
            onclick={() => toggle(t)}
          ><span class="knob"></span></button>
        </div>
        <div class="meta">
          <span class="cron">{t.cron || '—'}</span>
          <span class="runline">上次 {fmtDate(t.last_run)} · 下次 {t.enabled ? fmtDate(t.next_run) : '—'}</span>
        </div>
        <div class="acts">
          <button class="run" disabled={runningId !== null} onclick={() => runNow(t)}>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>
            {runningId === t.id ? '启动中…' : '立即运行'}
          </button>
        </div>
      </div>
    {/each}
  {/if}
</div>

<style>
  .head { flex: none; padding: 8px 18px 12px; }
  .head h1 { margin: 0; font-size: 24px; font-weight: 600; color: var(--m-text-strong); }
  .scroll { flex: 1; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; padding: 0 16px 20px; }

  .stats { display: flex; gap: 10px; margin-bottom: 14px; }
  .stat {
    flex: 1; display: flex; flex-direction: column; align-items: center; gap: 2px;
    background: var(--m-surface); border-radius: 14px; padding: 12px 0;
    box-shadow: var(--m-shadow-card);
  }
  .stat .num { font-size: 20px; font-weight: 700; color: var(--m-text-strong); font-variant-numeric: tabular-nums; }
  .stat .num.on { color: var(--m-accent); }
  .stat .cap { font-size: 11px; color: var(--m-text-3); }

  .empty { padding: 40px 16px; text-align: center; font-size: 13px; color: var(--m-text-3); }

  .card {
    background: var(--m-surface); border-radius: 14px; box-shadow: var(--m-shadow-card);
    padding: 14px 16px; margin-bottom: 10px;
  }
  .card.off .name, .card.off .cron { color: var(--m-text-3); }
  .top { display: flex; align-items: center; gap: 12px; }
  .names { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .name { font-size: 14.5px; font-weight: 600; color: var(--m-text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .target { font-size: 12px; color: var(--m-text-3); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta { display: flex; flex-direction: column; gap: 3px; margin-top: 8px; }
  .cron { font-family: ui-monospace, Menlo, monospace; font-size: 12.5px; color: var(--m-text-2); }
  .runline { font-size: 12px; color: var(--m-text-3); }
  .acts { display: flex; justify-content: flex-end; margin-top: 10px; }
  .run {
    display: inline-flex; align-items: center; gap: 5px;
    border: 1px solid var(--m-border); background: none; border-radius: 9999px;
    padding: 6px 14px; font-size: 12.5px; color: var(--m-accent);
    font-family: inherit; cursor: pointer;
  }
  .run:disabled { opacity: .5; }

  .switch {
    flex: none; width: 42px; height: 24px; border-radius: 9999px; border: none;
    padding: 0; position: relative; cursor: pointer; background: var(--m-border);
    transition: background .15s;
  }
  .switch.on { background: var(--m-accent); }
  .switch .knob {
    position: absolute; top: 2px; left: 2px; width: 20px; height: 20px; border-radius: 50%;
    background: #fff; box-shadow: 0 1px 2px rgba(0,0,0,.2); transition: left .15s;
  }
  .switch.on .knob { left: 20px; }
</style>
