<script lang="ts">
  import { confirmModal } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { t } from '../../lib/i18n'
  import StatusTag from '../ui/StatusTag.svelte'

  function deny() {
    if (!$confirmModal) return
    ws.answerConfirmation($confirmModal.id, 'deny')
    confirmModal.set(null)
  }

  function answer(result: string) {
    if (!$confirmModal) return
    ws.answerConfirmation($confirmModal.id, result)
    confirmModal.set(null)
  }
</script>

{#if $confirmModal}
<div class="backdrop" onclick={deny} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <div class="modal-header">
      <iconify-icon icon="ant-design:safety-outlined" width="16" style="color:var(--warning);flex-shrink:0"></iconify-icon>
      <span class="modal-title">{$t('perm.title')}</span>
      <span class="header-tag">
        <StatusTag status="warning">{$t('perm.awaiting')}</StatusTag>
      </span>
    </div>

    <div class="modal-body">
      {#if $confirmModal.message}
        <p class="desc">{$confirmModal.message}</p>
      {/if}
      {#if $confirmModal.command}
        <pre class="terminal"><span class="prompt">$</span> {$confirmModal.command}</pre>
      {:else if $confirmModal.content}
        <pre class="terminal">{$confirmModal.content}</pre>
      {/if}
    </div>

    <div class="modal-footer">
      {#if $confirmModal.kind === 'ok'}
        <button class="btn-primary" onclick={() => answer('ok')}>{$t('common.ok')}</button>
      {:else}
        <button class="btn-deny" onclick={deny}>
          <iconify-icon icon="ant-design:close-outlined" width="12"></iconify-icon>
          {$t('perm.deny')}
        </button>
        <span class="spacer"></span>
        <!-- Result strings are the wire contract with the server's mapConfirmResult:
             'yes' = allow once, 'always' = allow + remember for the session.
             Anything else (incl. 'deny') denies. Don't rename without updating the server. -->
        <button class="btn-secondary" onclick={() => answer('yes')}>{$t('perm.allow_once')}</button>
        <button class="btn-primary" onclick={() => answer('always')}>
          <iconify-icon icon="ant-design:check-outlined" width="12"></iconify-icon>
          {$t('perm.allow_session')}
        </button>
      {/if}
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 480px;
  background: var(--warning-bg);
  border: 1px solid var(--warning-border);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--warning-border);
}
.modal-title {
  font-size: 14px; font-weight: 600; color: var(--text-heading); flex: 1;
}
.header-tag {
  margin-left: auto; flex-shrink: 0;
}
.modal-body {
  padding: 16px 18px;
  display: flex; flex-direction: column; gap: 12px;
}
.desc {
  margin: 0;
  font-size: 13px; line-height: 1.6; color: var(--text-secondary);
}
.terminal {
  margin: 0; padding: 10px 12px;
  background: var(--terminal-bg); color: var(--terminal-text);
  border-radius: 6px;
  font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  overflow-x: auto; white-space: pre-wrap; word-break: break-all;
}
.prompt { color: var(--success); }
.modal-footer {
  padding: 12px 18px;
  border-top: 1px solid var(--warning-border);
  display: flex; align-items: center; gap: 8px;
}
.spacer { flex: 1; }
.btn-deny {
  height: 32px; padding: 0 12px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px;
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-deny:hover { border-color: var(--error); color: var(--error); }
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
.btn-primary:hover { background: var(--blue-5); }
</style>
