<script lang="ts">
  import { artifacts, panelContent, artifactSel, artifactModalOpen, showToast } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { copyArtifact, downloadArtifact, imagePreviewError } from '../lib/artifact-actions'
  import { ARTIFACT_SANDBOX, hydrateArtifact } from '../lib/artifacts'

  const cur = $derived($artifacts[$artifactSel] ?? $artifacts[0])
  let modalEl = $state<HTMLDivElement | null>(null)

  // Entries observe metadata-only; the body is fetched and the preview built
  // on first selection (see hydrateArtifact). Usually a no-op — the sidebar
  // hydrates before the maximize button is reachable — but the modal must not
  // depend on having passed through the sidebar.
  $effect(() => {
    if ($artifactModalOpen && cur && !cur.loaded) hydrateArtifact(cur)
  })

  // A failed image load would otherwise show only the browser's broken-image
  // glyph; surface the server's reason instead (e.g. the 10 MB preview cap).
  // The <img> stays mounted (hidden) while failed: an error event can dispatch
  // just after the selection switched and mark the wrong artifact, and only a
  // still-loading <img> can deliver the onload that clears that misfire.
  let imgFailed = $state(false)
  let imgFailDetail = $state('')
  $effect(() => {
    void cur?.src
    imgFailed = false
    imgFailDetail = ''
  })
  function onImgLoad() {
    imgFailed = false
    imgFailDetail = ''
  }
  async function onImgError() {
    const src = cur?.src
    if (!src) return
    imgFailed = true
    const detail = await imagePreviewError(src)
    if (cur?.src === src && imgFailed) imgFailDetail = detail
  }

  // Focus the modal on open so Esc closes it without first clicking the
  // backdrop (same pattern as ConfirmModal / ConfirmDialog).
  $effect(() => {
    if ($artifactModalOpen && cur && modalEl) modalEl.focus()
  })

  function onCopy() { copyArtifact(cur?.code ?? '', showToast) }
  function onDownload() { downloadArtifact(cur, showToast) }

  // "Back to sidebar": close modal, reopen the side panel.
  function restoreSidebar() {
    artifactModalOpen.set(false)
    panelContent.set('session')
  }

  function close() {
    artifactModalOpen.set(false)
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); close() }
  }
</script>

{#if $artifactModalOpen && cur}
<div class="backdrop" role="presentation" onclick={close}>
  <div class="modal" role="dialog" aria-modal="true" tabindex="-1" bind:this={modalEl}
       onkeydown={onKeydown} onclick={(e) => e.stopPropagation()}>
    <!-- Topbar — icon + filename + type + actions -->
    <div class="topbar">
      <iconify-icon icon={cur.icon} width="15" style="color:var(--blue-6);flex:0 0 auto"></iconify-icon>
      <div class="file-info">
        <span class="file-name">{cur.name}</span>
        <span class="file-meta">{cur.type}</span>
      </div>
      <button class="icon-btn" title={$t('artifacts.copy')} disabled={!cur.loaded || cur.loadFailed} onclick={onCopy}><iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon></button>
      <button class="icon-btn" title={$t('artifacts.download')} disabled={!cur.loaded || cur.loadFailed} onclick={onDownload}><iconify-icon icon="ant-design:download-outlined" width="14"></iconify-icon></button>
      <button class="icon-btn" title={$t('artifacts.restore')} onclick={restoreSidebar}>
        <iconify-icon icon="ant-design:compress-outlined" width="14"></iconify-icon>
      </button>
      <button class="icon-btn" title={$t('common.close')} onclick={close}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>

    <!-- Body — always preview, no toolbar / footer chrome -->
    <div class="body">
      {#if cur.src}
        <!-- Images render outside the sandboxed iframe (see lib/artifacts.ts). -->
        <div class="img-wrap">
          {#if imgFailed}
            <div class="img-error">
              <iconify-icon icon="ant-design:file-image-outlined" width="28"></iconify-icon>
              <span>{$t('artifacts.img_failed')}{imgFailDetail ? ` — ${imgFailDetail}` : ''}</span>
            </div>
          {/if}
          <img src={cur.src} alt={cur.name} class:img-hidden={imgFailed} onerror={onImgError} onload={onImgLoad} />
        </div>
      {:else if !cur.loaded}
        <div class="body-loading"><iconify-icon icon="ant-design:loading-outlined" width="28" class="spin"></iconify-icon></div>
      {:else}
        <iframe srcdoc={cur.preview} sandbox={ARTIFACT_SANDBOX} allow="clipboard-write" title={cur.name}></iframe>
      {/if}
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: var(--scrim);
  display: flex; align-items: center; justify-content: center;
  padding: 12px;
}
/* Percentages of the fixed inset:0 backdrop, NOT vw/vh: viewport units are
   not compensated for the root zoom the font-size setting applies, so 94vh
   under zoom 1.1 would overflow the screen. */
.modal {
  width: min(1400px, 96%);
  height: 94%;
  min-width: 320px;
  background: var(--bg-container);
  border: 1px solid var(--border-secondary);
  border-radius: 12px;
  display: flex; flex-direction: column; min-height: 0;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
  overflow: hidden;
}
/* Small desktop window / narrow viewport: fill the screen, drop the chrome */
@media (max-width: 900px), (max-height: 600px) {
  .backdrop { padding: 0; }
  .modal {
    width: 100%; height: 100%; max-width: none; max-height: none;
    border-radius: 0; border: none;
  }
}
.modal:focus { outline: none; }
.topbar {
  flex: 0 0 auto; padding: 10px 10px 10px 18px;
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
.icon-btn:hover:not(:disabled) { background: var(--hover-neutral); color: var(--blue-6); }
.icon-btn:disabled { opacity: 0.45; cursor: default; }
.body { flex: 1; min-height: 0; background: var(--bg-container); }
.body-loading { height: 100%; display: flex; align-items: center; justify-content: center; color: var(--text-tertiary); }
iframe { border: 0; width: 100%; height: 100%; display: block; }
.img-wrap {
  width: 100%; height: 100%; box-sizing: border-box; padding: 12px;
  display: flex; align-items: center; justify-content: center;
  overflow: auto; background: var(--bg-layout);
}
.img-wrap img { max-width: 100%; max-height: 100%; object-fit: contain; }
.img-wrap img.img-hidden { display: none; }
.img-error {
  display: flex; flex-direction: column; align-items: center; gap: 10px;
  max-width: 85%; text-align: center; color: var(--text-tertiary); font-size: 13px;
}
</style>
