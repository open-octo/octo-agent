<script lang="ts">
  import { onMount } from 'svelte'
  import { sidebar, nativeShell, panelContent, view, chatHeaderSnippet } from '../../lib/stores'
  import { t } from '../../lib/i18n'
  import { ws, wsState } from '../../lib/ws'
  import { nativeToggleMaximise, nativeMinimise, nativeClose, nativeWindowState } from '../../lib/api'

  // The title-bar toggle is a plain show/hide (the redesign has no manual rail
  // state); 'rail' remains reachable only through the responsive auto-collapse
  // in Sidebar, so toggling from rail expands back to full.
  function toggleSidebar() {
    sidebar.update(s => s === 'hidden' ? 'full' : 'hidden')
  }

  // Toggle the Artifacts panel. Only rendered here while it's closed — once
  // open, the equivalent control lives at the panel's own left edge
  // (ArtifactsPanel's topbar), the same way this bar's own sidebar toggle sits
  // at the boundary of the block it reveals rather than inside it.
  function togglePanel() {
    panelContent.set('session')
  }

  // This is the one bar that spans the whole window — everything else (brand,
  // search, notifications, settings) lives in Sidebar's own header instead, so
  // this carries only what has to reach across every block: the sidebar
  // toggle (has to work with the sidebar itself hidden), the artifacts toggle
  // (same reasoning, mirrored), the connection indicator, and desktop chrome.
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
  <button class="icon-btn" title={$t('header.toggle_left')} aria-pressed={$sidebar !== 'hidden'} onclick={toggleSidebar}>
    <iconify-icon icon="lucide:panel-left" width="16"></iconify-icon>
  </button>

  <!-- ChatView registers its own title/status/actions here (via the store) so
       they render in this shared row instead of a second row below it. Every
       other view has no snippet to render, so this just falls back to a
       spacer — their own page header stays exactly where it already was. -->
  {#if $view === 'chat' && $chatHeaderSnippet}
    <div class="chat-header-slot">{@render $chatHeaderSnippet()}</div>
  {:else}
    <span class="spacer"></span>
  {/if}

  <!-- Visible on every view, not just ChatView, whose own inline banner only
       renders while a chat session is open — Settings/MCP/Skills/Tasks/etc.
       otherwise had no indication a dropped socket was silently failing
       their actions. -->
  {#if $wsState !== 'connected'}
    <button class="icon-btn" title={$t('chat.connection_lost')} onclick={() => ws.connect()}>
      <iconify-icon icon="ant-design:loading-outlined" width="16" style="color:var(--warning);animation:octo-spin 0.8s linear infinite"></iconify-icon>
    </button>
  {/if}

  {#if !$panelContent}
    <button class="icon-btn" title={$t('header.toggle_right')} onclick={togglePanel}>
      <iconify-icon icon="lucide:panel-right" width="16"></iconify-icon>
    </button>
  {/if}

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
  height: 40px;
  flex: 0 0 40px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  border-bottom: 1px solid var(--border-secondary);
  background: var(--titlebar-frost);
  backdrop-filter: blur(var(--frost-blur));
  -webkit-backdrop-filter: blur(var(--frost-blur));
  z-index: 100;
}
/* Mac's native hidden-inset title bar floats the real traffic-light buttons
   over the top-left of the window, so inset this bar's content past them —
   this is the leftmost thing on screen again, now that it spans the full
   width. Padding-bottom (not padding-top) pulls the centering axis up to meet
   their fixed vertical position, the same reasoning as before the redesign. */
header.native-inset { padding-left: 82px; padding-bottom: 8px; }
/* The header is a window drag handle. Every interactive control on it opts
   back to no-drag so it stays clickable — the blank strip is what drags the
   window. */
header .icon-btn,
header .window-controls { --wails-draggable: no-drag; }

.spacer { flex: 1; }
/* Plain block, not flex — ChatView's own .chat-header is already display:flex
   with its own justify-content:space-between; as a block child here it fills
   this slot's width the ordinary block-layout way, so none of its own CSS
   needs to change to live in someone else's row. */
.chat-header-slot { flex: 1; min-width: 0; }

.icon-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: var(--radius-sm); display: grid; place-items: center;
  cursor: pointer; color: var(--text-secondary); flex: 0 0 auto;
}
.icon-btn:hover { background: var(--hover-neutral); color: var(--text); }

/* Window controls (Windows/Linux only — Mac uses native traffic lights). */
.window-controls {
  display: flex;
  align-self: stretch;
  align-items: stretch;
  gap: 0;
  margin-left: 4px;
  margin-right: -10px;
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
