<script lang="ts">
  import { onMount } from 'svelte'
  import { cmdkOpen, sidebar, nativeShell, panelContent, settingsModalOpen } from '../../lib/stores'
  import { t } from '../../lib/i18n'
  import { ws, wsState } from '../../lib/ws'
  import { notificationsEnabled, setNotificationsEnabled } from '../../lib/notifications'
  import { nativeToggleMaximise, nativeMinimise, nativeClose, nativeWindowState } from '../../lib/api'
  import OctoLogo from './OctoLogo.svelte'

  // The title-bar toggle is a plain show/hide (the redesign has no manual rail
  // state); 'rail' remains reachable only through the responsive auto-collapse
  // in Sidebar, so toggling from rail expands back to full.
  function toggleSidebar() {
    sidebar.update(s => s === 'hidden' ? 'full' : 'hidden')
  }

  // Toggle the Artifacts panel sidebar. Always the artifacts pane — with no
  // session selected that's just its empty state, same as a session with none.
  function togglePanel() {
    const cur = $panelContent
    if (cur) { panelContent.set(null); return }
    panelContent.set('session')
  }

  // The bell toggles desktop notifications on/off — the same preference the
  // "Desktop Notifications" switch in Settings drives. There is no feed.
  function toggleNotifications() {
    setNotificationsEnabled(!$notificationsEnabled)
  }

  // Mac keeps its native title bar (see bridge.go's MacTitleBarHiddenInset), so
  // the real NSWindow traffic lights render themselves, inset top-left — the
  // header just insets its own content past them (native-inset below).
  // Windows/Linux are Frameless, so the frontend draws its own right-side
  // window controls; the CSS --wails-draggable header region (framelessDrag.ts)
  // handles dragging there, and the native bridge handles minimise/maximise/close.
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

  // Track maximise state so the icon flips between □ (maximise) and ❐ (restore).
  // The frontend owns this state — there's no native title bar reading it. We
  // sync from the OS on mount, on window focus (catches Aero Snap / keyboard
  // maximize / taskbar restore the frontend can't otherwise observe), and after
  // every toggle so the icon always reflects reality. A sequence counter
  // prevents a stale focus response from overwriting a fresh toggle result.
  let isMaximised = false
  let stateSeq = 0
  async function refreshMaximised() {
    const seq = ++stateSeq
    const m = await nativeWindowState()
    if (seq === stateSeq) isMaximised = m
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
      await refreshMaximised()
    }
  }

  onMount(() => {
    if (!$nativeShell) return // web mode has no native bridge — skip entirely
    refreshMaximised()
    const onFocus = () => refreshMaximised()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  })
</script>

<header class:native-inset={$nativeShell && isMac} style="--wails-draggable:drag" ondblclick={onHeaderDblClick}>
  <!-- Left: sidebar toggle + brand (on desktop mac they sit after the native
       traffic lights' inset). -->
  <button class="icon-btn" title={$t('header.toggle_left')} aria-pressed={$sidebar !== 'hidden'} onclick={toggleSidebar}>
    <iconify-icon icon="lucide:panel-left" width="16"></iconify-icon>
  </button>
  <div class="brand">
    <OctoLogo class="logo" size={22} />
    <span class="name">Octo</span>
    <span class="brand-divider"></span>
    <span class="sub">{$t('nav.workbench')}</span>
  </div>

  <span class="spacer"></span>

  <!-- Visible on every view, not just ChatView, whose own inline banner only
       renders while a chat session is open — Settings/MCP/Skills/Tasks/etc.
       otherwise had no indication a dropped socket was silently failing
       their actions. -->
  {#if $wsState !== 'connected'}
    <button class="icon-btn" title={$t('chat.connection_lost')} onclick={() => ws.connect()}>
      <iconify-icon icon="ant-design:loading-outlined" width="16" style="color:var(--warning);animation:octo-spin 0.8s linear infinite"></iconify-icon>
    </button>
  {/if}

  <button class="search-btn" title={$t('header.search_sessions')} onclick={() => cmdkOpen.set(true)}>
    <iconify-icon icon="ant-design:search-outlined" width="15"></iconify-icon>
    <span class="label">{$t('header.search_sessions')}</span>
    <kbd>⌘K</kbd>
  </button>

  <div class="ptoggle-group">
    <button
      class="ptoggle"
      class:on={$panelContent !== null}
      title={$t('header.toggle_right')}
      aria-pressed={$panelContent !== null}
      onclick={togglePanel}
    >
      <iconify-icon icon="lucide:panel-right" width="16"></iconify-icon>
    </button>
  </div>

  <span class="divider"></span>

  <button class="icon-btn" class:active={$notificationsEnabled} title={$t('header.notifications')} aria-pressed={$notificationsEnabled} onclick={toggleNotifications}>
    <iconify-icon icon={$notificationsEnabled ? 'ant-design:bell-filled' : 'ant-design:bell-outlined'} width="17"></iconify-icon>
  </button>
  <button class="icon-btn" class:active={$settingsModalOpen} title={$t('nav.settings')} onclick={() => settingsModalOpen.set(true)}>
    <iconify-icon icon="ant-design:setting-outlined" width="17"></iconify-icon>
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
</header>

<style>
header {
  height: 48px;
  flex: 0 0 48px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--titlebar-frost);
  backdrop-filter: blur(var(--frost-blur));
  -webkit-backdrop-filter: blur(var(--frost-blur));
  z-index: 100;
}
/* Mac's native hidden-inset title bar floats the real traffic-light buttons
   over the top-left of the content area, so inset the header past them.
   Their vertical position is fixed by macOS (not by our header height), and
   it sits above this row's flex-centered line — padding-top would only push
   the content further down, away from the lights, so pull the centering axis
   up instead via padding-bottom (shrinks the box from the bottom, which
   raises the align-items:center midpoint without moving the header's own
   background/border box). Value confirmed by pixel-measuring a live build's
   traffic-light center against the content center, not eyeballed. */
header.native-inset { padding-left: 82px; padding-bottom: 8px; }
/* The header is a window drag handle. Every interactive control on it opts
   back to no-drag so it stays clickable — the blank strips between controls
   drag the window. Applied for all platforms (frameless window now), not just
   macOS, since --wails-draggable only activates under Frameless: true. */
header .icon-btn,
header .search-btn,
header .ptoggle-group,
header .brand,
header .window-controls { --wails-draggable: no-drag; }

.spacer { flex: 1; }
.brand { display: flex; align-items: center; gap: 9px; padding-left: 2px; }
.brand :global(.logo) { color: var(--blue-6); flex: 0 0 auto; }
.name { font-size: 14px; font-weight: 600; color: var(--text-heading); }
.brand-divider { width: 1px; height: 15px; background: var(--border); }
.sub { font-size: 12px; color: var(--text-tertiary); white-space: nowrap; }

.search-btn {
  display: flex; align-items: center; gap: 7px;
  height: 30px; padding: 0 9px;
  background: transparent; border: none; border-radius: var(--radius-sm);
  color: var(--text-secondary); cursor: pointer; font-family: inherit;
  /* The header can't wrap (fixed 48px) and every other control is
     fixed-width, so the label was the only thing that could give: it wrapped
     to several lines inside the 30px-tall button and spilled over the
     header's edge into the view below. */
  flex: 0 0 auto; white-space: nowrap;
}
.search-btn:hover { background: var(--hover-neutral); color: var(--text); }
kbd { font-size: 11px; font-family: var(--font-mono); }

/* Narrow windows: nothing here shrinks, so shed the label-only decoration
   instead of letting controls collide. Steps reuse the widths the sidebar
   already switches on (Sidebar.svelte: rail below 860, hidden below 640). */
@media (max-width: 860px) {
  .sub, .brand-divider { display: none; }
  .search-btn kbd { display: none; }
}
@media (max-width: 640px) {
  /* Icon-only from here — the title attribute carries the meaning, the same
     way ChatView's header buttons drop their labels below 680px. */
  .search-btn .label { display: none; }
  .search-btn { padding: 0 7px; }
}

.ptoggle-group {
  display: inline-flex; gap: 2px; padding: 2px;
  background: var(--control-track); border-radius: var(--radius-sm);
}
.ptoggle {
  width: 30px; height: 26px; display: grid; place-items: center;
  border: none; background: transparent; border-radius: 6px;
  cursor: pointer; color: var(--text-secondary); transition: 0.12s;
}
.ptoggle:hover { color: var(--text); }
.ptoggle.on {
  background: var(--bg-container); color: var(--blue-6);
  box-shadow: 0 1px 2px rgba(0,0,0,0.14);
}

.divider { width: 1px; height: 18px; background: var(--border); }

.icon-btn {
  width: 30px; height: 30px; border: none; background: transparent;
  border-radius: var(--radius-sm); display: grid; place-items: center;
  cursor: pointer; color: var(--text-secondary);
}
.icon-btn:hover { background: var(--hover-neutral); color: var(--text); }
.icon-btn.active { color: var(--blue-6); }

/* Window controls (Windows/Linux only — Mac uses native traffic lights via the
   hidden-inset title bar). Stretch to the bar height and bleed into the right
   padding so the hit area reaches the window edge. Maximise icon flips □/❐ to
   reflect the window state. */
.window-controls {
  display: flex;
  align-self: stretch;
  align-items: stretch;
  gap: 0;
  margin-left: 4px;
  margin-right: -16px;
}
.window-btn {
  width: 46px; border: none; background: transparent;
  display: grid; place-items: center;
  cursor: pointer; color: var(--text-secondary);
  border-radius: 0; font-size: 14px;
}
.window-btn:hover { background: var(--hover-neutral); color: var(--text); }
.window-btn.close:hover { background: #e81123; color: white; }
</style>
