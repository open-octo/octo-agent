<script lang="ts">
  import { questionModals, type QuestionModalEntry } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { t } from '../../lib/i18n'

  // With global broadcast, a question can originate from ANY session — not
  // just the active one. So we render a card for every pending question in the
  // store, keyed by session ID, and each card tracks its own draft state.
  let { entry }: { entry: QuestionModalEntry } = $props()

  let selected = $state<string[]>([])
  let customText = $state('')
  let inputEl = $state<HTMLInputElement | null>(null)
  let modalEl = $state<HTMLDivElement | null>(null)
  let expanded = $state(false)

  // Reset the draft whenever this card's question changes.
  let lastQuestionId = $state<string | null>(null)
  $effect(() => {
    if (entry.questionId !== lastQuestionId) {
      lastQuestionId = entry.questionId
      selected = []
      customText = ''
      expanded = false
      inputEl?.focus()
    }
  })

  function toggleOption(opt: string) {
    if (entry.multiSelect) {
      selected = selected.includes(opt) ? selected.filter(o => o !== opt) : [...selected, opt]
    } else {
      selected = selected[0] === opt ? [] : [opt]
    }
  }

  function clear() {
    questionModals.update(m => {
      const n = { ...m }
      delete n[entry.sessionId]
      return n
    })
  }

  function submit() {
    if (selected.length === 0 && !customText.trim()) return
    ws.answerQuestion(entry.questionId, [...selected], customText)
    clear()
  }

  function cancel() {
    ws.answerQuestion(entry.questionId, [], '', true)
    clear()
  }

  function softClose() { expanded = false }
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); softClose() }
  }
</script>

{#if expanded}
  <div class="backdrop" role="presentation">
    <div class="modal" bind:this={modalEl} onkeydown={onKeydown} role="dialog" aria-modal="true" tabindex="-1">
      <div class="modal-header">
        <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
        <span class="modal-title">{entry.header || t('question.title')}</span>
        <button class="close-btn" onclick={softClose} aria-label={t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
        </button>
      </div>

      <div class="modal-body">
        <p class="question-text">{entry.question}</p>

        {#if entry.options?.length}
          <div class="options">
            {#each entry.options as opt}
              <button class="option-pill" class:selected={selected.includes(opt)} onclick={() => toggleOption(opt)}>
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
            type={entry.secret ? 'password' : 'text'}
            autocomplete={entry.secret ? 'new-password' : 'off'}
            data-1p-ignore={entry.secret ? true : undefined}
            placeholder={t('question.custom_placeholder')}
            bind:value={customText}
            onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submit() } }}
          />
        </div>
      </div>

      <div class="modal-footer">
        <button class="btn-cancel" onclick={cancel}>{t('common.cancel')}</button>
        <span class="spacer"></span>
        <button class="btn-primary" onclick={submit} disabled={selected.length === 0 && !customText.trim()}>
          {t('common.submit')}
        </button>
      </div>
    </div>
  </div>
{:else}
  <div class="banner" role="dialog" aria-modal="false">
    <div class="banner-inner">
      <div class="banner-main">
        <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
        <span class="banner-question">{entry.question}</span>
        <button class="banner-expand" onclick={() => { expanded = true; inputEl?.focus() }}>
          <iconify-icon icon="ant-design:arrows-alt-outlined" width="12"></iconify-icon>
        </button>
      </div>

      {#if entry.options?.length}
        <div class="banner-options">
          {#each entry.options as opt}
            <button class="option-pill" class:selected={selected.includes(opt)} onclick={() => toggleOption(opt)}>
              {#if selected.includes(opt)}
                <iconify-icon icon="ant-design:check-outlined" width="11"></iconify-icon>
              {/if}
              {opt}
            </button>
          {/each}
        </div>
      {/if}

      <div class="banner-actions">
        <input
          bind:this={inputEl}
          class="banner-input"
          type={entry.secret ? 'password' : 'text'}
          autocomplete={entry.secret ? 'new-password' : 'off'}
          data-1p-ignore={entry.secret ? true : undefined}
          placeholder={t('question.custom_placeholder')}
          bind:value={customText}
          onkeydown={(e) => { if (e.key === 'Enter' && customText.trim()) { e.preventDefault(); submit() } }}
        />
        <button class="btn-cancel btn-cancel-sm" onclick={cancel}>{t('common.cancel')}</button>
        <button class="btn-primary btn-primary-sm" onclick={submit} disabled={selected.length === 0 && !customText.trim()}>
          {t('common.submit')}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .banner {
    flex: 0 0 auto;
    max-width: var(--chat-content-max-width); margin: 0 auto; width: 100%;
    padding: 0 24px 12px;
  }
  .banner-inner {
    background: var(--bg-container);
    border: 1px solid var(--blue-2);
    border-radius: 12px;
    box-shadow: 0 8px 32px rgba(15,23,42,0.12);
    padding: 12px 16px;
    display: flex; flex-direction: column; gap: 10px;
    animation: octo-banner-in 0.18s ease;
  }
  @keyframes octo-banner-in {
    from { opacity: 0; transform: translateY(12px); }
    to   { opacity: 1; transform: translateY(0); }
  }
  .banner-main { display: flex; align-items: center; gap: 10px; }
  .banner-question {
    flex: 1; min-width: 0;
    font-size: 14px; line-height: 1.5; color: var(--text);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .banner-expand {
    width: 24px; height: 24px; border: none; background: transparent;
    border-radius: 6px; cursor: pointer; color: var(--text-tertiary);
    display: flex; align-items: center; justify-content: center; flex-shrink: 0;
  }
  .banner-expand:hover { background: var(--hover-neutral); color: var(--blue-6); }
  .banner-options { display: flex; flex-wrap: wrap; gap: 8px; }
  .banner-actions { display: flex; align-items: center; gap: 8px; }
  .banner-input {
    flex: 1; height: 32px; padding: 0 10px;
    border: 1px solid var(--border); border-radius: 6px;
    font-size: 13px; color: var(--text);
    font-family: inherit; outline: none; background: var(--bg-container);
  }
  .banner-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }

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
  .modal-header { display: flex; align-items: center; gap: 8px; padding: 14px 24px; border-bottom: 1px solid var(--border-table); }
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

  .modal-body { padding: 20px 24px; display: flex; flex-direction: column; gap: 16px; }
  .question-text {
    margin: 0;
    font-size: 14px; line-height: 1.6; color: var(--text-secondary);
    white-space: pre-wrap; word-break: break-word;
    max-height: 40vh; overflow-y: auto;
  }
  .options { display: flex; flex-wrap: wrap; gap: 10px; }
  .custom-input-wrap { display: flex; }
  .custom-input {
    flex: 1; height: 34px; padding: 0 10px;
    border: 1px solid var(--border); border-radius: 6px;
    font-size: 13px; color: var(--text);
    font-family: inherit; outline: none; background: var(--bg-container);
  }
  .custom-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }

  .modal-footer { padding: 14px 24px; border-top: 1px solid var(--border-table); display: flex; align-items: center; gap: 8px; }
  .spacer { flex: 1; }

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
  .option-pill.selected { border-color: var(--blue-6); background: var(--blue-1); color: var(--blue-6); }
  .btn-cancel {
    height: 32px; padding: 0 14px;
    border: 1px solid var(--border); background: var(--bg-container);
    border-radius: 6px; font-size: 14px; color: var(--text-secondary);
    cursor: pointer; font-family: inherit;
  }
  .btn-cancel:hover { border-color: var(--blue-5); color: var(--blue-5); }
  .btn-cancel-sm { height: 32px; padding: 0 12px; font-size: 13px; }
  .btn-primary {
    height: 32px; padding: 0 14px;
    border: none; background: var(--blue-6);
    border-radius: 6px; font-size: 14px; color: #fff;
    cursor: pointer; font-family: inherit;
  }
  .btn-primary:hover:not(:disabled) { background: var(--blue-5); }
  .btn-primary:disabled { background: var(--border); cursor: not-allowed; }
  .btn-primary-sm { height: 32px; padding: 0 12px; font-size: 13px; }
</style>
