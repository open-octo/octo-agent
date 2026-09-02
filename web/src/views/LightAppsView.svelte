<script lang="ts">
  import { onMount } from 'svelte'
  import { showToast, openAgentSession, panelContent, lightapps, lightappSel, lightappOpen, lightappHTML, cacheLightApp } from '../lib/stores'
  import * as api from '../lib/api'
  import type { LightApp } from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { confirmDialog } from '../lib/confirm'

  let apps     = $state<LightApp[]>([])
  let loading  = $state(true)
  let busyId   = $state<string | null>(null)

  onMount(async () => { await load() })

  async function load() {
    loading = true
    try {
      apps = await api.listLightApps()
    } catch (e: any) {
      showToast(`Failed to load Light Apps: ${e.message}`, 'error')
    } finally {
      loading = false
    }
  }

  async function handleOpen(slug: string) {
    // Switch first, fetch second. Fetching the HTML before selecting left the
    // panel showing whatever was open before for the whole round trip, so a
    // click on Open looked like it did nothing — and on a heavier app, like it
    // had opened the wrong one. The tab appears and activates immediately; the
    // panel renders its own loading state until the HTML lands.
    lightappOpen.update(list => list.includes(slug) ? list : [...list, slug])
    lightappSel.set(slug)
    // Open it in the side panel at its regular width — many light apps are
    // laid out for a narrow column, and the panel's own maximize button is
    // there for the ones that want the full content area.
    panelContent.set('lightapps')
    if ($lightapps.length === 0) lightapps.set(apps)
    if ($lightappHTML[slug]) return
    try {
      cacheLightApp(slug, await api.getLightApp(slug))
    } catch (e: any) {
      showToast(`Failed to open: ${e.message}`, 'error')
    }
  }

  async function handleEdit(app: LightApp) {
    try {
      const detail = await api.getLightApp(app.slug)
      const contentPreview = detail.html.length > 3000
        ? detail.html.slice(0, 3000) + '\n…(truncated)'
        : detail.html
      const prompt = tr('lightapps.edit_prompt')
        .replace('{name}', app.name)
        .replace('{content}', contentPreview)
      await openAgentSession(prompt, tr('lightapps.session_edit').replace('{name}', app.name))
    } catch (e: any) {
      showToast(`Failed to load app: ${e.message}`, 'error')
    }
  }

  async function handleNew() {
    await openAgentSession($t('lightapps.new_prompt'), tr('lightapps.session_new'))
  }

  async function handleDelete(slug: string, name: string) {
    if (!(await confirmDialog(tr('lightapps.confirm_delete').replace('{name}', name)))) return
    busyId = slug
    try {
      await api.deleteLightApp(slug)
      apps = apps.filter(a => a.slug !== slug)
      showToast($t('lightapps.deleted'), 'success')
    } catch (e: any) {
      showToast(`Failed to delete: ${e.message}`, 'error')
    } finally {
      busyId = null
    }
  }
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <div class="header-row">
        <div>
          <h2>{$t('lightapps.title')}</h2>
          <p>{$t('lightapps.subtitle')}</p>
        </div>
        <button class="btn-primary" onclick={handleNew}>
          <iconify-icon icon="ant-design:plus-outlined" width="14"></iconify-icon>
          {$t('lightapps.new')}
        </button>
      </div>
    </div>

    {#if loading}
      <div class="empty-state">{$t('common.loading')}</div>
    {:else if apps.length === 0}
      <div class="empty-state">
        <iconify-icon icon="ant-design:appstore-outlined" width="32" style="color:var(--text-quaternary)"></iconify-icon>
        <span>{$t('lightapps.empty')}</span>
      </div>
    {:else}
      <div class="card-grid">
        {#each apps as app (app.slug)}
          <div class="app-card">
            <div class="card-icon">{app.icon || '📦'}</div>
            <div class="card-body">
              <div class="card-name">{app.name}</div>
              <div class="card-desc">{app.description}</div>
            </div>
            <div class="card-actions">
              <button class="btn-action" onclick={() => handleOpen(app.slug)}>
                <iconify-icon icon="ant-design:export-outlined" width="13"></iconify-icon>
                {$t('lightapps.open')}
              </button>
              <button class="btn-action" onclick={() => handleEdit(app)}>
                <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
                {$t('lightapps.edit')}
              </button>
              <button
                class="btn-action danger"
                disabled={busyId === app.slug}
                onclick={() => handleDelete(app.slug, app.name)}
              >
                <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
                {busyId === app.slug ? '…' : $t('lightapps.delete')}
              </button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; background: var(--bg-layout); }
.inner { max-width: 1000px; margin: 0 auto; padding: 26px 28px 40px; display: flex; flex-direction: column; gap: 20px; }
.page-header { display: flex; flex-direction: column; }
.header-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
h2 { margin: 0; font-size: 22px; font-weight: 700; letter-spacing: -0.01em; color: var(--text-heading); }
p { margin: 4px 0 0; font-size: 13px; color: var(--text-secondary); max-width: 60ch; }
.btn-primary {
  height: 32px; padding: 0 14px; border: none; border-radius: 8px;
  background: var(--blue-6); color: #fff; font-size: 13px; font-weight: 600;
  display: flex; align-items: center; gap: 6px;
  cursor: pointer; font-family: inherit; flex-shrink: 0;
  box-shadow: 0 1px 2px rgba(0,122,255,0.35);
}
.btn-primary:hover { background: var(--blue-5); }
.empty-state {
  padding: 60px; display: flex; flex-direction: column; align-items: center; gap: 12px;
  color: var(--text-tertiary); font-size: 14px;
}
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
}
.app-card {
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card); box-shadow: var(--card-shadow);
  padding: 24px; display: flex; flex-direction: column; gap: 16px;
  transition: box-shadow 0.15s;
}
.app-card:hover { box-shadow: 0 4px 16px rgba(0,0,0,0.08); }
.card-icon {
  font-size: 36px; line-height: 1;
}
.card-body { display: flex; flex-direction: column; gap: 4px; flex: 1; }
.card-name { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.card-desc {
  font-size: 13px; color: var(--text-tertiary);
  overflow: hidden; display: -webkit-box;
  -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical;
}
.card-actions {
  display: flex; gap: 6px; flex-wrap: wrap;
}
.btn-action {
  height: 28px; padding: 0 10px; border: 1px solid var(--border);
  background: var(--bg-container); border-radius: 8px;
  display: flex; align-items: center; gap: 4px;
  font-size: 12px; font-weight: 500; color: var(--text);
  cursor: pointer; font-family: inherit;
}
.btn-action:hover:not(:disabled) { background: var(--hover-neutral); border-color: var(--text-quaternary); }
.btn-action:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-action.danger:hover:not(:disabled) { background: var(--error-bg); border-color: var(--error-border); color: var(--error); }
</style>
