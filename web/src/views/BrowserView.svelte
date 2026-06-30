<script lang="ts">
  import { onMount } from 'svelte'
  import { showToast, openAgentSession } from '../lib/stores'
  import { t, tr } from '../lib/i18n'
  import * as api from '../lib/api'
  import type { BrowserRecording, BrowserStatus, BrowserVerifyResult } from '../lib/api'
  import BrowserSetupForm from '../components/settings/BrowserSetupForm.svelte'
  import StatusTag from '../components/ui/StatusTag.svelte'

  let recordings = $state<BrowserRecording[]>([])
  let loading = $state(false)

  // Connection status (moved here from Settings — the Browser view owns it now).
  let browser     = $state<BrowserStatus | null>(null)
  let browserBusy = $state(false)
  let setupOpen   = $state(false)

  async function loadBrowserStatus() {
    try {
      browser = await api.getBrowserStatus()
    } catch { /* leave as null */ }
  }

  async function recheckBrowser() {
    browserBusy = true
    try {
      await loadBrowserStatus()
    } finally {
      browserBusy = false
    }
  }

  async function onSetupVerified(res: BrowserVerifyResult) {
    showToast(tr('settings.browser.verify_ok') + (res.detail ? ` (${res.detail})` : ''), 'success')
    setupOpen = false
    await loadBrowserStatus()
  }

  // Edit modal state.
  let editOpen = $state(false)
  let editName = $state('')
  let editYAML = $state('')
  let saving = $state(false)

  async function reload() {
    loading = true
    try {
      recordings = await api.listBrowserRecordings()
    } catch (e: any) {
      showToast(e?.message ?? tr('browser.rec.load_fail'), 'error')
    } finally {
      loading = false
    }
  }

  onMount(() => {
    reload()
    loadBrowserStatus()
  })

  async function openEdit(name: string) {
    try {
      const r = await api.getBrowserRecording(name)
      editName = r.name
      editYAML = r.yaml
      editOpen = true
    } catch (e: any) {
      showToast(e?.message ?? tr('browser.rec.load_fail'), 'error')
    }
  }

  async function save() {
    saving = true
    try {
      await api.saveBrowserRecording(editName, editYAML)
      showToast(tr('browser.rec.saved'), 'success')
      editOpen = false
      await reload()
    } catch (e: any) {
      showToast(e?.message ?? tr('browser.rec.save_fail'), 'error')
    } finally {
      saving = false
    }
  }

  async function del(name: string) {
    if (!confirm(tr('browser.rec.delete_confirm').replace('{name}', name))) return
    try {
      await api.deleteBrowserRecording(name)
      showToast(tr('browser.rec.deleted'), 'success')
      await reload()
    } catch (e: any) {
      showToast(e?.message ?? tr('browser.rec.delete_fail'), 'error')
    }
  }

  // Replay reuses the full agent path (run_skill + self-heal): open a session
  // and let the model drive it, rather than a server-side replay endpoint.
  function run(name: string) {
    openAgentSession(tr('browser.rec.run_prompt').replace('{name}', name), '▶ ' + name).catch(() => {})
  }

  // Start a recording: open a fresh session whose first message kicks off the
  // record flow (record_start → hand control to the user → record_stop).
  function record() {
    openAgentSession(tr('browser.rec.record_prompt'), '● ' + tr('browser.rec.record')).catch(() => {})
  }
</script>

<div class="view">
  <div class="view-head">
    <h1>{$t('nav.browser')}</h1>
    <p class="sub">{$t('browser.view.sub')}</p>
  </div>

  <!-- Connection -->
  <section class="card">
    <div class="sec-head">
      <h2 class="sec-title">{$t('browser.view.connect_title')}</h2>
      {#if browser && !browser.configured}
        <button class="ghost-btn" onclick={() => (setupOpen = true)}>
          <iconify-icon icon="ant-design:setting-outlined" width="13"></iconify-icon>
          {$t('settings.browser.setup')}
        </button>
      {:else if browser}
        <div class="conn-actions">
          <button class="ghost-btn" disabled={browserBusy} onclick={recheckBrowser}>{$t('settings.browser.recheck')}</button>
          <button class="ghost-btn" onclick={() => (setupOpen = true)}>{$t('settings.browser.reconfigure')}</button>
        </div>
      {/if}
    </div>
    <div class="conn-row">
      <div class="conn-info">
        <span class="conn-label">{$t('settings.browser.status')}</span>
        <span class="conn-desc">{$t('settings.browser.desc')}</span>
      </div>
      <div class="conn-status">
        {#if !browser}
          <span class="muted">{$t('common.loading')}</span>
        {:else if browser.connected}
          <StatusTag status="success">{$t('settings.browser.connected')}</StatusTag>
        {:else if browser.configured}
          <StatusTag status="warning">{$t('settings.browser.unreachable')}</StatusTag>
        {:else}
          <StatusTag status="default">{$t('settings.browser.not_setup')}</StatusTag>
        {/if}
        {#if browser?.configured}
          <span class="muted mono">{$t('settings.browser.port')} {browser.port}</span>
        {/if}
      </div>
    </div>
    {#if browser && !browser.chrome_available}
      <div class="conn-note">
        <iconify-icon icon="ant-design:warning-outlined" width="14"></iconify-icon>
        {$t('settings.browser.no_chrome')}
      </div>
    {/if}
  </section>

  <!-- Recordings -->
  <section class="card">
    <div class="sec-head">
      <h2 class="sec-title">{$t('browser.view.recordings_title')}</h2>
      <div class="rec-head-actions">
        <button class="primary-btn" onclick={record}>
          <iconify-icon icon="ant-design:video-camera-outlined" width="13"></iconify-icon>
          {$t('browser.rec.record')}
        </button>
        <button class="ghost-btn" onclick={reload} aria-label={$t('common.refresh')}>
          <iconify-icon icon="ant-design:reload-outlined" width="14"></iconify-icon>
        </button>
      </div>
    </div>
    <p class="hint">{$t('browser.rec.how')}</p>

    {#if loading}
      <p class="muted">{$t('common.loading')}</p>
    {:else if recordings.length === 0}
      <p class="muted">{$t('browser.rec.empty')}</p>
    {:else}
      <ul class="rec-list">
        {#each recordings as r (r.name)}
          <li class="rec">
            <div class="rec-main">
              <div class="rec-name">{r.name}</div>
              {#if r.description}<div class="rec-desc">{r.description}</div>{/if}
              <div class="rec-meta">
                <span>{r.steps} {$t('browser.rec.steps')}</span>
                {#if r.params && r.params.length}<span>· {r.params.join(', ')}</span>{/if}
              </div>
            </div>
            <div class="rec-actions">
              <button class="ghost-btn" onclick={() => run(r.name)}>{$t('browser.rec.run')}</button>
              <button class="ghost-btn" onclick={() => openEdit(r.name)}>{$t('common.edit')}</button>
              <button class="ghost-btn danger" onclick={() => del(r.name)}>{$t('common.delete')}</button>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</div>

{#if setupOpen}
  <div class="modal-overlay" onclick={() => (setupOpen = false)} role="presentation">
    <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" tabindex="-1">
      <div class="modal-header">
        <span class="modal-title">{$t('settings.browser.modal_title')}</span>
        <button class="modal-close" onclick={() => (setupOpen = false)} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>
      <div class="modal-body">
        <BrowserSetupForm
          secondaryLabel={$t('common.cancel')}
          onSecondary={() => (setupOpen = false)}
          onVerified={onSetupVerified}
        />
      </div>
    </div>
  </div>
{/if}

{#if editOpen}
  <div class="modal-overlay" onclick={() => (editOpen = false)} role="presentation">
    <div class="modal lg" onclick={(e) => e.stopPropagation()} role="dialog" tabindex="-1">
      <div class="modal-header">
        <span class="modal-title">{editName}</span>
        <button class="modal-close" onclick={() => (editOpen = false)} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>
      <div class="modal-body">
        <textarea class="yaml" bind:value={editYAML} spellcheck="false"></textarea>
      </div>
      <div class="modal-foot">
        <button class="ghost-btn" onclick={() => (editOpen = false)}>{$t('common.cancel')}</button>
        <button class="primary-btn" disabled={saving} onclick={save}>
          {saving ? $t('common.saving') : $t('common.save')}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .view { padding: 24px; max-width: 880px; margin: 0 auto; overflow-y: auto; }
  .view-head h1 { margin: 0; font-size: 20px; }
  .sub { color: var(--text-muted); margin: 4px 0 20px; font-size: 13px; }
  .card { background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 18px; margin-bottom: 18px; }
  .sec-head { display: flex; align-items: center; justify-content: space-between; }
  .sec-title { margin: 0 0 12px; font-size: 15px; }
  .conn-actions { display: flex; gap: 6px; }
  .conn-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .conn-info { display: flex; flex-direction: column; gap: 2px; }
  .conn-label { font-size: 14px; color: var(--text); }
  .conn-desc { font-size: 12px; color: var(--text-muted); }
  .conn-status { display: flex; align-items: center; gap: 10px; flex: 0 0 auto; flex-wrap: wrap; justify-content: flex-end; }
  .conn-note { display: flex; align-items: center; gap: 8px; margin-top: 12px; padding: 10px 12px; border-radius: 8px; font-size: 12px; color: var(--warning); background: var(--warning-bg); }
  .mono { font-family: ui-monospace, monospace; }
  .hint { color: var(--text-muted); font-size: 12px; margin: 0 0 14px; }
  .muted { color: var(--text-muted); font-size: 13px; }
  .rec-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .rec { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 14px; border: 1px solid var(--border); border-radius: 8px; }
  .rec-name { font-weight: 600; font-size: 14px; }
  .rec-desc { color: var(--text-muted); font-size: 12px; margin-top: 2px; }
  .rec-meta { color: var(--text-muted); font-size: 11px; margin-top: 4px; }
  .rec-actions { display: flex; gap: 6px; flex-shrink: 0; }
  .ghost-btn { background: transparent; border: 1px solid var(--border); border-radius: 6px; padding: 5px 10px; font-size: 12px; cursor: pointer; color: var(--text); }
  .ghost-btn:hover { background: var(--surface-hover); }
  .ghost-btn.danger { color: var(--danger, #d4380d); }
  .rec-head-actions { display: flex; align-items: center; gap: 8px; }
  .primary-btn { display: inline-flex; align-items: center; gap: 5px; background: var(--accent); color: #fff; border: none; border-radius: 6px; padding: 6px 14px; font-size: 13px; cursor: pointer; }
  .primary-btn:disabled { opacity: 0.6; cursor: default; }
  .yaml { width: 100%; min-height: 360px; font-family: ui-monospace, monospace; font-size: 12px; line-height: 1.5; border: 1px solid var(--border); border-radius: 6px; padding: 10px; background: var(--bg); color: var(--text); resize: vertical; box-sizing: border-box; }
  .modal-foot { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 16px; border-top: 1px solid var(--border); }
  .modal.lg { width: min(720px, 92vw); }
</style>
