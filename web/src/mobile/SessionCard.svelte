<script lang="ts">
  import { projectOf, type FeedItem, type FeedKind } from './feedGroups'
  import { sessionActionsFor } from './sessionActions'
  import { pinnedSessions } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { ago, clockTick } from '../lib/relTime'

  // showProject is off inside a project's own page, where every card would
  // repeat the page title.
  let { item, onOpen, showProject = true }: {
    item: FeedItem
    onOpen: (id: string, kind: FeedKind) => void
    showProject?: boolean
  } = $props()

  const s = $derived(item.session)
  const title = $derived(s.title || s.name || $t('m.untitled'))
  const project = $derived(showProject ? $projectOf.get(s.id) : undefined)
  const pinned = $derived($pinnedSessions.includes(s.id))

  // Long press opens the session's action sheet. A timer covers iOS, whose
  // WebView fires no contextmenu on touch; Android's WebView fires contextmenu
  // on a long press, which opens the sheet too — whichever comes first wins.
  // The click that ends a long press must not also open the session.
  const LONG_PRESS_MS = 500
  const MOVE_TOLERANCE = 10
  let timer: ReturnType<typeof setTimeout> | undefined
  let startX = 0
  let startY = 0
  let longPressed = false
  // A card can unmount mid-press (its session changes section); drop the timer
  // with it.
  $effect(() => () => clearTimeout(timer))

  function openActions() {
    clearTimeout(timer)
    if (longPressed) return
    longPressed = true
    sessionActionsFor.set(s.id)
  }
  function onDown(e: PointerEvent) {
    longPressed = false
    startX = e.clientX
    startY = e.clientY
    clearTimeout(timer)
    timer = setTimeout(openActions, LONG_PRESS_MS)
  }
  function onMove(e: PointerEvent) {
    if (Math.abs(e.clientX - startX) > MOVE_TOLERANCE || Math.abs(e.clientY - startY) > MOVE_TOLERANCE) clearTimeout(timer)
  }
  function onClick() {
    // Swallow only the one click that ends the long press, so a later
    // keyboard activation (no pointerdown to reset it) still opens.
    if (longPressed) {
      longPressed = false
      return
    }
    onOpen(s.id, item.kind)
  }
  function onContextMenu(e: MouseEvent) {
    e.preventDefault()
    // A keyboard context-menu key comes with no pointerdown either.
    longPressed = false
    openActions()
  }
</script>

<button
  class="card"
  class:approval={item.kind === 'approval'}
  class:reply={item.kind === 'reply'}
  onclick={onClick}
  onpointerdown={onDown}
  onpointermove={onMove}
  onpointerup={() => clearTimeout(timer)}
  onpointercancel={() => clearTimeout(timer)}
  onpointerleave={() => clearTimeout(timer)}
  oncontextmenu={onContextMenu}
>
  <div class="top">
    <span class="head">
      {#if pinned}
        <svg class="pin" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" role="img" aria-label={$t('m.pinned')}><path d="M12 17v5M9 10.8V4h6v6.8l3 3.2v2H6v-2z"/></svg>
      {/if}
      <span class="title">{title}</span>
    </span>
    {#if item.kind === 'reply'}
      <span class="tag tag-accent">{$t('m.tag_reply')}</span>
    {:else if item.kind === 'approval'}
      <span class="tag tag-warn">{$t('m.tag_approval')}</span>
    {:else if item.kind === 'running'}
      <span class="pulse"></span>
    {:else}
      <svg class="done" width="18" height="18" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10" fill="var(--m-success)"/><path d="m8 12 2.5 2.5L16 9" stroke="#fff" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
    {/if}
  </div>
  <div class="meta">
    {#if project}
      <span class="proj">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
        <span class="pname">{project}</span>
      </span>
      <span class="dot">·</span>
    {/if}
    {#if item.kind === 'running'}
      <span class="running">{$t('m.card_running')}<span class="blink">•••</span></span>
    {:else}
      <span class="mono">{s.model || s.model_id || ''}</span>
    {/if}
    <span class="dot">·</span>
    <span>{ago(s.updated_at, $t, $clockTick)}</span>
  </div>
</button>

<style>
  .card {
    display: block;
    width: 100%;
    text-align: left;
    background: var(--m-surface);
    border: 1px solid transparent;
    border-radius: 14px;
    padding: 14px 16px;
    margin-bottom: 12px;
    cursor: pointer;
    font-family: inherit;
    box-shadow: var(--m-shadow-card);
    /* Keep a long press from selecting text or raising iOS's link callout. */
    -webkit-user-select: none;
    user-select: none;
    -webkit-touch-callout: none;
  }
  .head { display: flex; align-items: center; gap: 6px; min-width: 0; }
  .pin { flex: none; color: var(--m-text-3); }
  .card.approval { border-color: var(--m-tag-warn-border); }
  .card.reply { border-color: var(--m-accent); }
  .top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
  }
  .title {
    font-size: 15px;
    font-weight: 600;
    color: var(--m-text);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .tag {
    flex: none;
    font-size: 11px;
    font-weight: 600;
    border-radius: 4px;
    padding: 2px 7px;
  }
  .tag-accent { color: #fff; background: var(--m-accent); }
  .tag-warn {
    color: var(--m-tag-warn-text);
    background: var(--m-tag-warn-bg);
    border: 1px solid var(--m-tag-warn-border);
  }
  .done { flex: none; }
  .pulse {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--m-accent);
    position: relative;
  }
  .pulse::after {
    content: "";
    position: absolute;
    inset: -4px;
    border-radius: 50%;
    border: 2px solid var(--m-accent);
    animation: pr 1.6s ease-out infinite;
  }
  @keyframes pr { 0% { transform: scale(.6); opacity: .8 } 100% { transform: scale(1.8); opacity: 0 } }
  .meta {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 8px;
    font-size: 12px;
    color: var(--m-text-3);
  }
  .running { color: var(--m-accent); }
  .blink { animation: bk 1.1s steps(1) infinite; letter-spacing: 2px; margin-left: 3px; }
  @keyframes bk { 50% { opacity: .2 } }
  .mono { font-family: ui-monospace, Menlo, monospace; }
  .proj { display: inline-flex; align-items: center; gap: 4px; min-width: 0; max-width: 45%; color: var(--m-text-2); }
  .proj svg { flex: none; }
  .pname { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  .dot { color: var(--m-text-4); }
  @media (prefers-reduced-motion: reduce) {
    .pulse::after, .blink { animation: none; }
  }
</style>
