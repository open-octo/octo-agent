<script lang="ts">
  import { onMount } from 'svelte'
  import { view, cmdkOpen, sidebar, nativeShell } from '../../lib/stores'
  import { t } from '../../lib/i18n'
  import { ws, wsState } from '../../lib/ws'
  import { notificationsEnabled, setNotificationsEnabled } from '../../lib/notifications'
  import { nativeToggleMaximise, nativeToggleFullscreen, nativeMinimise, nativeClose, nativeWindowState } from '../../lib/api'
  import OctoLogo from './OctoLogo.svelte'

  function cycleSidebar() {
    sidebar.update(s => s === 'full' ? 'rail' : s === 'rail' ? 'hidden' : 'full')
  }

  // The bell toggles desktop notifications on/off — the same preference the
  // "Desktop Notifications" switch in Settings drives. There is no feed.
  function toggleNotifications() {
    setNotificationsEnabled(!$notificationsEnabled)
  }

  // Frameless window: the frontend draws its own window controls on every
  // platform. Mac gets traffic-light-style buttons on the left, Windows/Linux
  // keep their right-side controls. The CSS --wails-draggable header region
  // handles dragging; the native bridge handles minimise/maximise/close.
  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform)

  // Desktop only: double-clicking the draggable header zooms the window, the way
  // a native title bar does. Wails' custom drag region doesn't wire this up, and
  // the octo-served page can't call Wails directly, so it goes through the native
  // bridge over HTTP. Ignore double-clicks that land on a control.
  function onHeaderDblClick(e: MouseEvent) {
    if (!$nativeShell) return
    if ((e.target as HTMLElement).closest('button, a, input, select, textarea')) return
    flipMaximise()
  }

  // Track maximise/fullscreen state so the icons reflect reality. The frontend
  // owns this state — there's no native title bar reading it. We sync from the
  // OS on mount, on window focus (catches Aero Snap / keyboard maximize /
  // taskbar restore / Cmd+Ctrl+F the frontend can't otherwise observe), and
  // after every toggle. A sequence counter prevents a stale focus response
  // from overwriting a fresh toggle result.
  let isMaximised = false
  let isFullscreen = false
  let stateSeq = 0
  async function refreshWindowState() {
    const seq = ++stateSeq
    const s = await nativeWindowState()
    if (seq === stateSeq) {
      isMaximised = s.maximised
      isFullscreen = s.fullscreen
    }
  }
  async function flipMaximise() {
    const next = !isMaximised
    try {
      await nativeToggleMaximise()
      isMaximised = next
      ++stateSeq // stale focus refreshes that started before the toggle must not overwrite this
    } catch {
      // Toggle failed — fetch the real OS state to stay in sync rather than
      // gambling that the old isMaximised is still accurate.
      await refreshWindowState()
    }
  }

  // The green traffic light's default click enters/exits true fullscreen (its
  // own space, no menu bar/dock) — distinct from flipMaximise's "fill the
  // screen" zoom, which double-clicking the header still does.
  async function flipFullscreen() {
    const next = !isFullscreen
    try {
      await nativeToggleFullscreen()
      isFullscreen = next
      ++stateSeq
    } catch {
      await refreshWindowState()
    }
  }

  // Hovering the green button shows a small menu, matching native macOS
  // behaviour (hover the zoom button → window-tiling popup). We only offer the
  // one action our fullscreen bridge supports. A short close delay lets the
  // mouse travel from the button down into the menu without it disappearing.
  let showFullscreenMenu = false
  let fullscreenMenuHideTimer: ReturnType<typeof setTimeout> | undefined
  function onMaximiseHoverEnter() {
    clearTimeout(fullscreenMenuHideTimer)
    showFullscreenMenu = true
  }
  function onMaximiseHoverLeave() {
    clearTimeout(fullscreenMenuHideTimer)
    fullscreenMenuHideTimer = setTimeout(() => { showFullscreenMenu = false }, 150)
  }

  onMount(() => {
    if (!$nativeShell) return // web mode has no native bridge — skip entirely
    refreshWindowState()
    const onFocus = () => refreshWindowState()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  })
</script>

<header style="--wails-draggable:drag" ondblclick={onHeaderDblClick}>
  <div class="left">
    {#if $nativeShell && isMac}
      <div class="traffic-lights">
        <button class="traffic-light close" aria-label="Close" title="Close" onclick={() => nativeClose()}>
          <svg viewBox="0 0 10 10" fill="none"><path d="M3.2 3.2L6.8 6.8M6.8 3.2L3.2 6.8" stroke-width="1.1" stroke-linecap="round" /></svg>
        </button>
        <button class="traffic-light minimise" aria-label="Minimise" title="Minimise" onclick={() => nativeMinimise()}>
          <svg viewBox="0 0 10 10" fill="none"><path d="M3 5H7" stroke-width="1.1" stroke-linecap="round" /></svg>
        </button>
        <div class="maximise-wrap" onmouseenter={onMaximiseHoverEnter} onmouseleave={onMaximiseHoverLeave} role="presentation">
          <button
            class="traffic-light maximise"
            aria-label={isFullscreen ? $t('header.exit_fullscreen') : $t('header.enter_fullscreen')}
            title={isFullscreen ? $t('header.exit_fullscreen') : $t('header.enter_fullscreen')}
            onclick={flipFullscreen}
          >
            <svg viewBox="0 0 10 10" fill="none">
              {#if isFullscreen}
                <path d="M4.7 3.1H3.1V4.7M3.6 4.2L5.9 6.5M5.3 6.9H6.9V5.3" stroke-width="1.1" stroke-linecap="round" stroke-linejoin="round" />
              {:else}
                <path d="M3 5.8V7.4H4.6M4.2 6.2L6.5 3.9M5.8 2.6H7.4V4.2" stroke-width="1.1" stroke-linecap="round" stroke-linejoin="round" />
              {/if}
            </svg>
          </button>
          {#if showFullscreenMenu}
            <div class="fullscreen-menu" role="menu">
              <button
                role="menuitem"
                onclick={() => { showFullscreenMenu = false; flipFullscreen() }}
              >
                {isFullscreen ? $t('header.exit_fullscreen') : $t('header.fullscreen')}
              </button>
            </div>
          {/if}
        </div>
      </div>
    {/if}
    <button class="icon-btn" title={$t('header.toggle_sidebar')} onclick={cycleSidebar}>
      <iconify-icon icon="lucide:panel-left" width="16"></iconify-icon>
    </button>
    <div class="brand">
      <OctoLogo class="logo" size={26} />
      <span class="name">Octo</span>
      <span class="divider"></span>
      <span class="sub">{$t('nav.workbench')}</span>
    </div>
  </div>

  <div class="right">
    <!-- Visible on every view, not just ChatView, whose own inline banner only
         renders while a chat session is open — Settings/MCP/Skills/Tasks/etc.
         otherwise had no indication a dropped socket was silently failing
         their actions. -->
    {#if $wsState !== 'connected'}
      <button class="icon-btn" title={$t('chat.connection_lost')} onclick={() => ws.connect()}>
        <iconify-icon icon="ant-design:loading-outlined" width="16" style="color:var(--warning);animation:octo-spin 0.8s linear infinite"></iconify-icon>
      </button>
    {/if}
    <button class="search-pill" onclick={() => cmdkOpen.set(true)}>
      <iconify-icon icon="ant-design:search-outlined" width="14"></iconify-icon>
      <span>{$t('header.search_sessions')}</span>
      <kbd>⌘K</kbd>
    </button>
    <button class="icon-btn" class:active={$notificationsEnabled} title={$t('header.notifications')} aria-pressed={$notificationsEnabled} onclick={toggleNotifications}>
      <iconify-icon icon={$notificationsEnabled ? 'ant-design:bell-filled' : 'ant-design:bell-outlined'} width="16"></iconify-icon>
    </button>
    <button class="icon-btn" title={$t('nav.settings')} onclick={() => view.set('settings')}>
      <iconify-icon icon="ant-design:setting-outlined" width="16"></iconify-icon>
    </button>
    {#if $nativeShell && !isMac}
      <div class="window-controls">
        <button class="window-btn minimise" aria-label="Minimise" title="Minimise" onclick={() => nativeMinimise()}>−</button>
        <button class="window-btn maximise" aria-label={isMaximised ? 'Restore' : 'Maximise'} title={isMaximised ? 'Restore' : 'Maximise'} onclick={flipMaximise}>
          {isMaximised ? '❐' : '□'}
        </button>
        <button class="window-btn close" aria-label="Close" title="Close" onclick={() => nativeClose()}>×</button>
      </div>
    {/if}
  </div>
</header>

<style>
header {
  height: 56px;
  flex: 0 0 56px;
  background: var(--bg-container);
  border-bottom: 1px solid var(--border-secondary);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  z-index: 100;
}
/* The header is a window drag handle. Every interactive control on it opts
   back to no-drag so it stays clickable — the blank strips between controls
   drag the window. Applied for all platforms (frameless window now), not just
   macOS, since --wails-draggable only activates under Frameless: true. */
header .icon-btn,
header .search-pill,
header .brand,
header .traffic-lights,
header .window-controls { --wails-draggable: no-drag; }
.left, .right { display: flex; align-items: center; gap: 8px; }
.brand { display: flex; align-items: center; gap: 10px; padding-left: 4px; }
.brand :global(.logo) {
  color: var(--blue-6);
  flex: 0 0 auto;
}
.name { font-size: 15px; font-weight: 600; color: var(--text-heading); }
.divider { width: 1px; height: 16px; background: var(--border-secondary); }
.sub { font-size: 12px; color: var(--text-tertiary); }
.icon-btn {
  width: 32px; height: 32px; border: none; background: transparent;
  border-radius: 9999px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-secondary);
}
.icon-btn:hover { background: var(--hover-neutral); }
.icon-btn.active { color: var(--blue-6); }
.search-pill {
  display: flex; align-items: center; gap: 8px;
  height: 32px; padding: 0 6px 0 12px; width: 240px;
  background: var(--search-bg); border-radius: 9999px; cursor: pointer;
  color: var(--text-tertiary); border: none; font-family: inherit; font-size: 13px;
}
.search-pill:hover { background: var(--search-hover); }
.search-pill span { flex: 1; text-align: left; }
kbd {
  font-size: 11px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: var(--bg-container); border: 1px solid var(--border-secondary); border-radius: 4px;
  padding: 1px 5px; color: var(--text-tertiary);
}
/* Window controls: Windows/Linux get right-side controls; Mac gets traffic-light
   controls on the left (see .traffic-lights below). Isolated in their own visual
   group: a left separator line + 8px breathing room, so they never visually merge
   with the settings button to their left. Maximise icon flips □/❐ to reflect the
   window state. */
.window-controls {
  display: flex;
  align-items: center;
  gap: 0;
  margin-left: 8px;
  padding-left: 8px;
  border-left: 1px solid var(--border-secondary);
}
.window-btn {
  width: 40px; height: 32px; border: none; background: transparent;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-secondary);
  border-radius: 0;
}
.window-btn:hover { background: var(--hover-neutral); }
.window-btn.close:hover { background: #e81123; color: white; }

/* Mac traffic lights — frameless window, so the system traffic lights are gone
   and we draw our own in their accustomed top-left position. Each button shows
   a thin-stroke SVG glyph on hover (close/minimise/expand), matching native
   macOS's own hover glyphs rather than a bold unicode character. The green
   button's default click enters/exits true fullscreen (flipFullscreen), and
   its icon flips between the expand-diagonal and inward-arrows glyph to match. */
.traffic-lights {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-right: 10px;
}
.traffic-light {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 1px solid rgba(0, 0, 0, 0.06);
  padding: 0;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
}
.traffic-light svg {
  width: 100%;
  height: 100%;
  stroke: rgba(0, 0, 0, 0.55);
  opacity: 0;
  transition: opacity 0.1s ease;
}
.traffic-light:hover svg { opacity: 1; }
.traffic-light.close { background: #ff5f57; }
.traffic-light.minimise { background: #febc2e; }
.traffic-light.maximise { background: #28c840; }

/* Hover menu — a minimal stand-in for macOS's native window-tiling popup (which
   only exists for real NSWindow zoom buttons, not custom HTML ones): a single
   "Full Screen" action, since that's the one native capability the app wires
   up. Kept open briefly on mouseleave so the pointer can travel from the
   button down into the menu without it disappearing. */
.maximise-wrap { position: relative; display: flex; }
.fullscreen-menu {
  position: absolute;
  top: 100%;
  left: 50%;
  transform: translateX(-50%);
  margin-top: 6px;
  background: var(--bg-container);
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  padding: 4px;
  z-index: 200;
  white-space: nowrap;
}
.fullscreen-menu button {
  display: block;
  width: 100%;
  padding: 5px 12px;
  border: none;
  background: transparent;
  font-size: 12px;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  border-radius: 4px;
  color: var(--text);
}
.fullscreen-menu button:hover { background: var(--hover-neutral); }
</style>
