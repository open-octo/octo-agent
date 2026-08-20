<script lang="ts">
  import { artifacts, panelContent, panelExpanded, artifactSel, artifactView, lightappSel, lightapps, lightappHTML, showToast } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { copyArtifact, downloadArtifact, imagePreviewError } from '../lib/artifact-actions'
  import { hydrateArtifact, lightAppSource, pathIsInside } from '../lib/artifacts'
  import { CENTER_MIN } from '../lib/sidebarWidth'
  import * as api from '../lib/api'

  // ── Session artifacts (existing) ──────────────────────────────────────────
  const cur = $derived($artifacts[$artifactSel] ?? $artifacts[0])
  // Images render outside the sandboxed iframe and have no source view.
  const curIsImage = $derived(!!cur?.src)

  // Entries observe metadata-only; the body is fetched and the preview built
  // on first selection. Re-runs when a live re-write swaps the entry object,
  // so the rebuilt document replaces the stale one. hydrateArtifact itself
  // no-ops on loaded entries and in-flight builds.
  $effect(() => {
    if ($panelContent === 'session' && cur && !cur.loaded) hydrateArtifact(cur)
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
      // Save what the panel previews, not the raw source: a Light App renders
      // in the same kind of sandboxed iframe, where relative image paths
      // resolve against nothing (#1890).
      const app = await api.createLightApp({ name, html: lightAppSource(cur) })
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

  // ── Already-a-Light-App detection ──────────────────────────────────────────
  // "Save to Light App" is pointless for a file that already lives inside the
  // Light Apps directory (a Light App's own index.html, or a file beside it).
  // The directory itself is server-side knowledge (~/.octo/light-apps), so it
  // is fetched once, lazily, while the session panel shows an HTML artifact.
  // A failed lookup must not hide a working action, so the button stays
  // visible until the directory is actually known — and the attempt resets,
  // so a transient failure is retried on the next artifact switch instead of
  // disabling the feature for the rest of the session.
  let laDir = $state<string | null>(null)
  let laDirAttempted = $state(false)

  async function ensureLaDir() {
    if (laDir !== null || laDirAttempted) return
    laDirAttempted = true
    try {
      laDir = await api.getLightAppsDir()
    } catch {
      laDir = ''
      laDirAttempted = false
    }
  }

  const curIsLightApp = $derived(curIsHTML && pathIsInside(cur?.path ?? '', laDir ?? ''))

  $effect(() => {
    if ($panelContent === 'session' && curIsHTML) void ensureLaDir()
  })

  // The Save dialog must not outlive the button: once the lookup settles and
  // the artifact turns out to be inside the Light Apps directory, close it.
  $effect(() => {
    if (curIsLightApp) saveToLADialog = false
  })

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

  // Closing drops the expanded state too, so re-opening comes back at its own
  // width rather than silently swallowing the main column again.
  function closePanel() {
    panelExpanded.set(false)
    panelContent.set(null)
  }

  // Derive the current light app's HTML preview.
  const laCurSlug = $derived($lightappSel || $lightapps[0]?.slug || '')
  const laCurHTML = $derived($lightappHTML[laCurSlug] ?? '')
  const laCurName = $derived($lightapps.find(a => a.slug === laCurSlug)?.name ?? laCurSlug)

  // ── Resizable width ────────────────────────────────────────────────────────
  // Drag the left-edge handle to resize; the width persists across sessions.
  // Bounds: never narrower than the panel's usable minimum, never so wide the
  // center column drops under its own minimum (mirrors the design mock).
  const PANEL_WIDTH_KEY = 'octo.panelWidth'
  const PANEL_MIN = 320
  let panelEl = $state<HTMLElement | null>(null)
  let panelWidth = $state(readSavedWidth())

  function readSavedWidth(): number {
    const v = Number(localStorage.getItem(PANEL_WIDTH_KEY))
    return Number.isFinite(v) && v >= PANEL_MIN ? v : 420
  }

  // Take over the content area, leaving the sidebar in place: the main column
  // yields its width (App.svelte hides it) and this panel grows into it. No
  // pixel maths — flex fills whatever is left, so it stays right through a
  // window resize or the sidebar collapsing underneath.
  function toggleExpanded() {
    panelExpanded.update(v => !v)
  }

  function startResize(e: MouseEvent) {
    e.preventDefault()
    const row = panelEl?.parentElement
    if (!row) return
    const startX = e.clientX
    const startW = panelEl!.getBoundingClientRect().width
    // Everything left of the panel (sidebar + center) must keep CENTER_MIN for
    // the center column; the sidebar's share is whatever it currently occupies.
    const rowW = row.getBoundingClientRect().width
    const sideW = rowW - startW - (panelEl!.previousElementSibling as HTMLElement)?.getBoundingClientRect().width
    const maxW = Math.max(PANEL_MIN, rowW - sideW - CENTER_MIN)
    const move = (ev: MouseEvent) => {
      panelWidth = Math.max(PANEL_MIN, Math.min(maxW, startW + (startX - ev.clientX)))
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      localStorage.setItem(PANEL_WIDTH_KEY, String(Math.round(panelWidth)))
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }
</script>

<!-- The panel's own controls, at the far right of its top row in every mode.
     Expand is a layout action, so it stays clickable whatever the panel is
     showing — including the empty state. The close toggle carries an "on" fill
     because this row only exists while the panel is open. -->
{#snippet topbarControls()}
  <button class="icon-btn" title={$panelExpanded ? $t('artifacts.collapse_panel') : $t('artifacts.maximize')} onclick={toggleExpanded}>
    <iconify-icon icon={$panelExpanded ? 'ph:arrows-in-simple' : 'ph:arrows-out-simple'} width="15"></iconify-icon>
  </button>
  <button class="icon-btn on" title={$t('header.toggle_right')} onclick={closePanel}>
    <iconify-icon icon="lucide:panel-right" width="14"></iconify-icon>
  </button>
{/snippet}

<!-- A light app opens maximized, so there is no narrow state to shrink back to
     and nothing for an expand/collapse pair to toggle between. One close
     button instead — and only one, since the panel toggle beside it would have
     done exactly the same thing here. -->
{#snippet lightAppControls()}
  <button class="icon-btn" title={$t('common.close')} onclick={closePanel}>
    <iconify-icon icon="ant-design:close-outlined" width="15"></iconify-icon>
  </button>
{/snippet}

<aside class="panel" bind:this={panelEl} style={$panelExpanded ? 'flex:1 1 auto' : `width:${panelWidth}px;flex-basis:${panelWidth}px`}>
  <!-- Expanded, there is no neighbour left to drag against, so the handle goes
       with it rather than sitting there inert against the sidebar. -->
  {#if !$panelExpanded}
    <div class="resize-handle" role="separator" aria-orientation="vertical" onmousedown={startResize}></div>
  {/if}
  {#if $panelContent === 'lightapps'}
    <!-- ── Light Apps mode ───────────────────────────────────────────────── -->
    <div class="topbar">
      <span style="flex:1"></span>
      <span class="sandboxed-label">{$t('artifacts.sandboxed')}</span>
      {@render lightAppControls()}
    </div>

    <div class="body">
      {#if laLoading}
        <div class="empty"><iconify-icon icon="ant-design:loading-outlined" width="28" class="spin"></iconify-icon><span>{$t('common.loading')}</span></div>
      {:else if laCurHTML}
        <iframe srcdoc={laCurHTML} sandbox="allow-scripts" allow="clipboard-write" title={laCurName}></iframe>
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
        <span style="flex:1"></span>
        {@render topbarControls()}
      </div>
      <div class="empty">
        <iconify-icon icon="ant-design:file-text-outlined" width="28"></iconify-icon>
        <span>{$t('artifacts.empty')}</span>
      </div>
    {:else}
      <div class="topbar">
        <span style="flex:1"></span>
        {#if !curIsImage}
          <div class="seg">
            <button class="seg-btn" class:active={$artifactView === 'preview'} onclick={() => artifactView.set('preview')}>{$t('artifacts.preview')}</button>
            <button class="seg-btn" class:active={$artifactView === 'code'} onclick={() => artifactView.set('code')}>{$t('artifacts.code')}</button>
          </div>
        {/if}
        {@render topbarControls()}
      </div>

      <div class="file-row">
        <iconify-icon icon={cur.icon} width="14" style="color:var(--text-secondary);flex:0 0 auto"></iconify-icon>
        <span class="file-name mono">{cur.name}</span>
        <span class="file-meta">{cur.type}</span>
        <span style="flex:1"></span>
        <span class="sandboxed-label">{$t('artifacts.sandboxed')}</span>
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

      <div class="body">
        {#if curIsImage}
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
        {:else if $artifactView === 'preview'}
          <iframe srcdoc={cur.preview} sandbox="allow-scripts" allow="clipboard-write" title={cur.name}></iframe>
        {:else}
          <pre class="code-view">{cur.code}</pre>
        {/if}
      </div>

      {#if $artifacts.length > 1}
        <div class="switcher">
          {#each $artifacts as a, i}
          <button class="chip" class:active={i === $artifactSel} title={a.path} onclick={() => artifactSel.set(i)}>
            <iconify-icon icon={a.icon} width="13"></iconify-icon>
            {a.short}
          </button>
          {/each}
        </div>
      {/if}

      <div class="footer">
        <button class="wbtn" disabled={!cur.loaded || cur.loadFailed} onclick={onCopy}>
          <iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon>
          {$t('artifacts.copy')}
        </button>
        <button class="wbtn" disabled={!cur.loaded || cur.loadFailed} onclick={onDownload}>
          <iconify-icon icon="ant-design:download-outlined" width="14"></iconify-icon>
          {$t('artifacts.download')}
        </button>
        {#if curIsHTML && !curIsLightApp}
          <button class="wbtn" disabled={!cur.loaded || cur.loadFailed} onclick={openSaveToLA}>
            <iconify-icon icon="ant-design:save-outlined" width="14"></iconify-icon>
            {$t('artifacts.save_to_lightapp')}
          </button>
        {/if}
      </div>
    {/if}
  {/if}
</aside>

<style>
.panel {
  flex: 0 0 auto;
  background: var(--panel-frost);
  backdrop-filter: blur(var(--frost-blur));
  -webkit-backdrop-filter: blur(var(--frost-blur));
  border-left: 1px solid var(--border-secondary); display: flex; flex-direction: column; min-height: 0;
  position: relative;
}
.resize-handle {
  position: absolute; left: -3px; top: 0; bottom: 0; width: 8px;
  cursor: col-resize; z-index: 5;
}
.resize-handle:hover { background: var(--focus-ring); }
/* This column's own top row. No bottom border, matching Sidebar's and the main
   column's — the layout's only lines are the vertical dividers between them. */
.topbar {
  flex: 0 0 auto; min-height: 44px; padding: 0 8px 0 10px;
  display: flex; align-items: center; gap: 8px;
}
.file-row {
  flex: 0 0 auto; padding: 7px 10px 7px 16px;
  border-bottom: 1px solid var(--border-secondary); display: flex; align-items: center; gap: 8px;
}
.file-name { font-size: 12px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-meta { font-size: 11px; color: var(--text-tertiary); flex: 0 0 auto; }
.mono { font-family: var(--font-mono); }
.icon-btn {
  width: 28px; height: 28px; flex: 0 0 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.icon-btn:hover:not(:disabled) { background: var(--hover-neutral); color: var(--blue-6); }
.icon-btn:disabled { opacity: 0.45; cursor: default; }
/* Pressed/on state: a filled rounded square with a full-strength icon, so a
   control whose target is already showing reads as engaged rather than idle. */
.icon-btn.on { background: var(--hover-neutral); color: var(--text); }
.icon-btn.on:hover:not(:disabled) { color: var(--text); }
.seg { display: inline-flex; padding: 2px; background: var(--control-track); border-radius: 8px; gap: 2px; flex: 0 0 auto; }
.seg-btn {
  height: 24px; padding: 0 12px; border: none; border-radius: 6px; font-size: 12px;
  cursor: pointer; background: transparent; color: var(--text-secondary); font-family: inherit;
}
.seg-btn.active { background: var(--bg-container); color: var(--blue-6); font-weight: 600; box-shadow: 0 1px 2px rgba(0,0,0,0.12); }
.sandboxed-label { margin-left: auto; font-size: 11px; color: var(--text-tertiary); }
.body { flex: 1; min-height: 0; background: var(--bg-container); }
.body-loading { height: 100%; display: flex; align-items: center; justify-content: center; color: var(--text-tertiary); }
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
.img-wrap img.img-hidden { display: none; }
.img-error {
  display: flex; flex-direction: column; align-items: center; gap: 10px;
  max-width: 85%; text-align: center; color: var(--text-tertiary); font-size: 13px;
}
.code-view {
  margin: 0; height: 100%; box-sizing: border-box; overflow: auto;
  padding: 14px 16px; background: var(--bg-sidebar); font-size: 12px; line-height: 1.7;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text); white-space: pre;
}
.switcher {
  flex: 0 0 auto; border-top: 1px solid var(--border-secondary);
  padding: 8px 12px; display: flex; align-items: center; gap: 6px; overflow-x: auto;
}
.footer {
  flex: 0 0 auto; border-top: 1px solid var(--border-secondary);
  padding: 10px 16px; display: flex; align-items: center; gap: 8px; overflow-x: auto;
}
.footer-lbl { font-size: 11px; color: var(--text-tertiary); flex: 0 0 auto; margin-right: 2px; }
.wbtn {
  display: inline-flex; align-items: center; gap: 7px; height: 32px; padding: 0 12px;
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-sm);
  color: var(--text); font-size: 12px; font-weight: 500; cursor: pointer; font-family: inherit;
  white-space: nowrap; box-shadow: 0 1px 1.5px rgba(0,0,0,0.04); transition: 0.12s; flex: 0 0 auto;
}
.wbtn:hover:not(:disabled) { background: var(--bg-table-header); }
.wbtn:disabled { opacity: 0.5; cursor: default; }
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
