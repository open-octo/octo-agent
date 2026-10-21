<script lang="ts" module>
  import type { TaskResponse } from '../lib/api'
  // Outlives the component, which MobileApp unmounts on every tab switch:
  // coming back shows the last list at once and refreshes underneath, rather
  // than a loading screen for a round trip that may cross a tunnel.
  let cached: TaskResponse[] | null = null
</script>

<script lang="ts">
  // Tasks tab: scheduled (cron) tasks as mobile cards — stat chips up top, then
  // one card per task with an enable switch, run-now, edit and delete. Create
  // and edit are agentic as on desktop: a fresh chat runs the cron-task-creator
  // skill, which collects the schedule and prompt in conversation (an edit
  // answers with an inline octo-ui form the chat renders).
  import { onMount } from 'svelte'
  import { sessions, showToast, prependSession } from '../lib/stores'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import StatusTag from '../components/ui/StatusTag.svelte'

  let { onOpenSession, onAgentSession }: {
    onOpenSession: (id: string) => void
    // A fresh session plus the first message to send once its chat is open.
    onAgentSession: (id: string, prompt: string) => void
  } = $props()

  let loading = $state(cached === null)
  let loadError = $state(false)
  let tasks = $state<api.TaskResponse[]>(cached ?? [])
  $effect(() => {
    if (!loading && !loadError) cached = tasks
  })
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
    loading = cached === null
    loadError = false
    try {
      tasks = await api.listTasks()
    } catch (e: any) {
      // A failed background refresh keeps the cached list on screen.
      loadError = cached === null
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
  // The desktop's openAgentSession, on the phone's own navigation: name the
  // session, then hand its id and the skill command to the chat opener.
  let opening = $state(false)
  async function openAgentic(prompt: string, name: string) {
    if (opening) return
    opening = true
    try {
      const sess = await api.createSession({ source: 'manual', name })
      prependSession(sess)
      onAgentSession(sess.id, prompt)
    } catch (e: any) {
      showToast(e?.message ?? tr('m.create_fail'), 'error')
    } finally {
      opening = false
    }
  }
  const createTask = () => openAgentic('/cron-task-creator', tr('tasks.session_new'))
  const editTask = (t: api.TaskResponse) =>
    openAgentic(`/cron-task-creator edit ${t.id} "${t.name}"`, tr('tasks.session_edit').replace('{name}', t.name))

  // Delete asks first, inside the card (window.confirm isn't surfaced by every
  // WebView).
  let confirmingId = $state<string | null>(null)
  let deletingId = $state<string | null>(null)
  async function deleteTask(t: api.TaskResponse) {
    confirmingId = null
    deletingId = t.id
    try {
      await api.deleteTask(t.id)
      tasks = tasks.filter(x => x.id !== t.id)
      showToast(tr('tasks.toast_deleted'))
    } catch (e: any) {
      showToast(e?.message ?? String(e), 'error')
    } finally {
      deletingId = null
    }
  }

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

<header class="head">
  <h1>{$t('m.tab_tasks')}</h1>
  <button class="add" onclick={createTask} disabled={opening} aria-label={$t('tasks.create')}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M12 5v14M5 12h14"/></svg>
  </button>
</header>

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
            <div class="name-row">
              <span class="name">{t2.name}</span>
              {#if t2.permission_mode}
                <span class="perm">
                  <StatusTag status={t2.permission_mode === 'strict' ? 'warning' : 'default'}>
                    {$t(`tasks.perm_${t2.permission_mode}`)}
                  </StatusTag>
                </span>
              {/if}
            </div>
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
        {#if confirmingId === t2.id}
          <div class="confirm">
            <p>{$t('tasks.confirm_delete').replace('{name}', t2.name)}</p>
            <div class="acts">
              <button class="ghost" onclick={() => (confirmingId = null)}>{$t('m.cancel')}</button>
              <button class="ghost danger" onclick={() => deleteTask(t2)}>{$t('common.delete')}</button>
            </div>
          </div>
        {:else}
          <div class="acts">
            <button class="ghost danger" disabled={deletingId === t2.id} onclick={() => (confirmingId = t2.id)}>{$t('common.delete')}</button>
            <button class="ghost" disabled={opening} onclick={() => editTask(t2)}>{$t('common.edit')}</button>
            <button class="run" disabled={runningId !== null} onclick={() => runNow(t2)}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>
              {runningId === t2.id ? $t('m.starting') : $t('m.run_now')}
            </button>
          </div>
        {/if}
      </div>
    {/each}
  {/if}
</div>

<style>
  .head { flex: none; display: flex; align-items: center; justify-content: space-between; padding: 8px 18px 12px; }
  .add {
    width: 34px; height: 34px; border-radius: 50%; border: none; flex: none;
    background: var(--m-accent); color: #fff; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
  }
  .add:disabled { opacity: .5; }
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
  .name-row { display: flex; align-items: center; gap: 6px; min-width: 0; }
  .perm { flex-shrink: 0; display: inline-flex; }
  .name { font-size: 14.5px; font-weight: 600; color: var(--m-text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .target { font-size: 12px; color: var(--m-text-3); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta { display: flex; flex-direction: column; gap: 3px; margin-top: 8px; }
  .cron { font-family: ui-monospace, Menlo, monospace; font-size: 12.5px; color: var(--m-text-2); }
  .runline { font-size: 12px; color: var(--m-text-3); }
  .acts { display: flex; justify-content: flex-end; gap: 8px; margin-top: 10px; }
  .ghost {
    min-height: 36px; padding: 6px 14px; border-radius: 9999px;
    border: 1px solid var(--m-border); background: none;
    font-size: 12.5px; color: var(--m-text-2); font-family: inherit; cursor: pointer;
  }
  .ghost.danger { color: var(--m-error); }
  .ghost:disabled { opacity: .5; }
  .confirm p { margin: 10px 0 0; font-size: 12.5px; color: var(--m-text-2); }
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
