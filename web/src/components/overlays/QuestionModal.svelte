<script lang="ts">
  // Desktop question surface. With global broadcast, a question can originate
  // from ANY session. To avoid interrupting a conversation in progress:
  //
  //  - The modal/banner appears ONLY for the question of the ACTIVE session.
  //  - Questions from other sessions surface as compact, clickable rows
  //    ("Session B needs your input") — tapping one switches to that session,
  //    where the modal then appears.
  //
  // This way: viewing session A while B asks → no modal, just a quiet row; the
  // user clicks it only when ready to answer.
  import { questionModals, activeSessionId, sessions, setActiveSession } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { t } from '../../lib/i18n'

  // Active session's own pending question → full modal/banner.
  const current = $derived($activeSessionId ? $questionModals[$activeSessionId] : undefined)

  // All OTHER sessions' pending questions → compact notification rows.
  const others = $derived(
    Object.entries($questionModals).filter(([sid]) => sid !== $activeSessionId)
  )

  function sessionLabel(sid: string): string {
    const s = $sessions?.find?.(x => x.id === sid)
    return s?.title || s?.name || sid.slice(0, 8)
  }

  let selected = $state<string[]>([])
  let customText = $state('')
  let inputEl = $state<HTMLInputElement | null>(null)
  let modalEl = $state<HTMLDivElement | null>(null)
  let expanded = $state(false)
  let lastQuestionId = $state<string | null>(null)

  $effect(() => {
    if (current && current.questionId !== lastQuestionId) {
      lastQuestionId = current.questionId
      selected = []
      customText = ''
      expanded = false
      inputEl?.focus()
    }
  })

  function toggleOption(opt: string) {
    if (current?.multiSelect) {
      selected = selected.includes(opt) ? selected.filter(o => o !== opt) : [...selected, opt]
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
    ws.answerQuestion(current.questionId, [...selected], customText)
    clearCurrent()
  }

  function cancel() {
    if (!current) return
    ws.answerQuestion(current.questionId, [], '', true)
    clearCurrent()
  }

  function softClose() { expanded = false }
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); softClose() }
  }
</script>

<!-- Non-active sessions' questions: compact, non-interrupting rows. Clicking
     switches to that session, where the modal then appears. -->
{#if others.length}
  <div class="qnote-stack" role="status" aria-live="polite">
    {#each others as [sid, entry] (sid)}
      <button class="qnote" onclick={() => setActiveSession(sid)}>
        <iconify-icon icon="ant-design:form-outlined" width="14" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
        <span class="qnote-label">{sessionLabel(sid)}</span>
        <span class="qnote-q">{entry.question}</span>
        <span class="qnote-go">$t('m.view')} ›</span>
      </button>
    {/each}
  </div>
{/if}

<!-- Active session's own question: full modal/banner. -->
{#if current && expanded}
  <div class="backdrop" role="presentation">
    <div class="modal" bind:this={modalEl} onkeydown={onKeydown} role="dialog" aria-modal="true" tabindex="-1">
      <div class="modal-header">
        <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
        <span class="modal-title">{current.header || $t('question.title')}</span>
        <button class="close-btn" onclick={softClose} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
        </button>
      </div>

      <div class="modal-body">
        <p class="question-text">{current.question}</p>

        {#if current.options?.length}
          <div class="options">
            {#each current.options as opt}
              <button class="option-pill" class:selected={selected.includes(opt)} onclick={() => toggleOption(opt)}>
                {#if selected.includes(opt)}<iconify-icon icon="ant-design:check-outlined" width="11"></iconify-icon>{/if}
                {opt}
              </button>
            {/each}
          </div>
        {/if}

        <div class="custom-input-wrap">
          <input
            bind:this={inputEl}
            class="custom-input"
            type={current.secret ? 'password' : 'text'}
            autocomplete={current.secret ? 'new-password' : 'off'}
            data-1p-ignore={current.secret ? true : undefined}
            placeholder={$t('question.custom_placeholder')}
            bind:value={customText}
            onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submit() } }}
          />
        </div>
      </div>

      <div class="modal-footer">
        <button class="btn-cancel" onclick={cancel}>{$t('common.cancel')}</button>
        <span class="spacer"></span>
        <button class="btn-primary" onclick={submit} disabled={selected.length === 0 && !customText.trim()}>
          {$t('common.submit')}
        </button>
      </div>
    </div>
  </div>
{:else if current}
  <div class="banner" role="dialog" aria-modal="false">
    <div class="banner-inner">
      <div class="banner-main">
        <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
        <span class="banner-question">{current.question}</span>
        <button class="banner-expand" onclick={() => { expanded = true; inputEl?.focus() }}>
          <iconify-icon icon="ant-design:arrows-alt-outlined" width="12"></iconify-icon>
        </button>
      </div>

      {#if current.options?.length}
        <div class="banner-options">
          {#each current.options as opt}
            <button class="option-pill" class:selected={selected.includes(opt)} onclick={() => toggleOption(opt)}>
              {#if selected.includes(opt)}<iconify-icon icon="ant-design:check-outlined" width="11"></iconify-icon>{/if}
              {opt}
            </button>
          {/each}
        </div>
      {/if}

      <div class="banner-actions">
        <input
          bind:this={inputEl}
          class="banner-input"
          type={current.secret ? 'password' : 'text'}
          autocomplete={current.secret ? 'new-password' : 'off'}
          data-1p-ignore={current.secret ? true : undefined}
          placeholder={$t('question.custom_placeholder')}
          bind:value={customText}
          onkeydown={(e) => { if (e.key === 'Enter' && customText.trim()) { e.preventDefault(); submit() } }}
        />
        <button class="btn-cancel btn-cancel-sm" onclick={cancel}>{$t('common.cancel')}</button>
        <button class="btn-primary btn-primary-sm" onclick={submit} disabled={selected.length === 0 && !customText.trim()}>
          {$t('common.submit')}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  /* ─── Non-active-session question notes (non-interrupting) ──────── */
  .qnote-stack {
    position: fixed; right: 24px; bottom: 24px; z-index: 1090;
    display: flex; flex-direction: column-reverse; gap: 8px;
    align-items: flex-end; pointer-events: none;
  }
  .qnote {
    pointer-events: auto;
    display: flex; align-items: center; gap: 8px;
    max-width: 340px;
    padding: 9px 12px;
    background: var(--bg-container);
    border: 1px solid var(--blue-2);
    border-radius: 10px;
    box-shadow: 0 6px 20px rgba(15,23,42,0.12);
    cursor: pointer;
    font-family: inherit;
    text-align: left;
    animation: octo-fadein 0.18s ease;
    font-size: 12px;
  }
  .qnote:hover { border-color: var(--blue-5); }
  .qnote-label {
    font-weight: 600; color: var(--text);
    white-space: nowrap; flex-shrink: 0;
  }
  .qnote-q {
    flex: 1; min-width: 0;
    color: var(--text-secondary);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .qnote-go { color: var(--blue-6); flex-shrink: 0; }

  /* ─── Bottom banner (active session, non-blocking) ─────────────── */
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
  .banner-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px var(--focus-ring); }

  /* ─── Full modal (expanded) ──────────────────────────────────── */
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
  .custom-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px var(--focus-ring); }

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
  @keyframes octo-fadein { from { opacity: 0; } to { opacity: 1; } }
</style>
