<script lang="ts">
  // Bottom action sheet for one session, opened by a long press on its card.
  import { sessions, pinnedSessions } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { sessionActionsFor, togglePin } from './sessionActions'

  const id = $derived($sessionActionsFor)
  const session = $derived(id ? $sessions.find(s => s.id === id) : undefined)
  const pinned = $derived(!!id && $pinnedSessions.includes(id))

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
    if (id) pressed = false
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
</script>

<svelte:window onkeydown={onKeydown} />

{#if session}
  <div class="backdrop" role="presentation" onpointerdown={() => (pressed = true)} onclick={tapped(close)}></div>
  <div class="sheet" role="dialog" aria-modal="true" aria-label={session.title || session.name || $t('m.untitled')} onpointerdown={() => (pressed = true)}>
    <p class="title">{session.title || session.name || $t('m.untitled')}</p>
    <div class="group">
      <button class="act" onclick={tapped(pin)}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 17v5M9 10.8V4h6v6.8l3 3.2v2H6v-2z"/></svg>
        <span>{pinned ? $t('m.unpin') : $t('m.pin')}</span>
      </button>
    </div>
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
</style>
