<script lang="ts">
  import { skills, showToast, openAgentSession } from '../lib/stores'
  import { t, tr } from '../lib/i18n'
  import * as api from '../lib/api'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import Switch from '../components/ui/Switch.svelte'

  let loading = $state(true)
  let showSystem = $state(false)
  let fileInput: HTMLInputElement

  let filtered = $derived(
    $skills.filter((sk) => showSystem || sk.source !== 'default')
  )

  $effect(() => {
    api.listSkills()
      .then(list => skills.set(list))
      .catch(err => showToast(err.message, 'error'))
      .finally(() => { loading = false })
  })

  async function handleToggle(name: string, currentEnabled: boolean) {
    const next = !currentEnabled
    try {
      await api.toggleSkill(name, next)
      skills.update(list => list.map(s => s.name === name ? { ...s, enabled: next } : s))
    } catch (err: any) {
      showToast(err.message, 'error')
    }
  }

  async function handleDelete(name: string) {
    if (!confirm(tr('skills.confirm_delete').replace('{name}', name))) return
    try {
      await api.deleteSkill(name)
      skills.update(list => list.filter(s => s.name !== name))
      showToast(tr('skills.toast_deleted').replace('{name}', name))
    } catch (err: any) {
      showToast(err.message, 'error')
    }
  }

  // Agentic-first: creating a skill opens a fresh chat that invokes the
  // skill-creator skill, which guides the user through it in conversation.
  function handleCreateSkill() {
    openAgentSession('/skill-creator', 'New skill')
  }

  // Use: open a fresh chat that invokes the skill (matches the old UI's per-card
  // "Use" — the slash command runs the skill in conversation).
  function handleUse(name: string) {
    openAgentSession(`/${name}`, name)
  }

  // Export downloads the skill folder as a .zip from the server.
  //
  // #1109: this used to point an <a download> straight at the endpoint —
  // if the server 404/500'd, the browser has no way to surface that to JS,
  // so a failing export produced zero download and zero feedback. Fetching
  // first lets a non-2xx response throw with the server's actual error
  // instead of silently doing nothing.
  async function handleExport(name: string) {
    try {
      const res = await fetch(`/api/skills/${encodeURIComponent(name)}/export`)
      if (!res.ok) {
        throw new Error(await api.readErrorMessage(res, `${res.status} ${res.statusText}`))
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${name}.zip`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (e: any) {
      showToast(`Export failed: ${e.message}`, 'error')
    }
  }

  // Editing a skill's files is agent-assisted: open a session that drives the
  // skill-creator skill at this skill. (There is no in-browser file editor; the
  // agent edits the skill on disk.)
  function handleEdit(name: string) {
    openAgentSession(`/skill-creator Edit the existing "${name}" skill.`, `Edit skill: ${name}`)
  }

  // ── Import (GitHub URL / owner-repo / local path / browsed upload) ───────────
  // The server import endpoint is JSON-only ({source, force}); a local file is
  // uploaded first via /api/upload, then its /api/uploads/<name> URL is used as
  // the source. Mirrors the old hand-written import bar + `octo skills add`.
  let importOpen = $state(false)
  let importSource = $state('')
  let importing = $state(false)
  let uploading = $state(false)

  function handleImportClick() {
    importSource = ''
    importOpen = true
  }

  function browseFile() {
    fileInput.value = ''
    fileInput.click()
  }

  async function handleFileChange(e: Event) {
    const file = (e.target as HTMLInputElement).files?.[0]
    if (!file) return
    uploading = true
    try {
      importSource = await api.uploadFile(file)
    } catch (err: any) {
      showToast(err.message, 'error')
    } finally {
      uploading = false
    }
  }

  async function doImport(force = false) {
    const source = importSource.trim()
    if (!source) return
    importing = true
    try {
      const r = await api.importSkill(source, force)
      if (r.conflict && !force) {
        if (confirm(tr('skills.import_confirm_replace'))) {
          importing = false
          return doImport(true)
        }
        return
      }
      if (!r.ok) {
        showToast(tr('skills.import_error') + (r.error ?? 'unknown'), 'error')
        return
      }
      skills.set(await api.listSkills())
      showToast(tr('skills.toast_imported').replace('{name}', r.name ?? ''))
      importOpen = false
    } catch (err: any) {
      showToast(tr('skills.import_error') + err.message, 'error')
    } finally {
      importing = false
    }
  }
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <div class="title-block">
        <h2>{$t('skills.title')}</h2>
        <p>{$t('skills.desc')}</p>
      </div>
      <div class="header-actions">
        <input
          bind:this={fileInput}
          type="file"
          accept=".zip,.md,.yaml,.yml"
          style="display:none"
          onchange={handleFileChange}
        />
        <button class="btn-secondary" onclick={handleImportClick}>{$t('skills.import')}</button>
        <button class="btn-primary" onclick={handleCreateSkill}>{$t('skills.create')}</button>
      </div>
    </div>

    <div class="toolbar-row">
      <div></div>
      <div class="system-toggle">
        <Switch bind:checked={showSystem} />
        <span>{$t('skills.show_system')}</span>
      </div>
    </div>

    <div class="table-card">
      <div class="table-header">
        <span>{$t('skills.col_skill')}</span>
        <span>{$t('skills.col_description')}</span>
        <span>{$t('skills.col_status')}</span>
        <span>{$t('skills.col_enabled')}</span>
        <span style="text-align:right">{$t('common.actions')}</span>
      </div>

      {#if loading}
        <div class="empty-state">
          <div class="spinner"></div>
          <span>{$t('skills.loading')}</span>
        </div>
      {:else if filtered.length === 0}
        <div class="empty-state">
          <span>{$t('skills.empty')}</span>
        </div>
      {:else}
        {#each filtered as sk (sk.name)}
          <div class="table-row">
            <div class="skill-name-cell">
              <span class="skill-icon">
                <iconify-icon icon={sk.icon} width="14"></iconify-icon>
              </span>
              <span class="mono name">{sk.name}</span>
            </div>
            <span class="desc">{sk.desc}</span>
            <span><StatusTag status={sk.tagStatus}>{sk.tagLabel}</StatusTag></span>
            <span>
              <Switch
                checked={sk.enabled}
                onchange={() => handleToggle(sk.name, sk.enabled)}
              />
            </span>
            <div class="row-actions">
              <button class="act-btn" title={$t('skills.use')} disabled={!sk.enabled} onclick={() => handleUse(sk.name)}><iconify-icon icon="ant-design:play-circle-outlined" width="15"></iconify-icon></button>
              <button class="act-btn" title={$t('skills.edit_with_agent')} onclick={() => handleEdit(sk.name)}><iconify-icon icon="ant-design:edit-outlined" width="15"></iconify-icon></button>
              <button class="act-btn" title={$t('skills.export_zip')} onclick={() => handleExport(sk.name)}><iconify-icon icon="ant-design:export-outlined" width="15"></iconify-icon></button>
              <button class="act-btn del" title={$t('common.delete')} onclick={() => handleDelete(sk.name)}>
                <iconify-icon icon="ant-design:delete-outlined" width="15"></iconify-icon>
              </button>
            </div>
          </div>
        {/each}
      {/if}
    </div>
  </div>
</div>

<!-- Import modal: GitHub URL / owner-repo / local path, or browse to upload -->
{#if importOpen}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal-backdrop" onclick={(e) => { if ((e.target as HTMLElement).classList.contains('modal-backdrop')) importOpen = false }}>
    <div class="modal" role="dialog" aria-modal="true">
      <div class="modal-header">
        <span class="modal-title">{$t('skills.import_title')}</span>
        <button class="modal-close" onclick={() => (importOpen = false)} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>
      <div class="modal-body">
        <div class="import-row">
          <input
            class="field-input mono"
            type="text"
            placeholder={$t('skills.import_placeholder')}
            bind:value={importSource}
            disabled={importing}
            onkeydown={(e) => { if (e.key === 'Enter') doImport() }}
          />
          <button class="btn-secondary" onclick={browseFile} disabled={importing || uploading}>
            {uploading ? $t('skills.import_uploading') : $t('skills.import_browse')}
          </button>
        </div>
        <span class="field-hint">{$t('skills.import_hint')}</span>
      </div>
      <div class="modal-footer">
        <button class="btn-secondary" onclick={() => (importOpen = false)} disabled={importing}>{$t('common.cancel')}</button>
        <button class="btn-primary" onclick={() => doImport()} disabled={importing || uploading || !importSource.trim()}>
          {importing ? $t('skills.import_installing') : $t('skills.import_install')}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }

/* ── import modal ─────────────────────────────────────────────────────────── */
.modal-backdrop {
  position: fixed; inset: 0; background: var(--text-tertiary);
  display: flex; align-items: flex-start; justify-content: center; z-index: 200;
  padding: 56px 16px;
}
.modal {
  width: 520px; max-width: 100%;
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
.modal-body { padding: 20px 24px; display: flex; flex-direction: column; gap: 8px; }
.modal-footer {
  padding: 16px 24px 20px; display: flex; align-items: center; justify-content: flex-end; gap: 8px;
  border-top: 1px solid var(--border-table);
}
.import-row { display: flex; gap: 8px; }
.field-input {
  flex: 1; min-width: 0; font-family: inherit; font-size: 14px; color: var(--text);
  border: 1px solid var(--border); border-radius: 8px; padding: 8px 11px; outline: none; background: var(--bg-container);
}
.field-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(22,119,255,0.1); }
.field-hint { font-size: 12px; color: var(--text-tertiary); }
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
.table-card { background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow); overflow: hidden; }
.table-header, .table-row {
  display: grid;
  grid-template-columns: minmax(150px,2.2fr) minmax(120px,3fr) 96px 72px 100px;
  column-gap: 12px; align-items: center; padding: 0 24px;
}
.table-header { height: 44px; background: var(--bg-table-header); font-size: 12px; font-weight: 600; color: var(--text-secondary); border-bottom: 1px solid var(--border-table); }
.table-row { padding: 12px 24px; border-bottom: 1px solid var(--border-table); background: var(--bg-container); min-width: 680px; }
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
