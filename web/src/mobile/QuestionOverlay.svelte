<script lang="ts">
  // Mobile question overlay — listens for the globally-broadcast
  // request_user_question event and renders a mobile-native question card.
  // With global broadcast, a question can originate from ANY session (desktop
  // or mobile); this overlay renders whatever is pending regardless of which
  // session is open.
  import { onMount, onDestroy } from 'svelte'
  import { questionModals, type QuestionModalEntry } from '../lib/stores'
  import { ws } from '../lib/ws'
  import { t } from '../lib/i18n'

  const entries = $derived(Object.entries($questionModals))

  // Per-question getDraft state, keyed by sessionId.
  const drafts = $state<Record<string, { selected: string[]; custom: string; expanded: boolean }>>({})

  // Track the question-subscription cleanups so we can remove them on unmount.
  const cleanups: Array<() => void> = []

  onMount(() => {
    // With global broadcast, questions from any session reach every client.
    // These listeners populate the shared questionModals store; the overlay
    // reacts via the `entries` derivation above.
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

  function submitAll() {
    for (const sid of entries.map(([s]) => s)) submit(sid)
  }
</script>

{#if entries.length}
  <div class="qo-overlay">
    <div class="qo-stack">
      {#each entries as [sid, e] (sid)}
        <div class="qo-card" class:expanded={getDraft(sid).expanded}>
          <div class="qo-head">
            <span class="qo-icon">◆</span>
            <span class="qo-title">{e.header || t('question.title')}</span>
            {#if entries.length > 1}<span class="qo-count">{entries.length}</span>{/if}
          </div>

          <p class="qo-body">{e.question}</p>

          {#if e.options?.length}
            <div class="qo-options">
              {#each e.options as opt}
                <button
                  class="qo-opt"
                  class:on={getDraft(sid).selected.includes(opt)}
                  onclick={() => toggle(sid, opt, e.multiSelect ?? false)}
                >
                  <span class="qo-dot"></span>{opt}
                </button>
              {/each}
            </div>
          {/if}

          {#if getDraft(sid).expanded}
            <textarea
              class="qo-free"
              rows="2"
              placeholder={t('question.custom_placeholder')}
              value={getDraft(sid).custom}
              oninput={(e) => { getDraft(sid).custom = e.target.value }}
            ></textarea>
          {/if}

          <div class="qo-actions">
            <button class="qo-cancel" onclick={() => cancel(sid)}>{t('common.cancel')}</button>
            {#if !getDraft(sid).expanded}
              <button class="qo-expand" onclick={() => (getDraft(sid).expanded = true)}>展开</button>
            {/if}
            <button
              class="qo-submit"
              onclick={() => submit(sid)}
              disabled={getDraft(sid).selected.length === 0 && !getDraft(sid).custom.trim()}
            >
              {entries.length > 1 ? '全部提交' : t('common.submit')}
            </button>
          </div>
        </div>
      {/each}
    </div>
  </div>
{/if}

<style>
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
  .qo-stack {
    pointer-events: auto;
    width: 100%;
    max-height: 90vh;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px;
    padding-bottom: calc(16px + env(safe-area-inset-bottom));
  }
  .qo-card {
    background: var(--m-surface);
    border: 1px solid var(--m-border);
    border-radius: 16px;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.12);
  }
  .qo-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .qo-icon {
    color: var(--m-accent);
    font-size: 12px;
  }
  .qo-title {
    flex: 1;
    font-size: 14px;
    font-weight: 600;
    color: var(--m-text);
  }
  .qo-count {
    font-size: 11px;
    color: var(--m-text-3);
    background: var(--m-surface-2);
    border-radius: 999px;
    padding: 2px 8px;
  }
  .qo-body {
    margin: 0;
    font-size: 15px;
    line-height: 1.6;
    color: var(--m-text);
    white-space: pre-wrap;
    word-break: break-word;
  }
  .qo-options {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
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
  .qo-opt.on {
    border-color: var(--m-accent);
    background: var(--m-accent-soft);
    color: var(--m-accent);
  }
  .qo-dot {
    width: 18px;
    height: 18px;
    border-radius: 50%;
    border: 2px solid var(--m-text-4);
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .qo-opt.on .qo-dot {
    border-color: var(--m-accent);
    background: var(--m-accent);
  }
  .qo-opt.on .qo-dot::after {
    content: '';
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: #fff;
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
  .qo-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }
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
