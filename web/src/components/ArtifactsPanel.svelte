<script lang="ts">
  import { artifacts, artifactsOpen, artifactSel, artifactView } from '../lib/stores'
  import type { ArtifactView } from '../lib/types'

  const icons: Record<string, string> = {
    'ant-design:file-markdown-outlined': 'ant-design:file-markdown-outlined',
    'ant-design:html5-outlined': 'ant-design:html5-outlined',
    'ant-design:file-text-outlined': 'ant-design:file-text-outlined',
  }

  const cur = $derived($artifacts[$artifactSel] ?? $artifacts[0])
</script>

<aside class="panel">
  <!-- Topbar -->
  <div class="topbar">
    <iconify-icon icon={cur.icon} width="15" style="color:#1677FF;flex:0 0 auto"></iconify-icon>
    <div class="file-info">
      <span class="file-name">{cur.name}</span>
      <span class="file-meta">{cur.type} · {cur.ver}</span>
    </div>
    <button class="icon-btn" title="Copy"><iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon></button>
    <button class="icon-btn" title="Download"><iconify-icon icon="ant-design:download-outlined" width="14"></iconify-icon></button>
    <button class="icon-btn" title="Open in new tab"><iconify-icon icon="ant-design:export-outlined" width="14"></iconify-icon></button>
    <button class="icon-btn" title="Close" onclick={() => artifactsOpen.set(false)}>
      <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
    </button>
  </div>

  <!-- Preview/Code toggle -->
  <div class="toolbar">
    <div class="seg">
      <button class="seg-btn" class:active={$artifactView === 'preview'} onclick={() => artifactView.set('preview')}>Preview</button>
      <button class="seg-btn" class:active={$artifactView === 'code'} onclick={() => artifactView.set('code')}>Code</button>
    </div>
    <span class="sandboxed-label">Sandboxed preview</span>
  </div>

  <!-- Body -->
  <div class="body">
    {#if $artifactView === 'preview'}
      <iframe srcdoc={cur.preview} sandbox="allow-scripts" title={cur.name}></iframe>
    {:else}
      <pre class="code-view">{cur.code}</pre>
    {/if}
  </div>

  <!-- Footer chip switcher -->
  <div class="footer">
    <span class="footer-lbl">Artifacts</span>
    {#each $artifacts as a, i}
    <button
      class="chip"
      class:active={i === $artifactSel}
      onclick={() => artifactSel.set(i)}
    >
      <iconify-icon icon={a.icon} width="13"></iconify-icon>
      {a.short}
    </button>
    {/each}
  </div>
</aside>

<style>
.panel {
  width: 420px; flex: 0 0 420px; background: #fff;
  border-left: 1px solid #EEEFF1; display: flex; flex-direction: column; min-height: 0;
}
.topbar {
  flex: 0 0 auto; padding: 8px 8px 8px 16px;
  border-bottom: 1px solid #EEEFF1; display: flex; align-items: center; gap: 6px;
}
.file-info { display: flex; flex-direction: column; min-width: 0; flex: 1; }
.file-name { font-size: 13px; font-weight: 600; color: #1F1F1F; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-meta { font-size: 11px; color: rgba(0,0,0,0.45); }
.icon-btn {
  width: 28px; height: 28px; flex: 0 0 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.45);
}
.icon-btn:hover { background: rgba(0,0,0,0.04); color: #1677FF; }
.toolbar {
  flex: 0 0 auto; padding: 8px 12px; border-bottom: 1px solid #F0F0F0;
  display: flex; align-items: center; gap: 8px;
}
.seg { display: inline-flex; padding: 2px; background: #F0F2F5; border-radius: 8px; gap: 2px; }
.seg-btn {
  height: 26px; padding: 0 14px; border: none; border-radius: 6px; font-size: 12px;
  cursor: pointer; background: transparent; color: rgba(0,0,0,0.65); font-family: inherit;
}
.seg-btn.active { background: #fff; color: #1677FF; }
.sandboxed-label { margin-left: auto; font-size: 11px; color: rgba(0,0,0,0.35); }
.body { flex: 1; min-height: 0; background: #fff; }
iframe { border: 0; width: 100%; height: 100%; display: block; }
.code-view {
  margin: 0; height: 100%; box-sizing: border-box; overflow: auto;
  padding: 14px 16px; background: #FBFBFB; font-size: 12px; line-height: 1.7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: rgba(0,0,0,0.8); white-space: pre;
}
.footer {
  flex: 0 0 auto; border-top: 1px solid #EEEFF1;
  padding: 8px 12px; display: flex; align-items: center; gap: 6px; overflow-x: auto;
}
.footer-lbl { font-size: 11px; color: rgba(0,0,0,0.45); flex: 0 0 auto; margin-right: 2px; }
.chip {
  height: 30px; padding: 0 10px; border: 1px solid #EEEFF1; background: #fff;
  color: rgba(0,0,0,0.65); border-radius: 6px; display: flex; align-items: center;
  gap: 6px; font-size: 12px; cursor: pointer; flex: 0 0 auto; font-family: inherit;
}
.chip.active { border-color: #1677FF; background: rgba(22,119,255,0.06); color: #1677FF; }
</style>
