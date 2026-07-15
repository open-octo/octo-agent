<script lang="ts">
  import { onMount } from 'svelte'
  import { view, cmdkOpen, sidebar, nativeShell } from '../../lib/stores'
  import { t } from '../../lib/i18n'
  import { ws, wsState } from '../../lib/ws'
  import { notificationsEnabled, setNotificationsEnabled } from '../../lib/notifications'
  import { nativeToggleMaximise, nativeMinimise, nativeClose, nativeWindowState } from '../../lib/api'
  import OctoLogo from './OctoLogo.svelte'

  function cycleSidebar() {
    sidebar.update(s => s === 'full' ? 'rail' : s === 'rail' ? 'hidden' : 'full')
  }

  // The bell toggles desktop notifications on/off — the same preference the
  // "Desktop Notifications" switch in Settings drives. There is no feed.
  function toggleNotifications() {
    setNotificationsEnabled(!$notificationsEnabled)
  }

  // Frameless window: Mac gets a hidden drag strip (InvisibleTitleBarHeight)
  // that restores the native traffic-light behaviour, so we skip the custom
  // buttons there. Windows/Linux have no native buttons, so we draw our own.
  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform)

  // Desktop only: double-clicking the draggable header zooms the window, the way
  // a native title bar does. Wails' custom drag region doesn't wire this up, and
  // the octo-served page can't call Wails directly, so it goes through the native
  // bridge over HTTP. Ignore double-clicks that land on a control.
  function onHeaderDblClick(e: MouseEvent) {
    if (!$nativeShell || isMac) return
    if ((e.target as HTMLElement).closest('button, a, input, select, textarea')) return
    flipMaximise()
  }

  // Track maximise state so the icon flips between □ (maximise) and ❐ (restore).
  // The frontend owns this state — there's no native title bar reading it. We
  // sync from the OS on mount, on window focus (catches Aero Snap / keyboard
  // maximize / taskbar restore the frontend can't otherwise observe), and after
  // every toggle so the icon always reflects reality.
  let isMaximised = false
  async function flipMaximise() {
    const next = !isMaximised
    try {
      await nativeToggleMaximise()
      isMaximised = next
    } catch {
      // Keep current state on failure so the icon never desyncs from reality.
    }
  }

  onMount(async () => {
    isMaximised = await nativeWindowState()
    const onFocus = async () => { isMaximised = await nativeWindowState() }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  })
</script>

<header class:native-inset={$nativeShell && isMac} style="--wails-draggable:drag" ondblclick={onHeaderDblClick}>
  <div class="left">
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
/* Desktop shell (macOS): the hidden-inset title bar floats the traffic-light
   buttons over the top-left, so inset the header past them. */
header.native-inset { padding-left: 82px; }
/* The header is a window drag handle. Every interactive control on it opts
   back to no-drag so it stays clickable — the blank strips between controls
   drag the window. Applied for all platforms (frameless window now), not just
   macOS, since --wails-draggable only activates under Frameless: true. */
header .icon-btn,
header .search-pill,
header .brand { --wails-draggable: no-drag; }
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
/* Window controls (Windows/Linux only — Mac uses native traffic lights via
   InvisibleTitleBarHeight). Isolated in their own visual group: a left separator
   line + 16px breathing room, so they never visually merge with the settings
   button to their left. Maximise icon flips □/❐ to reflect the window state. */
.window-controls {
  display: flex;
  align-items: center;
  gap: 0;
  margin-left: 16px;
  padding-left: 16px;
  border-left: 1px solid var(--border-secondary);
  --wails-draggable: no-drag;
}
.window-btn {
  width: 40px; height: 32px; border: none; background: transparent;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-secondary);
  border-radius: 0;
}
.window-btn:hover { background: var(--hover-neutral); }
.window-btn.close:hover { background: #e81123; color: white; }
</style>
