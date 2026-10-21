<script lang="ts">
  // Bottom action sheet for one session, opened by a long press on its card.
  import { flushSync } from 'svelte'
  import { sessions, pinnedSessions } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { sessionActionsFor, togglePin, renameSession, deleteSession, copySessionMarkdown } from './sessionActions'

  const id = $derived($sessionActionsFor)
  const session = $derived(id ? $sessions.find(s => s.id === id) : undefined)
  const pinned = $derived(!!id && $pinnedSessions.includes(id))

  // The sheet's step: the action list, the rename field, or the delete
  // confirmation. Kept in-sheet rather than window.prompt/confirm, which
  // WebViews don't all surface.
  let mode = $state<'menu' | 'rename' | 'delete'>('menu')
  let draft = $state('')
  let renameInput: HTMLInputElement | undefined = $state()

  function close() {
    sessionActionsFor.set(null)
  }

  // A session deleted while its sheet is open would otherwise leave the id
  // behind, and the sheet would pop back if a refetch briefly restored it.
  $effect(() => {
    if (id && !session) close()
  })

  function onKeydown(e: KeyboardEvent) {
    if (id && e.key === 'Escape') close()
  }

  // The long press that opened the sheet ends with a click at the finger's
  // position, which now lands on the sheet or its backdrop — on a card near
  // the bottom, right on an action. Only a tap that also started inside the
  // sheet or backdrop counts.
  let pressed = false
  $effect(() => {
    if (id) {
      pressed = false
      mode = 'menu'
    }
  })
  function tapped(fn: () => void) {
    return (e: MouseEvent) => {
      // detail 0 is a keyboard activation, which no stray release can fake.
      if (pressed || e.detail === 0) fn()
      pressed = false
    }
  }

  function pin() {
    if (!id) return
    const target = id
    const next = !pinned
    close()
    void togglePin(target, next)
  }

  function startRename() {
    draft = session?.name || session?.title || ''
    // Render the field now and focus it inside the tap: iOS only raises the
    // keyboard for a focus made during a user gesture.
    flushSync(() => { mode = 'rename' })
    renameInput?.focus()
    renameInput?.select()
  }

  function saveRename() {
    if (!id) return
    const target = id
    const name = draft
    close()
    void renameSession(target, name)
  }

  function copyMarkdown() {
    if (!id) return
    const target = id
    const title = session?.title || session?.name || $t('m.untitled')
    close()
    void copySessionMarkdown(target, title)
  }

  function confirmDelete() {
    if (!id) return
    const target = id
    close()
    void deleteSession(target)
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if session}
  <div class="backdrop" role="presentation" onpointerdown={() => (pressed = true)} onclick={tapped(close)}></div>
  <div class="sheet" role="dialog" aria-modal="true" aria-label={session.title || session.name || $t('m.untitled')} onpointerdown={() => (pressed = true)}>
    <p class="title">{session.title || session.name || $t('m.untitled')}</p>
    {#if mode === 'rename'}
      <form class="group form" onsubmit={(e) => { e.preventDefault(); saveRename() }}>
        <input bind:this={renameInput} bind:value={draft} aria-label={$t('sidebar.rename')} enterkeyhint="done" />
        <button class="act strong" type="submit" disabled={!draft.trim()}>{$t('common.save')}</button>
      </form>
    {:else if mode === 'delete'}
      <div class="group">
        <p class="warn">{$t('sidebar.confirm_delete')}</p>
        <button class="act danger" onclick={tapped(confirmDelete)}>{$t('common.delete')}</button>
      </div>
    {:else}
      <div class="group">
        <button class="act" onclick={tapped(pin)}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 17v5M9 10.8V4h6v6.8l3 3.2v2H6v-2z"/></svg>
          <span>{pinned ? $t('m.unpin') : $t('m.pin')}</span>
        </button>
        <button class="act" onclick={tapped(startRename)}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>
          <span>{$t('sidebar.rename')}</span>
        </button>
        <button class="act" onclick={tapped(copyMarkdown)}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/></svg>
          <span>{$t('m.copy_markdown')}</span>
        </button>
        <button class="act danger" onclick={tapped(() => (mode = 'delete'))}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/></svg>
          <span>{$t('common.delete')}</span>
        </button>
      </div>
    {/if}
    <button class="cancel" onclick={tapped(close)}>{$t('m.cancel')}</button>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 2000;
    background: rgba(0, 0, 0, .35);
  }
  .sheet {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 2001;
    padding: 12px 12px calc(12px + env(safe-area-inset-bottom));
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .title {
    margin: 0 4px 2px;
    font-size: 13px;
    color: #fff;
    text-align: center;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .group, .cancel {
    background: var(--m-surface);
    border-radius: 14px;
    overflow: hidden;
  }
  .act, .cancel {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    width: 100%;
    min-height: 52px;
    border: none;
    background: none;
    font: inherit;
    font-size: 16px;
    color: var(--m-text);
    cursor: pointer;
  }
  .cancel {
    background: var(--m-surface);
    font-weight: 600;
  }
  .group .act + .act { border-top: 1px solid var(--m-divider); }
  .act.danger { color: var(--m-error); }
  .act.strong { font-weight: 600; color: var(--m-accent); }
  .act:disabled { opacity: .4; cursor: default; }
  .form { display: flex; flex-direction: column; }
  .form input {
    margin: 12px 12px 4px;
    padding: 10px 12px;
    border: 1px solid var(--m-border);
    border-radius: 10px;
    background: var(--m-bg);
    color: var(--m-text);
    font: inherit;
    font-size: 16px;
    outline: none;
  }
  .warn {
    margin: 0;
    padding: 14px 16px 10px;
    font-size: 13px;
    color: var(--m-text-2);
    text-align: center;
  }
</style>
