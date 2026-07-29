<script lang="ts">
  import { artifacts, panelContent, artifactSel, artifactModalOpen, showToast } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { copyArtifact, downloadArtifact } from '../lib/artifact-actions'

  const cur = $derived($artifacts[$artifactSel] ?? $artifacts[0])
  let modalEl = $state<HTMLDivElement | null>(null)

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
      <button class="icon-btn" title={$t('artifacts.copy')} onclick={onCopy}><iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon></button>
      <button class="icon-btn" title={$t('artifacts.download')} onclick={onDownload}><iconify-icon icon="ant-design:download-outlined" width="14"></iconify-icon></button>
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
        <div class="img-wrap"><img src={cur.src} alt={cur.name} /></div>
      {:else}
        <iframe srcdoc={cur.preview} sandbox="allow-scripts" allow="clipboard-write" title={cur.name}></iframe>
      {/if}
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: rgba(0,0,0,0.4);
  display: flex; align-items: center; justify-content: center;
  padding: 12px;
}
.modal {
  width: min(1400px, 96vw);
  height: 94vh;
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
  .backdrop { padding: 8px; }
  .modal {
    width: 100vw; height: 100vh; max-width: none; max-height: none;
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
.icon-btn:hover { background: var(--hover-neutral); color: var(--blue-6); }
.body { flex: 1; min-height: 0; background: var(--bg-container); }
iframe { border: 0; width: 100%; height: 100%; display: block; }
.img-wrap {
  width: 100%; height: 100%; box-sizing: border-box; padding: 12px;
  display: flex; align-items: center; justify-content: center;
  overflow: auto; background: var(--bg-layout);
}
.img-wrap img { max-width: 100%; max-height: 100%; object-fit: contain; }
</style>
