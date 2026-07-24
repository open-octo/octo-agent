<script lang="ts">
  // Mobile chat detail: renders a session's transcript and sends replies, wired
  // to the shared stores via chatWiring (which reuses the same store helpers the
  // desktop uses). A simplified view — tool cards are collapsed to a one-line
  // hint (the detailed tool/progress view is batch 2), and the composer is a
  // lightweight mobile input rather than the desktop Composer.
  import { tick } from 'svelte'
  import { activeSessionId, chatMessages, chatStreaming, sessions, clearMsgs, artifacts } from '../lib/stores'
  import { resetArtifacts } from '../lib/artifacts'
  import { ws } from '../lib/ws'
  import { wireMobileSession, loadMobileHistory, sendMobile } from './chatWiring'
  import ArtifactViewer from './ArtifactViewer.svelte'
  import { t } from '../lib/i18n'

  let { onBack, onViewApproval, initialPrompt = '', onInitialSent }: {
    onBack: () => void
    onViewApproval: () => void
    // Queued first message from the new-task sheet, sent once the WS
    // subscription is live (so the reply stream isn't missed). onInitialSent
    // lets the owner clear it so a remount doesn't re-send.
    initialPrompt?: string
    onInitialSent?: () => void
  } = $props()

  const sid = $derived($activeSessionId ?? '')
  const msgs = $derived($chatMessages[sid] ?? [])
  const streaming = $derived($chatStreaming[sid] ?? false)
  const session = $derived($sessions.find(s => s.id === sid) ?? null)
  const title = $derived(session?.title || session?.name || $t('m.session'))
  // A confirmation raised for THIS session while it's open — surface an inline
  // entry to the approval detail (the feed card is out of view here).
  const pendingApproval = $derived(session?.pending_confirmation ?? false)

  function stop() {
    if (sid) ws.interrupt(sid)
  }

  let draft = $state('')
  let scroller: HTMLElement | undefined
  let composing = $state(false)
  // The artifact opened full-screen, tracked by path and derived from the
  // store so a live re-write of the same file refreshes the open viewer
  // (observeArtifact replaces the entry object on re-write).
  let viewPath = $state<string | null>(null)
  const viewArtifact = $derived(viewPath ? ($artifacts.find(a => a.path === viewPath) ?? null) : null)

  // Subscribe + wire while this session is open; tear down on switch/unmount.
  $effect(() => {
    const s = sid
    if (!s) return
    viewPath = null
    clearMsgs(s)
    resetArtifacts(s)
    let cancelled = false
    // Subscribe only after history renders (same ordering the desktop relies on).
    loadMobileHistory(s).then(() => {
      if (cancelled) return
      ws.subscribe(s)
      if (initialPrompt) {
        sendMobile(s, initialPrompt, [])
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
    const t = draft.trim()
    if (!t || !sid) return
    sendMobile(sid, t, [])
    draft = ''
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !composing) {
      e.preventDefault()
      send()
    }
  }

  // Takes the reactive $t so the fallback re-renders on locale flip.
  function toolLabel(tools: any[], tf: (k: string) => string): string {
    if (!tools?.length) return ''
    const names = tools.map(tl => tl.name).filter(Boolean)
    return names.length ? names.join(' · ') : tf('m.n_tools').replace('{n}', String(tools.length))
  }
</script>

<header class="dhead">
  <button class="back" onclick={onBack} aria-label={$t('m.back')}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
  </button>
  <div class="dtitle">
    <span class="t">{title}</span>
    <span class="sub">
      <span class="d" class:live={streaming}></span>
      {streaming ? $t('m.working') : $t('m.idle')}{session?.model ? ` · ${session.model}` : ''}
    </span>
  </div>
</header>

{#if pendingApproval}
  <button class="approve-banner" onclick={onViewApproval}>
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--m-warning)" stroke-width="2"><path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 2 18a2 2 0 0 0 1.7 3h16.6A2 2 0 0 0 22 18L13.7 3.9a2 2 0 0 0-3.4 0z"/></svg>
    <span class="txt">{$t('m.approval_banner')}</span>
    <span class="go">{$t('m.view')} ›</span>
  </button>
{/if}

<div class="scroll" bind:this={scroller}>
  {#each msgs as msg (msg.id)}
    {#if msg.type === 'user'}
      <div class="bubble user" class:pending={msg.pending}>{msg.content}</div>
    {:else if msg.type === 'assistant'}
      <div class="bubble agent">
        {#if msg.thinking}<div class="thoughts">{msg.thinking}</div>{/if}
        <div class="body">{msg.content}{#if msg.streaming}<span class="caret">▋</span>{/if}</div>
      </div>
    {:else if msg.type === 'thinking'}
      <div class="thoughts standalone">{msg.thinking}</div>
    {:else if msg.type === 'tool_group'}
      <div class="tools">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-3)" stroke-width="2"><path d="M14.7 6.3a4 4 0 0 1-5 5L4 17v3h3l5.7-5.7a4 4 0 0 0 5-5z"/></svg>
        <span>{toolLabel(msg.tools, $t)}</span>
      </div>
    {:else if msg.type === 'notice'}
      <div class="notice" class:err={msg.level === 'error'}>{msg.content}</div>
    {/if}
  {/each}
  {#if $artifacts.length}
    <div class="artifacts">
      <div class="alabel">{$t('m.artifacts')}</div>
      {#each $artifacts as a (a.path)}
        <button class="acard" onclick={() => (viewPath = a.path)}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--m-accent)" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/></svg>
          <span class="aname">{a.name}</span>
          <span class="atype">{a.type}</span>
        </button>
      {/each}
    </div>
  {/if}
</div>

{#if streaming}
  <div class="stopbar">
    <button class="stop" onclick={stop}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>
      {$t('m.stop_gen')}
    </button>
  </div>
{/if}

<div class="composer">
  <textarea
    bind:value={draft}
    onkeydown={onKey}
    oncompositionstart={() => (composing = true)}
    oncompositionend={() => (composing = false)}
    placeholder={$t('m.reply_ph')}
    rows="1"
  ></textarea>
  <button class="send" onclick={send} disabled={!draft.trim()} aria-label={$t('m.send')}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2"><path d="M22 2 11 13M22 2l-7 20-4-9-9-4z"/></svg>
  </button>
</div>

{#if viewArtifact}
  <ArtifactViewer artifact={viewArtifact} onClose={() => (viewPath = null)} />
{/if}

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
  .dtitle { flex: 1; min-width: 0; }
  .dtitle .t {
    display: block;
    font-size: 15px;
    font-weight: 600;
    color: var(--m-text);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .dtitle .sub {
    display: flex;
    align-items: center;
    gap: 5px;
    margin-top: 2px;
    font-size: 11px;
    color: var(--m-text-3);
  }
  .sub .d { width: 7px; height: 7px; border-radius: 50%; background: var(--m-text-4); }
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
    align-self: flex-start;
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--m-text-3);
    font-family: ui-monospace, Menlo, monospace;
  }
  .notice {
    align-self: center;
    font-size: 12px;
    color: var(--m-text-3);
    text-align: center;
  }
  .notice.err { color: var(--m-error); }
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
    justify-content: center;
    padding: 0 12px 8px;
  }
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
  .artifacts { align-self: stretch; margin-top: 4px; }
  .alabel { font-size: 12px; color: var(--m-text-3); margin: 0 2px 8px; }
  .acard {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    border: none;
    font-family: inherit;
    text-align: left;
    cursor: pointer;
    background: var(--m-surface);
    border-radius: 12px;
    padding: 12px 14px;
    margin-bottom: 8px;
    box-shadow: var(--m-shadow-card);
  }
  .acard .aname { flex: 1; font-size: 14px; color: var(--m-text); overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  .acard .atype { flex: none; font-size: 11px; color: var(--m-text-3); font-family: ui-monospace, Menlo, monospace; }
</style>
