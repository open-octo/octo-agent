<script lang="ts">
  import { artifacts, panelContent, panelExpanded, artifactSel, artifactView, lightappSel, lightappOpen, lightapps, lightappHTML, showToast, nativeShell, activeSessionId, savePanelMode, type PanelMode } from '../lib/stores'
  import { titlebarDblClick } from '../lib/nativeWindow'
  import { t } from '../lib/i18n'
  import { copyArtifact, downloadArtifact, imagePreviewError } from '../lib/artifact-actions'
  import { hydrateArtifact, lightAppSource, pathIsInside } from '../lib/artifacts'
  import { CENTER_MIN } from '../lib/sidebarWidth'
  import { diffData, diffLoading, diffBadge, loadDiff } from '../lib/diff'
  import DiffView from './diff/DiffView.svelte'
  import * as api from '../lib/api'
  import { installLaStorageBridge, registerLaIframe, unregisterLaIframe, withLaBridge } from '../lib/laStorage'

  // This column never holds the traffic lights, but its top row has to sit on
  // the same axis as the chat title beside it, which Header lifts on mac.
  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform)
  const liftForTrafficLights = $derived($nativeShell && isMac)

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
      // source_path marks the app as saved from this artifact, which is what
      // hides the Save button for it from here on (curSavedAsLA).
      const app = await api.createLightApp({ name, html: lightAppSource(cur), source_path: cur.path })
      showToast(`Light App "${app.name}" saved`, 'success')
      saveToLADialog = false
      lightapps.update(list => [...list.filter(a => a.slug !== app.slug), app])
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
      // One request answers with both the directory and the apps; the apps
      // land in the store so curSavedAsLA below can match source paths.
      const { apps, dir } = await api.getLightAppList()
      laDir = dir
      lightapps.set(apps)
    } catch {
      laDir = ''
      laDirAttempted = false
    }
  }

  const curIsLightApp = $derived(curIsHTML && pathIsInside(cur?.path ?? '', laDir ?? ''))

  // Already saved as a Light App: some app records this artifact's path as
  // its source (manifest source_path). Equally redundant to save again — the
  // slug exists, so the server would 409 anyway.
  const curSavedAsLA = $derived(curIsHTML && !!cur?.path && $lightapps.some(a => a.source_path === cur.path))

  $effect(() => {
    if ($panelContent === 'session' && curIsHTML) void ensureLaDir()
  })

  // The Save dialog must not outlive the button: once the lookup settles and
  // the artifact turns out to be inside the Light Apps directory, close it.
  $effect(() => {
    if (curIsLightApp || curSavedAsLA) saveToLADialog = false
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

  function closeLightApp(slug: string) {
    const list = $lightappOpen
    const idx = list.indexOf(slug)
    if (idx < 0) return
    const rest = list.filter(s => s !== slug)
    lightappOpen.set(rest)
    // Closing the last tab closes the panel — an empty Light Apps panel has
    // nothing to offer, and stopping looking is what the click meant. The
    // cached HTML is kept, so reopening is instant.
    if (rest.length === 0) { lightappSel.set(''); closePanel(); return }
    // Closing the active tab hands over to its neighbour on the right, or the
    // left one at the end of the strip — the way a browser tab strip behaves.
    if ($lightappSel === slug) lightappSel.set(rest[Math.min(idx, rest.length - 1)])
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

  // ── Git Diff mode ─────────────────────────────────────────────────────────
  // Loaded on open and on demand; a finished turn refreshes it from lib/diff's
  // own WS subscription. No polling — a diff only moves when a turn does.
  $effect(() => {
    if ($panelContent === 'diff' && $activeSessionId) void loadDiff($activeSessionId)
  })

  const diffRepos = $derived($diffData?.repos ?? [])
  // With one repository the topbar names it; with several, DiffView's group
  // headers do, and the topbar just says how many files are in play.
  const diffSoleRepo = $derived(diffRepos.length === 1 ? diffRepos[0] : null)
  const diffFileCount = $derived(diffRepos.reduce((n, r) => n + r.files.length, 0))
  const badgeCount = $derived($diffBadge[$activeSessionId ?? ''] ?? 0)

  // ── Panel mode switcher ───────────────────────────────────────────────────
  // The Header button stays a plain open/close toggle; which of the two modes
  // the panel shows is decided here, from the topbar's leftmost icon slot.
  // Light Apps is deliberately absent — it has its own entry point, and is not
  // one of the two things a chat sidebar alternates between.
  let modeMenu = $state(false)
  const MODES: { id: PanelMode; icon: string; label: string }[] = [
    { id: 'session', icon: 'ant-design:file-text-outlined', label: 'panel.mode_artifacts' },
    { id: 'diff', icon: 'ant-design:branches-outlined', label: 'panel.mode_diff' },
  ]
  const curMode = $derived<PanelMode>($panelContent === 'diff' ? 'diff' : 'session')
  const curModeIcon = $derived(MODES.find(m => m.id === curMode)?.icon ?? MODES[0].icon)
  const curModeLabel = $derived(MODES.find(m => m.id === curMode)?.label ?? MODES[0].label)

  function pickMode(mode: PanelMode) {
    modeMenu = false
    savePanelMode(mode)
    panelContent.set(mode)
  }

  // Derive the current light app's HTML preview.
  // The tab strip is the apps the user opened, in open order. Selection falls
  // back to the first tab so closing the active one never leaves a blank panel.
  const laOpenApps = $derived(
    $lightappOpen.map(slug => {
      const a = $lightapps.find(x => x.slug === slug)
      return { slug, name: a?.name ?? slug, icon: a?.icon ?? '' }
    }),
  )
  const laCurSlug = $derived($lightappOpen.includes($lightappSel) ? $lightappSel : ($lightappOpen[0] ?? ''))
  const laCurHTML = $derived($lightappHTML[laCurSlug] ?? '')
  const laCurName = $derived($lightapps.find(a => a.slug === laCurSlug)?.name ?? laCurSlug)

  // ── Light App storage bridge ─────────────────────────────────────────────
  // The sandboxed srcdoc iframe has an opaque origin, so it can't touch any
  // persistent storage. We host an IndexedDB here and the injected bridge
  // script (withLaBridge) shims the app's own localStorage calls onto it over
  // postMessage. Register the iframe so the handler only serves OUR frame,
  // under the slug whose script we injected — the element is reused when the
  // user switches apps, so the namespace has to be re-pinned every time.
  let laFrameEl = $state<HTMLIFrameElement | null>(null)
  $effect(() => {
    installLaStorageBridge()
    const el = laFrameEl
    if (!el) return
    registerLaIframe(el.contentWindow, laCurSlug)
    return () => unregisterLaIframe(el.contentWindow)
  })

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

  // The sidebar's width isn't queried directly — main (this panel's previous
  // sibling) sits between it and the panel. rowW - currentPanelW - main's
  // rendered width isolates it instead: main's width already reflects
  // "whatever's left after the sidebar and the panel", so subtracting both
  // out algebraically cancels back to just the sidebar, independent of
  // currentPanelW's actual value (the term appears on both sides). Reading
  // previousElementSibling's width directly as "the sidebar" — an earlier
  // version of this function did — is wrong: that element IS main, so it
  // silently measured main's width as the sidebar's, inflating the room the
  // math thought main already had and starving the ceiling it computed for
  // the panel.
  function maxPanelWidth(rowW: number, currentPanelW: number): number {
    const main = panelEl?.previousElementSibling as HTMLElement | null
    const mainW = main?.getBoundingClientRect().width ?? 0
    const sideW = rowW - currentPanelW - mainW
    const centerProtectingCeiling = rowW - sideW - CENTER_MIN
    // Below PANEL_MIN, protecting CENTER_MIN is impossible regardless of the
    // panel's width — the window itself is too narrow, not something this
    // drag can fix. Falling through to Math.max(PANEL_MIN, ...) would floor
    // the ceiling at PANEL_MIN, freezing the handle at that single point
    // instead of just giving up on the trade-off (main already degrades
    // gracefully below CENTER_MIN when there's no room to spare — see
    // App.svelte's mainMinWidth).
    // The outer Math.min(rowW, ...) is a hard cap: on a phone browser the
    // viewport can be narrower than PANEL_MIN (or the saved desktop width),
    // in which case the panel must shrink to fit or its right edge — and the
    // close button on it — overflows past the screen and becomes unreachable.
    if (centerProtectingCeiling < PANEL_MIN) return Math.min(rowW, Math.max(PANEL_MIN, rowW - sideW))
    return Math.min(rowW, centerProtectingCeiling)
  }

  // The same bound startResize enforces mid-drag, applied on mount and on
  // every window resize too — otherwise a saved width from a wider window (or
  // one that fit before the OS window itself got dragged smaller) can outgrow
  // the room actually available. flex:0 0 auto below means this column never
  // shrinks on its own to make way; left unclamped, the excess just overflows
  // past the window edge instead of appearing anywhere.
  // When the window widens again the panel should grow back toward the user's
  // saved preference (or the maximum the room allows), not stay pinned at the
  // narrow-window clamp — otherwise a brief squeeze permanently shrinks the
  // panel until the user drags it manually.
  const savedWidth = readSavedWidth()
  $effect(() => {
    function clamp() {
      const row = panelEl?.parentElement
      if (!row || !panelEl) return
      const rowW = row.getBoundingClientRect().width
      const maxW = maxPanelWidth(rowW, panelWidth)
      if (panelWidth > maxW) {
        panelWidth = maxW
      } else if (panelWidth < Math.min(savedWidth, maxW)) {
        panelWidth = Math.min(savedWidth, maxW)
      }
    }
    clamp()
    window.addEventListener('resize', clamp)
    return () => window.removeEventListener('resize', clamp)
  })

  // Take over the content area, leaving the sidebar in place: the main column
  // yields its width (App.svelte hides it) and this panel grows into it. No
  // pixel maths — flex fills whatever is left, so it stays right through a
  // window resize or the sidebar collapsing underneath.
  function toggleExpanded() {
    panelExpanded.update(v => !v)
  }

  // The preview is a sandboxed iframe: its own document, in its own renderer
  // process. Writing a new panel width on every mousemove made the divider
  // feel towed behind the cursor — each width change is a cross-document
  // resize the compositor waits on before it can present the new edge, and a
  // 125Hz mouse asks for two of them per frame. So the drag coalesces its
  // writes to one per animation frame.
  function startResize(e: PointerEvent) {
    e.preventDefault()
    const row = panelEl?.parentElement
    if (!row) return
    const handle = e.currentTarget as HTMLElement
    const startX = e.clientX
    const startW = panelEl!.getBoundingClientRect().width
    const rowW = row.getBoundingClientRect().width
    const maxW = maxPanelWidth(rowW, startW)
    let raf = 0
    let pointerX = startX
    const apply = () => {
      raf = 0
      panelWidth = Math.max(PANEL_MIN, Math.min(maxW, startW + (startX - pointerX)))
    }
    const move = (ev: PointerEvent) => {
      pointerX = ev.clientX
      if (!raf) raf = requestAnimationFrame(apply)
    }
    const up = () => {
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', up)
      handle.removeEventListener('pointercancel', up)
      handle.removeEventListener('lostpointercapture', up)
      // A move already queued for the next frame still counts: without this the
      // last few pixels of the drag would be dropped on release.
      if (raf) { cancelAnimationFrame(raf); apply() }
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      localStorage.setItem(PANEL_WIDTH_KEY, String(Math.round(panelWidth)))
    }
    // Narrowing drags the handle toward the window's edge, where a fast flick
    // can carry the cursor past the webview bounds. Plain window mousemove/up
    // listeners never see the release when that happens — the browser has
    // nowhere in this page to deliver it — so the drag reads as "stuck" until
    // an unrelated later move is mistaken for more dragging. Pointer capture
    // routes every subsequent event for this pointer to the handle regardless
    // of where the cursor physically is, and lostpointercapture/pointercancel
    // guarantee up() still runs if capture is yanked away (e.g. focus loss).
    handle.setPointerCapture(e.pointerId)
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', up)
    handle.addEventListener('pointercancel', up)
    handle.addEventListener('lostpointercapture', up)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }
</script>

<!-- The panel's own controls, at the far right of its top row in every mode.
     Expand is a layout action, so it stays clickable whatever the panel is
     showing — including the empty state. The close toggle carries an "on" fill
     because this row only exists while the panel is open. -->
<!-- The topbar's leftmost icon slot doubles as the mode switcher: the icon is
     the mode you're in, clicking it offers the other. Absent in Light Apps
     mode, which arrives from its own entry point rather than from here. The
     changed-file badge rides on it while the panel is open — the Header button
     carries the same count while it is closed. -->
{#snippet modeSwitcher()}
  <div class="mode-wrap">
    <button
      class="icon-btn mode-trigger"
      class:on={modeMenu}
      title={$t('panel.switch_mode')}
      onclick={() => modeMenu = !modeMenu}
    >
      <iconify-icon icon={curModeIcon} width="14"></iconify-icon>
      <!-- The mode's name belongs to the switcher, not to the row: standing on
           its own beside the trigger it read as a heading, and whatever came
           next (a filename, a type badge) looked like part of the same group. -->
      <span class="mode-name">{$t(curModeLabel)}</span>
      <!-- Always-on caret: without it the trigger reads as a static mode
           indicator, and the dropdown is only discoverable by hovering for
           the tooltip. -->
      <iconify-icon class="caret" icon="ant-design:down-outlined" width="8"></iconify-icon>
      {#if badgeCount > 0 && curMode !== 'diff'}<span class="dot"></span>{/if}
    </button>
    {#if modeMenu}
      <!-- Clicking anywhere else dismisses it; the backdrop is what makes that
           work without a document-level listener that outlives the menu. -->
      <button class="backdrop" aria-label={$t('common.cancel')} onclick={() => modeMenu = false}></button>
      <div class="mode-menu" role="menu">
        {#each MODES as m}
          <button class="mode-item" class:active={m.id === curMode} role="menuitem" onclick={() => pickMode(m.id)}>
            <iconify-icon icon={m.icon} width="14"></iconify-icon>
            <span>{$t(m.label)}</span>
            {#if m.id === 'diff' && badgeCount > 0}<span class="mode-count">{badgeCount}</span>{/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/snippet}

{#snippet topbarControls()}
  <button class="icon-btn" title={$panelExpanded ? $t('artifacts.collapse_panel') : $t('artifacts.maximize')} onclick={toggleExpanded}>
    <iconify-icon icon={$panelExpanded ? 'ph:arrows-in-simple' : 'ph:arrows-out-simple'} width="15"></iconify-icon>
  </button>
  <button class="icon-btn on" title={$t('header.toggle_right')} onclick={closePanel}>
    <iconify-icon icon="lucide:panel-right" width="14"></iconify-icon>
  </button>
{/snippet}

<aside class="panel" bind:this={panelEl} style={$panelExpanded ? 'flex:1 1 auto' : `width:${panelWidth}px;flex-basis:${panelWidth}px`}>
  <!-- Expanded, there is no neighbour left to drag against, so the handle goes
       with it rather than sitting there inert against the sidebar. -->
  {#if !$panelExpanded}
    <div class="resize-handle" role="separator" aria-orientation="vertical" onpointerdown={startResize}></div>
  {/if}
  {#if $panelContent === 'lightapps'}
    <!-- ── Light Apps mode ───────────────────────────────────────────────── -->
    <!-- Every mode's topbar is the window's top edge like the other two columns'
         rows, so it drags the window (see .topbar's --wails-draggable) and takes
         the double-click that zooms it. -->
    <div class="topbar" class:native-lift={liftForTrafficLights} ondblclick={titlebarDblClick}>
      <div class="la-chips">
        <span class="footer-lbl">{$t('artifacts.light_apps')}</span>
        {#each laOpenApps as a (a.slug)}
        <span class="chip" class:active={a.slug === laCurSlug}>
          <button class="chip-main" title={a.name} onclick={() => selectLightApp(a.slug)}>
            <span>{a.icon || '📦'}</span>
            {a.name}
          </button>
          <button class="chip-close" title={$t('artifacts.close_tab')} aria-label={$t('artifacts.close_tab')} onclick={() => closeLightApp(a.slug)}>
            <iconify-icon icon="ant-design:close-outlined" width="10"></iconify-icon>
          </button>
        </span>
        {/each}
      </div>
      {@render topbarControls()}
    </div>

    <div class="body">
      {#if laCurHTML}
        <iframe bind:this={laFrameEl} srcdoc={withLaBridge(laCurHTML, laCurSlug)} sandbox="allow-scripts" allow="clipboard-write" title={laCurName}></iframe>
      {:else if laCurSlug || laLoading}
        <!-- A tab opens before its HTML arrives, so this covers the fetch. -->
        <div class="empty"><iconify-icon icon="ant-design:loading-outlined" width="28" class="spin"></iconify-icon><span>{$t('common.loading')}</span></div>
      {:else}
        <div class="empty">
          <iconify-icon icon="ant-design:appstore-outlined" width="28"></iconify-icon>
          <span>{$t('lightapps.empty')}</span>
        </div>
      {/if}
    </div>

  {:else if $panelContent === 'diff'}
    <!-- ── Git Diff mode ──────────────────────────────────────────────────── -->
    <div class="topbar" class:native-lift={liftForTrafficLights} ondblclick={titlebarDblClick}>
      {@render modeSwitcher()}
      <span class="file-id">
        {#if diffSoleRepo}
          <span class="file-name mono">{diffSoleRepo.name}</span>
          {#if diffSoleRepo.branch}<span class="file-meta">{diffSoleRepo.commit || diffSoleRepo.branch}</span>{/if}
        {:else if diffFileCount > 0}
          <span class="file-meta">{$t('files.count_files').replace('{n}', String(diffFileCount))}</span>
        {/if}
      </span>
      <span style="flex:1"></span>
      <button
        class="icon-btn"
        title={$t('diff.refresh')}
        disabled={$diffLoading || !$activeSessionId}
        onclick={() => loadDiff($activeSessionId ?? '')}
      >
        <iconify-icon icon="ant-design:reload-outlined" width="14" class={$diffLoading ? 'spin' : ''}></iconify-icon>
      </button>
      {@render topbarControls()}
    </div>

    <!-- No footer: the panel is read-only. Acting on what you find here means
         telling the agent in the chat, which is the whole point of reviewing
         inside the agent rather than in a git client. -->
    <div class="body">
      <DiffView />
    </div>

  {:else if $panelContent === 'session'}
    <!-- ── Session mode (existing behavior) ────────────────────────────────── -->
    {#if !cur}
      <div class="topbar" class:native-lift={liftForTrafficLights} ondblclick={titlebarDblClick}>
        {@render modeSwitcher()}
        <span style="flex:1"></span>
        {@render topbarControls()}
      </div>
      <div class="empty">
        <iconify-icon icon="ant-design:file-text-outlined" width="28"></iconify-icon>
        <span>{$t('artifacts.empty')}</span>
      </div>
    {:else}
      <div class="topbar" class:native-lift={liftForTrafficLights} ondblclick={titlebarDblClick}>
        {@render modeSwitcher()}
        <span class="file-id">
          <span class="file-name mono">{cur.name}</span>
          <span class="file-meta">{cur.type}</span>
        </span>
        <span style="flex:1"></span>
        {#if !curIsImage}
          <div class="seg">
            <button class="seg-btn" class:active={$artifactView === 'preview'} onclick={() => artifactView.set('preview')}>{$t('artifacts.preview')}</button>
            <button class="seg-btn" class:active={$artifactView === 'code'} onclick={() => artifactView.set('code')}>{$t('artifacts.code')}</button>
          </div>
        {/if}
        {@render topbarControls()}
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
        {#if curIsHTML && !curIsLightApp && !curSavedAsLA}
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
  /* Lets the topbar rules below query this column's width rather than the
     window's — see the @container blocks. */
  container-type: inline-size;
  /* Without this, a code view's long unwrapped lines set the flex item's
     automatic minimum width and push the topbar controls past the viewport
     edge (worst when expanded: the panel is then the row's only item). */
  min-width: 0;
  background: var(--panel-frost);
  backdrop-filter: blur(var(--frost-blur));
  -webkit-backdrop-filter: blur(var(--frost-blur));
  border-left: 1px solid var(--border-secondary); display: flex; flex-direction: column; min-height: 0;
  position: relative;
  /* On a phone browser the panel can be clamped to the viewport width; without
     overflow:hidden the topbar's flex children (mode trigger, close button)
     can still push past the panel's own edge and become unreachable. */
  overflow: hidden;
  max-width: 100%;
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
  --wails-draggable: drag;
}
/* Every control in here opts back out, leaving the labels and the flex spacer
   to drag the window. Matched by element rather than by class the way Header
   and Sidebar do it: the controls are spread across four mode branches and two
   snippets, and a class this rule missed would become a button that drags the
   window instead of clicking. That includes the mode menu's full-bleed
   backdrop, which is a button too. */
.topbar button { --wails-draggable: no-drag; }
/* The same axis lift Header and Sidebar apply on mac — see Header.native-lift
   for why the height has to be pinned for the padding to move anything. */
.topbar.native-lift { box-sizing: border-box; max-height: 44px; padding-bottom: 4px; }
.file-name { min-width: 0; font-size: 12px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-meta { font-size: 11px; color: var(--text-tertiary); flex: 0 0 auto; }
/* A name and the badge beside it are one identity, so they shrink and vanish as
   one. Left to themselves the name (which is what gives) ellipsised away to
   nothing while the unshrinkable badge stayed — and a lone "HTML" sitting next
   to the mode switcher reads as a label for the switcher rather than the
   remains of a filename. */
.file-id { display: flex; align-items: center; gap: 6px; min-width: 0; flex: 0 1 auto; }
/* Driven by the panel's own width, not the window's: the panel is dragged
   narrow inside a wide window, which is precisely when the topbar runs out of
   room and the trailing controls get pushed under the panel's edge. Below 400px
   the name has too few pixels left to say anything, and below 340px even the
   mode's name has to go so the controls keep their place. */
@container (max-width: 400px) {
  .topbar .file-id { display: none; }
}
@container (max-width: 340px) {
  .topbar .mode-name { display: none; }
}
/* Small screens (a phone browser gets the desktop layout squeezed into a few
   hundred px): the topbar's text — mode name, file/repo name, type badge,
   branch, file count — crowds the icon controls past each other. The mode
   switcher's icon still names the mode and tooltips carry the rest, so the
   text is what gives. Same breakpoint as ChatView's narrow-screen rules. */
@media (max-width: 620px) {
  .topbar .file-id,
  .topbar .mode-name { display: none; }
}
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
/* ── Panel mode switcher ─────────────────────────────────────────────── */
.mode-wrap { position: relative; flex: 0 0 auto; display: flex; }
/* Wider than a plain .icon-btn (which pins 28px square): the trigger carries
   the mode icon, the mode's name and a dropdown caret, so it sizes to its
   content. */
.mode-trigger { position: relative; width: auto; flex: 0 0 auto; padding: 0 7px; gap: 4px; }
/* Sits inside the trigger, so it takes the trigger's colour rather than the
   muted tone a standalone label wanted. */
.mode-name { font-size: 12px; color: var(--text); white-space: nowrap; }
.mode-trigger .caret { transition: transform 0.15s ease; }
.mode-trigger.on .caret { transform: rotate(180deg); }
/* Same "there is something here" dot the sidebar's unread marker uses, sized
   for a compact control rather than a list row. */
.mode-trigger .dot {
  position: absolute; top: 4px; right: 4px; width: 6px; height: 6px;
  border-radius: 50%; background: var(--blue-6);
}
.backdrop {
  position: fixed; inset: 0; z-index: 40;
  border: none; background: transparent; padding: 0; cursor: default;
}
.mode-menu {
  position: absolute; top: 32px; left: 0; z-index: 41; min-width: 168px;
  padding: 4px; background: var(--bg-elevated, var(--bg-container));
  border: 1px solid var(--border-secondary); border-radius: 8px;
  box-shadow: 0 6px 20px rgba(0,0,0,0.14); display: flex; flex-direction: column; gap: 2px;
}
.mode-item {
  display: flex; align-items: center; gap: 8px; height: 30px; padding: 0 8px;
  border: none; border-radius: 6px; background: transparent; cursor: pointer;
  color: var(--text); font-size: 12px; font-family: inherit; text-align: left;
}
.mode-item:hover { background: var(--hover-neutral); }
.mode-item.active { color: var(--blue-6); font-weight: 600; }
.mode-count {
  margin-left: auto; min-width: 18px; height: 16px; padding: 0 5px;
  display: inline-flex; align-items: center; justify-content: center;
  border-radius: 8px; background: var(--active-blue-bg); color: var(--blue-6);
  font-size: 10px; font-weight: 600; font-variant-numeric: tabular-nums;
}
.seg { display: inline-flex; padding: 2px; background: var(--control-track); border-radius: 8px; gap: 2px; flex: 0 0 auto; }
.seg-btn {
  height: 24px; padding: 0 12px; border: none; border-radius: 6px; font-size: 12px;
  cursor: pointer; background: transparent; color: var(--text-secondary); font-family: inherit;
}
.seg-btn.active { background: var(--bg-container); color: var(--blue-6); font-weight: 600; box-shadow: 0 1px 2px rgba(0,0,0,0.12); }
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
/* Light Apps switcher now lives in the topbar; it scrolls sideways rather
   than crowding out the panel controls when many apps are installed. */
.la-chips {
  flex: 1; min-width: 0; display: flex; align-items: center; gap: 6px; overflow-x: auto;
}
.wbtn {
  display: inline-flex; align-items: center; gap: 7px; height: 32px; padding: 0 12px;
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-sm);
  color: var(--text); font-size: 12px; font-weight: 500; cursor: pointer; font-family: inherit;
  white-space: nowrap; box-shadow: 0 1px 1.5px rgba(0,0,0,0.04); transition: 0.12s; flex: 0 0 auto;
}
.wbtn:hover:not(:disabled) { background: var(--bg-table-header); }
.wbtn:disabled { opacity: 0.5; cursor: default; }
/* A tab, not a button: the label selects and the × closes, so the chip itself
   is the shell and each half is its own control. */
.chip {
  height: 30px; padding: 0 4px 0 10px; border: 1px solid var(--border-secondary); background: var(--bg-container);
  color: var(--text-secondary); border-radius: 6px; display: flex; align-items: center;
  gap: 2px; font-size: 12px; flex: 0 0 auto; font-family: inherit;
}
.chip.active { border-color: var(--blue-6); background: var(--active-blue-bg); color: var(--blue-6); }
.chip-main {
  display: flex; align-items: center; gap: 6px; max-width: 180px;
  background: none; border: none; padding: 0; margin: 0;
  color: inherit; font: inherit; cursor: pointer;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.chip-close {
  display: flex; align-items: center; justify-content: center;
  width: 18px; height: 18px; flex: 0 0 auto;
  background: none; border: none; padding: 0; border-radius: 4px;
  color: var(--text-tertiary); cursor: pointer; opacity: 0.65;
}
.chip-close:hover { opacity: 1; background: var(--hover-neutral); color: var(--text); }
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
