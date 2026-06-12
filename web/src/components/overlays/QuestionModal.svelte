<script lang="ts">
  import { questionModal } from '../../lib/stores'
  import { ws } from '../../lib/ws'

  let selected = $state<string[]>([])
  let customText = $state('')

  // Reset state whenever a new question arrives
  $effect(() => {
    if ($questionModal) {
      selected = []
      customText = ''
    }
  })

  function toggleOption(opt: string) {
    if ($questionModal?.multi_select) {
      selected = selected.includes(opt)
        ? selected.filter(o => o !== opt)
        : [...selected, opt]
    } else {
      selected = selected[0] === opt ? [] : [opt]
    }
  }

  function submit() {
    if (!$questionModal) return
    ws.answerQuestion($questionModal.question_id, selected, customText)
    questionModal.set(null)
  }

  function cancel() {
    if (!$questionModal) return
    ws.answerQuestion($questionModal.question_id, [], '', true)
    questionModal.set(null)
  }

  function close() { cancel() }
</script>

{#if $questionModal}
<div class="backdrop" onclick={close} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <div class="modal-header">
      <iconify-icon icon="ant-design:form-outlined" width="16" style="color:#1677FF;flex-shrink:0"></iconify-icon>
      <span class="modal-title">
        {$questionModal.header || $questionModal.question}
      </span>
      <button class="close-btn" onclick={close} aria-label="Close">
        <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
      </button>
    </div>

    <div class="modal-body">
      {#if $questionModal.header}
        <p class="question-text">{$questionModal.question}</p>
      {/if}

      {#if $questionModal.options?.length}
        <div class="options">
          {#each $questionModal.options as opt}
            <button
              class="option-pill"
              class:selected={selected.includes(opt)}
              onclick={() => toggleOption(opt)}
            >
              {#if selected.includes(opt)}
                <iconify-icon icon="ant-design:check-outlined" width="11"></iconify-icon>
              {/if}
              {opt}
            </button>
          {/each}
        </div>
      {/if}

      <div class="custom-input-wrap">
        <input
          class="custom-input"
          placeholder="Or type a custom answer…"
          bind:value={customText}
          onkeydown={(e) => { if (e.key === 'Enter') submit() }}
        />
      </div>
    </div>

    <div class="modal-footer">
      <button class="btn-cancel" onclick={cancel}>Cancel</button>
      <span class="spacer"></span>
      <button
        class="btn-primary"
        onclick={submit}
        disabled={selected.length === 0 && !customText.trim()}
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
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.close-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.45); flex-shrink: 0;
}
.close-btn:hover { background: rgba(0,0,0,0.04); }

.modal-body {
  padding: 16px 18px;
  display: flex; flex-direction: column; gap: 14px;
}
.question-text {
  margin: 0;
  font-size: 13px; line-height: 1.6; color: rgba(0,0,0,0.65);
}
.options {
  display: flex; flex-wrap: wrap; gap: 8px;
}
.option-pill {
  display: inline-flex; align-items: center; gap: 5px;
  height: 30px; padding: 0 12px;
  border: 1px solid #D9D9D9; background: #fff;
  border-radius: 9999px;
  font-size: 13px; color: rgba(0,0,0,0.65);
  cursor: pointer; font-family: inherit;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}
.option-pill:hover { border-color: #4096FF; color: #4096FF; }
.option-pill.selected {
  border-color: #1677FF; background: #E6F4FF; color: #1677FF;
}
.custom-input-wrap { display: flex; }
.custom-input {
  flex: 1; height: 34px; padding: 0 10px;
  border: 1px solid #D9D9D9; border-radius: 6px;
  font-size: 13px; color: rgba(0,0,0,0.88);
  font-family: inherit; outline: none; background: #fff;
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
.btn-cancel:hover { border-color: #4096FF; color: #4096FF; }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: #1677FF;
  border-radius: 6px; font-size: 14px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: #4096FF; }
.btn-primary:disabled { background: #D9D9D9; cursor: not-allowed; }
</style>
