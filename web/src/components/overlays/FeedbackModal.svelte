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
      <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
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
                <iconify-icon icon="ant-design:check-circle-filled" width="14" style="color:var(--blue-6)"></iconify-icon>
              {:else}
                <iconify-icon icon="ant-design:circle-outlined" width="14" style="color:var(--border)"></iconify-icon>
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
  background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 480px;
  background: var(--bg-container);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border-table);
}
.modal-title {
  font-size: 15px; font-weight: 600; color: var(--text-heading); flex: 1;
}
.modal-body {
  padding: 16px 18px;
  display: flex; flex-direction: column; gap: 14px;
}
.context-text {
  margin: 0;
  font-size: 13px; line-height: 1.6; color: var(--text-secondary);
}
.options {
  display: flex; flex-direction: column; gap: 6px;
}
.option-btn {
  display: flex; align-items: center; gap: 10px;
  width: 100%; padding: 10px 12px;
  border: 1px solid var(--border-table); background: var(--bg-table-header);
  border-radius: 8px;
  font-size: 13px; color: var(--text);
  text-align: left; cursor: pointer; font-family: inherit;
  transition: border-color 0.15s, background 0.15s;
}
.option-btn:hover { border-color: var(--blue-2); background: var(--blue-1); }
.option-btn.selected { border-color: var(--blue-6); background: var(--blue-1); color: var(--blue-6); }

.custom-input-wrap { display: flex; }
.custom-input {
  flex: 1; padding: 8px 10px;
  border: 1px solid var(--border); border-radius: 6px;
  font-size: 13px; color: var(--text); line-height: 1.6;
  font-family: inherit; outline: none; background: var(--bg-container);
  resize: vertical;
}
.custom-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }

.modal-footer {
  padding: 12px 18px;
  border-top: 1px solid var(--border-table);
  display: flex; align-items: center; gap: 8px;
}
.spacer { flex: 1; }
.btn-cancel {
  height: 32px; padding: 0 14px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 14px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-cancel:hover { border-color: var(--border); color: var(--text); background: var(--bg-layout); }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 14px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { background: var(--border); cursor: not-allowed; }
</style>
