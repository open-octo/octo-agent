<script lang="ts">
  import { questionModals, activeSessionId } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { t } from '../../lib/i18n'

  // Only the active session's own pending question renders here — a
  // different session's question surfaces via the sidebar badge instead
  // (Session.pending_question), not a second competing modal.
  let current = $derived($activeSessionId ? $questionModals[$activeSessionId] : undefined)

  let selected = $state<string[]>([])
  let customText = $state('')
  let inputEl = $state<HTMLInputElement | null>(null)
  let modalEl = $state<HTMLDivElement | null>(null)
  let lastQuestionId = $state<string | null>(null)

  // Reset the in-progress draft whenever a genuinely new question becomes
  // active (a fresh question, or switching to a session with a different
  // pending one) — not on every store update, which would wipe what the
  // user already selected/typed on each re-render. Autofocus the free-text
  // input (was inconsistent with AuthGate/CommandPalette, which both do this).
  $effect(() => {
    if (current && current.questionId !== lastQuestionId) {
      lastQuestionId = current.questionId
      selected = []
      customText = ''
      inputEl?.focus()
    }
  })

  function toggleOption(opt: string) {
    // ChatView stores the server field `multi_select` as `multiSelect`.
    if (current?.multiSelect) {
      selected = selected.includes(opt)
        ? selected.filter(o => o !== opt)
        : [...selected, opt]
    } else {
      selected = selected[0] === opt ? [] : [opt]
    }
  }

  function clearCurrent() {
    const sid = current?.sessionId
    if (!sid) return
    questionModals.update(m => {
      const n = { ...m }
      delete n[sid]
      return n
    })
  }

  function submit() {
    if (!current) return
    if (selected.length === 0 && !customText.trim()) return
    // ChatView stores the server field `question_id` as `questionId`.
    ws.answerQuestion(current.questionId, [...selected], customText)
    clearCurrent()
  }

  // The only path that actually answers "cancelled" to the agent — reached
  // solely via the explicit Cancel button, an unambiguous user decision.
  function cancel() {
    if (!current) return
    ws.answerQuestion(current.questionId, [], '', true)
    clearCurrent()
  }

  // Safe close: hides the modal without telling the agent anything. The
  // question is still pending — the reopen pill brings the modal back with
  // whatever was selected/typed still intact. Stored on the session's own
  // entry so switching sessions and back preserves it correctly.
  function softClose() {
    const sid = current?.sessionId
    if (!sid) return
    questionModals.update(m => m[sid] ? { ...m, [sid]: { ...m[sid], dismissed: true } } : m)
  }

  function reopen() {
    const sid = current?.sessionId
    if (!sid) return
    questionModals.update(m => m[sid] ? { ...m, [sid]: { ...m[sid], dismissed: false } } : m)
    inputEl?.focus()
  }

  // Only Escape is handled at the modal level — Enter-to-submit already works
  // from the free-text input, and a focused option-pill/button already
  // activates on Enter natively, so a second modal-level handler would double
  // fire.
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); softClose() }
  }
</script>

{#if current && !current.dismissed}
<!-- #1112: backdrop is inert (no onclick) — matches ConfirmModal (#1105).
     Dismissal without answering only happens via Esc/softClose. -->
<div class="backdrop" role="presentation">
  <div class="modal" bind:this={modalEl} onkeydown={onKeydown} role="dialog" aria-modal="true" tabindex="-1">
    <div class="modal-header">
      <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
      <span class="modal-title">
        {current.header || $t('question.title')}
      </span>
      <button class="close-btn" onclick={softClose} aria-label={$t('common.close')}>
        <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
      </button>
    </div>

    <div class="modal-body">
      <p class="question-text">{current.question}</p>

      {#if current.options?.length}
        <div class="options">
          {#each current.options as opt}
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
          bind:this={inputEl}
          class="custom-input"
          placeholder={$t('question.custom_placeholder')}
          bind:value={customText}
          onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submit() } }}
        />
      </div>
    </div>

    <div class="modal-footer">
      <button class="btn-cancel" onclick={cancel}>{$t('common.cancel')}</button>
      <span class="spacer"></span>
      <button
        class="btn-primary"
        onclick={submit}
        disabled={selected.length === 0 && !customText.trim()}
      >
        {$t('common.submit')}
      </button>
    </div>
  </div>
</div>
{:else if current && current.dismissed}
<button class="reopen-pill" onclick={reopen}>
  <iconify-icon icon="ant-design:form-outlined" width="14"></iconify-icon>
  {$t('question.reopen')}
</button>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 560px;
  background: var(--bg-container);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal:focus { outline: none; }
.reopen-pill {
  position: fixed; left: 24px; bottom: 24px; z-index: 1100;
  display: flex; align-items: center; gap: 8px;
  height: 36px; padding: 0 14px;
  border: 1px solid var(--blue-2); background: var(--surface-info);
  border-radius: 9999px; box-shadow: 0 8px 24px rgba(15,23,42,0.14);
  font-size: 13px; color: var(--blue-6); cursor: pointer; font-family: inherit;
  animation: octo-fadein 0.16s ease;
}
.reopen-pill:hover { border-color: var(--blue-5); }
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 14px 24px;
  border-bottom: 1px solid var(--border-table);
}
.modal-title {
  font-size: 15px; font-weight: 600; color: var(--text-heading); flex: 1;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.close-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary); flex-shrink: 0;
}
.close-btn:hover { background: var(--hover-neutral); }

.modal-body {
  padding: 20px 24px;
  display: flex; flex-direction: column; gap: 16px;
}
.question-text {
  margin: 0;
  font-size: 14px; line-height: 1.6; color: var(--text-secondary);
  white-space: pre-wrap; word-break: break-word;
  max-height: 40vh; overflow-y: auto;
}
.options {
  display: flex; flex-wrap: wrap; gap: 10px;
}
.option-pill {
  display: inline-flex; align-items: center; gap: 5px;
  min-height: 34px; padding: 7px 14px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 9999px;
  font-size: 13px; color: var(--text-secondary); line-height: 1.4;
  text-align: left; white-space: normal; word-break: break-word;
  cursor: pointer; font-family: inherit;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}
.option-pill:hover { border-color: var(--blue-5); color: var(--blue-5); }
.option-pill.selected {
  border-color: var(--blue-6); background: var(--blue-1); color: var(--blue-6);
}
.custom-input-wrap { display: flex; }
.custom-input {
  flex: 1; height: 34px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 6px;
  font-size: 13px; color: var(--text);
  font-family: inherit; outline: none; background: var(--bg-container);
}
.custom-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }

.modal-footer {
  padding: 14px 24px;
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
.btn-cancel:hover { border-color: var(--blue-5); color: var(--blue-5); }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 14px; color: #fff;
  cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { background: var(--border); cursor: not-allowed; }
</style>
