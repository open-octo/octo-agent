<script lang="ts">
  import { onMount } from 'svelte'
  import { tasks, showToast } from '../lib/stores'
  import * as api from '../lib/api'
  import type { TaskResponse } from '../lib/types'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import StatCard from '../components/ui/StatCard.svelte'

  // ── local state ──────────────────────────────────────────────────────────────

  let loading = $state(false)
  let rawTasks = $state<TaskResponse[]>([])

  // Create-task modal
  let showCreate = $state(false)
  let creating = $state(false)
  let newName = $state('')
  let newCron = $state('')
  let newPrompt = $state('')

  // ── derived KPIs ─────────────────────────────────────────────────────────────

  let activeCount = $derived(rawTasks.filter(t => t.enabled).length)

  // ── helpers ──────────────────────────────────────────────────────────────────

  function fmtDate(iso: string): string {
    if (!iso || iso === '0001-01-01T00:00:00Z') return '—'
    try {
      return new Intl.DateTimeFormat(undefined, {
        month: 'short', day: 'numeric',
        hour: '2-digit', minute: '2-digit',
      }).format(new Date(iso))
    } catch {
      return iso
    }
  }

  function nextRunLabel(t: TaskResponse): string {
    // Server doesn't expose next_run yet; fall back to last_run
    return fmtDate(t.last_run)
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
        nextRun: nextRunLabel(t),
        tagStatus: t.enabled ? 'success' : 'default',
        tagLabel: t.enabled ? 'Active' : 'Disabled',
      })))
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to load tasks', 'error')
    } finally {
      loading = false
    }
  }

  onMount(load)

  // ── actions ──────────────────────────────────────────────────────────────────

  async function handleRun(t: TaskResponse) {
    try {
      await api.runTask(t.id)
      showToast('Task started')
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to run task', 'error')
    }
  }

  async function handleDelete(t: TaskResponse) {
    try {
      await api.deleteTask(t.id)
      rawTasks = rawTasks.filter(r => r.id !== t.id)
      showToast('Task deleted')
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to delete task', 'error')
    }
  }

  function openCreate() {
    newName = ''
    newCron = ''
    newPrompt = ''
    showCreate = true
  }

  async function submitCreate() {
    if (!newName.trim() || !newCron.trim() || !newPrompt.trim()) return
    creating = true
    try {
      const t = await api.createTask({ name: newName.trim(), cron: newCron.trim(), prompt: newPrompt.trim() })
      rawTasks = [...rawTasks, t]
      showToast('Task created')
      showCreate = false
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to create task', 'error')
    } finally {
      creating = false
    }
  }

  function onBackdropClick(e: MouseEvent) {
    if ((e.target as HTMLElement).classList.contains('modal-backdrop')) showCreate = false
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') showCreate = false
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="page">
  <div class="inner">

    <!-- Header -->
    <div class="page-header">
      <div class="title-block">
        <h2>Scheduled Tasks</h2>
        <p>Tasks run automatically on a cron schedule — create or delete them here</p>
      </div>
      <button class="btn-primary" onclick={openCreate}>
        <iconify-icon icon="ant-design:plus-outlined" width="13"></iconify-icon>
        Create Task
      </button>
    </div>

    <!-- KPI row -->
    <div class="kpi-row">
      <StatCard label="Active Tasks" value={String(activeCount)} />
      <StatCard label="Total Tasks" value={String(rawTasks.length)} />
      <StatCard label="Paused" value={String(rawTasks.length - activeCount)} />
    </div>

    <!-- Table -->
    <div class="table-card">
      <div class="table-header">
        <span>Task</span>
        <span>Schedule</span>
        <span>Last / Next Run</span>
        <span>Status</span>
        <span style="text-align:right">Actions</span>
      </div>

      {#if loading}
        <div class="empty-row">Loading…</div>
      {:else if rawTasks.length === 0}
        <div class="empty-row">No scheduled tasks yet — create one to get started.</div>
      {:else}
        {#each rawTasks as t (t.id)}
          <div class="table-row">
            <!-- Name + agent/model target -->
            <div class="task-name-cell">
              <span class="task-name">{t.name}</span>
              {#if t.agent || t.model}
                <span class="task-target">{t.agent || t.model}</span>
              {/if}
            </div>

            <!-- Cron expression -->
            <span class="mono cron">{t.cron || '—'}</span>

            <!-- Next run (last_run fallback) -->
            <span class="next-run">{nextRunLabel(t)}</span>

            <!-- Status tag -->
            <span>
              <StatusTag status={t.enabled ? 'success' : 'default'}>
                {t.enabled ? 'Active' : 'Disabled'}
              </StatusTag>
            </span>

            <!-- Actions -->
            <div class="row-actions">
              <button class="act-btn" title="Run now" onclick={() => handleRun(t)}>
                <iconify-icon icon="ant-design:caret-right-outlined" width="15"></iconify-icon>
              </button>
              <button class="act-btn del" title="Delete" onclick={() => handleDelete(t)}>
                <iconify-icon icon="ant-design:delete-outlined" width="15"></iconify-icon>
              </button>
            </div>
          </div>
        {/each}
      {/if}
    </div>

  </div>
</div>

<!-- Create Task Modal -->
{#if showCreate}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal-backdrop" onclick={onBackdropClick}>
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
      <div class="modal-header">
        <span id="modal-title" class="modal-title">New Scheduled Task</span>
        <button class="modal-close" onclick={() => (showCreate = false)} aria-label="Close">
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>

      <div class="modal-body">
        <label class="field">
          <span class="field-label">Name <span class="req">*</span></span>
          <input
            class="field-input"
            type="text"
            placeholder="e.g. Daily digest"
            bind:value={newName}
            disabled={creating}
          />
        </label>

        <label class="field">
          <span class="field-label">Cron expression <span class="req">*</span></span>
          <input
            class="field-input mono"
            type="text"
            placeholder="0 9 * * *"
            bind:value={newCron}
            disabled={creating}
          />
          <span class="field-hint">Standard 5-field cron: min hour dom month dow</span>
        </label>

        <label class="field">
          <span class="field-label">Prompt <span class="req">*</span></span>
          <textarea
            class="field-textarea"
            rows="4"
            placeholder="What should the agent do each time this task runs?"
            bind:value={newPrompt}
            disabled={creating}
          ></textarea>
        </label>
      </div>

      <div class="modal-footer">
        <button class="btn-secondary" onclick={() => (showCreate = false)} disabled={creating}>Cancel</button>
        <button
          class="btn-primary"
          onclick={submitCreate}
          disabled={creating || !newName.trim() || !newCron.trim() || !newPrompt.trim()}
        >
          {creating ? 'Creating…' : 'Create Task'}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
/* ── layout ─────────────────────────────────────────────────────────────────── */
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }

/* ── page header ─────────────────────────────────────────────────────────────── */
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.title-block { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: #1F1F1F; }
p { margin: 0; font-size: 14px; color: rgba(0,0,0,0.65); }

/* ── buttons ─────────────────────────────────────────────────────────────────── */
.btn-primary {
  height: 32px; padding: 0 14px; border: none; background: #1677FF;
  border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer;
  font-family: inherit; display: inline-flex; align-items: center; gap: 6px;
}
.btn-primary:hover:not(:disabled) { background: #4096FF; }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-secondary {
  height: 32px; padding: 0 14px; border: 1px solid #D9D9D9; background: #fff;
  border-radius: 6px; font-size: 13px; color: rgba(0,0,0,0.65); cursor: pointer;
  font-family: inherit;
}
.btn-secondary:hover:not(:disabled) { border-color: #4096FF; color: #4096FF; }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── KPI row ─────────────────────────────────────────────────────────────────── */
.kpi-row { display: grid; grid-template-columns: repeat(3, minmax(0,1fr)); gap: 16px; }

/* ── table ───────────────────────────────────────────────────────────────────── */
.table-card {
  background: #fff; border-radius: 16px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.03); overflow: hidden;
}
.table-header, .table-row {
  display: grid;
  grid-template-columns: minmax(170px,2.4fr) 120px minmax(110px,1.2fr) 96px 88px;
  column-gap: 12px; align-items: center; padding: 0 24px; min-width: 660px;
}
.table-header {
  height: 44px; background: #FAFAFA;
  font-size: 12px; font-weight: 600; color: rgba(0,0,0,0.65);
  border-bottom: 1px solid #F0F0F0;
}
.table-row { padding: 12px 24px; border-bottom: 1px solid #F0F0F0; background: #fff; }
.table-row:last-child { border-bottom: none; }
.table-row:hover { background: rgba(22,119,255,0.06); }
.empty-row {
  padding: 36px 24px; text-align: center;
  font-size: 14px; color: rgba(0,0,0,0.35);
}

/* ── table cells ─────────────────────────────────────────────────────────────── */
.task-name-cell { display: flex; flex-direction: column; gap: 2px; min-width: 0; padding-right: 16px; }
.task-name { font-size: 14px; color: rgba(0,0,0,0.88); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.task-target { font-size: 12px; color: rgba(0,0,0,0.45); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.cron { font-size: 13px; color: rgba(0,0,0,0.65); }
.next-run { font-size: 13px; color: rgba(0,0,0,0.65); }
.row-actions { display: flex; align-items: center; justify-content: flex-end; gap: 4px; }
.act-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.45);
}
.act-btn:hover { background: rgba(0,0,0,0.04); color: #1677FF; }
.act-btn.del:hover { color: #FF4D4F; }

/* ── modal ───────────────────────────────────────────────────────────────────── */
.modal-backdrop {
  position: fixed; inset: 0; background: rgba(0,0,0,0.35);
  display: flex; align-items: center; justify-content: center;
  z-index: 200;
}
.modal {
  width: 480px; max-width: calc(100vw - 32px);
  background: #fff; border-radius: 16px;
  box-shadow: 0 24px 48px rgba(15,23,42,0.18);
  display: flex; flex-direction: column; overflow: hidden;
}
.modal-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 18px 24px 16px; border-bottom: 1px solid #F0F0F0;
}
.modal-title { font-size: 16px; font-weight: 600; color: #1F1F1F; }
.modal-close {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.45);
}
.modal-close:hover { background: rgba(0,0,0,0.04); color: rgba(0,0,0,0.88); }
.modal-body { padding: 20px 24px; display: flex; flex-direction: column; gap: 16px; }
.modal-footer {
  padding: 16px 24px 20px;
  display: flex; align-items: center; justify-content: flex-end; gap: 8px;
  border-top: 1px solid #F0F0F0;
}

/* ── form fields ─────────────────────────────────────────────────────────────── */
.field { display: flex; flex-direction: column; gap: 6px; }
.field-label { font-size: 13px; font-weight: 500; color: rgba(0,0,0,0.75); }
.req { color: #FF4D4F; }
.field-input, .field-textarea {
  font-family: inherit; font-size: 14px; color: rgba(0,0,0,0.88);
  border: 1px solid #D9D9D9; border-radius: 8px;
  padding: 7px 11px; outline: none; background: #fff;
  transition: border-color 0.15s;
}
.field-input:focus, .field-textarea:focus { border-color: #1677FF; box-shadow: 0 0 0 2px rgba(22,119,255,0.1); }
.field-input:disabled, .field-textarea:disabled { background: #FAFAFA; cursor: not-allowed; }
.field-textarea { resize: vertical; }
.field-hint { font-size: 12px; color: rgba(0,0,0,0.35); }
</style>
