<script lang="ts">
  // Mobile question surface. With global broadcast, a question can originate
  // from ANY session. To avoid interrupting a conversation in progress:
  //
  //  - The question card appears ONLY for the ACTIVE session (the one open in
  //    the chat detail view).
  //  - Questions from other sessions surface as a compact toast with a "View"
  //    action — tapping it navigates to that session, where the card then
  //    appears.
  //
  // Same model as the desktop picker (tabs + review/submit tab + the two
  // layouts), laid out for one narrow column: the tabs become a pager and a
  // preview goes in a disclosure under the option rather than a second column.
  import { onMount, onDestroy } from 'svelte'
  import { questionModals, activeSessionId, setActiveSession } from '../lib/stores'
  import { ws } from '../lib/ws'
  import { t } from '../lib/i18n'
  import {
    advanceIndex, allAnswered, answerSummary, answersPayload, anyAnswered,
    emptyDraft, emptyDrafts, hasReviewTab, isAnswered, isReviewTab,
    toggleChoice, usesPreviewLayout,
    type AskDraft, type AskOutcome,
  } from '../lib/askStepper'

  // Track WS event cleanups so we can remove them on unmount.
  const cleanups: Array<() => void> = []

  // Per-session state: which tab is open and one draft per question.
  const state = $state<Record<string, { qIdx: number; drafts: AskDraft[] }>>({})

  // Read-only view for the template. Seeding happens in the $effect below and
  // in the event handlers — Svelte 5 forbids mutating state during render, and
  // a lazily-creating getter called from the markup takes the whole card down.
  function slot(sid: string) {
    return state[sid] ?? { qIdx: 0, drafts: emptyDrafts($questionModals[sid]?.questions ?? []) }
  }

  function ensure(sid: string) {
    if (!state[sid]) {
      state[sid] = { qIdx: 0, drafts: emptyDrafts($questionModals[sid]?.questions ?? []) }
    }
    return state[sid]
  }

  function draftAt(sid: string, i: number): AskDraft {
    return slot(sid).drafts[i] ?? emptyDraft()
  }

  // Reset when the question set changes.
  const lastIds: Record<string, string> = {}
  $effect(() => {
    for (const [sid, e] of Object.entries($questionModals)) {
      if (lastIds[sid] !== e.questionId) {
        lastIds[sid] = e.questionId
        state[sid] = { qIdx: 0, drafts: emptyDrafts(e.questions ?? []) }
      }
    }
  })

  const activeQ = $derived($activeSessionId ? $questionModals[$activeSessionId] : undefined)
  const others = $derived(Object.entries($questionModals).filter(([sid]) => sid !== $activeSessionId))

  onMount(() => {
    cleanups.push(ws.on('request_user_question', (ev: any) => {
      const sid = ev.session_id
      if (!sid) return
      questionModals.update(m => ({
        ...m,
        [sid]: {
          questionId: ev.question_id,
          sessionId: sid,
          questions: ev.questions ?? [],
          secret: ev.secret === true,
          dismissed: false,
        },
      }))
    }))
    cleanups.push(ws.on('dismiss_user_question', (ev: any) => {
      const sid = ev.session_id
      if (!sid) return
      questionModals.update(m => {
        const n = { ...m }
        delete n[sid]
        return n
      })
    }))
  })

  onDestroy(() => {
    cleanups.forEach(fn => fn())
  })

  function setDraft(sid: string, i: number, next: AskDraft) {
    const s = ensure(sid)
    s.drafts = s.drafts.map((d, j) => (j === i ? next : d))
  }

  function goTab(sid: string, i: number) {
    ensure(sid).qIdx = i
  }

  function pick(sid: string, label: string) {
    const entry = $questionModals[sid]
    const s = ensure(sid)
    const q = entry?.questions?.[s.qIdx]
    if (!q) return
    const next = toggleChoice(draftAt(sid, s.qIdx), q, label)
    setDraft(sid, s.qIdx, next)
    if (q.multi_select) return
    const to = advanceIndex(entry.questions, s.qIdx)
    if (to === -1) finish(sid, 'submitted')
    else s.qIdx = to
  }

  function clear(sid: string) {
    questionModals.update(m => {
      const n = { ...m }
      delete n[sid]
      return n
    })
  }

  function finish(sid: string, outcome: AskOutcome) {
    const e = $questionModals[sid]
    if (!e) return
    ws.answerQuestion(e.questionId, outcome, outcome === 'rejected' ? [] : answersPayload(slot(sid).drafts))
    clear(sid)
  }
</script>

<!-- Non-active sessions: compact toast with a "View" action. -->
{#if others.length}
  <div class="qo-toast-stack" role="status" aria-live="polite">
    {#each others as [sid, e] (sid)}
      <button class="qo-toast" onclick={() => setActiveSession(sid)}>
        <span class="qo-toast-icon">◆</span>
        <span class="qo-toast-q">
          {e.questions?.[0]?.question ?? ''}{(e.questions?.length ?? 0) > 1 ? ` +${e.questions.length - 1}` : ''}
        </span>
        <span class="qo-toast-go">{$t('m.view')} ›</span>
      </button>
    {/each}
  </div>
{/if}

<!-- Active session's own question: full card. -->
{#if activeQ}
  {@const sid = activeQ.sessionId}
  {@const s = slot(sid)}
  {@const questions = activeQ.questions ?? []}
  {@const onReview = isReviewTab(questions, s.qIdx)}
  {@const q = questions[s.qIdx]}
  {@const d = draftAt(sid, s.qIdx)}
  <div class="qo-overlay">
    <div class="qo-card">
      <div class="qo-head">
        <span class="qo-icon">◆</span>
        <span class="qo-title">{onReview ? $t('question.submit_tab') : (q?.header || $t('question.title'))}</span>
        {#if questions.length > 1}
          <span class="qo-progress">{Math.min(s.qIdx + 1, questions.length)}/{questions.length}</span>
        {/if}
      </div>

      {#if questions.length > 1 || hasReviewTab(questions)}
        <div class="qo-pager">
          {#each questions as pq, i}
            <button class="qo-pill" class:on={i === s.qIdx} onclick={() => goTab(sid, i)}>
              {#if isAnswered(s.drafts[i])}✓{/if} {pq.header}
            </button>
          {/each}
          {#if hasReviewTab(questions)}
            <button class="qo-pill" class:on={onReview} onclick={() => goTab(sid, questions.length)}>
              {$t('question.submit_tab')}
            </button>
          {/if}
        </div>
      {/if}

      {#if onReview}
        <div class="qo-review">
          {#each questions as rq, i}
            {#if isAnswered(s.drafts[i])}
              <div class="qo-review-item">
                <div class="qo-review-q">{rq.question}</div>
                <div class="qo-review-a">→ {answerSummary(s.drafts[i])}</div>
              </div>
            {/if}
          {/each}
          {#if !allAnswered(questions, s.drafts)}
            <div class="qo-review-warn">{$t('question.not_all_answered')}</div>
          {/if}
        </div>
      {:else}
        <p class="qo-body">{q?.question}</p>

        <div class="qo-options">
          {#each q?.options ?? [] as o}
            <div class="qo-opt-wrap">
              <button class="qo-opt" class:on={d.choices.includes(o.label)} onclick={() => pick(sid, o.label)}>
                <span class="qo-dot"></span>
                <span class="qo-opt-body">
                  <span class="qo-opt-label">{o.label}</span>
                  <!-- The preview layout drops descriptions, as on the desktop. -->
                  {#if o.description && !usesPreviewLayout(q)}
                    <span class="qo-opt-desc">{o.description}</span>
                  {/if}
                </span>
              </button>
              {#if o.preview && usesPreviewLayout(q)}
                <details class="qo-preview">
                  <summary>{$t('question.preview')}</summary>
                  <pre>{o.preview}</pre>
                </details>
              {/if}
            </div>
          {/each}

          <!-- Notes replace the free-text slot in the preview layout. -->
          {#if usesPreviewLayout(q)}
            <textarea
              class="qo-free"
              rows="2"
              placeholder={$t('question.add_note')}
              value={d.notes}
              oninput={(e) => setDraft(sid, s.qIdx, { ...d, notes: (e.target as HTMLTextAreaElement).value })}
            ></textarea>
          {:else}
            <textarea
              class="qo-free"
              rows="2"
              placeholder={$t('question.custom_placeholder')}
              value={d.custom}
              oninput={(e) => setDraft(sid, s.qIdx, { ...d, custom: (e.target as HTMLTextAreaElement).value })}
            ></textarea>
          {/if}

          <button class="qo-opt qo-clarify" onclick={() => finish(sid, 'clarify')}>
            <span class="qo-opt-body"><span class="qo-opt-label">{$t('question.chat_about_this')}</span></span>
          </button>
        </div>
      {/if}

      <div class="qo-actions">
        <button class="qo-cancel" onclick={() => finish(sid, 'rejected')}>{$t('common.cancel')}</button>
        {#if onReview}
          <button class="qo-submit" onclick={() => finish(sid, 'submitted')} disabled={!anyAnswered(s.drafts)}>
            {$t('question.submit_answers')}
          </button>
        {:else}
          <button
            class="qo-submit"
            onclick={() => { const to = advanceIndex(questions, s.qIdx); if (to === -1) finish(sid, 'submitted'); else goTab(sid, to) }}
            disabled={!isAnswered(d) && !d.notes.trim()}
          >
            {hasReviewTab(questions) ? $t('question.next') : $t('common.submit')}
          </button>
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  /* ─── Non-active-session toasts (non-interrupting) ─────────────── */
  .qo-toast-stack {
    position: fixed;
    top: calc(12px + env(safe-area-inset-top));
    left: 0;
    right: 0;
    z-index: 2147483646;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 0 12px;
    pointer-events: none;
  }
  .qo-toast {
    pointer-events: auto;
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    max-width: 480px;
    padding: 10px 14px;
    background: var(--m-surface);
    border: 1px solid var(--m-border);
    border-radius: 12px;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.12);
    cursor: pointer;
    font-family: inherit;
    text-align: left;
    animation: octo-toast-in 0.18s ease;
    font-size: 13px;
  }
  .qo-toast:hover { border-color: var(--m-accent); }
  .qo-toast-icon { color: var(--m-accent); font-size: 10px; flex-shrink: 0; }
  .qo-toast-q {
    flex: 1; min-width: 0;
    color: var(--m-text);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .qo-toast-go { color: var(--m-accent); flex-shrink: 0; }
  @keyframes octo-toast-in {
    from { opacity: 0; transform: translateY(-8px); }
    to   { opacity: 1; transform: translateY(0); }
  }

  /* ─── Active-session card overlay ──────────────────────────────── */
  .qo-overlay {
    position: fixed;
    inset: 0;
    z-index: 2147483646;
    display: flex;
    align-items: flex-end;
    justify-content: stretch;
    background: rgba(0, 0, 0, 0.35);
    pointer-events: none;
  }
  .qo-card {
    pointer-events: auto;
    width: 100%;
    max-height: calc(90vh / var(--font-zoom));
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px;
    padding-bottom: calc(16px + env(safe-area-inset-bottom));
    background: var(--m-surface);
    border: 1px solid var(--m-border);
    border-radius: 16px 16px 0 0;
    box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.12);
  }
  .qo-head { display: flex; align-items: center; gap: 8px; }
  .qo-icon { color: var(--m-accent); font-size: 12px; }
  .qo-title { font-size: 14px; font-weight: 600; color: var(--m-text); flex: 1; }
  .qo-progress { font-size: 12px; color: var(--m-text-2); }

  .qo-pager { display: flex; flex-wrap: wrap; gap: 6px; }
  .qo-pill {
    height: 28px;
    padding: 0 10px;
    border: 1px solid var(--m-border);
    border-radius: 999px;
    background: var(--m-surface-2);
    color: var(--m-text-2);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
    max-width: 45%;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .qo-pill.on { border-color: var(--m-accent); background: var(--m-accent-soft); color: var(--m-accent); }

  .qo-review { display: flex; flex-direction: column; gap: 8px; }
  .qo-review-item { display: flex; flex-direction: column; gap: 2px; }
  .qo-review-q { font-size: 14px; color: var(--m-text); }
  .qo-review-a { font-size: 14px; color: var(--m-accent); padding-left: 10px; }
  .qo-review-warn { font-size: 12px; color: var(--m-text-2); }

  .qo-body {
    margin: 0;
    font-size: 15px;
    line-height: 1.6;
    color: var(--m-text);
    white-space: pre-wrap;
    word-break: break-word;
  }
  .qo-options { display: flex; flex-direction: column; gap: 8px; }
  .qo-opt-wrap { display: flex; flex-direction: column; gap: 4px; }
  .qo-opt {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    width: 100%;
    text-align: left;
    padding: 12px 14px;
    border: 1px solid var(--m-border);
    border-radius: 12px;
    background: var(--m-surface-2);
    font-size: 14px;
    color: var(--m-text);
    cursor: pointer;
    font-family: inherit;
    transition: border-color 0.15s, background 0.15s;
  }
  .qo-opt.on { border-color: var(--m-accent); background: var(--m-accent-soft); color: var(--m-accent); }
  .qo-opt-body { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .qo-opt-label { font-weight: 600; word-break: break-word; }
  .qo-opt-desc { font-size: 12px; color: var(--m-text-2); line-height: 1.45; }
  .qo-clarify { border-style: dashed; }
  .qo-clarify .qo-opt-label { font-weight: 500; color: var(--m-text-2); }
  .qo-dot {
    width: 18px; height: 18px;
    border-radius: 50%;
    border: 2px solid var(--m-text-4);
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .qo-opt.on .qo-dot { border-color: var(--m-accent); background: var(--m-accent); }
  .qo-opt.on .qo-dot::after { content: ''; width: 6px; height: 6px; border-radius: 50%; background: #fff; }

  .qo-preview { font-size: 12px; color: var(--m-text-2); padding-left: 14px; }
  .qo-preview pre {
    margin: 6px 0 0;
    padding: 10px;
    background: var(--m-bg);
    border: 1px solid var(--m-border);
    border-radius: 10px;
    overflow-x: auto;
    font-size: 12px;
    line-height: 1.5;
  }

  .qo-free {
    border: 1px solid var(--m-border);
    border-radius: 10px;
    padding: 10px 12px;
    font-size: 14px;
    font-family: inherit;
    resize: none;
    outline: none;
    color: var(--m-text);
    background: var(--m-bg);
  }
  .qo-free:focus { border-color: var(--m-accent); }
  .qo-actions { display: flex; align-items: center; gap: 8px; }
  .qo-cancel {
    height: 38px;
    padding: 0 16px;
    border: 1px solid var(--m-border);
    border-radius: 10px;
    background: var(--m-surface-2);
    color: var(--m-text-2);
    font-size: 14px;
    cursor: pointer;
    font-family: inherit;
  }
  .qo-submit {
    flex: 1;
    height: 38px;
    border: none;
    border-radius: 10px;
    background: var(--m-accent);
    color: #fff;
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
    font-family: inherit;
  }
  .qo-submit:disabled { opacity: 0.4; cursor: default; }
</style>
