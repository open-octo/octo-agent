<script lang="ts">
  import { onMount } from 'svelte'
  import { showToast } from '../lib/stores'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'

  interface TrashEntry {
    id: string
    original: string
    trash_path: string
    deleted_at: string
    project: string
    size: number
    orphan: boolean
  }

  let items       = $state<TrashEntry[]>([])
  let loading     = $state(true)
  let busyId      = $state<string | null>(null)
  let totalSize   = $state(0)
  let totalCount  = $state(0)
  let orphanCount = $state(0)

  onMount(async () => {
    await reload()
  })

  async function reload() {
    loading = true
    try {
      const data = await api.listTrash() as any
      // server returns { files: [...], total_count, total_size }
      items = data.files ?? data ?? []
      totalCount = data.total_count ?? items.length
      totalSize  = data.total_size  ?? items.reduce((s: number, e: TrashEntry) => s + (e.size ?? 0), 0)
      orphanCount = data.orphan_count ?? items.filter((e: TrashEntry) => e.orphan).length
    } catch (e: any) {
      showToast(`Failed to load trash: ${e.message}`, 'error')
    } finally {
      loading = false
    }
  }

  async function handleRestore(id: string) {
    busyId = id
    try {
      await api.restoreTrash(id)
      items = items.filter(i => i.id !== id)
      totalCount = Math.max(0, totalCount - 1)
      showToast(tr('files.toast_restored'), 'success')
    } catch (e: any) {
      showToast(`Restore failed: ${e.message}`, 'error')
    } finally {
      busyId = null
    }
  }

  async function handleDelete(id: string) {
    const entry = items.find(i => i.id === id)
    // Permanent delete — confirm first (the bulk actions already do), and name
    // the file so the user knows exactly what they're discarding.
    if (!confirm(`${tr('files.confirm_delete_one')}${entry ? `\n\n${basename(entry.original)}` : ''}`)) return
    busyId = id
    try {
      await api.deleteTrashItem(id)
      if (entry) totalSize = Math.max(0, totalSize - (entry.size ?? 0))
      items = items.filter(i => i.id !== id)
      totalCount = Math.max(0, totalCount - 1)
      showToast(tr('files.toast_deleted'), 'success')
    } catch (e: any) {
      showToast(`Delete failed: ${e.message}`, 'error')
    } finally {
      busyId = null
    }
  }

  async function handleEmptyAll() {
    if (!confirm(tr('files.confirm_empty_all').replace('{n}', String(totalCount)))) return
    try {
      await api.emptyTrash({ mode: 'all' })
      items = []
      totalCount = 0
      totalSize  = 0
      orphanCount = 0
      showToast(tr('files.toast_emptied'), 'success')
    } catch (e: any) {
      showToast(`Empty failed: ${e.message}`, 'error')
    }
  }

  async function handleEmptyOld() {
    try {
      await api.emptyTrash({ mode: 'old' })
      showToast(tr('files.toast_old_cleared'), 'success')
      await reload()
    } catch (e: any) {
      showToast(`Failed: ${e.message}`, 'error')
    }
  }

  async function handleCleanOrphans() {
    try {
      await api.emptyTrash({ mode: 'orphans' })
      showToast(tr('files.toast_orphans_cleared'), 'success')
      await reload()
    } catch (e: any) {
      showToast(`Failed: ${e.message}`, 'error')
    }
  }

  function fmtSize(bytes: number): string {
    if (bytes < 1024)        return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
  }

  function fmtAge(iso: string): string {
    try {
      const ms = Date.now() - new Date(iso).getTime()
      const days = Math.floor(ms / 86400000)
      if (days === 0) return tr('files.age_today')
      if (days === 1) return tr('files.age_yesterday')
      return tr('files.age_days_ago').replace('{n}', String(days))
    } catch { return iso }
  }

  function basename(path: string): string {
    return path.split('/').pop() ?? path
  }

  function iconFor(name: string): string {
    const ext = name.split('.').pop()?.toLowerCase() ?? ''
    if (['ts', 'tsx', 'js', 'jsx', 'go', 'py', 'rb', 'rs'].includes(ext)) return 'ant-design:code-outlined'
    if (['md', 'txt', 'log'].includes(ext))  return 'ant-design:file-text-outlined'
    if (['png', 'jpg', 'gif', 'svg'].includes(ext)) return 'ant-design:file-image-outlined'
    if (['zip', 'tar', 'gz'].includes(ext))  return 'ant-design:file-zip-outlined'
    return 'ant-design:file-outlined'
  }

  function isOld(iso: string): boolean {
    try {
      const ms = Date.now() - new Date(iso).getTime()
      return ms > 7 * 86400000
    } catch { return false }
  }
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <h2>{$t('files.title')}</h2>
      <p>{$t('files.subtitle')}</p>
    </div>

    <!-- Stats + actions -->
    <div class="stats-bar">
      <span class="stats-text">
        {$t(totalCount === 1 ? 'files.count_file' : 'files.count_files').replace('{n}', String(totalCount))}, {fmtSize(totalSize)}{#if orphanCount > 0} · {$t(orphanCount === 1 ? 'files.count_orphan' : 'files.count_orphans').replace('{n}', String(orphanCount))}{/if}
      </span>
      <div class="bar-actions">
        <button class="btn-outline" onclick={reload} disabled={loading}>
          <iconify-icon icon="ant-design:reload-outlined" width="13"></iconify-icon>
          {loading ? $t('common.loading') : $t('files.refresh')}
        </button>
        <button class="btn-outline" onclick={handleEmptyOld}>
          <iconify-icon icon="ant-design:clock-circle-outlined" width="13"></iconify-icon>
          {$t('files.empty_7d')}
        </button>
        <button class="btn-outline" onclick={handleCleanOrphans} disabled={orphanCount === 0} title={$t('files.clean_orphans_tip')}>
          <iconify-icon icon="ant-design:disconnect-outlined" width="13"></iconify-icon>
          {$t('files.clean_orphans')}
        </button>
        <button class="btn-danger-ghost" onclick={handleEmptyAll} disabled={items.length === 0}>
          <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
          {$t('files.empty_all')}
        </button>
      </div>
    </div>

    {#if loading}
      <div class="empty-state">{$t('common.loading')}</div>
    {:else if items.length === 0}
      <div class="empty-state">
        <iconify-icon icon="ant-design:delete-outlined" width="32" style="color:var(--text-quaternary)"></iconify-icon>
        <span>{$t('files.empty')}</span>
      </div>
    {:else}
      <div class="file-list">
        {#each items as f (f.id)}
          <div class="file-card">
            <span class="file-icon">
              <iconify-icon icon={iconFor(basename(f.original))} width="17"></iconify-icon>
            </span>
            <div class="file-info">
              <div class="file-name-row">
                <span class="file-name mono">{basename(f.original)}</span>
                {#if f.orphan}
                  <StatusTag status="error">{$t('files.orphan')}</StatusTag>
                {:else if isOld(f.deleted_at)}
                  <StatusTag status="warning">{$t('files.old')}</StatusTag>
                {/if}
              </div>
              <span class="file-path mono">{f.original}</span>
              <div class="file-meta">
                <iconify-icon icon="ant-design:folder-outlined" width="12"></iconify-icon>
                <span>{fmtSize(f.size)}</span>
                <span class="sep"></span>
                <span>{fmtAge(f.deleted_at)}</span>
                {#if f.project}
                  <span class="sep"></span>
                  <span>{f.project}</span>
                {/if}
              </div>
            </div>
            <div class="file-actions">
              <button
                class="btn-outline"
                disabled={busyId === f.id}
                onclick={() => handleRestore(f.id)}
              >
                <iconify-icon icon="ant-design:undo-outlined" width="14"></iconify-icon>
                {busyId === f.id ? '…' : $t('files.restore')}
              </button>
              <button
                class="icon-btn del"
                title={$t('files.delete_perm')}
                disabled={busyId === f.id}
                onclick={() => handleDelete(f.id)}
              >
                <iconify-icon icon="ant-design:delete-outlined" width="14"></iconify-icon>
              </button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }
.page-header { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p { margin: 0; font-size: 14px; color: var(--text-secondary); }
.stats-bar {
  background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow);
  padding: 14px 20px; display: flex; align-items: center; gap: 16px; flex-wrap: wrap;
}
.stats-text { font-size: 13px; color: var(--text-tertiary); flex: 1; min-width: 0; }
.bar-actions { display: flex; align-items: center; gap: 8px; }
.btn-outline {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container); border-radius: 6px;
  display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-outline:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-outline:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-danger-ghost {
  height: 30px; padding: 0 12px; border: 1px solid var(--border-secondary); background: var(--bg-container); border-radius: 6px;
  display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--text-tertiary);
  cursor: pointer; font-family: inherit;
}
.btn-danger-ghost:hover:not(:disabled) { border-color: var(--error); color: var(--error); }
.btn-danger-ghost:disabled { opacity: 0.4; cursor: not-allowed; }
.empty-state {
  padding: 60px; display: flex; flex-direction: column; align-items: center; gap: 12px;
  color: var(--text-tertiary); font-size: 14px;
}
.file-list { display: flex; flex-direction: column; gap: 12px; }
.file-card {
  background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow);
  padding: 16px 24px; display: flex; align-items: center; gap: 16px;
}
.file-icon {
  width: 36px; height: 36px; flex: 0 0 36px; border-radius: 10px;
  background: var(--bg-layout); color: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
}
.file-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.file-name-row { display: flex; align-items: center; gap: 8px; min-width: 0; }
.file-name { font-size: 15px; font-weight: 600; color: var(--text-heading); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-path { font-size: 12px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-meta { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--text-tertiary); padding-top: 1px; }
.sep { width: 3px; height: 3px; border-radius: 9999px; background: var(--text-quaternary); }
.file-actions { display: flex; align-items: center; gap: 8px; flex: 0 0 auto; }
.icon-btn {
  width: 30px; height: 30px; border: 1px solid var(--border-secondary); background: var(--bg-container); border-radius: 6px;
  display: flex; align-items: center; justify-content: center; cursor: pointer;
  color: var(--text-tertiary);
}
.icon-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.icon-btn.del:hover:not(:disabled) { border-color: var(--error); color: var(--error); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
