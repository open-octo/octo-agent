<script lang="ts">
  // Edit a sent message: the server cuts the transcript just before it,
  // interrupting a running turn, and reruns with the new text (saving it
  // unchanged reruns it as is). Opened by a long press on a user bubble.
  import { flushSync, onMount } from 'svelte'
  import { t } from '../lib/i18n'

  let { content, onSave, onClose }: {
    content: string
    onSave: (text: string) => Promise<boolean>
    onClose: () => void
  } = $props()

  // svelte-ignore state_referenced_locally
  let draft = $state(content)
  let busy = $state(false)
  let field: HTMLTextAreaElement | undefined = $state()

  // Focus the field at once. A long press opens this from a timer, which is
  // not a user gesture, so iOS may still want a tap before raising the keyboard.
  onMount(() => {
    flushSync()
    field?.focus()
  })

  // The long press that opened the sheet ends with a click at the finger's
  // position, which now lands on the backdrop or a button — on the newest
  // message, right above the composer, that is Save. Only a tap that also
  // started on the sheet or backdrop counts (SessionActions' guard).
  let pressed = false
  function tapped(fn: () => void) {
    return (e: MouseEvent) => {
      // detail 0 is a keyboard activation, which no stray release can fake.
      if (e.detail !== 0 && !pressed) return
      fn()
    }
  }

  async function save() {
    if (busy || !draft.trim()) return
    busy = true
    const ok = await onSave(draft.trim())
    busy = false
    if (ok) onClose()
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') onClose()
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="backdrop" role="presentation" onpointerdown={() => (pressed = true)} onclick={tapped(onClose)}></div>
<div class="sheet" role="dialog" aria-modal="true" aria-label={$t('chat.edit')} onpointerdown={() => (pressed = true)}>
  <p class="title">{$t('chat.edit')}</p>
  <textarea bind:this={field} bind:value={draft} rows="4" aria-label={$t('chat.edit')}></textarea>
  <p class="note">{$t('m.edit_note')}</p>
  <div class="row">
    <button class="btn" onclick={tapped(onClose)} disabled={busy}>{$t('m.cancel')}</button>
    <button class="btn primary" onclick={tapped(save)} disabled={busy || !draft.trim()}>{$t('common.save')}</button>
  </div>
</div>

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
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 14px 16px calc(16px + env(safe-area-inset-bottom));
    background: var(--m-surface);
    border-radius: 16px 16px 0 0;
  }
  .title { margin: 0; font-size: 15px; font-weight: 600; color: var(--m-text); }
  textarea {
    resize: none;
    padding: 10px 12px;
    border: 1px solid var(--m-border);
    border-radius: 10px;
    background: var(--m-bg);
    color: var(--m-text);
    font: inherit;
    font-size: 16px;
    max-height: 40vh;
    outline: none;
  }
  .note { margin: 0; font-size: 12px; color: var(--m-text-3); }
  .row { display: flex; gap: 10px; }
  .btn {
    flex: 1;
    min-height: 44px;
    border-radius: 12px;
    border: 1px solid var(--m-border);
    background: var(--m-surface);
    color: var(--m-text);
    font: inherit;
    font-size: 15px;
    cursor: pointer;
  }
  .btn.primary { background: var(--m-accent); border-color: var(--m-accent); color: #fff; font-weight: 600; }
  .btn:disabled { opacity: .5; cursor: default; }
</style>
