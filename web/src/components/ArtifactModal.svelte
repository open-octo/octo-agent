<script lang="ts">
  import { artifacts, artifactsOpen, artifactSel, artifactView, artifactModalOpen, showToast } from '../lib/stores'
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
  function onDownload() { downloadArtifact(cur?.name, cur?.code ?? '', showToast) }

  // "Back to sidebar": close modal, reopen the side panel.
  function restoreSidebar() {
    artifactModalOpen.set(false)
    artifactsOpen.set(true)
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
    <!-- Topbar -->
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

    <!-- Preview/Code toggle -->
    <div class="toolbar">
      <div class="seg">
        <button class="seg-btn" class:active={$artifactView === 'preview'} onclick={() => artifactView.set('preview')}>{$t('artifacts.preview')}</button>
        <button class="seg-btn" class:active={$artifactView === 'code'} onclick={() => artifactView.set('code')}>{$t('artifacts.code')}</button>
      </div>
      <span class="sandboxed-label">{$t('artifacts.sandboxed')}</span>
    </div>

    <!-- Body -->
    <div class="body">
      {#if $artifactView === 'preview'}
        <iframe srcdoc={cur.preview} sandbox="allow-scripts clipboard-write" title={cur.name}></iframe>
      {:else}
        <pre class="code-view">{cur.code}</pre>
      {/if}
    </div>

    <!-- Footer chip switcher -->
    <div class="footer">
      <span class="footer-lbl">{$t('chat.artifacts')}</span>
      {#each $artifacts as a, i}
      <button
        class="chip"
        class:active={i === $artifactSel}
        title={a.path}
        onclick={() => artifactSel.set(i)}
      >
        <iconify-icon icon={a.icon} width="13"></iconify-icon>
        {a.short}
      </button>
      {/each}
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: rgba(0,0,0,0.4);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: min(1200px, 90vw);
  height: min(760px, 82vh);
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
.toolbar {
  flex: 0 0 auto; padding: 8px 14px; border-bottom: 1px solid var(--border-table);
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
iframe { border: 0; width: 100%; height: 100%; display: block; }
.code-view {
  margin: 0; height: 100%; box-sizing: border-box; overflow: auto;
  padding: 14px 16px; background: var(--bg-sidebar); font-size: 12px; line-height: 1.7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text); white-space: pre;
}
.footer {
  flex: 0 0 auto; border-top: 1px solid var(--border-secondary);
  padding: 8px 14px; display: flex; align-items: center; gap: 6px; overflow-x: auto;
}
.footer-lbl { font-size: 11px; color: var(--text-tertiary); flex: 0 0 auto; margin-right: 2px; }
.chip {
  height: 30px; padding: 0 10px; border: 1px solid var(--border-secondary); background: var(--bg-container);
  color: var(--text-secondary); border-radius: 6px; display: flex; align-items: center;
  gap: 6px; font-size: 12px; cursor: pointer; flex: 0 0 auto; font-family: inherit;
}
.chip.active { border-color: var(--blue-6); background: var(--active-blue-bg); color: var(--blue-6); }
</style>
