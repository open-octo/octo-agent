<script lang="ts">
  import { workflows, showToast, openAgentSession } from '../lib/stores'
  import { t, tr } from '../lib/i18n'
  import { confirmDialog } from '../lib/confirm'
  import * as api from '../lib/api'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import Switch from '../components/ui/Switch.svelte'

  let loading = $state(true)
  let showSystem = $state(false)

  let filtered = $derived(
    $workflows.filter((wf) => showSystem || wf.source !== 'default')
  )

  $effect(() => {
    api.listWorkflowsView()
      .then(list => workflows.set(list))
      .catch(err => showToast(err.message, 'error'))
      .finally(() => { loading = false })
  })

  // Agentic-first: building a new workflow is a guided conversation with the
  // workflow-creator skill, which inventories existing skills/recordings and
  // chains them, then saves the result with workflow_save. Mirrors Skills'
  // "Create" → skill-creator.
  function handleCreate() {
    openAgentSession('/workflow-creator', 'New workflow')
  }

  // Run: matches the Composer's own /wf menu — plain instruction text, the
  // agent calls the workflow tool by name (agentic-first, no dedicated slash
  // trigger for a saved workflow).
  function handleRun(name: string) {
    openAgentSession(`Run the "${name}" workflow`, name)
  }

  // Editing a workflow's script is agent-assisted, same as skills: there is
  // no in-browser script editor, workflow-creator edits it and re-saves with
  // workflow_save.
  function handleEdit(name: string) {
    openAgentSession(`/workflow-creator Edit the existing "${name}" workflow.`, `Edit workflow: ${name}`)
  }

  async function handleDelete(name: string) {
    if (!(await confirmDialog(tr('workflows.confirm_delete').replace('{name}', name)))) return
    try {
      await api.deleteWorkflow(name)
      workflows.update(list => list.filter(w => w.name !== name))
      showToast(tr('workflows.toast_deleted').replace('{name}', name))
    } catch (err: any) {
      showToast(err.message, 'error')
    }
  }

  // Export downloads the raw .rb script. Fetching first (rather than an <a
  // download> straight at the endpoint) lets a non-2xx response surface its
  // actual error instead of silently doing nothing (#1109, same fix as Skills).
  async function handleExport(name: string) {
    try {
      const res = await fetch(`/api/workflows/${encodeURIComponent(name)}/export`)
      if (!res.ok) {
        throw new Error(await api.readErrorMessage(res, `${res.status} ${res.statusText}`))
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${name}.rb`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (e: any) {
      showToast(`Export failed: ${e.message}`, 'error')
    }
  }

  // ── View source modal ─────────────────────────────────────────────────────
  let sourceOpen = $state(false)
  let sourceLoading = $state(false)
  let sourceName = $state('')
  let sourceScript = $state('')

  async function handleViewSource(name: string) {
    sourceName = name
    sourceScript = ''
    sourceOpen = true
    sourceLoading = true
    try {
      const detail = await api.getWorkflow(name)
      sourceScript = detail.script
    } catch (err: any) {
      showToast(err.message, 'error')
      sourceOpen = false
    } finally {
      sourceLoading = false
    }
  }

  function closeSource() {
    sourceOpen = false
  }
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <div class="title-block">
        <h2>{$t('workflows.title')}</h2>
        <p>{$t('workflows.desc')}</p>
      </div>
      <div class="header-actions">
        <button class="btn-primary" onclick={handleCreate}>{$t('workflows.create')}</button>
      </div>
    </div>

    <div class="toolbar-row">
      <div></div>
      <div class="system-toggle">
        <Switch bind:checked={showSystem} />
        <span>{$t('workflows.show_system')}</span>
      </div>
    </div>

    <div class="table-card">
      <div class="table-header">
        <span>{$t('workflows.col_name')}</span>
        <span>{$t('workflows.col_description')}</span>
        <span>{$t('workflows.col_source')}</span>
        <span style="text-align:right">{$t('common.actions')}</span>
      </div>

      {#if loading}
        <div class="empty-state">
          <div class="spinner"></div>
          <span>{$t('workflows.loading')}</span>
        </div>
      {:else if filtered.length === 0}
        <div class="empty-state">
          <span>{$t('workflows.empty')}</span>
        </div>
      {:else}
        {#each filtered as wf (wf.name)}
          <div class="table-row">
            <div class="skill-name-cell">
              <span class="skill-icon">
                <iconify-icon icon={wf.icon} width="14"></iconify-icon>
              </span>
              <span class="mono name">{wf.name}</span>
            </div>
            <span class="desc">{wf.desc}</span>
            <span><StatusTag status={wf.tagStatus}>{wf.tagLabel}</StatusTag></span>
            <div class="row-actions">
              <button class="act-btn" title={$t('workflows.run')} onclick={() => handleRun(wf.name)}><iconify-icon icon="ant-design:play-circle-outlined" width="15"></iconify-icon></button>
              <button class="act-btn" title={$t('workflows.view_source')} onclick={() => handleViewSource(wf.name)}><iconify-icon icon="ant-design:code-outlined" width="15"></iconify-icon></button>
              <button class="act-btn" title={$t('workflows.edit_with_agent')} onclick={() => handleEdit(wf.name)}><iconify-icon icon="ant-design:edit-outlined" width="15"></iconify-icon></button>
              <button class="act-btn" title={$t('workflows.export_rb')} onclick={() => handleExport(wf.name)}><iconify-icon icon="ant-design:export-outlined" width="15"></iconify-icon></button>
              <button class="act-btn del" title={$t('common.delete')} disabled={wf.source === 'default'} onclick={() => handleDelete(wf.name)}>
                <iconify-icon icon="ant-design:delete-outlined" width="15"></iconify-icon>
              </button>
            </div>
          </div>
        {/each}
      {/if}
    </div>
  </div>
</div>

<!-- View-source modal: read-only, the script is edited via workflow-creator -->
{#if sourceOpen}
  <div class="modal-backdrop" role="presentation">
    <div class="modal" role="dialog" aria-modal="true" tabindex="-1" onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); closeSource() } }}>
      <div class="modal-header">
        <span class="modal-title mono">{sourceName}.rb</span>
        <button class="modal-close" onclick={closeSource} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>
      <div class="modal-body">
        {#if sourceLoading}
          <div class="empty-state"><div class="spinner"></div></div>
        {:else}
          <pre class="script-view">{sourceScript}</pre>
        {/if}
      </div>
      <div class="modal-footer">
        <button class="btn-secondary" onclick={closeSource}>{$t('common.close')}</button>
      </div>
    </div>
  </div>
{/if}

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }

/* ── view-source modal ────────────────────────────────────────────────────── */
.modal-backdrop {
  position: fixed; inset: 0; background: var(--text-tertiary);
  display: flex; align-items: flex-start; justify-content: center; z-index: 200;
  padding: 56px 16px;
}
.modal {
  width: 640px; max-width: 100%;
  background: var(--bg-container); border-radius: 16px; box-shadow: 0 24px 48px rgba(15,23,42,0.18);
  display: flex; flex-direction: column; overflow: hidden;
}
.modal-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 18px 24px 16px; border-bottom: 1px solid var(--border-table);
}
.modal-title { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.modal-close {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 6px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-tertiary);
}
.modal-close:hover { background: var(--hover-neutral); color: var(--text); }
.modal-body { padding: 20px 24px; max-height: 60vh; overflow-y: auto; }
.modal-footer {
  padding: 16px 24px 20px; display: flex; align-items: center; justify-content: flex-end; gap: 8px;
  border-top: 1px solid var(--border-table);
}
.script-view {
  margin: 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; line-height: 1.6;
  color: var(--text); white-space: pre-wrap; word-break: break-word;
}
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.title-block { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p { margin: 0; font-size: 14px; color: var(--text-secondary); }
.header-actions { display: flex; align-items: center; gap: 8px; }
.btn-primary { height: 32px; padding: 0 14px; border: none; background: var(--blue-6); border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit; }
.btn-primary:hover { background: var(--blue-5); }
.btn-secondary { height: 32px; padding: 0 14px; border: 1px solid var(--border); background: var(--bg-container); border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer; font-family: inherit; }
.btn-secondary:hover { border-color: var(--blue-5); color: var(--blue-5); }
.toolbar-row { display: flex; align-items: center; justify-content: space-between; }
.system-toggle { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text-secondary); }
.table-card { background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow); overflow-x: auto; }
.table-header, .table-row {
  display: grid;
  grid-template-columns: minmax(150px,2fr) minmax(120px,3fr) 96px 180px;
  column-gap: 12px; align-items: center; padding: 0 24px;
}
.table-header { height: 44px; background: var(--bg-table-header); font-size: 12px; font-weight: 600; color: var(--text-secondary); border-bottom: 1px solid var(--border-table); }
.table-row { padding: 12px 24px; border-bottom: 1px solid var(--border-table); background: var(--bg-container); min-width: 720px; }
.table-row:last-child { border-bottom: none; }
.table-row:hover { background: var(--active-blue-bg); }
.skill-name-cell { display: flex; align-items: center; gap: 10px; min-width: 0; }
.skill-icon {
  width: 28px; height: 28px; flex: 0 0 28px; border-radius: 9999px;
  background: var(--blue-1); color: var(--blue-6); display: flex; align-items: center; justify-content: center;
}
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.name { font-size: 14px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.desc { font-size: 13px; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding-right: 16px; }
.row-actions { display: flex; align-items: center; justify-content: flex-end; gap: 4px; }
.act-btn {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 6px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-tertiary);
}
.act-btn:hover { background: var(--hover-neutral); color: var(--blue-6); }
.act-btn.del:hover { color: var(--error); }
.act-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.act-btn:disabled:hover { background: transparent; color: var(--text-tertiary); }
.empty-state {
  display: flex; align-items: center; justify-content: center; gap: 10px;
  padding: 48px 24px; font-size: 14px; color: var(--text-tertiary);
}
.spinner {
  width: 18px; height: 18px; border: 2px solid rgba(22,119,255,0.2);
  border-top-color: var(--blue-6); border-radius: 50%;
  animation: spin 0.6s linear infinite; flex: 0 0 18px;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
