<script lang="ts">
  // Mobile chat detail: renders a session's transcript and sends replies, wired
  // to the shared stores via chatWiring (which reuses the same store helpers the
  // desktop uses). Tool calls render through the desktop ToolGroup (same
  // expandable cards: command, output, diff, todo list); the composer is a
  // lightweight mobile input rather than the desktop Composer.
  import { tick } from 'svelte'
  import { get } from 'svelte/store'
  import { activeSessionId, chatMessages, chatStreaming, chatProgress, chatTodos, chatTurnStart, chatContextUsage, sessions, clearMsgs, artifacts } from '../lib/stores'
  import type { Attachment } from '../lib/composerDrafts'
  import { ws } from '../lib/ws'
  import { wireMobileSession, loadMobileHistory, sendMobile, mobileSteers } from './chatWiring'
  import { renderMarkdown } from '../lib/markdown'
  import { t } from '../lib/i18n'
  import ArtifactsSheet from './ArtifactsSheet.svelte'
  import SessionSettings from './SessionSettings.svelte'
  import AuthedImage from './AuthedImage.svelte'
  import ToolGroup from '../components/chat/ToolGroup.svelte'
  import AttachmentStrip from './AttachmentStrip.svelte'
  import { AttachmentTray } from './attachments.svelte'

  let { onBack, onViewApproval, initial = null, onInitialSent }: {
    onBack: () => void
    onViewApproval: () => void
    // Queued first message from the new-task sheet, sent once the WS
    // subscription is live (so the reply stream isn't missed). Carries the id
    // of the session it was queued for: activeSessionId can flip to another
    // session while the history fetch is in flight (the question-toast "View"
    // button sets it without going through openSession), and the prompt must
    // only ever be sent to its own session (#2093). onInitialSent lets the
    // owner clear it so a remount doesn't re-send.
    initial?: { id: string; prompt: string; files?: Attachment[] } | null
    onInitialSent?: () => void
  } = $props()

  const sid = $derived($activeSessionId ?? '')
  let showArtifacts = $state(false)
  let showSettings = $state(false)
  const msgs = $derived($chatMessages[sid] ?? [])
  const steers = $derived($mobileSteers[sid] ?? [])
  const streaming = $derived($chatStreaming[sid] ?? false)
  const session = $derived($sessions.find(s => s.id === sid) ?? null)
  const title = $derived(session?.title || session?.name || $t('m.session'))
  // A confirmation raised for THIS session while it's open — surface an inline
  // entry to the approval detail (the feed card is out of view here).
  const pendingApproval = $derived(session?.pending_confirmation ?? false)
  // How full the context is, as the desktop composer's chip shows it.
  const ctxUsage = $derived(Number($chatContextUsage[sid] ?? session?.context_usage ?? 0))

  function stop() {
    if (sid) ws.interrupt(sid)
  }

  // The session plan, as the desktop's plan card shows it: a summary with a
  // progress bar, steps on tap. Dropped 3s after every step completes (the
  // server doesn't replay a finished plan, so it won't come back on reopen).
  const todos = $derived($chatTodos[sid] ?? [])
  const planDone = $derived(todos.filter((s: any) => s.status === 'completed').length)
  let planOpen = $state(false)
  const PLAN_DISMISS_MS = 3000
  $effect(() => {
    const s = sid
    if (!s || todos.length === 0 || todos.some((x: any) => x.status !== 'completed')) return
    const timer = setTimeout(() => chatTodos.update(t => ({ ...t, [s]: [] })), PLAN_DISMISS_MS)
    return () => clearTimeout(timer)
  })

  // Status line while a turn runs: the server's progress message (or a plain
  // "Thinking") plus the time since the turn began. The start is kept in the
  // shared per-session store, as on desktop, so reopening the chat mid-turn
  // doesn't restart the clock.
  const progress = $derived($chatProgress[sid] ?? null)
  const statusLabel = $derived(progress?.message && progress.message !== 'Thinking' ? progress.message : $t('chat.thinking_0'))
  let now = $state(Date.now())
  $effect(() => {
    const s = sid
    if (!s) return
    if (streaming) {
      if (!get(chatTurnStart)[s]) chatTurnStart.update(m => ({ ...m, [s]: Date.now() }))
      now = Date.now()
      const tick = setInterval(() => (now = Date.now()), 1000)
      return () => clearInterval(tick)
    } else if (get(chatTurnStart)[s]) {
      chatTurnStart.update(m => { const n = { ...m }; delete n[s]; return n })
    }
  })
  const elapsed = $derived.by(() => {
    const start = $chatTurnStart[sid]
    if (!start) return ''
    const sec = Math.max(0, Math.floor((now - start) / 1000))
    return sec < 60 ? `${sec}s` : `${Math.floor(sec / 60)}m${sec % 60}s`
  })

  let draft = $state('')
  let scroller: HTMLElement | undefined
  let composing = $state(false)

  const tray = new AttachmentTray()
  let fileInput: HTMLInputElement | undefined = $state()

  // Staged files never follow the user into another session.
  $effect(() => {
    void sid
    tray.clear()
  })

  // Subscribe + wire while this session is open; tear down on switch/unmount.
  $effect(() => {
    const s = sid
    if (!s) return
    clearMsgs(s)
    // chatTodos outlives this view, and the server doesn't replay a plan that
    // finished meanwhile — drop it; an unfinished one comes back on subscribe.
    chatTodos.update(t => ({ ...t, [s]: [] }))
    // Same for pending steers: one picked up while the chat was closed is in
    // the history now, or its echo comes back in the subscribe replay.
    mobileSteers.update(m => ({ ...m, [s]: [] }))
    let cancelled = false
    // Subscribe only after history renders (same ordering the desktop relies on).
    loadMobileHistory(s).then(() => {
      if (cancelled) return
      ws.subscribe(s)
      if ((initial?.prompt || initial?.files?.length) && initial.id === s) {
        sendMobile(s, initial.prompt, initial.files ?? [])
        onInitialSent?.()
      }
    })
    const cleanup = wireMobileSession(s)
    return () => {
      cancelled = true
      ws.unsubscribe(s)
      cleanup()
    }
  })

  // Keep pinned to the latest message as the transcript grows.
  $effect(() => {
    void msgs.length
    tick().then(() => {
      if (scroller) scroller.scrollTop = scroller.scrollHeight
    })
  })

  function send() {
    const text = draft.trim()
    if ((!text && !tray.items.length) || !sid || !tray.ready(text)) return
    sendMobile(sid, text, [...tray.items])
    draft = ''
    tray.clear()
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !composing) {
      e.preventDefault()
      send()
    }
  }
</script>

<header class="dhead">
  <button class="back" onclick={onBack} aria-label={$t('m.back')}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
  </button>
  <button class="dtitle" onclick={() => (showSettings = true)} aria-haspopup="dialog">
    <span class="t">{title}</span>
    <span class="sub">
      <span class="d" class:live={streaming}></span>
      <span class="sub-text">{streaming ? $t('m.working') : $t('m.idle')}{session?.model ? ` · ${session.model}` : ''}{` · ${ctxUsage}%`}</span>
      <svg class="chev" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="m6 9 6 6 6-6"/></svg>
    </span>
  </button>
  {#if $artifacts.length}
    <button class="arts" onclick={() => (showArtifacts = true)} aria-label={$t('m.artifacts')}>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/></svg>
      <span>{$artifacts.length}</span>
    </button>
  {/if}
</header>

{#if showSettings && sid}
  <SessionSettings sessionId={sid} onClose={() => (showSettings = false)} />
{/if}

{#if showArtifacts}
  <ArtifactsSheet onClose={() => (showArtifacts = false)} />
{/if}

{#if pendingApproval}
  <button class="approve-banner" onclick={onViewApproval}>
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--m-warning)" stroke-width="2"><path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 2 18a2 2 0 0 0 1.7 3h16.6A2 2 0 0 0 22 18L13.7 3.9a2 2 0 0 0-3.4 0z"/></svg>
    <span class="txt">{$t('m.approval_banner')}</span>
    <span class="go">{$t('m.view')} ›</span>
  </button>
{/if}

{#if todos.length}
  <button class="plan" onclick={() => (planOpen = !planOpen)} aria-expanded={planOpen}>
    <span class="plan-head">
      <span class="plan-title">{$t('agent.plan')}</span>
      <span class="plan-meta">{planDone} / {todos.length}</span>
      <span class="plan-bar"><span style="width:{Math.round((planDone / todos.length) * 100)}%"></span></span>
      <svg class="chev" class:open={planOpen} width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m6 9 6 6 6-6"/></svg>
    </span>
    {#if planOpen}
      <span class="plan-steps">
        {#each todos as step, i (i)}
          <span class="step {step.status}">{step.content}</span>
        {/each}
      </span>
    {/if}
  </button>
{/if}

<div class="scroll" bind:this={scroller}>
  {#each msgs as msg (msg.id)}
    {#if msg.type === 'user'}
      {#if msg.images?.length}
        <div class="atts sent" class:pending={msg.pending}>
          {#each msg.images as ref, i (i)}
            {#if ref.startsWith('pdf:')}
              <span class="chip">{ref.slice(4)}</span>
            {:else}
              <span class="thumb"><AuthedImage src={ref} /></span>
            {/if}
          {/each}
        </div>
      {/if}
      {#if msg.content}
        <div class="bubble user" class:pending={msg.pending}>{msg.content}</div>
      {/if}
    {:else if msg.type === 'assistant'}
      <div class="bubble agent">
        {#if msg.thinking}<div class="thoughts">{@html renderMarkdown(msg.thinking)}</div>{/if}
        <div class="body">{@html renderMarkdown(msg.content)}{#if msg.streaming}<span class="caret">▋</span>{/if}</div>
      </div>
    {:else if msg.type === 'thinking'}
      <div class="thoughts standalone">{@html renderMarkdown(msg.thinking)}</div>
    {:else if msg.type === 'tool_group'}
      <div class="tools"><ToolGroup tools={msg.tools} streaming={msg.streaming} /></div>
    {:else if msg.type === 'notice'}
      <div class="notice" class:err={msg.level === 'error'}>{msg.content}</div>
    {/if}
  {/each}
  {#each steers as s (s.id)}
    <div class="steer">
      <span class="spin"></span>
      <span class="steer-body">
        {#if s.content}<span class="steer-text">{s.content}</span>{/if}
        {#if s.images.length}
          <span class="atts">
            {#each s.images as ref, i (i)}
              {#if ref.startsWith('pdf:')}<span class="chip">{ref.slice(4)}</span>{:else}<span class="thumb small"><AuthedImage src={ref} /></span>{/if}
            {/each}
          </span>
        {/if}
      </span>
    </div>
  {/each}
</div>

{#if streaming}
  <div class="stopbar">
    <span class="status"><span class="label">{statusLabel}</span>{#if elapsed}<span class="time">{elapsed}</span>{/if}</span>
    <button class="stop" onclick={stop}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>
      {$t('m.stop_gen')}
    </button>
  </div>
{/if}

{#if tray.items.length}
  <div class="staged"><AttachmentStrip {tray} /></div>
{/if}

<div class="composer">
  <input type="file" multiple hidden bind:this={fileInput} onchange={(e) => tray.addFromInput(e)} />
  <button class="attach" onclick={() => fileInput?.click()} aria-label={$t('chat.attach_file')}>
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m21.4 11.1-9.2 9.2a6 6 0 0 1-8.5-8.5l9.2-9.2a4 4 0 0 1 5.7 5.7l-9.2 9.2a2 2 0 0 1-2.8-2.8l8.5-8.5"/></svg>
  </button>
  <textarea
    bind:value={draft}
    onkeydown={onKey}
    oncompositionstart={() => (composing = true)}
    oncompositionend={() => (composing = false)}
    placeholder={$t('m.reply_ph')}
    rows="1"
  ></textarea>
  <button class="send" onclick={send} disabled={(!draft.trim() && !tray.items.length) || tray.uploading} aria-label={$t('m.send')}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2"><path d="M22 2 11 13M22 2l-7 20-4-9-9-4z"/></svg>
  </button>
</div>

<style>
  .dhead {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 14px 12px;
  }
  .back {
    flex: none;
    width: 34px;
    height: 34px;
    border-radius: 50%;
    border: none;
    background: var(--m-surface);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    box-shadow: var(--m-shadow-card);
  }
  .dtitle { flex: 1; min-width: 0; padding: 0; border: none; background: none; font: inherit; text-align: left; cursor: pointer; }
  .dtitle .t {
    display: block;
    font-size: 15px;
    font-weight: 600;
    color: var(--m-text);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .arts {
    flex: none;
    display: flex;
    align-items: center;
    gap: 5px;
    height: 34px;
    padding: 0 12px;
    border-radius: 17px;
    border: none;
    background: var(--m-surface);
    color: var(--m-accent);
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    box-shadow: var(--m-shadow-card);
  }
  .dtitle .sub {
    display: flex;
    align-items: center;
    gap: 5px;
    margin-top: 2px;
    font-size: 11px;
    color: var(--m-text-3);
  }
  .sub .sub-text { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sub .chev { flex: none; }
  .sub .d { flex: none; width: 7px; height: 7px; border-radius: 50%; background: var(--m-text-4); }
  .sub .d.live { background: var(--m-success); }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    padding: 8px 16px 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .bubble {
    max-width: 82%;
    padding: 11px 14px;
    font-size: 14px;
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .bubble.user {
    align-self: flex-end;
    background: var(--m-accent);
    color: #fff;
    border-radius: 14px 4px 14px 14px;
  }
  .bubble.user.pending { opacity: .6; }
  .bubble.agent {
    align-self: flex-start;
    background: var(--m-surface);
    color: var(--m-text);
    border-radius: 4px 14px 14px 14px;
    box-shadow: var(--m-shadow-card);
  }
  .thoughts {
    font-size: 12.5px;
    color: var(--m-text-3);
    border-left: 2px solid var(--m-border);
    padding-left: 8px;
    margin-bottom: 6px;
    white-space: pre-wrap;
  }
  .thoughts.standalone {
    align-self: flex-start;
    max-width: 82%;
    margin: 0;
  }
  .caret { color: var(--m-accent); animation: bk 1.1s steps(1) infinite; }
  @keyframes bk { 50% { opacity: .2 } }
  @media (prefers-reduced-motion: reduce) { .caret { animation: none } }
  .tools {
    align-self: stretch;
    min-width: 0;
  }
  .notice {
    align-self: center;
    font-size: 12px;
    color: var(--m-text-3);
    text-align: center;
  }
  .notice.err { color: var(--m-error); }
  .steer {
    align-self: flex-end;
    display: flex;
    align-items: center;
    gap: 8px;
    max-width: 82%;
    padding: 9px 12px;
    border: 1px dashed var(--m-accent);
    border-radius: 14px 4px 14px 14px;
    color: var(--m-text-2);
    font-size: 14px;
  }
  .steer-body { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
  .steer-text { white-space: pre-wrap; word-break: break-word; }
  .thumb.small { width: 40px; height: 40px; border-radius: 8px; }
  .steer .spin {
    flex: none;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    border: 1.5px solid var(--m-accent);
    border-right-color: transparent;
    animation: spin .8s linear infinite;
  }
  @media (prefers-reduced-motion: reduce) { .steer .spin { animation: none } }
  .composer {
    flex: none;
    display: flex;
    align-items: flex-end;
    gap: 8px;
    padding: 10px 12px calc(14px + env(safe-area-inset-bottom));
    background: var(--m-surface);
    border-top: 1px solid var(--m-border-2);
  }
  .composer textarea {
    flex: 1;
    resize: none;
    border: 1px solid var(--m-border);
    border-radius: 12px;
    padding: 9px 12px;
    font-size: 14px;
    font-family: inherit;
    outline: none;
    max-height: 96px;
    color: var(--m-text);
    background: var(--m-bg);
  }
  .composer .send {
    flex: none;
    width: 38px;
    height: 38px;
    border-radius: 50%;
    background: var(--m-accent);
    border: none;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
  }
  .composer .send:disabled { opacity: .4; cursor: default; }
  .composer .attach {
    flex: none;
    width: 38px;
    height: 38px;
    border: none;
    background: none;
    color: var(--m-text-2);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
  }
  .atts {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .atts.sent {
    align-self: flex-end;
    justify-content: flex-end;
    max-width: 82%;
  }
  .atts.sent.pending { opacity: .6; }
  .staged {
    flex: none;
    padding: 8px 12px 0;
    background: var(--m-surface);
    border-top: 1px solid var(--m-border-2);
  }
  .staged + .composer { border-top: none; }
  .thumb {
    position: relative;
    width: 64px;
    height: 64px;
    border-radius: 10px;
    overflow: hidden;
    flex: none;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    max-width: 200px;
    padding: 6px 10px;
    border-radius: 10px;
    background: var(--m-surface-2);
    border: 1px solid var(--m-border);
    color: var(--m-text);
    font-size: 12.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .approve-banner {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 12px 8px;
    padding: 10px 14px;
    background: var(--m-tag-warn-bg);
    border: 1px solid var(--m-tag-warn-border);
    border-radius: 12px;
    font-size: 13px;
    color: var(--m-text);
    font-family: inherit;
    cursor: pointer;
    text-align: left;
  }
  .approve-banner .txt { flex: 1; font-weight: 500; }
  .approve-banner .go { color: var(--m-warning); font-weight: 600; }
  .stopbar {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 0 12px 8px;
  }
  .stopbar .status {
    display: flex;
    gap: 6px;
    min-width: 0;
    font-size: 12.5px;
    color: var(--m-text-3);
  }
  .stopbar .status .label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .stopbar .status .time { flex: none; font-variant-numeric: tabular-nums; }
  .stopbar .stop { flex: none; }
  .plan {
    flex: none;
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 0 12px 8px;
    padding: 10px 14px;
    background: var(--m-surface);
    border: 1px solid var(--m-border-2);
    border-radius: 12px;
    font-family: inherit;
    color: var(--m-text);
    text-align: left;
    cursor: pointer;
  }
  .plan-head { display: flex; align-items: center; gap: 8px; font-size: 13px; }
  .plan-title { font-weight: 600; }
  .plan-meta { color: var(--m-text-3); font-variant-numeric: tabular-nums; }
  .plan-bar { flex: 1; height: 4px; border-radius: 2px; background: var(--m-border-2); overflow: hidden; }
  .plan-bar span { display: block; height: 100%; background: var(--m-accent); }
  .chev { flex: none; color: var(--m-text-3); transition: transform .15s; }
  .chev.open { transform: rotate(180deg); }
  .plan-steps { display: flex; flex-direction: column; gap: 6px; }
  .step { position: relative; padding-left: 20px; font-size: 13px; line-height: 1.4; }
  .step::before {
    content: '';
    position: absolute;
    left: 2px;
    top: 4px;
    width: 10px;
    height: 10px;
    border-radius: 50%;
    border: 1.5px solid var(--m-text-4);
    box-sizing: border-box;
  }
  .step.completed { color: var(--m-text-3); text-decoration: line-through; }
  .step.completed::before { background: var(--m-success); border-color: var(--m-success); }
  .step.in_progress { font-weight: 500; }
  .step.in_progress::before { border-color: var(--m-accent); border-right-color: transparent; animation: spin .8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg) } }
  @media (prefers-reduced-motion: reduce) { .step.in_progress::before { animation: none } }
  .stop {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 7px 16px;
    border-radius: 9999px;
    border: 1px solid var(--m-border);
    background: var(--m-surface);
    color: var(--m-error);
    font-size: 13px;
    font-family: inherit;
    cursor: pointer;
    box-shadow: var(--m-shadow-card);
  }
  /* ── Markdown rendering (adapted from desktop for mobile) ─────────────────── */
  .bubble.agent .body :global(p) { margin: 0; }
  .bubble.agent .body :global(p:not(:last-child)) { margin: 0 0 8px; }
  .bubble.agent .body :global(h1), .bubble.agent .body :global(h2), .bubble.agent .body :global(h3), .bubble.agent .body :global(h4) {
    margin: 12px 0 6px; font-weight: 600; line-height: 1.3;
  }
  .bubble.agent .body :global(h1) { font-size: 18px; }
  .bubble.agent .body :global(h2) { font-size: 16px; }
  .bubble.agent .body :global(h3) { font-size: 15px; }
  .bubble.agent .body :global(ul), .bubble.agent .body :global(ol) { margin: 0; padding-left: 22px; }
  .bubble.agent .body :global(li) { margin: 3px 0; }
  .bubble.agent .body :global(a) { color: var(--m-accent); text-decoration: none; }
  .bubble.agent .body :global(code) {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px;
    background: rgba(127, 127, 127, 0.15); border-radius: 4px; padding: 1px 5px;
  }
  .bubble.agent .body :global(pre) {
    margin: 8px 0; padding: 10px 12px; overflow-x: auto; border-radius: 8px;
    background: var(--m-surface); border: 1px solid var(--m-border);
    font-size: 12.5px; line-height: 1.6;
  }
  .bubble.agent .body :global(pre code) { background: none; padding: 0; border-radius: 0; }
  .bubble.agent .body :global(blockquote) {
    margin: 8px 0; padding: 8px 12px; border-left: 3px solid var(--m-accent);
    background: rgba(127, 127, 127, 0.08); border-radius: 0 6px 6px 0; font-size: 13px; color: var(--m-text-2);
  }
  .bubble.agent .body :global(table) { border-collapse: collapse; margin: 8px 0; font-size: 13px; }
  .bubble.agent .body :global(th), .bubble.agent .body :global(td) { border: 1px solid var(--m-border); padding: 5px 10px; text-align: left; }
  .bubble.agent .body :global(th) { background: var(--m-surface); font-weight: 600; }
  .bubble.agent .body :global(hr) { border: none; border-top: 1px solid var(--m-border); margin: 12px 0; }
  .bubble.agent .body :global(img) { max-width: 100%; border-radius: 8px; }
  .thoughts :global(p) { margin: 0; }
  .thoughts :global(pre) { margin: 6px 0; padding: 8px 10px; border-radius: 6px; background: rgba(127, 127, 127, 0.08); overflow-x: auto; font-size: 12px; }
  .thoughts :global(code) { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
  /* highlight.js theme overrides for mobile dark context */
  .bubble.agent .body :global(.hljs) { background: transparent; }
  .bubble.agent .body :global(.code-block) { border: 1px solid var(--m-border); border-radius: 8px; overflow: hidden; margin: 8px 0; }
  .bubble.agent .body :global(.code-header) { display: flex; align-items: center; gap: 8px; padding: 5px 10px; background: var(--m-surface); border-bottom: 1px solid var(--m-border); }
  .bubble.agent .body :global(.code-lang) { font-size: 10px; color: var(--m-text-3); font-family: ui-monospace, Menlo, monospace; }
  .bubble.agent .body :global(.copy-btn) { margin-left: auto; border: none; background: transparent; color: var(--m-text-3); font-size: 11px; padding: 2px 6px; border-radius: 4px; cursor: pointer; font-family: inherit; }
  .bubble.agent .body :global(.copy-btn:active) { background: var(--m-border); }
</style>
