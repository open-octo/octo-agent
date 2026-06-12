<script lang="ts">
  import { confirmModal } from '../../lib/stores'
  import { ws } from '../../lib/ws'
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
      <iconify-icon icon="ant-design:safety-outlined" width="16" style="color:#FAAD14;flex-shrink:0"></iconify-icon>
      <span class="modal-title">Permission needed</span>
      <span class="header-tag">
        <StatusTag status="warning">Awaiting approval</StatusTag>
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
        <button class="btn-primary" onclick={() => answer('ok')}>OK</button>
      {:else}
        <button class="btn-deny" onclick={deny}>
          <iconify-icon icon="ant-design:close-outlined" width="12"></iconify-icon>
          Deny
        </button>
        <span class="spacer"></span>
        <button class="btn-secondary" onclick={() => answer('allow_once')}>Allow Once</button>
        <button class="btn-primary" onclick={() => answer('allow_session')}>
          <iconify-icon icon="ant-design:check-outlined" width="12"></iconify-icon>
          Allow for Session
        </button>
      {/if}
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: rgba(0,0,0,0.45);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 480px;
  background: #FFFBE6;
  border: 1px solid #FFE58F;
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid #FFE58F;
}
.modal-title {
  font-size: 14px; font-weight: 600; color: #1F1F1F; flex: 1;
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
  font-size: 13px; line-height: 1.6; color: rgba(0,0,0,0.65);
}
.terminal {
  margin: 0; padding: 10px 12px;
  background: #1F1F1F; color: #E6E6E6;
  border-radius: 6px;
  font-size: 12px; line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  overflow-x: auto; white-space: pre-wrap; word-break: break-all;
}
.prompt { color: #52C41A; }
.modal-footer {
  padding: 12px 18px;
  border-top: 1px solid #FFE58F;
  display: flex; align-items: center; gap: 8px;
}
.spacer { flex: 1; }
.btn-deny {
  height: 32px; padding: 0 12px;
  border: 1px solid #D9D9D9; background: #fff;
  border-radius: 6px;
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: rgba(0,0,0,0.65);
  cursor: pointer; font-family: inherit;
}
.btn-deny:hover { border-color: #FF4D4F; color: #FF4D4F; }
.btn-secondary {
  height: 32px; padding: 0 12px;
  border: 1px solid #D9D9D9; background: #fff;
  border-radius: 6px;
  font-size: 13px; color: rgba(0,0,0,0.65);
  cursor: pointer; font-family: inherit;
}
.btn-secondary:hover { border-color: #4096FF; color: #4096FF; }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: #1677FF;
  border-radius: 6px;
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover { background: #4096FF; }
</style>
