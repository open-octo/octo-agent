<script lang="ts">
  import { feedbackModal, activeSessionId } from '../../lib/stores'
  import { ws } from '../../lib/ws'

  let selectedOption = $state('')
  let customText = $state('')

  // Reset state when a new feedback request arrives
  $effect(() => {
    if ($feedbackModal) {
      selectedOption = ''
      customText = ''
    }
  })

  function submit() {
    if (!$feedbackModal || !$activeSessionId) return
    const answer = selectedOption || customText.trim()
    if (!answer) return
    ws.sendMessage($activeSessionId, answer)
    feedbackModal.set(null)
  }

  function dismiss() {
    feedbackModal.set(null)
  }
</script>

{#if $feedbackModal}
<div class="backdrop" onclick={dismiss} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <div class="modal-header">
      <iconify-icon icon="ant-design:form-outlined" width="16" style="color:#1677FF;flex-shrink:0"></iconify-icon>
      <span class="modal-title">{$feedbackModal.question}</span>
    </div>

    <div class="modal-body">
      {#if $feedbackModal.context}
        <p class="context-text">{$feedbackModal.context}</p>
      {/if}

      {#if $feedbackModal.options?.length}
        <div class="options">
          {#each $feedbackModal.options as opt}
            <button
              class="option-btn"
              class:selected={selectedOption === opt}
              onclick={() => { selectedOption = selectedOption === opt ? '' : opt }}
            >
              {#if selectedOption === opt}
                <iconify-icon icon="ant-design:check-circle-filled" width="14" style="color:#1677FF"></iconify-icon>
              {:else}
                <iconify-icon icon="ant-design:circle-outlined" width="14" style="color:#D9D9D9"></iconify-icon>
              {/if}
              <span>{opt}</span>
            </button>
          {/each}
        </div>
      {/if}

      <div class="custom-input-wrap">
        <textarea
          class="custom-input"
          placeholder="Or type a custom reply…"
          rows="3"
          bind:value={customText}
        ></textarea>
      </div>
    </div>

    <div class="modal-footer">
      <button class="btn-cancel" onclick={dismiss}>Not Now</button>
      <span class="spacer"></span>
      <button
        class="btn-primary"
        onclick={submit}
        disabled={!selectedOption && !customText.trim()}
      >
        Submit
      </button>
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
  background: #fff;
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 14px 18px;
  border-bottom: 1px solid #F0F0F0;
}
.modal-title {
  font-size: 15px; font-weight: 600; color: #1F1F1F; flex: 1;
}
.modal-body {
  padding: 16px 18px;
  display: flex; flex-direction: column; gap: 14px;
}
.context-text {
  margin: 0;
  font-size: 13px; line-height: 1.6; color: rgba(0,0,0,0.65);
}
.options {
  display: flex; flex-direction: column; gap: 6px;
}
.option-btn {
  display: flex; align-items: center; gap: 10px;
  width: 100%; padding: 10px 12px;
  border: 1px solid #F0F0F0; background: #FAFAFA;
  border-radius: 8px;
  font-size: 13px; color: rgba(0,0,0,0.88);
  text-align: left; cursor: pointer; font-family: inherit;
  transition: border-color 0.15s, background 0.15s;
}
.option-btn:hover { border-color: #BAE0FF; background: #E6F4FF; }
.option-btn.selected { border-color: #1677FF; background: #E6F4FF; color: #1677FF; }

.custom-input-wrap { display: flex; }
.custom-input {
  flex: 1; padding: 8px 10px;
  border: 1px solid #D9D9D9; border-radius: 6px;
  font-size: 13px; color: rgba(0,0,0,0.88); line-height: 1.6;
  font-family: inherit; outline: none; background: #fff;
  resize: vertical;
}
.custom-input:focus { border-color: #1677FF; box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }

.modal-footer {
  padding: 12px 18px;
  border-top: 1px solid #F0F0F0;
  display: flex; align-items: center; gap: 8px;
}
.spacer { flex: 1; }
.btn-cancel {
  height: 32px; padding: 0 14px;
  border: 1px solid #D9D9D9; background: #fff;
  border-radius: 6px; font-size: 14px; color: rgba(0,0,0,0.65);
  cursor: pointer; font-family: inherit;
}
.btn-cancel:hover { border-color: #D9D9D9; color: rgba(0,0,0,0.88); background: #F5F5F5; }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: #1677FF;
  border-radius: 6px; font-size: 14px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: #4096FF; }
.btn-primary:disabled { background: #D9D9D9; cursor: not-allowed; }
</style>
