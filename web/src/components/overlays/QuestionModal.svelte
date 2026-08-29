<script lang="ts">
  // Desktop question surface. With global broadcast, a question can originate
  // from ANY session. To avoid interrupting a conversation in progress:
  //
  //  - The modal/banner appears ONLY for the question of the ACTIVE session.
  //  - Questions from other sessions surface as compact, clickable rows
  //    ("Session B needs your input") — tapping one switches to that session,
  //    where the modal then appears.
  //
  // A set is navigated as tabs with a review/submit tab, and each question
  // renders in one of two mutually exclusive layouts (see askStepper).
  // Everything decided rather than drawn lives in askStepper.ts.
  import { questionModals, activeSessionId, sessions, setActiveSession } from '../../lib/stores'
  import { ws } from '../../lib/ws'
  import { t } from '../../lib/i18n'
  import {
    advanceIndex, allAnswered, answerSummary, answersPayload, anyAnswered,
    emptyDraft, emptyDrafts, focusedPreview, hasReviewTab, isAnswered,
    isReviewTab, stepTab, toggleChoice, usesPreviewLayout,
    type AskDraft, type AskOutcome,
  } from '../../lib/askStepper'

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

  const questions = $derived(current?.questions ?? [])
  let qIdx = $state(0)
  let drafts = $state<AskDraft[]>([])
  let focusedLabel = $state('')
  let inputEl = $state<HTMLInputElement | null>(null)
  let rowListEl = $state<HTMLDivElement | null>(null)
  let expanded = $state(false)
  let lastQuestionId = $state<string | null>(null)

  const question = $derived(questions[qIdx])
  const draft = $derived(drafts[qIdx] ?? emptyDraft())
  const onReview = $derived(isReviewTab(questions, qIdx))
  const preview = $derived(usesPreviewLayout(question))
  const previewBody = $derived(preview ? focusedPreview(question, focusedLabel) : '')

  $effect(() => {
    if (current && current.questionId !== lastQuestionId) {
      lastQuestionId = current.questionId
      qIdx = 0
      drafts = emptyDrafts(current.questions ?? [])
      focusedLabel = current.questions?.[0]?.options?.[0]?.label ?? ''
      expanded = false
      inputEl?.focus()
    }
  })

  function setDraft(next: AskDraft) {
    drafts = drafts.map((d, i) => (i === qIdx ? next : d))
  }

  function goTab(i: number) {
    qIdx = i
    focusedLabel = questions[i]?.options?.[0]?.label ?? ''
  }

  // The banner caps the option list and scrolls it. A new tab — or a whole new
  // question, which reuses the same DOM node — must start at the top rather
  // than inherit the previous list's scroll offset.
  $effect(() => {
    qIdx
    current?.questionId
    rowListEl?.scrollTo({ top: 0 })
  })

  function pick(label: string) {
    if (!question) return
    focusedLabel = label
    const next = toggleChoice(draft, question, label)
    setDraft(next)
    // Multi-select accumulates: the user decides when the question is done.
    if (question.multi_select) return
    const to = advanceIndex(questions, qIdx)
    if (to === -1) finish('submitted', drafts.map((d, i) => (i === qIdx ? next : d)))
    else goTab(to)
  }

  function openOther() {
    setDraft({ ...draft, otherOpen: true })
    queueMicrotask(() => inputEl?.focus())
  }

  function commitOther() {
    if (!draft.custom.trim()) {
      setDraft({ ...draft, otherOpen: false })
      return
    }
    const next = { ...draft, choices: [], otherOpen: false }
    setDraft(next)
    const to = advanceIndex(questions, qIdx)
    if (to === -1) finish('submitted', drafts.map((d, i) => (i === qIdx ? next : d)))
    else goTab(to)
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

  function finish(outcome: AskOutcome, ds: AskDraft[] = drafts) {
    if (!current) return
    ws.answerQuestion(current.questionId, outcome, outcome === 'rejected' ? [] : answersPayload(ds))
    clearCurrent()
  }

  // "Chat about this": abandon the set and let the model ask what the user
  // means. The answers given so far ride along in the result.
  function clarify() { finish('clarify') }
  function reject() { finish('rejected') }

  function softClose() { expanded = false }
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); softClose() }
    // Preview layout: a click only focuses (so previews can be browsed without
    // committing), so Enter is the confirm key — Claude Code's own binding.
    // Also reached from the note input, submitting the note with the choice.
    if (e.key === 'Enter' && preview && !onReview && focusedLabel) {
      e.preventDefault()
      pick(focusedLabel)
    }
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
        <span class="qnote-q">
          {entry.questions?.[0]?.question ?? ''}{(entry.questions?.length ?? 0) > 1 ? ` +${entry.questions.length - 1}` : ''}
        </span>
        <span class="qnote-go">{$t('m.view')} ›</span>
      </button>
    {/each}
  </div>
{/if}

{#snippet tabs()}
  {#if questions.length > 1 || hasReviewTab(questions)}
    <div class="tabs" role="tablist">
      {#each questions as q, i}
        <button class="tab" class:active={i === qIdx} role="tab" aria-selected={i === qIdx} onclick={() => goTab(i)}>
          {#if isAnswered(drafts[i])}
            <iconify-icon icon="ant-design:check-outlined" width="10"></iconify-icon>
          {/if}
          {q.header}
        </button>
      {/each}
      {#if hasReviewTab(questions)}
        <button class="tab" class:active={onReview} role="tab" aria-selected={onReview} onclick={() => goTab(questions.length)}>
          {$t('question.submit_tab')}
        </button>
      {/if}
    </div>
  {/if}
{/snippet}

{#snippet rows()}
  <div class="rows" class:with-preview={preview}>
    <div class="row-list" bind:this={rowListEl}>
      {#each question?.options ?? [] as o, i}
        <button
          class="row"
          class:selected={draft.choices.includes(o.label)}
          class:focused={preview && focusedLabel === o.label}
          title={o.description || o.label}
          onclick={() => (preview ? (focusedLabel = o.label) : pick(o.label))}
          ondblclick={() => pick(o.label)}
        >
          <span class="row-mark">
            {#if draft.choices.includes(o.label)}
              <iconify-icon icon="ant-design:check-outlined" width="11"></iconify-icon>
            {/if}
          </span>
          <span class="row-body">
            <span class="row-label">{o.label}</span>
            <!-- The preview layout drops descriptions: the preview stands in
                 for them, exactly as Claude Code renders it. -->
            {#if o.description && !preview}
              <span class="row-desc">{o.description}</span>
            {/if}
          </span>
          <span class="row-num">{i + 1}</span>
        </button>
      {/each}

      <!-- No "Other" row in the preview layout: that text slot holds notes. -->
      {#if !preview}
        {#if draft.otherOpen}
          <div class="other-open">
            <input
              bind:this={inputEl}
              class="other-input"
              type={current?.secret ? 'password' : 'text'}
              autocomplete={current?.secret ? 'new-password' : 'off'}
              placeholder={$t('question.custom_placeholder')}
              value={draft.custom}
              oninput={(e) => setDraft({ ...draft, custom: e.currentTarget.value })}
              onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); commitOther() } }}
            />
            <button class="btn-primary btn-primary-sm" onclick={commitOther}>{$t('common.submit')}</button>
          </div>
        {:else}
          <button class="row" onclick={openOther}>
            <span class="row-mark"></span>
            <span class="row-body"><span class="row-label">{$t('question.other')}</span></span>
            <span class="row-num">{(question?.options?.length ?? 0) + 1}</span>
          </button>
        {/if}
      {/if}

      <button class="row row-clarify" onclick={clarify}>
        <span class="row-mark"></span>
        <span class="row-body"><span class="row-label">{$t('question.chat_about_this')}</span></span>
      </button>
    </div>

    {#if preview}
      <div class="preview-col">
        <pre class="preview-body">{previewBody || $t('question.no_preview')}</pre>
        <input
          class="note-input"
          placeholder={$t('question.add_note')}
          value={draft.notes}
          oninput={(e) => setDraft({ ...draft, notes: e.currentTarget.value })}
        />
      </div>
    {/if}
  </div>
{/snippet}

{#snippet review()}
  <div class="review">
    {#each questions as q, i}
      {#if isAnswered(drafts[i])}
        <div class="review-item">
          <div class="review-q">{q.question}</div>
          <div class="review-a">→ {answerSummary(drafts[i])}</div>
        </div>
      {/if}
    {/each}
    {#if !allAnswered(questions, drafts)}
      <div class="review-warn">{$t('question.not_all_answered')}</div>
    {/if}
  </div>
{/snippet}

<!-- Active session's own question: full modal/banner. -->
{#if current && expanded}
  <div class="backdrop" role="presentation">
    <div class="modal" onkeydown={onKeydown} role="dialog" aria-modal="true" tabindex="-1">
      <div class="modal-header">
        <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
        <span class="modal-title">{$t('question.title')}</span>
        <button class="close-btn" onclick={softClose} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
        </button>
      </div>

      <div class="modal-body">
        {@render tabs()}
        {#if onReview}
          {@render review()}
        {:else}
          <p class="question-text">{question?.question}</p>
          {@render rows()}
        {/if}
      </div>

      <div class="modal-footer">
        <button class="btn-cancel" onclick={reject}>{$t('common.cancel')}</button>
        <span class="spacer"></span>
        {#if onReview}
          <button class="btn-primary" onclick={() => finish('submitted')} disabled={!anyAnswered(drafts)}>
            {$t('question.submit_answers')}
          </button>
        {:else if question?.multi_select}
          <button
            class="btn-primary"
            onclick={() => { const to = advanceIndex(questions, qIdx); if (to === -1) finish('submitted'); else goTab(to) }}
            disabled={draft.choices.length === 0}
          >
            {hasReviewTab(questions) ? $t('question.next') : $t('common.submit')}
          </button>
        {:else if preview}
          <!-- Preview layout: a single click only focuses a row so its preview
               can be inspected without committing, which leaves no visible way
               to answer (double-click and Enter both work, but nothing says
               so). This commits the focused option, note included. -->
          <button class="btn-primary" onclick={() => pick(focusedLabel)} disabled={!focusedLabel}>
            {hasReviewTab(questions) ? $t('question.next') : $t('common.submit')}
          </button>
        {/if}
      </div>
    </div>
  </div>
{:else if current}
  <div class="banner" role="dialog" aria-modal="false">
    <div class="banner-inner">
      <div class="banner-main">
        <iconify-icon icon="ant-design:form-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
        <span class="banner-question">{onReview ? $t('question.submit_tab') : question?.question}</span>
        {#if questions.length > 1}
          <span class="banner-progress">{Math.min(qIdx + 1, questions.length)}/{questions.length}</span>
        {/if}
        <button class="banner-expand" onclick={() => { expanded = true; inputEl?.focus() }}>
          <iconify-icon icon="ant-design:arrows-alt-outlined" width="12"></iconify-icon>
        </button>
      </div>

      {@render tabs()}
      {#if onReview}
        {@render review()}
      {:else}
        {@render rows()}
      {/if}

      <div class="banner-actions">
        <button class="btn-cancel btn-cancel-sm" onclick={reject}>{$t('common.cancel')}</button>
        {#if onReview}
          <button class="btn-primary btn-primary-sm" onclick={() => finish('submitted')} disabled={!anyAnswered(drafts)}>
            {$t('question.submit_answers')}
          </button>
        {:else if question?.multi_select}
          <button
            class="btn-primary btn-primary-sm"
            onclick={() => { const to = advanceIndex(questions, qIdx); if (to === -1) finish('submitted'); else goTab(to) }}
            disabled={draft.choices.length === 0}
          >
            {hasReviewTab(questions) ? $t('question.next') : $t('common.submit')}
          </button>
        {:else if preview}
          <!-- Same confirm affordance as the modal footer above. -->
          <button class="btn-primary btn-primary-sm" onclick={() => pick(focusedLabel)} disabled={!focusedLabel}>
            {hasReviewTab(questions) ? $t('question.next') : $t('common.submit')}
          </button>
        {/if}
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

  /* ─── Question tabs ────────────────────────────────────────────── */
  .tabs { display: flex; flex-wrap: wrap; gap: 6px; }
  .tab {
    display: inline-flex; align-items: center; gap: 4px;
    height: 26px; padding: 0 10px;
    border: 1px solid var(--border); background: var(--bg-container);
    border-radius: 6px;
    font-size: 12px; color: var(--text-secondary);
    cursor: pointer; font-family: inherit;
    max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .tab:hover { border-color: var(--blue-5); color: var(--blue-5); }
  .tab.active { border-color: var(--blue-6); background: var(--blue-1); color: var(--blue-6); font-weight: 600; }

  /* ─── Option rows / preview column ─────────────────────────────── */
  .rows { display: flex; gap: 12px; }
  .rows.with-preview .row-list { flex: 0 0 44%; }
  .row-list { display: flex; flex-direction: column; gap: 6px; flex: 1; min-width: 0; }
  .row {
    display: flex; align-items: flex-start; gap: 8px;
    padding: 8px 10px;
    border: 1px solid var(--border); background: var(--bg-container);
    border-radius: 8px;
    text-align: left; cursor: pointer; font-family: inherit;
    transition: border-color 0.15s, background 0.15s;
  }
  .row:hover { border-color: var(--blue-5); }
  .row.selected { border-color: var(--blue-6); background: var(--blue-1); }
  .row.focused { border-color: var(--blue-5); background: var(--hover-neutral); }
  .row-mark { width: 12px; flex-shrink: 0; color: var(--blue-6); padding-top: 2px; }
  .row-body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .row-label { font-size: 13px; font-weight: 600; color: var(--text); word-break: break-word; }
  .row-desc { font-size: 12px; color: var(--text-tertiary); line-height: 1.45; word-break: break-word; }
  .row-num { font-size: 11px; color: var(--text-tertiary); flex-shrink: 0; }
  .row-clarify { border-style: dashed; }
  .row-clarify .row-label { font-weight: 500; color: var(--text-secondary); }

  .other-open { display: flex; gap: 6px; }
  .other-input {
    flex: 1; height: 32px; padding: 0 10px;
    border: 1px solid var(--blue-6); border-radius: 6px;
    font-size: 13px; color: var(--text);
    font-family: inherit; outline: none; background: var(--bg-container);
  }

  .preview-col { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 6px; }
  .preview-body {
    margin: 0; padding: 10px;
    background: var(--bg-layout); border: 1px solid var(--border-table); border-radius: 8px;
    font-family: var(--font-mono, ui-monospace, monospace); font-size: 12px; line-height: 1.5;
    color: var(--text-secondary);
    max-height: calc(38vh / var(--font-zoom)); overflow: auto;
    white-space: pre;
  }
  .note-input {
    height: 30px; padding: 0 10px;
    border: 1px solid var(--border); border-radius: 6px;
    font-size: 12px; color: var(--text);
    font-family: inherit; outline: none; background: var(--bg-container);
  }
  .note-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px var(--focus-ring); }

  /* Narrow windows stack the preview under the list. */
  @media (max-width: 640px) {
    .rows { flex-direction: column; }
    .rows.with-preview .row-list { flex: 1 1 auto; }
  }

  /* ─── Review / submit tab ──────────────────────────────────────── */
  .review { display: flex; flex-direction: column; gap: 8px; }
  .review-item { display: flex; flex-direction: column; gap: 2px; }
  .review-q { font-size: 13px; color: var(--text); }
  .review-a { font-size: 13px; color: var(--blue-6); padding-left: 10px; }
  .review-warn { font-size: 12px; color: var(--text-tertiary); }

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
  .banner-progress { font-size: 12px; color: var(--text-tertiary); flex-shrink: 0; }
  .banner-expand {
    width: 24px; height: 24px; border: none; background: transparent;
    border-radius: 6px; cursor: pointer; color: var(--text-tertiary);
    display: flex; align-items: center; justify-content: center; flex-shrink: 0;
  }
  .banner-expand:hover { background: var(--hover-neutral); color: var(--blue-6); }
  .banner-actions { display: flex; align-items: center; gap: 8px; justify-content: flex-end; }

  /* The banner shares the chat column with the transcript, so its option list
     is capped and scrolls instead of pushing the messages off-screen. Rows are
     tightened and descriptions clamped to one line; the full text stays
     reachable through the row's title. The expanded modal keeps the roomy
     layout — it already owns the whole viewport. */
  .banner-inner .row-list {
    max-height: calc(30vh / var(--font-zoom));
    overflow-y: auto;
    gap: 4px;
  }
  .banner-inner .row { padding: 6px 10px; }
  .banner-inner .row-desc {
    display: -webkit-box; -webkit-line-clamp: 1; line-clamp: 1;
    -webkit-box-orient: vertical; overflow: hidden;
  }
  .banner-inner .preview-body { max-height: calc(26vh / var(--font-zoom)); }

  /* ─── Full modal (expanded) ──────────────────────────────────── */
  .backdrop {
    position: fixed; inset: 0; z-index: 1100;
    background: var(--scrim);
    display: flex; align-items: center; justify-content: center;
    padding: 24px;
  }
  .modal {
    width: 100%; max-width: 720px;
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
    max-height: calc(30vh / var(--font-zoom)); overflow-y: auto;
  }

  .modal-footer { padding: 14px 24px; border-top: 1px solid var(--border-table); display: flex; align-items: center; gap: 8px; }
  .spacer { flex: 1; }

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
