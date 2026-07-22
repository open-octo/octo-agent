<script lang="ts">
  import { onMount } from 'svelte'
  import { tasks, showToast, sessions, sessionGroups, activeSessionId, view, setActiveSession, openAgentSession } from '../lib/stores'
  import { t, tr, locale } from '../lib/i18n'
  import { confirmDialog } from '../lib/confirm'
  import { get } from 'svelte/store'
  import * as api from '../lib/api'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import StatCard from '../components/ui/StatCard.svelte'

  // ── local state ──────────────────────────────────────────────────────────────

  let loading = $state(false)
  let rawTasks = $state<api.TaskResponse[]>([])

  // ── derived KPIs ─────────────────────────────────────────────────────────────

  let activeCount = $derived(rawTasks.filter(t => t.enabled).length)

  // ── helpers ──────────────────────────────────────────────────────────────────

  function fmtDate(iso: string): string {
    if (!iso || iso === '0001-01-01T00:00:00Z') return '—'
    try {
      return new Intl.DateTimeFormat(get(locale) === 'zh' ? 'zh-CN' : 'en-US', {
        month: 'short', day: 'numeric',
        hour: '2-digit', minute: '2-digit',
      }).format(new Date(iso))
    } catch {
      return iso
    }
  }

  // ── data loading ─────────────────────────────────────────────────────────────

  async function load() {
    loading = true
    try {
      rawTasks = await api.listTasks()
      // Sync into shared store (ScheduledTask display shape) for other consumers
      tasks.set(rawTasks.map(t => ({
        name: t.name,
        target: t.agent || t.model || '',
        cron: t.cron,
        nextRun: t.enabled ? fmtDate(t.next_run) : '—',
        tagStatus: t.enabled ? 'success' : 'default',
        tagLabel: t.enabled ? tr('status.active') : tr('status.disabled'),
      })))
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to load tasks', 'error')
    } finally {
      loading = false
    }
  }

  onMount(load)

  // ── actions ──────────────────────────────────────────────────────────────────

  async function handleRun(t: api.TaskResponse) {
    try {
      const res = await api.runTask(t.id)
      showToast(tr('tasks.toast_started'))
      if (res.session_id) {
        // Refresh the session list so the new session shows in the sidebar,
        // then activate it and switch to chat. The streamed turn events will
        // appear automatically once the WebSocket subscribes. Also refresh
        // the groups snapshot — a cron run files its session into the task's
        // group server-side, but that store is otherwise only ever populated
        // once at sidebar mount, so without this the session would render
        // ungrouped until a manual reload (#1699). The groups fetch is
        // best-effort: the task already started, so its failure must not
        // abort activating the session or read as "Failed to run task".
        const [data, org] = await Promise.all([
          api.listSessions(),
          api.listSessionGroups().catch(() => null),
        ])
        sessions.set(data.sessions ?? [])
        if (org) sessionGroups.set(org.groups)
        setActiveSession(res.session_id)
        view.set('chat')
      }
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to run task', 'error')
    }
  }

  async function handleToggle(t: api.TaskResponse) {
    const next = !t.enabled
    try {
      await api.toggleTask(t.id, next)
      rawTasks = rawTasks.map(r => r.id === t.id ? { ...r, enabled: next } : r)
      showToast(next ? tr('tasks.resumed') : tr('tasks.paused_toast'))
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to update task', 'error')
    }
  }

  async function handleDelete(t: api.TaskResponse) {
    if (!(await confirmDialog(tr('tasks.confirm_delete').replace('{name}', t.name)))) return
    try {
      await api.deleteTask(t.id)
      rawTasks = rawTasks.filter(r => r.id !== t.id)
      showToast(tr('tasks.toast_deleted'))
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to delete task', 'error')
    }
  }

  // Agentic-first: creating/editing a task opens a fresh chat that invokes the
  // cron-task-creator skill, which collects the schedule + prompt in conversation
  // (no form).
  function openCreate() {
    openAgentSession('/cron-task-creator', 'New task')
  }

  function openEdit(t: api.TaskResponse) {
    openAgentSession(`/cron-task-creator edit ${t.id} "${t.name}"`, `Edit task: ${t.name}`)
  }
</script>

<div class="page">
  <div class="inner">

    <!-- Header -->
    <div class="page-header">
      <div class="title-block">
        <h2>{$t('tasks.title')}</h2>
        <p>{$t('tasks.desc')}</p>
      </div>
      <button class="btn-primary" onclick={openCreate}>
        <iconify-icon icon="ant-design:plus-outlined" width="13"></iconify-icon>
        {$t('tasks.create')}
      </button>
    </div>

    <!-- KPI row -->
    <div class="kpi-row">
      <StatCard label={$t('tasks.active')} value={String(activeCount)} />
      <StatCard label={$t('tasks.total')} value={String(rawTasks.length)} />
      <StatCard label={$t('status.paused')} value={String(rawTasks.length - activeCount)} />
    </div>

    <!-- Table -->
    <div class="table-card">
      <div class="table-header">
        <span>{$t('tasks.col_task')}</span>
        <span>{$t('tasks.col_schedule')}</span>
        <span>{$t('tasks.col_last_next')}</span>
        <span>{$t('tasks.col_status')}</span>
        <span style="text-align:right">{$t('common.actions')}</span>
      </div>

      {#if loading}
        <div class="empty-row">{$t('common.loading')}</div>
      {:else if rawTasks.length === 0}
        <div class="empty-row">{$t('tasks.empty')}</div>
      {:else}
        {#each rawTasks as task (task.id)}
          <div class="table-row">
            <!-- Name + agent/model target -->
            <div class="task-name-cell">
              <span class="task-name">{task.name}</span>
              {#if task.agent || task.model}
                <span class="task-target">{task.agent || task.model}</span>
              {/if}
            </div>

            <!-- Cron expression -->
            <span class="mono cron">{task.cron || '—'}</span>

            <!-- Last run / next scheduled run (truthfully distinct values) -->
            <div class="run-times-cell">
              <span class="run-line">{$t('tasks.last_run_label')} {fmtDate(task.last_run)}</span>
              <span class="run-line">{$t('tasks.next_run_label')} {task.enabled ? fmtDate(task.next_run) : '—'}</span>
            </div>

            <!-- Status tag -->
            <span>
              <StatusTag status={task.enabled ? 'success' : 'default'}>
                {task.enabled ? $t('status.active') : $t('status.disabled')}
              </StatusTag>
            </span>

            <!-- Actions -->
            <div class="row-actions">
              <button class="act-btn" title={$t('tasks.run_now')} onclick={() => handleRun(task)}>
                <iconify-icon icon="ant-design:caret-right-outlined" width="15"></iconify-icon>
              </button>
              <button
                class="act-btn"
                title={task.enabled ? $t('tasks.pause') : $t('tasks.resume')}
                onclick={() => handleToggle(task)}
              >
                <iconify-icon icon={task.enabled ? 'ant-design:pause-outlined' : 'ant-design:play-circle-outlined'} width="15"></iconify-icon>
              </button>
              <button class="act-btn" title={$t('common.edit')} onclick={() => openEdit(task)}>
                <iconify-icon icon="ant-design:edit-outlined" width="14"></iconify-icon>
              </button>
              <button class="act-btn del" title={$t('common.delete')} onclick={() => handleDelete(task)}>
                <iconify-icon icon="ant-design:delete-outlined" width="15"></iconify-icon>
              </button>
            </div>
          </div>
        {/each}
      {/if}
    </div>

  </div>
</div>

<style>
/* ── layout ─────────────────────────────────────────────────────────────────── */
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }

/* ── page header ─────────────────────────────────────────────────────────────── */
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.title-block { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p { margin: 0; font-size: 14px; color: var(--text-secondary); }

/* ── buttons ─────────────────────────────────────────────────────────────────── */
.btn-primary {
  height: 32px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer;
  font-family: inherit; display: inline-flex; align-items: center; gap: 6px;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── KPI row ─────────────────────────────────────────────────────────────────── */
.kpi-row { display: grid; grid-template-columns: repeat(3, minmax(0,1fr)); gap: 16px; }

/* ── table ───────────────────────────────────────────────────────────────────── */
.table-card {
  background: var(--bg-container); border-radius: 16px;
  box-shadow: var(--card-shadow); overflow-x: auto;
}
.table-header, .table-row {
  display: grid;
  grid-template-columns: minmax(170px,2.4fr) 120px minmax(110px,1.2fr) 96px 120px;
  column-gap: 12px; align-items: center; padding: 0 24px; min-width: 690px;
}
.table-header {
  height: 44px; background: var(--bg-table-header);
  font-size: 12px; font-weight: 600; color: var(--text-secondary);
  border-bottom: 1px solid var(--border-table);
}
.table-row { padding: 12px 24px; border-bottom: 1px solid var(--border-table); background: var(--bg-container); }
.table-row:last-child { border-bottom: none; }
.table-row:hover { background: var(--active-blue-bg); }
.empty-row {
  padding: 36px 24px; text-align: center;
  font-size: 14px; color: var(--text-tertiary);
}

/* ── table cells ─────────────────────────────────────────────────────────────── */
.task-name-cell { display: flex; flex-direction: column; gap: 2px; min-width: 0; padding-right: 16px; }
.task-name { font-size: 14px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.task-target { font-size: 12px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.cron { font-size: 13px; color: var(--text-secondary); }
.run-times-cell { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.run-line { font-size: 12px; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row-actions { display: flex; align-items: center; justify-content: flex-end; gap: 4px; }
.act-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.act-btn:hover { background: var(--hover-neutral); color: var(--blue-6); }
.act-btn.del:hover { color: var(--error); }
</style>
