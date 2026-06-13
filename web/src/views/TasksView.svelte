<script lang="ts">
  import { onMount } from 'svelte'
  import { tasks, showToast } from '../lib/stores'
  import { t, tr } from '../lib/i18n'
  import * as api from '../lib/api'
  import type { TaskResponse } from '../lib/types'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import StatCard from '../components/ui/StatCard.svelte'

  // ── local state ──────────────────────────────────────────────────────────────

  let loading = $state(false)
  let rawTasks = $state<TaskResponse[]>([])

  // Create/edit-task modal
  let showCreate = $state(false)
  let creating = $state(false)
  let editingName = $state<string | null>(null)   // non-null ⇒ editing that task
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
    editingName = null
    newName = ''
    newCron = ''
    newPrompt = ''
    showCreate = true
  }

  function openEdit(t: TaskResponse) {
    editingName = t.name
    newName = t.name
    newCron = t.cron
    newPrompt = t.prompt
    showCreate = true
  }

  async function submitCreate() {
    if (!newName.trim() || !newCron.trim() || !newPrompt.trim()) return
    creating = true
    try {
      if (editingName) {
        await api.updateTask(editingName, { cron: newCron.trim(), prompt: newPrompt.trim() })
        await load()
        showToast('Task updated')
      } else {
        const t = await api.createTask({ name: newName.trim(), cron: newCron.trim(), prompt: newPrompt.trim() })
        rawTasks = [...rawTasks, t]
        showToast('Task created')
      }
      showCreate = false
    } catch (e: any) {
      showToast(e?.message ?? 'Failed to save task', 'error')
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

            <!-- Next run (last_run fallback) -->
            <span class="next-run">{nextRunLabel(task)}</span>

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

<!-- Create Task Modal -->
{#if showCreate}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal-backdrop" onclick={onBackdropClick}>
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
      <div class="modal-header">
        <span id="modal-title" class="modal-title">{editingName ? $t('tasks.modal_edit') : $t('tasks.modal_new')}</span>
        <button class="modal-close" onclick={() => (showCreate = false)} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>

      <div class="modal-body">
        <label class="field">
          <span class="field-label">{$t('tasks.field_name')} <span class="req">*</span></span>
          <input
            class="field-input"
            type="text"
            placeholder={$t('tasks.field_name_ph')}
            bind:value={newName}
            disabled={creating || editingName !== null}
          />
        </label>

        <label class="field">
          <span class="field-label">{$t('tasks.field_cron')} <span class="req">*</span></span>
          <input
            class="field-input mono"
            type="text"
            placeholder="0 9 * * *"
            bind:value={newCron}
            disabled={creating}
          />
          <span class="field-hint">{$t('tasks.field_cron_hint')}</span>
        </label>

        <label class="field">
          <span class="field-label">{$t('tasks.field_prompt')} <span class="req">*</span></span>
          <textarea
            class="field-textarea"
            rows="4"
            placeholder={$t('tasks.field_prompt_ph')}
            bind:value={newPrompt}
            disabled={creating}
          ></textarea>
        </label>
      </div>

      <div class="modal-footer">
        <button class="btn-secondary" onclick={() => (showCreate = false)} disabled={creating}>{$t('common.cancel')}</button>
        <button
          class="btn-primary"
          onclick={submitCreate}
          disabled={creating || !newName.trim() || !newCron.trim() || !newPrompt.trim()}
        >
          {creating ? $t('common.saving') : editingName ? $t('common.save') : $t('tasks.create')}
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
.btn-secondary {
  height: 32px; padding: 0 14px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer;
  font-family: inherit;
}
.btn-secondary:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── KPI row ─────────────────────────────────────────────────────────────────── */
.kpi-row { display: grid; grid-template-columns: repeat(3, minmax(0,1fr)); gap: 16px; }

/* ── table ───────────────────────────────────────────────────────────────────── */
.table-card {
  background: var(--bg-container); border-radius: 16px;
  box-shadow: var(--card-shadow); overflow: hidden;
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
.next-run { font-size: 13px; color: var(--text-secondary); }
.row-actions { display: flex; align-items: center; justify-content: flex-end; gap: 4px; }
.act-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.act-btn:hover { background: var(--hover-neutral); color: var(--blue-6); }
.act-btn.del:hover { color: var(--error); }

/* ── modal ───────────────────────────────────────────────────────────────────── */
.modal-backdrop {
  position: fixed; inset: 0; background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
  z-index: 200;
}
.modal {
  width: 480px; max-width: calc(100vw - 32px);
  background: var(--bg-container); border-radius: 16px;
  box-shadow: 0 24px 48px rgba(15,23,42,0.18);
  display: flex; flex-direction: column; overflow: hidden;
}
.modal-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 18px 24px 16px; border-bottom: 1px solid var(--border-table);
}
.modal-title { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.modal-close {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.modal-close:hover { background: var(--hover-neutral); color: var(--text); }
.modal-body { padding: 20px 24px; display: flex; flex-direction: column; gap: 16px; }
.modal-footer {
  padding: 16px 24px 20px;
  display: flex; align-items: center; justify-content: flex-end; gap: 8px;
  border-top: 1px solid var(--border-table);
}

/* ── form fields ─────────────────────────────────────────────────────────────── */
.field { display: flex; flex-direction: column; gap: 6px; }
.field-label { font-size: 13px; font-weight: 500; color: var(--text-secondary); }
.req { color: var(--error); }
.field-input, .field-textarea {
  font-family: inherit; font-size: 14px; color: var(--text);
  border: 1px solid var(--border); border-radius: 8px;
  padding: 7px 11px; outline: none; background: var(--bg-container);
  transition: border-color 0.15s;
}
.field-input:focus, .field-textarea:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(22,119,255,0.1); }
.field-input:disabled, .field-textarea:disabled { background: var(--bg-table-header); cursor: not-allowed; }
.field-textarea { resize: vertical; }
.field-hint { font-size: 12px; color: var(--text-tertiary); }
</style>
