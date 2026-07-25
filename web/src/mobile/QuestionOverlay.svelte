<script lang="ts">
  // Mobile question surface. With global broadcast, a question can originate
  // from ANY session. To avoid interrupting a conversation in progress:
  //
  //  - The question card appears ONLY for the ACTIVE session (the one open in
  //    the chat detail view).
  //  - Questions from other sessions surface as a compact toast with a "View"
  //    action — tapping it navigates to that session, where the card then
  //    appears.
  import { onMount, onDestroy } from 'svelte'
  import { questionModals, activeSessionId } from '../lib/stores'
  import { ws } from '../lib/ws'
  import { t } from '../lib/i18n'

  // Track WS event cleanups so we can remove them on unmount.
  const cleanups: Array<() => void> = []

  // Per-question draft state, keyed by sessionId.
  const drafts = $state<Record<string, { selected: string[]; custom: string; expanded: boolean }>>({})

  function getDraft(sid: string) {
    if (!drafts[sid]) drafts[sid] = { selected: [], custom: '', expanded: false }
    return drafts[sid]
  }

  // Reset a draft when its question changes.
  const lastIds: Record<string, string> = {}
  $effect(() => {
    for (const [sid, e] of Object.entries($questionModals)) {
      if (lastIds[sid] !== e.questionId) {
        lastIds[sid] = e.questionId
        drafts[sid] = { selected: [], custom: '', expanded: false }
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
          question: ev.question,
          options: ev.options,
          multiSelect: ev.multi_select,
          header: ev.header,
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

  function toggle(sid: string, opt: string, multi: boolean) {
    const d = getDraft(sid)
    if (multi) {
      d.selected = d.selected.includes(opt) ? d.selected.filter(o => o !== opt) : [...d.selected, opt]
    } else {
      d.selected = d.selected[0] === opt ? [] : [opt]
    }
  }

  function clear(sid: string) {
    questionModals.update(m => {
      const n = { ...m }
      delete n[sid]
      return n
    })
  }

  function submit(sid: string) {
    const e = $questionModals[sid]
    const d = getDraft(sid)
    if (!e || (d.selected.length === 0 && !d.custom.trim())) return
    ws.answerQuestion(e.questionId, [...d.selected], d.custom)
    clear(sid)
  }

  function cancel(sid: string) {
    const e = $questionModals[sid]
    if (!e) return
    ws.answerQuestion(e.questionId, [], '', true)
    clear(sid)
  }
</script>

<!-- Non-active sessions: compact toast with a "View" action. -->
{#if others.length}
  <div class="qo-toast-stack" role="status" aria-live="polite">
    {#each others as [sid, e] (sid)}
      <button class="qo-toast" onclick={() => setActiveSession(sid)}>
        <span class="qo-toast-icon">◆</span>
        <span class="qo-toast-q">{e.question}</span>
        <span class="qo-toast-go">{$t('m.view')} ›</span>
      </button>
    {/each}
  </div>
{/if}

<!-- Active session's own question: full card. -->
{#if activeQ}
  <div class="qo-overlay">
    <div class="qo-card" class:expanded={getDraft(activeQ.sessionId).expanded}>
      <div class="qo-head">
        <span class="qo-icon">◆</span>
        <span class="qo-title">{activeQ.header || t('question.title')}</span>
      </div>

      <p class="qo-body">{activeQ.question}</p>

      {#if activeQ.options?.length}
        <div class="qo-options">
          {#each activeQ.options as opt}
            <button
              class="qo-opt"
              class:on={getDraft(activeQ.sessionId).selected.includes(opt)}
              onclick={() => toggle(activeQ.sessionId, opt, activeQ.multiSelect ?? false)}
            >
              <span class="qo-dot"></span>{opt}
            </button>
          {/each}
        </div>
      {/if}

      {#if getDraft(activeQ.sessionId).expanded}
        <textarea
          class="qo-free"
          rows="2"
          placeholder={t('question.custom_placeholder')}
          value={getDraft(activeQ.sessionId).custom}
          oninput={(e) => { getDraft(activeQ.sessionId).custom = e.target.value }}
        ></textarea>
      {/if}

      <div class="qo-actions">
        <button class="qo-cancel" onclick={() => cancel(activeQ.sessionId)}>{t('common.cancel')}</button>
        {#if !getDraft(activeQ.sessionId).expanded}
          <button class="qo-expand" onclick={() => (getDraft(activeQ.sessionId).expanded = true)}>展开</button>
        {/if}
        <button
          class="qo-submit"
          onclick={() => submit(activeQ.sessionId)}
          disabled={getDraft(activeQ.sessionId).selected.length === 0 && !getDraft(activeQ.sessionId).custom.trim()}
        >
          {t('common.submit')}
        </button>
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
    max-height: 90vh;
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
  .qo-title { font-size: 14px; font-weight: 600; color: var(--m-text); }
  .qo-body {
    margin: 0;
    font-size: 15px;
    line-height: 1.6;
    color: var(--m-text);
    white-space: pre-wrap;
    word-break: break-word;
  }
  .qo-options { display: flex; flex-direction: column; gap: 8px; }
  .qo-opt {
    display: flex;
    align-items: center;
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
  .qo-expand {
    height: 38px;
    padding: 0 12px;
    border: none;
    background: transparent;
    color: var(--m-accent);
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
