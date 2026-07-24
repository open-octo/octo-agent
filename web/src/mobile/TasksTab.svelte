<script lang="ts">
  // Tasks tab: scheduled (cron) tasks as mobile cards — stat chips up top, then
  // one card per task with an enable switch and a run-now action. Management
  // beyond that (create/edit/delete) stays on desktop; the phone is a remote
  // control, so this view is monitor + toggle + fire.
  import { onMount } from 'svelte'
  import { sessions, showToast } from '../lib/stores'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'

  let { onOpenSession }: { onOpenSession: (id: string) => void } = $props()

  let loading = $state(true)
  let loadError = $state(false)
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
    loading = true
    loadError = false
    try {
      tasks = await api.listTasks()
    } catch (e: any) {
      loadError = true
      showToast(e?.message ?? tr('m.tasks_load_fail'), 'error')
    } finally {
      loading = false
    }
  }
  onMount(load)

  let togglingId = $state<string | null>(null)
  async function toggle(t: api.TaskResponse) {
    if (togglingId) return
    togglingId = t.id
    const next = !t.enabled
    try {
      await api.toggleTask(t.id, next)
      tasks = tasks.map(r => (r.id === t.id ? { ...r, enabled: next } : r))
      showToast(next ? tr('m.task_resumed') : tr('m.task_paused'))
      // The server recomputes next_run on resume; refresh silently so the
      // card doesn't show a stale "下次 —" until the next tab visit.
      api.listTasks().then(r => (tasks = r)).catch(() => {})
    } catch (e: any) {
      showToast(e?.message ?? tr('m.task_update_fail'), 'error')
    } finally {
      togglingId = null
    }
  }

  let runningId = $state<string | null>(null)
  async function runNow(t: api.TaskResponse) {
    if (runningId) return
    runningId = t.id
    try {
      const res = await api.runTask(t.id)
      showToast(tr('m.task_started'))
      if (res.session_id) {
        // Refresh the session list so the feed knows the new session, then
        // jump straight into its chat detail to watch the run live.
        const data = await api.listSessions().catch(() => null)
        if (data) sessions.set(data.sessions ?? [])
        onOpenSession(res.session_id)
      }
    } catch (e: any) {
      showToast(e?.message ?? tr('m.task_run_fail'), 'error')
    } finally {
      runningId = null
    }
  }
</script>

<header class="head"><h1>{$t('m.tab_tasks')}</h1></header>

<div class="scroll">
  <div class="stats">
    <div class="stat"><span class="num on">{activeCount}</span><span class="cap">{$t('m.tasks_stat_enabled')}</span></div>
    <div class="stat"><span class="num">{tasks.length - activeCount}</span><span class="cap">{$t('m.tasks_stat_paused')}</span></div>
    <div class="stat"><span class="num">{tasks.length}</span><span class="cap">{$t('m.tasks_stat_total')}</span></div>
  </div>

  {#if loading}
    <div class="empty">{$t('m.loading')}</div>
  {:else if loadError}
    <button class="empty retry" onclick={load}>{$t('m.load_retry')}</button>
  {:else if tasks.length === 0}
    <div class="empty">{$t('m.tasks_empty')}</div>
  {:else}
    {#each tasks as t2 (t2.id)}
      <div class="card" class:off={!t2.enabled}>
        <div class="top">
          <div class="names">
            <span class="name">{t2.name}</span>
            {#if t2.agent || t2.model}<span class="target">{t2.agent || t2.model}</span>{/if}
          </div>
          <button
            class="switch"
            class:on={t2.enabled}
            role="switch"
            aria-checked={t2.enabled}
            aria-label={t2.enabled ? $t('m.pause_task') : $t('m.resume_task')}
            disabled={togglingId !== null}
            onclick={() => toggle(t2)}
          ><span class="knob"></span></button>
        </div>
        <div class="meta">
          <span class="cron">{t2.cron || '—'}</span>
          <span class="runline">{$t('m.last_run')} {fmtDate(t2.last_run)} · {$t('m.next_run')} {t2.enabled ? fmtDate(t2.next_run) : '—'}</span>
        </div>
        <div class="acts">
          <button class="run" disabled={runningId !== null} onclick={() => runNow(t2)}>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>
            {runningId === t2.id ? $t('m.starting') : $t('m.run_now')}
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
  .retry { display: block; width: 100%; background: none; border: none; font-family: inherit; color: var(--m-accent); cursor: pointer; }

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
    display: inline-flex; align-items: center; gap: 5px; min-height: 36px;
    border: 1px solid var(--m-border); background: none; border-radius: 9999px;
    padding: 6px 16px; font-size: 12.5px; color: var(--m-accent);
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
