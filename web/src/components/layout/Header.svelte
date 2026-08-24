<script lang="ts">
  import { onMount } from 'svelte'
  import { sidebar, nativeShell, panelContent, view, chatHeaderSnippet, activeSessionId, readPanelMode } from '../../lib/stores'
  import { diffBadge } from '../../lib/diff'
  import { t } from '../../lib/i18n'
  import { ws, wsState } from '../../lib/ws'
  import { nativeMinimise, nativeClose } from '../../lib/api'
  import { isMaximised, flipMaximise, refreshMaximised, titlebarDblClick } from '../../lib/nativeWindow'

  // The main column's own top row. There is no window-spanning title bar: each
  // of the three columns (Sidebar, this, ArtifactsPanel) starts at the very top
  // and carries its own chrome, separated only by the vertical dividers that
  // run the full height. So this row spans this column and nothing else.
  function toggleSidebar() {
    sidebar.update(s => s === 'hidden' ? 'full' : 'hidden')
  }

  // Both toggles live in the column they reveal — the sidebar's inside Sidebar's
  // own header, the panel's at the panel's own left edge. Each falls back to
  // this row only while its column is gone, since a control inside a hidden
  // column can't be clicked to bring it back.
  //
  // It opens the panel in whichever mode was last used, and stays a plain
  // open/close toggle: choosing the mode is the panel's own job, from its
  // topbar. What this button does gain is a badge — with the panel closed,
  // nothing else can say the agent left changes to review.
  function openPanel() {
    panelContent.set(readPanelMode())
  }

  const diffCount = $derived($diffBadge[$activeSessionId ?? ''] ?? 0)

  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform)
  // Mac's traffic lights float over the window's top-left corner. That corner
  // is Sidebar's header while the sidebar is showing (it insets itself), and
  // this row only once the sidebar is gone.
  const insetForTrafficLights = $derived($nativeShell && isMac && $sidebar === 'hidden')
  // Making room for the lights is one thing; sitting on the same axis as them is
  // another, and this row needs the second one whether or not it holds the first.
  // While the sidebar shows, the lights are over ITS header — but if only that
  // column lifted its content, the brand row and this row's chat title would sit
  // 4px apart.
  const liftForTrafficLights = $derived($nativeShell && isMac)

  // The □/❐ icon reflects maximise state the frontend owns — there's no native
  // title bar reading it. This row holds the only copy of that icon, so it is
  // also where the OS gets polled for it: on mount and on window focus (which
  // catches Aero Snap / keyboard maximize / taskbar restore the frontend can't
  // otherwise observe). The toggle itself lives in lib/nativeWindow because the
  // sidebar's titlebar flips it too.

  onMount(() => {
    if (!$nativeShell) return // web mode has no native bridge — skip entirely
    refreshMaximised()
    const onFocus = () => refreshMaximised()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  })
</script>

<header class:native-inset={insetForTrafficLights} class:native-lift={liftForTrafficLights} style="--wails-draggable:drag" ondblclick={titlebarDblClick}>
  {#if $sidebar === 'hidden'}
    <button class="icon-btn" title={$t('header.toggle_left')} aria-pressed={false} onclick={toggleSidebar}>
      <iconify-icon icon="lucide:panel-left" width="16"></iconify-icon>
    </button>
  {/if}

  <!-- ChatView registers its own title/status/actions here (via the store) so
       they share this row instead of adding a second one below it. Every other
       view has no snippet to render and falls back to a spacer — their own
       page header stays exactly where it already was. -->
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
    <button class="icon-btn panel-btn" title={$t('header.toggle_right')} onclick={openPanel}>
      <iconify-icon icon="lucide:panel-right" width="16"></iconify-icon>
      {#if diffCount > 0}<span class="dot"></span>{/if}
    </button>
  {/if}

  {#if $nativeShell && !isMac}
    <div class="window-controls">
      <button class="window-btn minimise" aria-label="Minimise" title="Minimise" onclick={() => nativeMinimise()}>−</button>
      <button class="window-btn maximise" aria-label={$isMaximised ? 'Restore' : 'Maximise'} title={$isMaximised ? 'Restore' : 'Maximise'} onclick={flipMaximise}>
        {$isMaximised ? '❐' : '□'}
      </button>
      <button class="window-btn close" aria-label="Close" title="Close" onclick={() => nativeClose()}>×</button>
    </div>
  {/if}
</header>

<style>
/* No bottom border and no background of its own: this row is the top of the
   main column's own surface, not a bar laid across it — the only lines in the
   layout are the vertical dividers between columns. min-height rather than a
   fixed height so the chat header it hosts sizes the row instead of
   overflowing a shorter one. */
header {
  flex: 0 0 auto;
  min-height: 44px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  z-index: 20;
}
/* Only while the sidebar is hidden — see insetForTrafficLights: horizontal room
   for the lights, nothing more. */
header.native-inset { padding-left: 82px; }
/* Lifting the axis is the other half, and max-height is the load-bearing part of
   it. Padding-bottom alone only moves the axis while min-height still decides the
   row height; .chat-header-slot is taller than the 36px that would leave, so the
   row grew to 52px instead and the axis never moved. Pinning the height makes the
   padding actually shorten the content box.
   The lights' centre sits 20px below the window's top edge, so 4px of padding is
   the whole lift: (44 - 4) / 2 lands the row's axis exactly there. */
header.native-lift {
  box-sizing: border-box; max-height: 44px; padding-bottom: 4px;
}
/* The row is a window drag handle; every control opts back out so it stays
   clickable, leaving the blank stretches to drag the window. */
header .icon-btn,
header .window-controls { --wails-draggable: no-drag; }

.spacer { flex: 1; }
/* ChatView's own .chat-header is already display:flex with its own
   justify-content:space-between, so as a block child it fills this slot the
   ordinary way — none of its own CSS has to change to live in this row. */
.chat-header-slot { flex: 1; min-width: 0; }

.icon-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: var(--radius-sm); display: grid; place-items: center;
  cursor: pointer; color: var(--text-secondary); flex: 0 0 auto;
}
.icon-btn:hover { background: var(--hover-neutral); color: var(--text); }
/* Uncommitted changes waiting behind a closed panel. A dot, not a count: the
   button's job is still to open the panel, and a number here would read as
   something to act on in the header itself. */
.panel-btn { position: relative; }
.panel-btn .dot {
  position: absolute; top: 4px; right: 4px; width: 6px; height: 6px;
  border-radius: 50%; background: var(--blue-6);
}

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
