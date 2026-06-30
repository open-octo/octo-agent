<script lang="ts">
  import { onMount } from 'svelte'
  import { showToast, openAgentSession } from '../lib/stores'
  import { t, tr } from '../lib/i18n'
  import * as api from '../lib/api'
  import type { BrowserRecording } from '../lib/api'
  import BrowserSetupForm from '../components/settings/BrowserSetupForm.svelte'

  let recordings = $state<BrowserRecording[]>([])
  let loading = $state(false)

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

  onMount(reload)

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
</script>

<div class="view">
  <div class="view-head">
    <h1>{$t('nav.browser')}</h1>
    <p class="sub">{$t('browser.view.sub')}</p>
  </div>

  <!-- Connection setup -->
  <section class="card">
    <h2 class="sec-title">{$t('browser.view.connect_title')}</h2>
    <BrowserSetupForm onVerified={() => showToast(tr('browser.connected'), 'success')} />
  </section>

  <!-- Recordings -->
  <section class="card">
    <div class="sec-head">
      <h2 class="sec-title">{$t('browser.view.recordings_title')}</h2>
      <button class="ghost-btn" onclick={reload} aria-label={$t('common.refresh')}>
        <iconify-icon icon="ant-design:reload-outlined" width="14"></iconify-icon>
      </button>
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
  .primary-btn { background: var(--accent); color: #fff; border: none; border-radius: 6px; padding: 6px 14px; font-size: 13px; cursor: pointer; }
  .primary-btn:disabled { opacity: 0.6; cursor: default; }
  .yaml { width: 100%; min-height: 360px; font-family: ui-monospace, monospace; font-size: 12px; line-height: 1.5; border: 1px solid var(--border); border-radius: 6px; padding: 10px; background: var(--bg); color: var(--text); resize: vertical; box-sizing: border-box; }
  .modal-foot { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 16px; border-top: 1px solid var(--border); }
  .modal.lg { width: min(720px, 92vw); }
</style>
