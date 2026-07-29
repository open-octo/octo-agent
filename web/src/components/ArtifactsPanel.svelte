<script lang="ts">
  import { artifacts, panelContent, artifactSel, artifactView, artifactModalOpen, lightappSel, lightapps, lightappHTML, showToast } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { copyArtifact, downloadArtifact } from '../lib/artifact-actions'
  import * as api from '../lib/api'

  // ── Session artifacts (existing) ──────────────────────────────────────────
  const cur = $derived($artifacts[$artifactSel] ?? $artifacts[0])
  // Images render outside the sandboxed iframe and have no source view.
  const curIsImage = $derived(!!cur?.src)

  function onCopy() { copyArtifact(cur?.code ?? '', showToast) }
  function onDownload() { downloadArtifact(cur, showToast) }

  // ── Save to Light App (HTML artifacts only) ─────────────────────────────
  let saveToLAName = $state('')
  let saveToLADialog = $state(false)
  let saveToLALoading = $state(false)

  function openSaveToLA() {
    saveToLAName = cur?.name?.replace(/\.[^.]+$/, '') ?? ''
    saveToLADialog = true
  }

  async function doSaveToLA() {
    const name = saveToLAName.trim()
    if (!name || !cur) return
    saveToLALoading = true
    try {
      const app = await api.createLightApp({ name, html: cur.code })
      showToast(`Light App "${app.name}" saved`, 'success')
      saveToLADialog = false
      // If the panel is in lightapps mode, refresh the list.
      if ($panelContent === 'lightapps') {
        try {
          const list = await api.listLightApps()
          lightapps.set(list)
        } catch { /* ignore */ }
      }
    } catch (e: any) {
      showToast(`Save failed: ${e.message}`, 'error')
    } finally {
      saveToLALoading = false
    }
  }

  const curIsHTML = $derived(cur?.type === 'HTML')

  // ── Light Apps (new) ──────────────────────────────────────────────────────
  let laLoading = $state(false)
  let laAttempted = $state(false)

  async function loadLightApps() {
    if ($lightapps.length > 0) return
    laLoading = true
    try {
      const list = await api.listLightApps()
      lightapps.set(list)
    } catch { /* empty list */ }
    finally {
      laLoading = false
      laAttempted = true
    }
  }

  async function selectLightApp(slug: string) {
    lightappSel.set(slug)
    if ($lightappHTML[slug]) return
    laLoading = true
    try {
      const detail = await api.getLightApp(slug)
      lightappHTML.update(m => ({ ...m, [slug]: detail.html }))
    } catch { /* ignore */ }
    finally { laLoading = false }
  }

  // Load light apps list once when the panel opens in lightapps mode.
  $effect(() => {
    if ($panelContent === 'lightapps' && $lightapps.length === 0 && !laAttempted) loadLightApps()
    if ($panelContent !== 'lightapps') laAttempted = false
  })

  function closePanel() { panelContent.set(null) }

  // Derive the current light app's HTML preview.
  const laCurSlug = $derived($lightappSel || $lightapps[0]?.slug || '')
  const laCurHTML = $derived($lightappHTML[laCurSlug] ?? '')
  const laCurName = $derived($lightapps.find(a => a.slug === laCurSlug)?.name ?? laCurSlug)
</script>

<aside class="panel">
  {#if $panelContent === 'lightapps'}
    <!-- ── Light Apps mode ───────────────────────────────────────────────── -->
    <div class="topbar">
      <span class="file-name">{$t('artifacts.light_apps')}</span>
      <span style="flex:1"></span>
      <button class="icon-btn" title={$t('common.close')} onclick={closePanel}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>

    <div class="toolbar">
      <span class="sandboxed-label">{$t('artifacts.sandboxed')}</span>
    </div>

    <div class="body">
      {#if laLoading}
        <div class="empty"><iconify-icon icon="ant-design:loading-outlined" width="28" class="spin"></iconify-icon><span>{$t('common.loading')}</span></div>
      {:else if laCurHTML}
        <iframe srcdoc={laCurHTML} sandbox="allow-scripts clipboard-write" title={laCurName}></iframe>
      {:else if $lightapps.length === 0}
        <div class="empty">
          <iconify-icon icon="ant-design:appstore-outlined" width="28"></iconify-icon>
          <span>{$t('lightapps.empty')}</span>
        </div>
      {:else}
        <div class="empty"><span>Select a Light App below</span></div>
      {/if}
    </div>

    <!-- Footer: Light Apps chip switcher -->
    <div class="footer">
      <span class="footer-lbl">{$t('artifacts.light_apps')}</span>
      {#each $lightapps as a}
      <button class="chip" class:active={a.slug === laCurSlug} title={a.name} onclick={() => selectLightApp(a.slug)}>
        <span>{a.icon || '📦'}</span>
        {a.name}
      </button>
      {/each}
    </div>

  {:else if $panelContent === 'session'}
    <!-- ── Session mode (existing behavior) ────────────────────────────────── -->
    {#if !cur}
      <div class="topbar">
        <span class="file-name">{$t('chat.artifacts')}</span>
        <span style="flex:1"></span>
        <button class="icon-btn" title={$t('common.close')} onclick={closePanel}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>
      <div class="empty">
        <iconify-icon icon="ant-design:file-text-outlined" width="28"></iconify-icon>
        <span>{$t('artifacts.empty')}</span>
      </div>
    {:else}
      <div class="topbar">
        <iconify-icon icon={cur.icon} width="15" style="color:var(--blue-6);flex:0 0 auto"></iconify-icon>
        <div class="file-info">
          <span class="file-name">{cur.name}</span>
          <span class="file-meta">{cur.type}</span>
        </div>
        <button class="icon-btn" title={$t('artifacts.copy')} onclick={onCopy}><iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon></button>
        <button class="icon-btn" title={$t('artifacts.download')} onclick={onDownload}><iconify-icon icon="ant-design:download-outlined" width="14"></iconify-icon></button>
        {#if curIsHTML}
          <button class="icon-btn" title={$t('artifacts.save_to_lightapp')} onclick={openSaveToLA}>
            <iconify-icon icon="ant-design:save-outlined" width="14"></iconify-icon>
          </button>
        {/if}
        <button class="icon-btn" title={$t('artifacts.maximize')} onclick={() => { panelContent.set(null); artifactModalOpen.set(true) }}>
          <iconify-icon icon="ant-design:expand-outlined" width="14"></iconify-icon>
        </button>
        <button class="icon-btn" title={$t('common.close')} onclick={closePanel}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>

      {#if saveToLADialog}
        <div class="save-to-la-bar">
          <input
            class="save-to-la-input"
            type="text"
            bind:value={saveToLAName}
            placeholder={$t('artifacts.save_to_lightapp_placeholder')}
            onkeydown={(e) => { if (e.key === 'Enter') doSaveToLA(); if (e.key === 'Escape') saveToLADialog = false }}
          />
          <button class="btn-action" disabled={saveToLALoading || !saveToLAName.trim()} onclick={doSaveToLA}>
            {saveToLALoading ? '…' : $t('common.save')}
          </button>
          <button class="btn-action" onclick={() => saveToLADialog = false}>{$t('common.cancel')}</button>
        </div>
      {/if}

      {#if !curIsImage}
        <div class="toolbar">
          <div class="seg">
            <button class="seg-btn" class:active={$artifactView === 'preview'} onclick={() => artifactView.set('preview')}>{$t('artifacts.preview')}</button>
            <button class="seg-btn" class:active={$artifactView === 'code'} onclick={() => artifactView.set('code')}>{$t('artifacts.code')}</button>
          </div>
          <span class="sandboxed-label">{$t('artifacts.sandboxed')}</span>
        </div>
      {/if}

      <div class="body">
        {#if curIsImage}
          <div class="img-wrap"><img src={cur.src} alt={cur.name} /></div>
        {:else if $artifactView === 'preview'}
          <iframe srcdoc={cur.preview} sandbox="allow-scripts clipboard-write" title={cur.name}></iframe>
        {:else}
          <pre class="code-view">{cur.code}</pre>
        {/if}
      </div>

      <div class="footer">
        <span class="footer-lbl">{$t('chat.artifacts')}</span>
        {#each $artifacts as a, i}
        <button class="chip" class:active={i === $artifactSel} title={a.path} onclick={() => artifactSel.set(i)}>
          <iconify-icon icon={a.icon} width="13"></iconify-icon>
          {a.short}
        </button>
        {/each}
      </div>
    {/if}
  {/if}
</aside>

<style>
.panel {
  width: 420px; flex: 0 0 420px; background: var(--bg-container);
  border-left: 1px solid var(--border-secondary); display: flex; flex-direction: column; min-height: 0;
}
.topbar {
  flex: 0 0 auto; padding: 8px 8px 8px 16px;
  border-bottom: 1px solid var(--border-secondary); display: flex; align-items: center; gap: 6px;
}
.file-info { display: flex; flex-direction: column; min-width: 0; flex: 1; }
.file-name { font-size: 13px; font-weight: 600; color: var(--text-heading); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-meta { font-size: 11px; color: var(--text-tertiary); }
.icon-btn {
  width: 28px; height: 28px; flex: 0 0 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.icon-btn:hover { background: var(--hover-neutral); color: var(--blue-6); }
.toolbar {
  flex: 0 0 auto; padding: 8px 12px; border-bottom: 1px solid var(--border-table);
  display: flex; align-items: center; gap: 8px;
}
.seg { display: inline-flex; padding: 2px; background: var(--control-track); border-radius: 8px; gap: 2px; }
.seg-btn {
  height: 26px; padding: 0 14px; border: none; border-radius: 6px; font-size: 12px;
  cursor: pointer; background: transparent; color: var(--text-secondary); font-family: inherit;
}
.seg-btn.active { background: var(--bg-container); color: var(--blue-6); }
.sandboxed-label { margin-left: auto; font-size: 11px; color: var(--text-tertiary); }
.body { flex: 1; min-height: 0; background: var(--bg-container); }
.empty {
  flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 12px; padding: 32px; text-align: center; color: var(--text-tertiary); font-size: 13px;
}
iframe { border: 0; width: 100%; height: 100%; display: block; }
.img-wrap {
  width: 100%; height: 100%; box-sizing: border-box; padding: 8px;
  display: flex; align-items: center; justify-content: center;
  overflow: auto; background: var(--bg-layout);
}
.img-wrap img { max-width: 100%; max-height: 100%; object-fit: contain; }
.code-view {
  margin: 0; height: 100%; box-sizing: border-box; overflow: auto;
  padding: 14px 16px; background: var(--bg-sidebar); font-size: 12px; line-height: 1.7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text); white-space: pre;
}
.footer {
  flex: 0 0 auto; border-top: 1px solid var(--border-secondary);
  padding: 8px 12px; display: flex; align-items: center; gap: 6px; overflow-x: auto;
}
.footer-lbl { font-size: 11px; color: var(--text-tertiary); flex: 0 0 auto; margin-right: 2px; }
.chip {
  height: 30px; padding: 0 10px; border: 1px solid var(--border-secondary); background: var(--bg-container);
  color: var(--text-secondary); border-radius: 6px; display: flex; align-items: center;
  gap: 6px; font-size: 12px; cursor: pointer; flex: 0 0 auto; font-family: inherit;
}
.chip.active { border-color: var(--blue-6); background: var(--active-blue-bg); color: var(--blue-6); }
.spin { animation: octo-spin 0.8s linear infinite; }

/* ── Save-to-Light-App inline form ──────────────────────────────────── */
.save-to-la-bar {
  flex: 0 0 auto; padding: 6px 10px;
  border-bottom: 1px solid var(--border-secondary);
  display: flex; align-items: center; gap: 6px;
}
.save-to-la-input {
  flex: 1; height: 28px; padding: 0 8px;
  border: 1px solid var(--border-secondary);
  border-radius: 6px; font-size: 12px; font-family: inherit;
  background: var(--bg-container); color: var(--text);
  outline: none;
}
.save-to-la-input:focus { border-color: var(--blue-5); }
.btn-action {
  height: 28px; padding: 0 12px; border: none;
  border-radius: 6px; font-size: 12px; cursor: pointer;
  background: var(--blue-6); color: #fff; font-family: inherit;
}
.btn-action:hover:not(:disabled) { background: var(--blue-5); }
.btn-action:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
