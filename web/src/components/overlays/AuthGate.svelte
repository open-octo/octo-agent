<script lang="ts">
  import { authPrompt, submitAuthKey } from '../../lib/auth'
  import { t } from '../../lib/i18n'

  let value = $state('')
  let inputEl = $state<HTMLInputElement | null>(null)

  // Focus the field whenever the prompt (re)opens.
  $effect(() => {
    if ($authPrompt && inputEl) inputEl.focus()
  })

  function submit() {
    const key = value.trim()
    if (!key) return
    value = ''
    submitAuthKey(key)
  }

  function cancel() {
    value = ''
    submitAuthKey(null)
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') { e.preventDefault(); submit() }
    else if (e.key === 'Escape') { e.preventDefault(); cancel() }
  }
</script>

{#if $authPrompt}
<div class="backdrop" role="presentation">
  <div class="modal" role="dialog" aria-modal="true">
    <div class="modal-header">
      <iconify-icon icon="ant-design:lock-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
      <span class="modal-title">{$t('auth.title')}</span>
    </div>

    <div class="modal-body">
      <p class="desc">{$t('auth.accessKeyRequired')}</p>
      {#if $authPrompt.retry}
        <p class="err">{$t('auth.rateLimited')}</p>
      {/if}
      <input
        bind:this={inputEl}
        bind:value
        type="password"
        class="key-input"
        placeholder={$t('auth.placeholder')}
        onkeydown={onKeydown}
        autocomplete="off"
      />
    </div>

    <div class="modal-footer">
      <button class="btn-secondary" onclick={cancel}>{$t('common.cancel')}</button>
      <span class="spacer"></span>
      <button class="btn-primary" onclick={submit} disabled={!value.trim()}>
        <iconify-icon icon="ant-design:login-outlined" width="12"></iconify-icon>
        {$t('auth.submit')}
      </button>
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1200;
  background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 420px;
  background: var(--bg-container);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--border);
}
.modal-title {
  font-size: 14px; font-weight: 600; color: var(--text-heading); flex: 1;
}
.modal-body {
  padding: 16px 18px;
  display: flex; flex-direction: column; gap: 10px;
}
.desc {
  margin: 0;
  font-size: 13px; line-height: 1.6; color: var(--text-secondary);
}
.err {
  margin: 0;
  font-size: 12px; line-height: 1.5; color: var(--error);
}
.key-input {
  height: 36px; padding: 0 12px;
  border: 1px solid var(--border); background: var(--bg-base);
  border-radius: 6px;
  font-size: 13px; color: var(--text-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.key-input:focus {
  outline: none; border-color: var(--blue-5);
}
.modal-footer {
  padding: 12px 18px;
  border-top: 1px solid var(--border);
  display: flex; align-items: center; gap: 8px;
}
.spacer { flex: 1; }
.btn-secondary {
  height: 32px; padding: 0 12px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px;
  font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-secondary:hover { border-color: var(--blue-5); color: var(--blue-5); }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: var(--blue-6);
  border-radius: 6px;
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
