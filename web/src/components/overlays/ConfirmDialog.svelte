<script lang="ts">
  import { confirmRequest } from '../../lib/confirm'
  import { t } from '../../lib/i18n'

  let modalEl = $state<HTMLDivElement | null>(null)

  // Focus the modal so Enter/Esc work without leaking to whatever was focused
  // underneath (same reason ConfirmModal does this).
  $effect(() => {
    if ($confirmRequest && modalEl) modalEl.focus()
  })

  function answer(ok: boolean) {
    const req = $confirmRequest
    confirmRequest.set(null)
    req?.resolve(ok)
  }

  function onKeydown(e: KeyboardEvent) {
    if (!$confirmRequest) return
    if (e.key === 'Escape') { e.preventDefault(); answer(false) }
    else if (e.key === 'Enter') { e.preventDefault(); answer(true) }
  }
</script>

{#if $confirmRequest}
<div class="backdrop" role="presentation" onclick={() => answer(false)}>
  <div class="modal" role="dialog" aria-modal="true" tabindex="-1" bind:this={modalEl}
       onkeydown={onKeydown} onclick={(e) => e.stopPropagation()}>
    {#if $confirmRequest.title}
      <h2 class="title">{$confirmRequest.title}</h2>
    {/if}
    <p class="msg" class:under-title={!!$confirmRequest.title}>{$confirmRequest.message}</p>
    <div class="footer">
      <button class="btn-secondary" onclick={() => answer(false)}>{$t('common.cancel')}</button>
      <button class:btn-danger={$confirmRequest.danger} class:btn-primary={!$confirmRequest.danger} onclick={() => answer(true)}>
        {$confirmRequest.confirmLabel ?? $t('common.ok')}
      </button>
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1200;
  background: var(--scrim);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 420px;
  background: var(--bg-container);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal:focus { outline: none; }
.title {
  margin: 0; padding: 20px 20px 0;
  font-size: 16px; font-weight: 600; color: var(--text);
}
.btn-danger {
  height: 32px; padding: 0 14px; border: none; border-radius: 7px;
  background: var(--error); color: #fff;
  font: 600 13px/1 inherit; font-family: inherit; cursor: pointer;
}
.btn-danger:hover { background: #FF7875; }
.msg.under-title { padding-top: 10px; color: var(--text-secondary); }
.msg {
  margin: 0; padding: 20px 20px 4px;
  font-size: 14px; line-height: 1.6; color: var(--text);
  white-space: pre-wrap; word-break: break-word;
}
.footer {
  padding: 16px 20px 18px;
  display: flex; justify-content: flex-end; gap: 8px;
}
.btn-secondary {
  height: 32px; padding: 0 14px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-secondary:hover { border-color: var(--blue-5); color: var(--blue-5); }
.btn-primary {
  height: 32px; padding: 0 16px;
  border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 13px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover { background: var(--blue-5); }
</style>
