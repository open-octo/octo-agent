<script lang="ts">
  import { artifacts, artifactsOpen, artifactSel, artifactView, showToast } from '../lib/stores'
  import { t } from '../lib/i18n'

  const cur = $derived($artifacts[$artifactSel] ?? $artifacts[0])

  let maximized = $state(false)

  // #1109: had no .catch — on a non-secure context or a permission denial,
  // clipboard writes reject and the failure was invisible (no toast either
  // way, so it looked identical to success).
  function copyArtifact() {
    navigator.clipboard.writeText(cur?.code ?? '')
      .then(() => showToast('Copied to clipboard'))
      .catch(() => showToast('Copy failed — clipboard access denied', 'error'))
  }

  function downloadArtifact() {
    const blob = new Blob([cur?.code ?? ''], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = cur?.name || 'artifact.txt'
    a.click()
    URL.revokeObjectURL(url)
  }

</script>

<aside class="panel" class:maximized>
  {#if !cur}
    <!-- Open with nothing collected yet: explain instead of crashing on cur.* -->
    <div class="topbar">
      <span class="file-name">{$t('chat.artifacts')}</span>
      <span style="flex:1"></span>
      <button class="icon-btn" title={$t('common.close')} onclick={() => artifactsOpen.set(false)}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>
    <div class="empty">
      <iconify-icon icon="ant-design:file-text-outlined" width="28"></iconify-icon>
      <span>{$t('artifacts.empty')}</span>
    </div>
  {:else}
  <!-- Topbar -->
  <div class="topbar">
    <iconify-icon icon={cur.icon} width="15" style="color:var(--blue-6);flex:0 0 auto"></iconify-icon>
    <div class="file-info">
      <span class="file-name">{cur.name}</span>
      <span class="file-meta">{cur.type}</span>
    </div>
    <button class="icon-btn" title={$t('artifacts.copy')} onclick={copyArtifact}><iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon></button>
    <button class="icon-btn" title={$t('artifacts.download')} onclick={downloadArtifact}><iconify-icon icon="ant-design:download-outlined" width="14"></iconify-icon></button>
    <button class="icon-btn" title={maximized ? $t('artifacts.restore') : $t('artifacts.maximize')} onclick={() => maximized = !maximized}>
      <iconify-icon icon={maximized ? 'ant-design:compress-outlined' : 'ant-design:expand-outlined'} width="14"></iconify-icon>
    </button>
    <button class="icon-btn" title={$t('common.close')} onclick={() => artifactsOpen.set(false)}>
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
  {/if}
</aside>

<style>
.panel {
  width: 420px; flex: 0 0 420px; background: var(--bg-container);
  border-left: 1px solid var(--border-secondary); display: flex; flex-direction: column; min-height: 0;
}
.panel.maximized {
  position: fixed; right: 0; top: 0; bottom: 0;
  width: min(900px, 75vw); flex: none;
  z-index: 200;
  box-shadow: -4px 0 32px rgba(0,0,0,0.25);
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
</style>
