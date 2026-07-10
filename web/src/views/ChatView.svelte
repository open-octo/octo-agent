<script lang="ts">
  import { get } from 'svelte/store'
  import {
    activeSessionId,
    activeSession,
    sessions,
    chatMessages,
    chatStreaming,
    chatTurnStart,
    chatProgress,
    chatBgTasks,
    chatTodos,
    chatContextUsage,
    chatContextTokens,
    chatWorkingDir,
    chatPermMode,
    chatReasoningEffort,
    chatShowReasoning,
    chatSuggestion,
    chatThinking,
    chatSubAgents,
    chatWorkflows,
    applyWorkflowEvent,
    confirmModal,
    questionModals,
    feedbackModal,
    pendingPrompt,
    artifactsOpen,
    artifacts,
    addChatMsg,
    clearMsgs,
    appendToLastAssistant,
    addToolCallToGroup,
    commitThinking,
    updateToolResult,
    setToolError,
    appendToolStdout,
    finishAllTools,
    finishToolsById,
    resetSubAgents,
    clearDoneSubAgents,
    applySubAgentEvent,
    showToast,
    uid,
    agenticSessions,
    chatGoal,
  } from '../lib/stores'
  import { ws, wsState, wsReconnect } from '../lib/ws'
  import * as api from '../lib/api'
  import { observeArtifact, resetArtifacts } from '../lib/artifacts'
  import { renderMarkdown, setupCopyButtons } from '../lib/markdown'
  import { t, tr } from '../lib/i18n'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import ToolGroup from '../components/chat/ToolGroup.svelte'
  import SubAgentsCard from '../components/chat/SubAgentsCard.svelte'
  import WorkflowsCard from '../components/chat/WorkflowsCard.svelte'
  import BackgroundProcesses from '../components/chat/BackgroundProcesses.svelte'
  import Composer from '../components/chat/Composer.svelte'
  import ArtifactsPanel from '../components/ArtifactsPanel.svelte'
  import OctoLogo from '../components/layout/OctoLogo.svelte'

  // ── reactive state ─────────────────────────────────────────────────────────
  let messagesEl = $state<HTMLElement | null>(null)

  // In Svelte 5 runes mode, $store is reactive inside $derived / $effect.
  // get(store) is a one-time read — do NOT use inside $derived/$effect.
  let id          = $derived($activeSessionId)
  let msgs        = $derived($chatMessages[$activeSessionId ?? ''] ?? [])
  let streaming   = $derived($chatStreaming[$activeSessionId ?? ''] ?? false)
  let progress    = $derived($chatProgress[$activeSessionId ?? ''] ?? null)
  let bgTasks     = $derived($chatBgTasks[$activeSessionId ?? ''] ?? [])
  let todos       = $derived($chatTodos[$activeSessionId ?? ''] ?? [])
  let suggestion  = $derived($chatSuggestion[$activeSessionId ?? ''] ?? '')
  let thinking    = $derived($chatThinking[$activeSessionId ?? ''] ?? '')
  let subAgents   = $derived($chatSubAgents[$activeSessionId ?? ''] ?? [])
  let workflows   = $derived($chatWorkflows[$activeSessionId ?? ''] ?? [])
  let currentSession = $derived($sessions.find(s => s.id === $activeSessionId) ?? null)
  let artifactCount  = $derived($artifacts.length)
  let wsDisconnected = $derived($wsState === 'disconnected')
  let showReasoning  = $derived($chatShowReasoning[$activeSessionId ?? ''] ?? currentSession?.show_reasoning ?? true)

  // Session-level plan panel: collapsed by default so it never occludes the
  // message stream; the user can expand it into a floating dropdown.
  let planExpanded = $state(false)

  // Tracks optimistic UI state for in-flight sends. A FIFO queue per session
  // supports multiple messages (e.g. consecutive steer messages mid-turn); if
  // the server rejects one, we roll back only that pending bubble and restore
  // the streaming flag to its pre-send value. Content and files are kept so a
  // force takeover can retry the same message.
  const pendingSends = new Map<string, { pendingId: string; wasStreaming: boolean; text: string; files?: any[] }[]>()

  // Pending steer messages typed while a turn is running. They are shown above
  // the composer as ghost user bubbles until the server drains the inbox and
  // confirms them in the scrollback.
  let pendingSteers = $state<{ pendingId: string; text: string; files?: any[] }[]>([])

  // Set when the server reports a recoverable binding conflict. The UI shows a
  // banner with a "Force bind" button; clicking it retries the pending send
  // with force=true, matching the IM /bind --force semantics.
  let bindRequiredFor = $state<string | null>(null)
  let bindRequiredMessage = $state('')

  // Sub-agents card elapsed time + reconnect countdown both tick off `now`.
  let now = $state(Date.now())
  $effect(() => {
    const h = setInterval(() => { now = Date.now() }, 1000)
    return () => clearInterval(h)
  })
  let subAgentsStart = $derived(subAgents.length ? Math.min(...subAgents.map(a => a.startedAt)) : 0)
  let subAgentsElapsed = $derived(subAgentsStart ? (now - subAgentsStart) / 1000 : 0)
  let reconnectIn = $derived($wsReconnect ? Math.max(0, Math.ceil(($wsReconnect.nextAt - now) / 1000)) : 0)

  // Live "Thinking" readout — mirrors the TUI thinkingLine: elapsed since the
  // turn began plus a rough output-token estimate (streamed chars / 4) so a
  // long silent wait reads as the model working, not a freeze.
  // Persist the turn's start across view remounts. A page switch unmounts
  // ChatView; a component-local start would restart from ~0 on return — and
  // since `now` is captured at mount, a start stamped a few ms later renders as
  // -1s until the next tick. Keying the start by session in a module store keeps
  // elapsed correct and monotonic across switches. (get() for the guard so the
  // effect doesn't re-trigger on its own store write.)
  $effect(() => {
    const sid = $activeSessionId ?? ''
    if (!sid) return
    if (streaming) {
      if (!get(chatTurnStart)[sid]) chatTurnStart.update(m => ({ ...m, [sid]: Date.now() }))
    } else if (get(chatTurnStart)[sid]) {
      chatTurnStart.update(m => { const n = { ...m }; delete n[sid]; return n })
    }
  })
  let turnStartAt = $derived($chatTurnStart[$activeSessionId ?? ''] ?? 0)
  let thinkElapsed = $derived(turnStartAt ? Math.max(0, Math.floor((now - turnStartAt) / 1000)) : 0)
  // Output-token estimate (~chars/4), derived from persisted stores — the live
  // assistant text plus the reasoning buffer — so it survives view remounts
  // alongside the elapsed clock instead of resetting to 0.
  let turnOutChars = $derived(
    ((msgs.find((m: any) => m.streaming && m.type === 'assistant')?.content?.length) ?? 0) + thinking.length
  )
  let thinkTokens = $derived(Math.floor(turnOutChars / 4))
  // Uplink size: the context being sent up (last known occupancy in tokens).
  let ctxTokens = $derived(Number($chatContextTokens[$activeSessionId ?? ''] ?? 0))
  function fmtDur(s: number): string {
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${s % 60}s`
  }
  function fmtTokens(n: number): string {
    return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`
  }

  // ── history handler ────────────────────────────────────────────────────────
  function handleHistoryEvent(ev: Record<string, any>, historyShowReasoning: boolean) {
    const sid = get(activeSessionId)   // get() is fine in imperative functions
    if (!sid) return
    if (ev.type === 'history_user_message') {
      addChatMsg(sid, {
        id: uid('u'),
        type: 'user',
        content: ev.content ?? '',
        createdAt: ev.created_at ?? Date.now(),
        streaming: false,
        pending: false,
        tools: [],
        todos: [],
        // Server-derived attachment refs (image thumbnails + "pdf:<name>" doc
        // chips) so a reloaded transcript shows the same attachments the live
        // turn did — this is the only place reload rehydrates them.
        images: ev.images ?? [],
      })
    } else if (ev.type === 'assistant_message') {
      // Skip empty assistant turns (thinking-only / tool-only rounds) so they
      // don't render as blank bubbles.
      if (!(ev.content ?? '').trim() && !(ev.thinking ?? '').trim()) return
      addChatMsg(sid, {
        id: uid('a'),
        type: 'assistant',
        content: ev.content ?? '',
        thinking: ev.thinking ?? '',
        createdAt: Date.now(),
        streaming: false,
        tools: [],
        todos: [],
      })
    } else if (ev.type === 'thinking') {
      // Standalone reasoning segment from an intermediate (tool) round — render
      // it before the tools it preceded. The server persists (and replays)
      // this regardless of the session's reasoning-display setting, but the
      // live stream only ever delivers it when reasoning display is on
      // (thinking_delta is gated server-side) — so live never breaks a tool
      // group on reasoning it never received. Committing unconditionally here
      // would insert an invisible boundary that fragments a group live
      // rendered as one card. Skipping the commit when reasoning is hidden
      // keeps replay consistent with what was actually shown live.
      //
      // historyShowReasoning comes from this fetch's own response (not the
      // reactive `showReasoning` derived from $sessions) because on a
      // page-load landing directly on a session via URL hash, loadHistory's
      // REST call races api.listSessions()/the WS session_list broadcast —
      // $sessions can still be empty when this loop runs, which would make
      // `showReasoning` fall back to its default (true) regardless of the
      // session's real setting.
      if (historyShowReasoning) commitThinking(sid, ev.text ?? '')
    } else if (ev.type === 'tool_call') {
      addToolCallToGroup(sid, {
        id: uid('t'),
        toolId: ev.tool_id ?? '',
        name: ev.name ?? '',
        args: ev.args ?? '',
        summary: ev.summary ?? '',
        done: false,
        error: null,
        result: null,
        stdout: [],
        diff: null,
      })
    } else if (ev.type === 'tool_result') {
      updateToolResult(sid, ev.tool_id, ev.result, ev.ui_payload)
      observeArtifact(sid, ev.ui_payload, false)   // history replay — silent
    }
  }

  // loadHistory fetches and renders a session's persisted transcript. Used on
  // session switch and on a server `history_reload` (after /clear or /compact
  // rewrote history out of band). Returns a promise that resolves once the
  // fetch settles (success or failure) — the mount effect below awaits it
  // before subscribing over WS; see the comment there for why.
  function loadHistory(sid: string): Promise<void> {
    // Seed the goal chip for this session; failures (older server, goals
    // disabled) just leave the chip hidden.
    api.getSessionGoal(sid)
      .then(resp => chatGoal.update(m => ({ ...m, [sid]: resp?.goal ?? null })))
      .catch(() => {})
    return api.getSessionMessages(sid).then((resp: any) => {
      const events: any[] = resp?.events ?? []
      // Server-resolved, so it's correct even before $sessions has loaded —
      // see the comment on the 'thinking' branch in handleHistoryEvent.
      const historyShowReasoning = resp?.show_reasoning ?? true
      // Collect the tool_ids that came from history so we only close those,
      // leaving any concurrently-replayed live-turn tools untouched.
      const historyToolIds = new Set<string>()
      for (const ev of events) {
        if (ev.type === 'tool_call' && ev.tool_id) historyToolIds.add(ev.tool_id)
        handleHistoryEvent(ev, historyShowReasoning)
      }
      // Finish only the history tools (not live-turn tools from WS replay).
      finishToolsById(sid, historyToolIds)
      // Pin to bottom after the DOM update so the user lands at the latest message.
      queueMicrotask(() => {
        if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight
      })
    }).catch(() => {/* silently ignore history load errors */})
  }

  // Clear transient runtime state for a session so switching back from another
  // conversation never shows a stale thinking indicator or spinning sub-agent.
  function resetSessionRuntimeState(sid: string) {
    chatStreaming.update(s => ({ ...s, [sid]: false }))
    chatProgress.update(p => ({ ...p, [sid]: null }))
    chatThinking.update(t => ({ ...t, [sid]: '' }))
    chatTurnStart.update(m => { const n = { ...m }; delete n[sid]; return n })
    resetSubAgents(sid)
    finishAllTools(sid)
  }

  // ── main lifecycle effect ──────────────────────────────────────────────────
  // $activeSessionId makes this effect re-run whenever the session changes.
  $effect(() => {
    const sid = $activeSessionId
    let cancelled = false
    if (!sid) {
      // No active session: drop any stale force-bind banner so a deleted session
      // does not leave the chat view showing "Session is bound to another entry."
      bindRequiredFor = null
      return
    }

    clearMsgs(sid)
    resetArtifacts(sid)
    resetSessionRuntimeState(sid)
    if (get(pendingPrompt)?.sessionId === sid) {
      // A freshly opened agentic session (openAgentSession queued a
      // pendingPrompt) is empty at creation, so loadHistory has nothing to
      // fetch — and worse, its async GET races the flush-on-subscribe send:
      // by the time it resolves the server has already persisted the
      // just-sent user message, which it then appends on top of the
      // optimistic/echoed bubble (the duplicate that vanishes on refresh).
      // Skip it; the subscribed handler drives the first message, so
      // subscribe immediately.
      ws.subscribe(sid)
    } else {
      // Subscribe only after history renders (#1125, #1129): the WS
      // subscribe's replay of this turn's live tool activity is a separate,
      // faster round-trip than this REST fetch — if it were fired
      // concurrently, the replayed tool cards (append-only, no causal
      // reordering) would land and render *before* loadHistory's slower
      // response inserts the user message that started that very turn,
      // putting the question after its own tool output, or — if the turn
      // ran long enough to evict its own early rounds from the server's
      // bounded replay buffer before the (delayed) subscribe request even
      // arrives — never showing them at all. Delaying the subscribe send
      // costs nothing: the server's replay buffer is per-session, not
      // per-connection, so it still holds everything broadcast since turn
      // start whenever we do subscribe.
      loadHistory(sid).then(() => {
        // The user may have switched sessions (or this view unmounted)
        // while the fetch was in flight — the effect's cleanup below already
        // unsubscribed sid in that case, so subscribing now would resurrect
        // a stale WS subscription nothing is listening for any more.
        if (!cancelled) ws.subscribe(sid)
      })
    }

    // ── WS event handlers ───────────────────────────────────────────────────
    const cleanups: Array<() => void> = []

    // A panel ("create skill", "new task", "MCP setup", …) opened this session
    // with a slash-command queued in pendingPrompt. The server must register
    // the subscription before the agent broadcasts, so we wait for its
    // `subscribed` ack, then auto-send — mirroring the old UI's flush-on-subscribe.
    cleanups.push(ws.on('subscribed', (ev) => {
      if ((ev as any).session_id !== sid) return
      const pend = get(pendingPrompt)
      if (pend && pend.sessionId === sid) {
        pendingPrompt.set(null)
        send(pend.content)
      }
    }))

    // History rewritten out of band (/clear, /compact): drop the rendered
    // transcript and re-fetch the persisted one.
    cleanups.push(ws.on('goal_updated', (ev) => {
      const gsid = (ev as any).session_id
      if (!gsid) return
      chatGoal.update(m => ({ ...m, [gsid]: (ev as any).goal ?? null }))
    }))

    cleanups.push(ws.on('history_reload', (ev) => {
      if ((ev as any).session_id !== sid) return
      clearMsgs(sid)
      resetArtifacts(sid)
      loadHistory(sid)
    }))

    // The transcript tail was stripped server-side (retry / rollback): re-render
    // from the persisted history before any new events stream in. Same effect as
    // history_reload — different trigger.
    cleanups.push(ws.on('history_rollback', (ev) => {
      if ((ev as any).session_id !== sid) return
      clearMsgs(sid)
      resetArtifacts(sid)
      loadHistory(sid)
    }))

    // Transient server-side notice (command result, error).
    cleanups.push(ws.on('toast', (ev) => {
      if ((ev as any).session_id !== sid) return
      showToast((ev as any).message ?? '', (ev as any).level ?? 'info')
    }))

    // Operation errors surfaced over WS (e.g. "can't retry while a turn is
    // running", session-not-found). The payload carries no session_id — delivery
    // is already scoped to this session — so only filter when one is present.
    cleanups.push(ws.on('error', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      showToast((ev as any).message ?? 'Error', 'error')
    }))

    // The server rejected a user_message (session bound to another entry,
    // session not found, etc.). Roll back the optimistic pending bubble / ghost
    // steer and restore the streaming flag so the composer doesn't get stuck
    // showing Stop / a phantom steer message.
    cleanups.push(ws.on('send_rejected', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const queue = pendingSends.get(sid)
      const meta = queue?.shift()
      if (queue && queue.length === 0) pendingSends.delete(sid)
      if (meta) {
        if (meta.wasStreaming) {
          pendingSteers = pendingSteers.filter(s => s.pendingId !== meta.pendingId)
        } else {
          chatMessages.update(m => ({
            ...m,
            [sid]: (m[sid] || []).filter((msg: any) => msg.id !== meta.pendingId),
          }))
        }
        chatStreaming.update(s => ({ ...s, [sid]: meta.wasStreaming }))
      }
      bindRequiredFor = null
      showToast((ev as any).message ?? 'Error', 'error')
    }))

    // The session is bound to another entry but no turn lease is active. Offer
    // a force takeover instead of dropping the message.
    cleanups.push(ws.on('bind_required', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const queue = pendingSends.get(sid)
      const meta = queue?.[queue.length - 1]
      if (!meta) return
      // Keep the pending bubble and streaming state; the user can confirm.
      bindRequiredFor = sid
      bindRequiredMessage = (ev as any).message ?? 'Session is bound to another entry.'
    }))

    // The turn was interrupted. `complete` still fires and handles cleanup, so
    // this is purely a heads-up.
    cleanups.push(ws.on('interrupted', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      showToast(tr('chat.interrupted'), 'info')
    }))

    // A background process finished (the badge updates via
    // background_tasks_update); render the outcome as an inline scrollback
    // notice in the message stream, mirroring the TUI's bgDoneStyle line.
    cleanups.push(ws.on('background_task_notice', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const status = (ev as any).status ?? ''
      const level = status === 'success' ? 'success' : status === 'cancelled' ? 'info' : 'error'
      const command = (ev as any).command ?? ''
      addChatMsg(sid, {
        id: uid('note'),
        type: 'notice',
        content: `Background \`${command}\` ${status}`,
        level,
        createdAt: Date.now(),
        streaming: false,
        tools: [],
        todos: [],
      })
    }))

    cleanups.push(ws.on('text_delta', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const txt = (ev as any).text ?? ''
      // Reasoning for this segment is done the moment its reply starts
      // streaming — hand off whatever is sitting in the live thinking buffer to
      // the new bubble right away instead of leaving it pinned at the bottom of
      // the list until the whole turn ends (assistant_message/complete). That
      // gap is what let a stale "still typing" thinking block linger below an
      // already-visible reply, then jump/disappear once the turn finally
      // finished.
      const pendingThinking = get(chatThinking)[sid] ?? ''
      appendToLastAssistant(sid, txt, pendingThinking)
      if (pendingThinking) chatThinking.update(tt => ({ ...tt, [sid]: '' }))
    }))

    cleanups.push(ws.on('thinking_delta', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      // The server only emits thinking_delta when show_reasoning is on for the
      // session's sender (it's off by default and never surfaced to the
      // terminal), so any delta that reaches the Web UI is meant to be shown.
      const txt = (ev as any).text ?? ''
      chatThinking.update(tt => ({ ...tt, [sid]: (tt[sid] ?? '') + txt }))
    }))

    cleanups.push(ws.on('sub_agent_event', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      applySubAgentEvent(
        sid,
        (ev as any).agent_id ?? '',
        (ev as any).description ?? '',
        (ev as any).agent_type ?? '',
        (ev as any).kind ?? '',
        (ev as any).tool_name ?? '',
        (ev as any).tool_input,
      )
    }))

    cleanups.push(ws.on('sub_agent_notice', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const status = (ev as any).status ?? ''
      const level = status === 'success' ? 'success' : 'error'
      const description = (ev as any).description ?? ''
      const agentId = (ev as any).agent_id ?? ''
      const label = description || agentId || 'sub-agent'
      const text = status === 'success'
        ? `Sub-agent \`${label}\` completed`
        : `Sub-agent \`${label}\` failed`
      addChatMsg(sid, {
        id: uid('note'),
        type: 'notice',
        content: text,
        level,
        createdAt: Date.now(),
        streaming: false,
        tools: [],
        todos: [],
      })
    }))

    cleanups.push(ws.on('workflow_event', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const kind = (ev as any).kind ?? ''
      const runId = (ev as any).run_id ?? ''
      const description = (ev as any).description ?? ''
      const status = (ev as any).status ?? ''
      applyWorkflowEvent(sid, runId, description, kind, (ev as any).line ?? '', status)
      // When a background workflow finishes, mirror the TUI scrollback notice
      // so the completion is visible in the message stream.
      if (kind === 'done') {
        const level = status === 'error' ? 'error' : 'success'
        const label = description || runId || 'workflow'
        const text = status === 'error'
          ? `Workflow \`${label}\` failed`
          : `Workflow \`${label}\` completed`
        addChatMsg(sid, {
          id: uid('note'),
          type: 'notice',
          content: text,
          level,
          createdAt: Date.now(),
          streaming: false,
          tools: [],
          todos: [],
        })
      }
    }))

    cleanups.push(ws.on('assistant_message', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const think = (ev as any).thinking ?? ''
      // The live thinking buffer has been consumed into this message; clear it.
      chatThinking.update(tt => ({ ...tt, [sid]: '' }))
      const curMsgs = get(chatMessages)[sid] ?? []
      if (streaming && curMsgs.length > 0 && curMsgs[curMsgs.length - 1]?.type === 'assistant') {
        // finalize streaming message
        chatMessages.update(m => {
          const arr = [...(m[sid] || [])]
          const last = arr.length - 1
          if (last >= 0) arr[last] = { ...arr[last], content: (ev as any).content ?? arr[last].content, thinking: think || arr[last].thinking, streaming: false }
          return { ...m, [sid]: arr }
        })
      } else {
        addChatMsg(sid, {
          id: uid('a'),
          type: 'assistant',
          content: (ev as any).content ?? '',
          thinking: think,
          createdAt: Date.now(),
          streaming: false,
          tools: [],
          todos: [],
        })
      }
    }))

    cleanups.push(ws.on('history_user_message', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const content = (ev as any).content ?? ''
      const createdAt = (ev as any).created_at ?? Date.now()
      const images = (ev as any).images ?? []
      const queue = pendingSends.get(sid)
      const meta = queue?.shift()
      if (queue && queue.length === 0) pendingSends.delete(sid)
      // Trust the FIFO queue to decide whether this confirmation belongs to a
      // steer. If the queue is empty (e.g. page refresh), any pendingSteers are
      // orphaned UI state from before the refresh; don't guess by content and
      // risk removing the wrong duplicate.
      const isSteer = meta?.wasStreaming ?? false
      let confirmedPendingId: string | null = null
      chatMessages.update(m => {
        const msgs = [...(m[sid] || [])]
        if (isSteer) {
          // Steer messages enter history in chronological order: before any
          // assistant reply that is still streaming, so the transcript reads as
          // user-steer → next-assistant-reply (mirrors the TUI's EventSteerInjected).
          const confirmedMsg = { id: uid('u'), type: 'user', content, createdAt, streaming: false, pending: false, tools: [], todos: [], images }
          const lastStreamingAssistant = msgs.findLastIndex((x: any) => x.type === 'assistant' && x.streaming)
          if (lastStreamingAssistant >= 0) {
            msgs.splice(lastStreamingAssistant, 0, confirmedMsg)
          } else {
            msgs.push(confirmedMsg)
          }
        } else {
          // If the last user bubble is a pending optimistic echo of the same
          // text, replace it in place (de-dup). Otherwise append a fresh one.
          const lastPending = msgs.findLastIndex((x: any) => x.type === 'user' && x.pending)
          if (lastPending >= 0 && msgs[lastPending].content === content) {
            confirmedPendingId = msgs[lastPending].id
            msgs[lastPending] = { ...msgs[lastPending], id: uid('u'), createdAt, pending: false, images }
          } else {
            msgs.push({ id: uid('u'), type: 'user', content, createdAt, streaming: false, pending: false, tools: [], todos: [], images })
          }
        }
        return { ...m, [sid]: msgs }
      })
      if (isSteer && meta) {
        // The server drained this steer from the inbox; drop the ghost bubble.
        pendingSteers = pendingSteers.filter(s => s.pendingId !== meta.pendingId)
      } else if (meta && meta.pendingId === confirmedPendingId) {
        // The server confirmed this optimistic send; stop tracking it for rollback.
        // (queue already shifted above)
      }
    }))

    cleanups.push(ws.on('tool_call', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      // Step boundary: commit the reasoning that preceded this tool call as a
      // standalone Thoughts segment so it renders before the tool (think → act)
      // and breaks the tool group, then reset the live buffer for the next step.
      commitThinking(sid, get(chatThinking)[sid] ?? '')
      chatThinking.update(tt => ({ ...tt, [sid]: '' }))
      addToolCallToGroup(sid, {
        id: uid('t'),
        toolId: (ev as any).tool_id ?? '',
        name: (ev as any).name ?? '',
        args: (ev as any).args ?? '',
        summary: (ev as any).summary ?? '',
        startedAt: Date.now(),
        done: false,
        error: null,
        result: null,
        stdout: [],
        diff: null,
      })
    }))

    cleanups.push(ws.on('tool_result', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      updateToolResult(sid, (ev as any).tool_id, (ev as any).result, (ev as any).ui_payload)
      observeArtifact(sid, (ev as any).ui_payload, true)   // live turn — may auto-open
    }))

    cleanups.push(ws.on('tool_error', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      setToolError(sid, (ev as any).tool_id, (ev as any).error ?? 'error')
    }))

    // Turn-level failure (sender/tool setup or the LLM call itself errored).
    // It belongs to no tool card, so render it as a standalone error notice in
    // the transcript instead of dropping it. `complete` clears the caret.
    cleanups.push(ws.on('turn_error', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      addChatMsg(sid, {
        id: uid('err'),
        type: 'notice',
        content: `**Error:** ${(ev as any).error ?? 'request failed'}`,
        level: 'error',
        createdAt: Date.now(),
        streaming: false,
        tools: [],
        todos: [],
      })
    }))

    cleanups.push(ws.on('tool_stdout', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      appendToolStdout(sid, (ev as any).tool_id, (ev as any).lines ?? [])
    }))

    cleanups.push(ws.on('progress', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      chatProgress.update(p => ({
        ...p,
        [sid]: { message: (ev as any).message || 'Thinking', phase: (ev as any).phase },
      }))
      // A fresh or replayed progress event means a turn is in flight. When the
      // user switches back to a running session the indicator was reset, so
      // restore the streaming flag so the thinking block/spinner renders.
      if ((ev as any).phase === 'active') {
        chatStreaming.update(s => ({ ...s, [sid]: true }))
      }
    }))

    cleanups.push(ws.on('complete', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      chatStreaming.update(s => ({ ...s, [sid]: false }))
      chatProgress.update(p => ({ ...p, [sid]: null }))
      // A turn that ends without an assistant_message (interrupt / error) would
      // otherwise leave the live bubble's streaming caret blinking forever, so
      // clear any lingering per-message streaming flags here.
      chatMessages.update(m => {
        const msgs = (m[sid] || []).map((x: any) => (x.streaming ? { ...x, streaming: false } : x))
        return { ...m, [sid]: msgs }
      })
      // Close open tool groups AND mark any still-spinning tools done — a
      // finished turn must never leave a tool on "running" (e.g. parallel
      // results that never matched a tool, or a dropped result event).
      finishAllTools(sid)
      // Dismiss finished sub-agents from the live panel. Agents still running
      // (e.g. a sync sub-agent promoted to background) remain visible.
      clearDoneSubAgents(sid)
      // Per-turn summary: elapsed time + tokens spent. The backend omits both
      // fields on error/interrupt, so this only fires on a clean completion.
      const durationMs = (ev as any).duration_ms
      const tokens = (ev as any).tokens
      if (typeof durationMs === 'number' && typeof tokens === 'number') {
        addChatMsg(sid, {
          id: uid('sum'),
          type: 'notice',
          content: `⏱ ${fmtDur(Math.round(durationMs / 1000))}, ${fmtTokens(tokens)} tokens`,
          level: 'info',
          createdAt: Date.now(),
          streaming: false,
          tools: [],
          todos: [],
        })
      }
    }))

    cleanups.push(ws.on('session_update', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      // Guard every field: session_update is sometimes partial (a turn-start
      // "running" ping, the working-dir PATCH echo), so an absent field must
      // not overwrite a good value with undefined.
      if (typeof (ev as any).context_usage === 'number') {
        chatContextUsage.update(u => ({ ...u, [sid]: (ev as any).context_usage }))
      }
      if (typeof (ev as any).context_tokens === 'number') {
        chatContextTokens.update(u => ({ ...u, [sid]: (ev as any).context_tokens }))
      }
      if (typeof (ev as any).permission_mode === 'string' && (ev as any).permission_mode) {
        chatPermMode.update(mm => ({ ...mm, [sid]: (ev as any).permission_mode }))
      }
      if (typeof (ev as any).reasoning_effort === 'string' && (ev as any).reasoning_effort) {
        chatReasoningEffort.update(r => ({ ...r, [sid]: (ev as any).reasoning_effort }))
      }
      if (typeof (ev as any).show_reasoning === 'boolean') {
        chatShowReasoning.update(r => ({ ...r, [sid]: (ev as any).show_reasoning }))
      }
      if (typeof (ev as any).working_dir === 'string' && (ev as any).working_dir) {
        chatWorkingDir.update(w => ({ ...w, [sid]: (ev as any).working_dir }))
      }
      // An idle snapshot from the server clears a stale thinking indicator.
      if ((ev as any).status === 'idle') {
        resetSessionRuntimeState(sid)
      }
    }))

    cleanups.push(ws.on('todo_update', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      chatTodos.update(t => ({ ...t, [sid]: (ev as any).todos ?? [] }))
    }))

    cleanups.push(ws.on('background_tasks_update', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      chatBgTasks.update(b => ({ ...b, [sid]: (ev as any).tasks ?? [] }))
    }))

    cleanups.push(ws.on('request_confirmation', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      confirmModal.set({
        // ConfirmModal reads $confirmModal.id when answering; storing it under
        // any other key sends a confirmation with no id, which the server can't
        // route back to the waiting tool (the turn then hangs on the popup).
        id: (ev as any).id,
        sessionId: (ev as any).session_id,
        message: (ev as any).message,
        kind: (ev as any).kind,
        // #1105: detail fields so the modal shows what it's approving
        // instead of just "Allow <tool>?".
        toolName: (ev as any).tool_name,
        command: (ev as any).command,
        diff: (ev as any).diff,
        input: (ev as any).input,
      })
    }))

    cleanups.push(ws.on('confirmation_complete', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      confirmModal.update(current => {
        if (!current) return current
        // Only close if this completion is for the confirmation currently
        // shown in this tab; otherwise leave any unrelated modal untouched.
        if ((ev as any).id === current.id) {
          return null
        }
        return current
      })
    }))

    cleanups.push(ws.on('request_user_question', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      // Falls back to sid like the dismiss_user_question handler below —
      // the current emitter always sets session_id, but without this
      // fallback a hypothetical session_id-less event would key the entry
      // by "" instead of the active session, making it unreachable from
      // QuestionModal's $questionModals[$activeSessionId] lookup.
      const qsid = (ev as any).session_id || sid
      questionModals.update(m => ({
        ...m,
        [qsid]: {
          questionId: (ev as any).question_id,
          sessionId: qsid,
          question: (ev as any).question,
          options: (ev as any).options,
          multiSelect: (ev as any).multi_select,
          header: (ev as any).header,
          dismissed: false,
        },
      }))
    }))

    cleanups.push(ws.on('dismiss_user_question', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      questionModals.update(m => {
        const n = { ...m }
        delete n[sid]
        return n
      })
    }))

    cleanups.push(ws.on('request_feedback', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      feedbackModal.set({
        sessionId: (ev as any).session_id,
        question: (ev as any).question,
        context: (ev as any).context,
        options: (ev as any).options,
      })
    }))

    cleanups.push(ws.on('next_message_suggestion', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      // Panel-launched agentic sessions (Replay/Record/Edit/…) are single-purpose;
      // a follow-up suggestion is just noise there.
      if (agenticSessions.has(sid)) return
      chatSuggestion.update(s => ({ ...s, [sid]: (ev as any).text ?? '' }))
    }))

    return () => {
      cancelled = true
      ws.unsubscribe(sid)
      for (const cleanup of cleanups) cleanup()
    }
  })

  // ── auto-scroll ────────────────────────────────────────────────────────────
  // Streaming appends text to the SAME message, so msgs.length doesn't change
  // and a length-only effect never re-fires. A ResizeObserver on the inner
  // content keeps us pinned to the bottom while the user is already there, and
  // backs off the moment they scroll up to read.
  let innerEl = $state<HTMLElement | null>(null)
  let stick = true

  $effect(() => {
    const scroller = messagesEl
    const content = innerEl
    if (!scroller || !content) return

    // A gentle upward gesture only moves scrollTop a few px, which usually
    // still lands within the 80px "near bottom" band below — recomputing
    // stick from distance-to-bottom alone on every 'scroll' event re-engaged
    // it immediately after 'wheel' had just disengaged it, so the very next
    // ResizeObserver tick (fired constantly while streaming) yanked the view
    // back to the bottom before the gesture could carry it out of the band.
    // That race is what made small scroll-ups jitter in place and never
    // escape (recurring case of #1069/#1187). Only let 'scroll' events moving
    // *toward* the bottom re-engage stick; ones moving away can disengage it
    // but never re-arm it purely from still being within the band.
    let lastScrollTop = scroller.scrollTop
    const onScroll = () => {
      const top = scroller.scrollTop
      const nearBottom = scroller.scrollHeight - top - scroller.clientHeight < 80
      if (top < lastScrollTop) {
        if (!nearBottom) stick = false
      } else {
        stick = nearBottom
      }
      lastScrollTop = top
    }
    scroller.addEventListener('scroll', onScroll, { passive: true })

    // The content resizes far more often than just while a reply streams —
    // a background workflow/sub-agent card's elapsed-time ticker (`now`,
    // updated every second) keeps nudging layout even after the turn ends.
    // Relying on 'scroll' alone to unstick means a manual scroll-up near the
    // bottom gets raced and snapped back before the position update lands,
    // which reads as the message jittering up and down (#1069). 'wheel'
    // disengages stick synchronously the instant an upward gesture starts,
    // before the browser even applies the delta. Dragging the scrollbar
    // thumb (or a touch drag) emits no 'wheel' events at all, so `interacting`
    // additionally blocks the ResizeObserver outright for the duration of any
    // pointer press on the scroller.
    const onWheel = (e: WheelEvent) => {
      if (e.deltaY < 0) stick = false
    }
    scroller.addEventListener('wheel', onWheel, { passive: true })

    let interacting = false
    const onPointerDown = () => { interacting = true }
    const onPointerUp = () => { interacting = false; onScroll() }
    scroller.addEventListener('pointerdown', onPointerDown)
    window.addEventListener('pointerup', onPointerUp)
    window.addEventListener('pointercancel', onPointerUp)
    // A drag released outside the window (e.g. the pointer is still down when
    // focus is stolen by alt-tabbing) never fires 'pointerup' on this
    // document, which would otherwise leave `interacting` stuck true and
    // silently disable auto-scroll for the rest of the tab's life.
    window.addEventListener('blur', onPointerUp)

    const ro = new ResizeObserver(() => {
      if (stick && !interacting) scroller.scrollTop = scroller.scrollHeight
    })
    ro.observe(content)

    // Initial pin to bottom after history loads.
    scroller.scrollTop = scroller.scrollHeight

    return () => {
      scroller.removeEventListener('scroll', onScroll)
      scroller.removeEventListener('wheel', onWheel)
      scroller.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('pointerup', onPointerUp)
      window.removeEventListener('pointercancel', onPointerUp)
      window.removeEventListener('blur', onPointerUp)
      ro.disconnect()
    }
  })

  // When the active session changes, re-pin to the bottom.
  $effect(() => {
    void $activeSessionId
    stick = true
  })

  // ── markdown copy buttons setup ────────────────────────────────────────────
  function setupAssistantEl(el: HTMLElement) {
    return setupCopyButtons(el)
  }

  // ── throttled markdown rendering while streaming ────────────────────────────
  // marked.parse() re-parses the ENTIRE accumulated text on every delta, which
  // is O(n²) over the life of a long streamed reply and shows up as visible
  // jank (#1114). Re-parse at most every RENDER_THROTTLE_MS while a bubble is
  // still streaming; once it finishes, always render fresh so the final
  // content is never stale. This only throttles the *view*, not the
  // underlying store, so it can't reorder anything else that reads chatMessages.
  const RENDER_THROTTLE_MS = 80
  const renderCache = new Map<string, { html: string; at: number; content: string }>()
  function throttledMarkdown(cacheKey: string, content: string, streaming: boolean, showReasoning = true): string {
    const cached = renderCache.get(cacheKey)
    if (streaming && cached && (content === cached.content || Date.now() - cached.at < RENDER_THROTTLE_MS)) {
      return cached.html
    }
    const html = renderMarkdown(content, showReasoning)
    renderCache.set(cacheKey, { html, at: Date.now(), content })
    return html
  }

  // ── edit a prior user message: load it back into the composer for resend ─────
  let composer = $state<{ setText: (v: string) => void } | null>(null)
  function editMessage(content: string) {
    composer?.setText(content)
  }

  // ── suggestion chip: fill the composer, don't fire (mirrors the TUI's Tab) ──
  function fillSuggestion(text: string) {
    composer?.setText(text)
    const sid = get(activeSessionId)
    if (sid) chatSuggestion.update(s => ({ ...s, [sid]: '' }))
  }

  // ── export the visible transcript as a markdown file ────────────────────────
  async function exportTranscript() {
    const sid = get(activeSessionId)
    if (!sid) return

    // Fetch complete history from server instead of relying on the local
    // chatMessages store, which may only contain a partial view of the
    // session. The server has no pagination (GET .../messages always returns
    // the full transcript), so there is nothing to page through here.
    let events: any[] = []
    try {
      const data = await api.getSessionMessages(sid)
      events = (data as { events?: any[] }).events ?? []
    } catch (e) {
      console.error('Export failed:', e)
      showToast(tr('chat.export_failed'), 'error')
      return
    }

    if (!events.length) { showToast(tr('chat.nothing_to_export'), 'error'); return }

    const title = currentSession?.title ?? currentSession?.name ?? 'session'
    const lines: string[] = [`# ${title}`, '']
    let omittedToolEvents = false

    for (const ev of events) {
      const type = ev.type ?? ''

      if (type === 'history_user_message') {
        lines.push('## You', '')
        lines.push(ev.content ?? '', '')
      } else if (type === 'assistant_message') {
        lines.push('## Octo', '')
        if (ev.thinking) {
          lines.push('<details><summary>Thoughts</summary>', '', ev.thinking, '', '</details>', '')
        }
        lines.push(ev.content ?? '', '')
      } else if (type === 'thinking' && ev.text) {
        // Standalone thinking block (tool round) — include as a note.
        lines.push('<!-- Thinking -->', ev.text, '')
      } else if (type === 'tool_call' || type === 'tool_result') {
        // Rendered as tool cards in the UI but don't belong in a readable
        // markdown transcript — noted below rather than silently dropped.
        omittedToolEvents = true
      }
    }

    const blob = new Blob([lines.join('\n')], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${title.replace(/[^\w.-]+/g, '_')}.md`
    a.click()
    URL.revokeObjectURL(url)

    if (omittedToolEvents) {
      showToast(tr('chat.export_tools_omitted'), 'info')
    }
  }

  // ensureActiveSession returns the active session id, creating one first if
  // none is active — e.g. right after deleting the session that was open.
  // Mirrors Sidebar's "+" new-session flow so typing into that empty state
  // and hitting send works instead of silently dropping the message.
  async function ensureActiveSession(): Promise<string | null> {
    const existing = get(activeSessionId)
    if (existing) return existing
    try {
      const created = await api.createSession({ source: 'manual' }) as any
      const newSess = created.session ?? created
      sessions.update(ss => [newSess, ...ss])
      activeSessionId.set(newSess.id)
      activeSession.set(newSess.id)
      return newSess.id
    } catch (e: any) {
      showToast(e.message, 'error')
      return null
    }
  }

  // ── send message ───────────────────────────────────────────────────────────
  async function send(text: string, files?: any[]) {
    if (!text.trim() && !(files && files.length)) return
    const sid = await ensureActiveSession()
    if (!sid) return
    // Steering: a message sent while a turn is already running rides the
    // running turn's Inbox on the server. It must NOT reset the live UI —
    // the sub-agents panel and thinking buffer belong to the turn in flight.
    const wasStreaming = get(chatStreaming)[sid] ?? false
    const steering = wasStreaming
    if (!steering) {
      // A fresh turn starts: clear the previous turn's sub-agents panel and
      // thinking buffer, and flip the session into streaming.
      resetSubAgents(sid)
      chatThinking.update(tt => ({ ...tt, [sid]: '' }))
      chatStreaming.update(s => ({ ...s, [sid]: true }))
    }
    const pendingId = 'pending-' + Date.now()
    const queue = pendingSends.get(sid) ?? []
    queue.push({ pendingId, wasStreaming, text, files })
    pendingSends.set(sid, queue)
    if (steering) {
      // Mid-turn input: show above the composer, not in the scrollback, until
      // the server drains it into the running turn (mirrors TUI pendingSteer).
      pendingSteers = [...pendingSteers, { pendingId, text, files }]
    } else {
      // Optimistically show the user bubble, marked pending. The server echoes
      // it back as a history_user_message — that handler replaces this pending
      // bubble (matching by content) instead of appending a duplicate.
      addChatMsg(sid, {
        id: pendingId,
        type: 'user',
        content: text,
        files: files && files.length ? files : undefined,
        createdAt: Date.now(),
        streaming: false,
        pending: true,
        tools: [],
        todos: [],
      })
    }
    ws.sendMessage(sid, text, files)
  }

  // ── force bind ─────────────────────────────────────────────────────────────
  // Retry the pending send with force=true, taking over a session bound to
  // another entry as long as no turn lease is active.
  function forceBindAndSend() {
    const sid = bindRequiredFor
    if (!sid) return
    const queue = pendingSends.get(sid)
    const meta = queue?.[queue.length - 1]
    if (!meta) {
      bindRequiredFor = null
      return
    }
    bindRequiredFor = null
    ws.sendMessage(sid, meta.text, meta.files, true)
  }

  // ── plan progress helpers ──────────────────────────────────────────────────
  function planDoneCount(todos: any[]): number {
    return todos.filter((t: any) => t.status === 'completed').length
  }
  function planFill(todos: any[]): string {
    if (!todos.length) return '0%'
    return `${Math.round((planDoneCount(todos) / todos.length) * 100)}%`
  }

  function formatBindMessage(msg: string): string {
    // Keep the message concise for the inline banner.
    return msg.replace(/since [^;]+;?/i, '').trim() || 'Session is bound to another entry.'
  }
</script>

<div class="chat-view">
  <!-- Chat header -->
  <div class="chat-header">
    <div class="title-row">
      <span class="session-title">
        {currentSession?.title ?? currentSession?.name ?? 'Chat'}
      </span>
      {#if streaming}
        <StatusTag status="info">{$t('status.running')}</StatusTag>
      {:else}
        <StatusTag status="default">{$t('status.idle')}</StatusTag>
      {/if}
    </div>
    <div class="header-actions">
      <button class="hdr-btn" title={$t('chat.compact_tooltip')} disabled={!id || streaming} onclick={() => send('/compact')}>
        <iconify-icon icon="ant-design:compress-outlined" width="13"></iconify-icon>
        {$t('chat.compact')}
      </button>
      <button class="hdr-btn" title={$t('chat.clear_tooltip')} disabled={!id || streaming} onclick={() => {
        if (confirm($t('chat.clear_confirm'))) send('/clear')
      }}>
        <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
        {$t('chat.clear')}
      </button>
      <button class="hdr-btn" class:active={$artifactsOpen} onclick={() => artifactsOpen.update(v => !v)}>
        <iconify-icon icon="ant-design:file-text-outlined" width="13"></iconify-icon>
        {$t('chat.artifacts')}
        {#if artifactCount > 0}
          <span class="count-badge">{artifactCount}</span>
        {/if}
      </button>
      <button class="hdr-btn" onclick={exportTranscript}>
        <iconify-icon icon="ant-design:export-outlined" width="13"></iconify-icon>
        {$t('chat.export')}
      </button>
    </div>
  </div>

  <!-- Force-bind banner: session is owned by another entry but can be taken over.
       Guard on `id` too: when the active session is deleted, both
       `bindRequiredFor` and `id` become null, and `null === null` would
       incorrectly render the banner over the empty chat landing. -->
  {#if id && bindRequiredFor === id}
    <div class="ws-banner bind-banner">
      <iconify-icon icon="ant-design:warning-outlined" width="15" style="color:var(--warning)"></iconify-icon>
      <span class="ws-msg">{formatBindMessage(bindRequiredMessage)}</span>
      <span style="margin-left:auto"></span>
      <button class="ws-retry" onclick={forceBindAndSend}>{$t('chat.force_bind')}</button>
    </div>
  {/if}

  <!-- WS disconnect banner -->
  {#if wsDisconnected}
    <div class="ws-banner">
      <iconify-icon icon="ant-design:loading-outlined" width="15" style="color:var(--warning);animation:octo-spin 0.8s linear infinite"></iconify-icon>
      <span class="ws-msg">{$t('chat.connection_lost')}</span>
      {#if $wsReconnect}
        <span class="ws-meta">attempt {$wsReconnect.attempt} · next in {reconnectIn}s</span>
      {/if}
      <span style="margin-left:auto"></span>
      <button class="ws-retry" onclick={() => ws.connect()}>{$t('chat.retry_now')}</button>
    </div>
  {/if}

  <!-- Session-level task progress (driven by task_create / task_update / task_list).
       Collapsed by default; expands its step list in-flow below the summary. -->
  {#if todos && todos.length > 0}
    <div class="session-tasks">
      <details bind:open={planExpanded} class="plan-card">
        <summary class="plan-summary">
          <iconify-icon icon="ant-design:ordered-list-outlined" width="14" style="color:var(--blue-6)"></iconify-icon>
          <span class="plan-title">{$t('agent.plan')}</span>
          <span class="plan-meta">{planDoneCount(todos)} / {todos.length} done</span>
          <span class="plan-progress"><span class="plan-fill" style="width:{planFill(todos)}"></span></span>
          <span style="margin-left:auto"></span>
          <iconify-icon icon={planExpanded ? 'lucide:chevron-up' : 'lucide:chevron-down'} width="14" style="color:var(--text-tertiary)"></iconify-icon>
        </summary>
        <div class="plan-steps">
          {#each todos as step}
            <div class="step" class:active={step.status === 'in_progress'}>
              {#if step.status === 'completed'}
                <iconify-icon icon="ant-design:check-circle-outlined" width="14" style="color:var(--success)"></iconify-icon>
                <span class="done">{step.content}</span>
              {:else if step.status === 'in_progress'}
                <iconify-icon icon="ant-design:loading-outlined" width="14" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                <span>{step.content}</span>
              {:else}
                <iconify-icon icon="lucide:circle" width="14" style="color:var(--text-quaternary)"></iconify-icon>
                <span class="pending">{step.content}</span>
              {/if}
            </div>
          {/each}
        </div>
      </details>
    </div>
  {/if}

  <!-- Body row: messages + artifacts -->
  <div class="body-row">
    <div class="conversation">
      <!-- Messages scroll area -->
      <div class="messages" bind:this={messagesEl}>
        <div class="messages-inner" bind:this={innerEl}>

          {#each msgs as msg (msg.id)}
            {#if msg.type === 'user'}
              <!-- Right-aligned user bubble -->
              <div class="msg-user fadein">
                <div class="user-avatar" aria-hidden="true">
                  <iconify-icon icon="ant-design:user-outlined" width="16"></iconify-icon>
                </div>
                <div class="user-bubble-wrap">
                  <div class="user-bubble" class:pending={msg.pending}>
                    {#if msg.files && msg.files.length > 0}
                      <div class="msg-attachments">
                        {#each msg.files as f}
                          {#if f.mime_type?.startsWith('image/')}
                            <img src={f.data_url} alt={f.name} class="msg-image" />
                          {:else}
                            <span class="attach-chip"><iconify-icon icon="ant-design:paper-clip-outlined" width="12"></iconify-icon>{f.name}</span>
                          {/if}
                        {/each}
                      </div>
                    {:else if msg.images && msg.images.length > 0}
                      <!-- Server-derived refs (survive reload): a "/api/uploads/…"
                           URL is an image thumbnail; a "pdf:<name>" sentinel is a
                           document chip. -->
                      <div class="msg-attachments">
                        {#each msg.images as ref}
                          {#if ref.startsWith('pdf:')}
                            <span class="attach-chip"><iconify-icon icon="ant-design:paper-clip-outlined" width="12"></iconify-icon>{ref.slice(4)}</span>
                          {:else}
                            <img src={ref} alt="attachment" class="msg-image" />
                          {/if}
                        {/each}
                      </div>
                    {/if}
                    {#if msg.content}{msg.content}{/if}
                    {#if msg.pending}<span class="pending-spinner" title={$t('status.running')}></span>{/if}
                  </div>
                  <div class="msg-actions">
                    <button class="action-btn" title={$t('chat.edit')} onclick={() => editMessage(msg.content)}>
                      <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
                    </button>
                    <button class="action-btn" title={$t('chat.copy')} onclick={() => navigator.clipboard.writeText(msg.content)}>
                      <iconify-icon icon="ant-design:copy-outlined" width="13"></iconify-icon>
                    </button>
                  </div>
                </div>
              </div>

            {:else if msg.type === 'assistant'}
              <!-- Assistant message with avatar -->
              <div class="msg-agent fadein">
                <div class="agent-avatar"><OctoLogo size={18} /></div>
                <div class="agent-content">
                  <!-- Plan card (todos attached to this message) -->
                  {#if msg.todos && msg.todos.length > 0}
                    <details class="plan-card">
                      <summary class="plan-summary">
                        <iconify-icon icon="ant-design:ordered-list-outlined" width="14" style="color:var(--blue-6)"></iconify-icon>
                        <span class="plan-title">{$t('agent.plan')}</span>
                        <span class="plan-meta">{planDoneCount(msg.todos)} / {msg.todos.length} done</span>
                        <span class="plan-progress"><span class="plan-fill" style="width:{planFill(msg.todos)}"></span></span>
                        <span style="margin-left:auto"></span>
                        <iconify-icon icon="lucide:chevron-down" width="14" style="color:var(--text-tertiary)"></iconify-icon>
                      </summary>
                      <div class="plan-steps">
                        {#each msg.todos as step}
                          <div class="step" class:active={step.status === 'in_progress'}>
                            {#if step.status === 'completed'}
                              <iconify-icon icon="ant-design:check-circle-outlined" width="14" style="color:var(--success)"></iconify-icon>
                              <span class="done">{step.content}</span>
                            {:else if step.status === 'in_progress'}
                              <iconify-icon icon="ant-design:loading-outlined" width="14" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                              <span>{step.content}</span>
                            {:else}
                              <iconify-icon icon="lucide:circle" width="14" style="color:var(--text-quaternary)"></iconify-icon>
                              <span class="pending">{step.content}</span>
                            {/if}
                          </div>
                        {/each}
                      </div>
                    </details>
                  {/if}

                  <!-- Thoughts / reasoning block -->
                  {#if msg.thinking && showReasoning}
                    <details class="think-block">
                      <summary class="think-summary">
                        <iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>
                        <span>{$t('chat.thoughts')}</span>
                        <iconify-icon icon="lucide:chevron-right" width="13"></iconify-icon>
                      </summary>
                      <div class="think-body" use:setupAssistantEl>{@html renderMarkdown(msg.thinking)}</div>
                    </details>
                  {/if}

                  <!-- Rendered markdown content -->
                  {#if msg.content}
                    <div
                      class="rich-answer"
                      use:setupAssistantEl
                    >
                      {@html throttledMarkdown(msg.id, msg.content, msg.streaming, showReasoning)}
                    </div>
                  {/if}

                  <!-- Streaming caret -->
                  {#if msg.streaming}
                    <span class="caret"></span>
                  {/if}

                  <!-- Message actions -->
                  {#if !msg.streaming}
                    <div class="msg-actions reply-actions">
                      <button class="action-btn" title={$t('chat.copy')} onclick={() => navigator.clipboard.writeText(msg.content)}>
                        <iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon>
                      </button>
                      <button class="action-btn" title={$t('chat.retry')} onclick={() => ws.retry($activeSessionId ?? '')}>
                        <iconify-icon icon="ant-design:reload-outlined" width="14"></iconify-icon>
                      </button>
                    </div>
                  {/if}
                </div>
              </div>

            {:else if msg.type === 'thinking' && showReasoning}
              <!-- Standalone Thoughts segment (reasoning before a tool round) -->
              <div class="msg-agent fadein">
                <div class="agent-avatar"><OctoLogo size={18} /></div>
                <div class="agent-content">
                  <details class="think-block">
                    <summary class="think-summary">
                      <iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>
                      <span>{$t('chat.thoughts')}</span>
                      <iconify-icon icon="lucide:chevron-right" width="13"></iconify-icon>
                    </summary>
                    <div class="think-body" use:setupAssistantEl>{@html renderMarkdown(msg.thinking)}</div>
                  </details>
                </div>
              </div>

            {:else if msg.type === 'tool_group'}
              <!-- Tool group card -->
              <div class="msg-agent fadein">
                <div class="agent-avatar"><OctoLogo size={18} /></div>
                <div class="agent-content">
                  <ToolGroup tools={msg.tools} streaming={msg.streaming} />
                </div>
              </div>

            {:else if msg.type === 'progress'}
              <!-- Inline progress message -->
              <div class="msg-agent fadein">
                <div class="agent-avatar"><OctoLogo size={18} /></div>
                <div class="thinking-indicator">
                  <iconify-icon icon="ant-design:loading-outlined" width="15" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                  <span>{msg.content || $t('chat.thinking')}</span>
                </div>
              </div>

            {:else if msg.type === 'notice'}
              <!-- Inline scrollback notice (background process completion, etc.) -->
              <div class="msg-agent fadein">
                <div class="agent-avatar notice-avatar" data-level={msg.level}>
                  <iconify-icon icon="lucide:info" width="14"></iconify-icon>
                </div>
                <div class="agent-content">
                  <div class="notice-line" data-level={msg.level}>{@html renderMarkdown(msg.content, showReasoning)}</div>
                </div>
              </div>
            {/if}
          {/each}

          <!-- Live sub-agents panel (current turn) -->
          {#if subAgents.length > 0}
            <div class="msg-agent fadein">
              <div class="agent-avatar"><OctoLogo size={18} /></div>
              <div class="agent-content">
                <SubAgentsCard agents={subAgents} elapsed={subAgentsElapsed} />
              </div>
            </div>
          {/if}

          <!-- Live thinking block while streaming -->
          {#if streaming && thinking && showReasoning}
            <div class="msg-agent fadein">
              <div class="agent-avatar"><OctoLogo size={18} /></div>
              <div class="agent-content">
                <details class="think-block" open>
                  <summary class="think-summary">
                    <iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>
                    <span>{$t('chat.thinking')}</span>
                    <span class="think-meta mono">{fmtDur(thinkElapsed)}{#if thinkTokens > 0} · ↓ ~{fmtTokens(thinkTokens)} tokens{:else if ctxTokens > 0} · ↑ ~{fmtTokens(ctxTokens)} tokens{:else} · ↑{/if}</span>
                  </summary>
                  <div class="think-body" use:setupAssistantEl>{@html throttledMarkdown('live-thinking:' + id, thinking, true, showReasoning)}</div>
                </details>
              </div>
            </div>
          {/if}

          <!-- Live thinking indicator while streaming -->
          {#if streaming && progress}
            <div class="msg-agent fadein">
              <div class="agent-avatar"><OctoLogo size={18} /></div>
              <div class="thinking-indicator">
                <iconify-icon icon="ant-design:loading-outlined" width="15" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                <span>{progress.message || $t('chat.thinking')}</span>
                <span class="dots">
                  <span></span>
                  <span style="animation-delay:0.2s"></span>
                  <span style="animation-delay:0.4s"></span>
                </span>
                <span class="think-meta mono">
                  {fmtDur(thinkElapsed)}{#if thinkTokens > 0} · ↓ ~{fmtTokens(thinkTokens)} tokens{:else if ctxTokens > 0} · ↑ ~{fmtTokens(ctxTokens)} tokens{:else} · ↑{/if}
                </span>
              </div>
            </div>
          {/if}

          <!-- Suggestion chip -->
          {#if suggestion && !streaming}
            <div class="suggestion-row">
              <button class="suggestion-chip" onclick={() => fillSuggestion(suggestion)}>
                <iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>
                {suggestion}
              </button>
            </div>
          {/if}
        </div>
      </div>

      <!-- Background workflows panel (persists across turns, pinned above composer) -->
      {#if workflows.length > 0}
        <div class="workflows-bar fadein">
          <WorkflowsCard runs={workflows} {now} />
        </div>
      {/if}

      <!-- Background processes tray -->
      {#if bgTasks && bgTasks.length > 0}
        <BackgroundProcesses tasks={bgTasks} />
      {/if}

      <!-- Pending steer messages (mid-turn input) — shown above the composer
           as ghost user bubbles so they don't break the chronological order
           of the scrollback while waiting to be drained. -->
      {#if pendingSteers.length > 0}
        <div class="pending-steer-bar fadein">
          {#each pendingSteers as s}
            <div class="pending-steer-bubble">
              <div class="user-avatar" aria-hidden="true">
                <iconify-icon icon="ant-design:user-outlined" width="16"></iconify-icon>
              </div>
              <div class="user-bubble-wrap">
                <div class="user-bubble pending">
                  {#if s.files && s.files.length > 0}
                    <div class="msg-attachments">
                      {#each s.files as f}
                        {#if f.mime_type?.startsWith('image/')}
                          <img src={f.data_url} alt={f.name} class="msg-image" />
                        {:else}
                          <span class="attach-chip"><iconify-icon icon="ant-design:paper-clip-outlined" width="12"></iconify-icon>{f.name}</span>
                        {/if}
                      {/each}
                    </div>
                  {/if}
                  {#if s.text}{s.text}{/if}
                  <span class="pending-spinner" title={$t('status.running')}></span>
                </div>
              </div>
            </div>
          {/each}
        </div>
      {/if}

      <!-- Composer -->
      <Composer bind:this={composer} onSend={send} />
    </div>

    <!-- Artifacts panel -->
    {#if $artifactsOpen}
      <ArtifactsPanel />
    {/if}
  </div>
</div>

<style>
/* ── Layout ──────────────────────────────────────────────────────────────── */
.chat-view { flex: 1; display: flex; flex-direction: column; min-height: 0; }

/* ── Header ──────────────────────────────────────────────────────────────── */
.chat-header {
  flex: 0 0 auto; background: var(--bg-container); border-bottom: 1px solid var(--border-secondary);
  padding: 12px 24px; display: flex; align-items: center; justify-content: space-between;
}
.title-row { display: flex; align-items: center; gap: 10px; }
.session-title { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.header-actions { display: flex; align-items: center; gap: 8px; }
.hdr-btn {
  height: 28px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; display: flex; align-items: center; gap: 8px;
  font-size: 13px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.hdr-btn:hover { border-color: var(--blue-5); color: var(--blue-5); }
.hdr-btn.active { border-color: var(--blue-6); color: var(--blue-6); background: var(--active-blue-bg); }
.hdr-btn:disabled { opacity: 0.5; cursor: not-allowed; border-color: var(--border); color: var(--text-quaternary); }
.count-badge {
  font-size: 11px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: var(--blue-1); color: var(--blue-6); border-radius: 9999px;
  min-width: 16px; height: 16px; padding: 0 5px;
  display: flex; align-items: center; justify-content: center;
}

/* ── WS banner ───────────────────────────────────────────────────────────── */
.ws-banner {
  flex: 0 0 auto; display: flex; align-items: center; gap: 10px;
  padding: 10px 24px; background: var(--warning-bg); border-bottom: 1px solid var(--warning-border);
}
.ws-msg { font-size: 13px; color: var(--warning-text); }
.ws-meta { font-size: 12px; color: rgba(135,77,0,0.6); }
.ws-retry {
  height: 28px; padding: 0 12px; border: 1px solid var(--warning-border); background: var(--bg-container);
  border-radius: 6px; font-size: 12px; color: var(--warning-text); cursor: pointer; font-family: inherit;
}
.ws-retry:hover { border-color: var(--warning); }

.bind-banner {
  background: var(--surface-info);
  border-bottom-color: var(--blue-2);
}

/* ── Session task progress ───────────────────────────────────────────────── */
.session-tasks {
  flex: 0 0 auto;
  padding: 8px 24px;
  background: var(--bg-container);
  border-bottom: 1px solid var(--border);
  position: relative;
  z-index: 10;
}
.session-tasks .plan-card { margin: 0; }
/* The step list expands in-flow below the summary. It used to render as an
   absolute-positioned floating overlay, but .plan-card's overflow:hidden (same
   element, equal specificity, declared later) overrode the overlay's
   overflow:visible and clipped it away — the panel looked stuck closed. In flow
   there is nothing to clip: the bar is flex:0 0 auto, so growing it shrinks the
   scrollable conversation underneath rather than hiding it. Cap the height so a
   long plan scrolls inside the bar instead of swallowing the message area. */
.session-tasks .plan-steps {
  max-height: min(320px, 40vh);
  overflow-y: auto;
}

/* ── Body row ────────────────────────────────────────────────────────────── */
.body-row { flex: 1; display: flex; min-height: 0; }
.conversation {
  flex: 1; display: flex; flex-direction: column; min-width: 0; min-height: 0;
  /* Keep the chat column narrower than full-width settings pages for
     readability; Composer picks this up via CSS var inheritance. Wide enough
     that the composer's status chips (model · reasoning · cwd · context · mode)
     sit on one row without wrapping. */
  --chat-content-max-width: 960px;
}
.workflows-bar {
  flex: 0 0 auto;
  max-width: var(--chat-content-max-width); margin: 0 auto; width: 100%;
  padding: 0 24px 12px;
}
.messages {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  /* Keep the conversation's overscroll (rubber-band) from chaining to the
     body. On WebKit this prevents the page from juddering when the user is
     at the bottom and swipes up a small amount. */
  overscroll-behavior-y: contain;
  -webkit-overflow-scrolling: touch;
}
.messages-inner {
  max-width: var(--chat-content-max-width); margin: 0 auto;
  padding: 24px 24px 16px; display: flex; flex-direction: column; gap: 20px;
}

/* ── User message ────────────────────────────────────────────────────────── */
.msg-user { display: flex; justify-content: flex-end; gap: 12px; }
.user-bubble-wrap { display: flex; flex-direction: column; align-items: flex-end; gap: 4px; max-width: 80%; }
.user-avatar {
  width: 28px; height: 28px; flex: 0 0 28px; border-radius: 8px;
  background: var(--bg-tertiary); color: var(--text-secondary);
  display: flex; align-items: center; justify-content: center;
  order: 1;
}
.user-bubble {
  background: var(--blue-1); border: 1px solid var(--blue-2);
  border-radius: 12px 12px 4px 12px; padding: 10px 14px;
  font-size: 14px; line-height: 1.6; color: var(--text);
  white-space: pre-wrap; word-break: break-word;
  display: flex; flex-direction: column; gap: 8px;
}
/* Pending (queued) bubble — an optimistic echo, or a steer message waiting to
   be drained into the running turn. Dimmed with a small spinner until the
   server confirms it via history_user_message. */
.user-bubble.pending { opacity: 0.65; }
.pending-spinner {
  display: inline-block; width: 10px; height: 10px; margin-left: 6px;
  vertical-align: -1px; border-radius: 50%;
  border: 1.5px solid var(--blue-2); border-top-color: var(--blue-6);
  animation: octo-spin 0.8s linear infinite;
}

/* ── Inline attachments inside user bubbles ──────────────────────────────── */
.msg-attachments { display: flex; flex-wrap: wrap; gap: 8px; justify-content: flex-end; }
.msg-image { max-width: 100%; max-height: 320px; border-radius: 8px; border: 1px solid var(--blue-2); }

/* ── Agent message ───────────────────────────────────────────────────────── */
.msg-agent { display: flex; gap: 12px; }
.agent-avatar {
  width: 28px; height: 28px; flex: 0 0 28px; border-radius: 8px;
  background: var(--blue-6); color: var(--blue-6);
  display: flex; align-items: center; justify-content: center;
  font-size: 13px; font-weight: 600;
  overflow: hidden;
}
.agent-avatar :global(svg) { width: 100%; height: 100%; }
.agent-content { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 12px; }

/* ── Plan card ───────────────────────────────────────────────────────────── */
.plan-card { border: 1px solid var(--blue-2); border-radius: 10px; background: var(--surface-info); overflow: hidden; }
.plan-summary {
  list-style: none; display: flex; align-items: center; gap: 10px;
  padding: 10px 12px; cursor: pointer; user-select: none;
}
.plan-title { font-size: 13px; font-weight: 600; color: var(--text-heading); }
.plan-meta { font-size: 12px; color: var(--text-tertiary); }
.plan-progress {
  flex: 1; min-width: 40px; max-width: 160px; height: 4px;
  background: var(--blue-2); border-radius: 9999px; overflow: hidden;
}
.plan-fill { display: block; height: 100%; background: var(--blue-6); }
.plan-steps {
  border-top: 1px solid var(--blue-2); background: var(--bg-container);
  padding: 10px 14px; display: flex; flex-direction: column; gap: 8px;
}
.step { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.step .done { color: var(--text-tertiary); text-decoration: line-through; }
.step .pending { color: var(--text-tertiary); }
.step.active { margin: 0 -6px; padding: 4px 6px; background: var(--active-blue-bg); border-radius: 6px; }

/* ── Rich answer (markdown) ──────────────────────────────────────────────── */
.rich-answer { font-size: 14px; line-height: 1.6; color: var(--text); display: flex; flex-direction: column; gap: 12px; }
:global(.rich-answer p) { margin: 0; }
:global(.rich-answer code), :global(.think-body code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; font-style: normal;
  background: var(--bg-table-header); border: 1px solid var(--border-table); border-radius: 4px; padding: 1px 5px;
}
:global(.rich-answer .code-block), :global(.think-body .code-block) {
  border: 1px solid var(--border-table); border-radius: 8px; overflow: hidden;
  background: var(--bg-sidebar); font-style: normal;
}
:global(.rich-answer .code-header), :global(.think-body .code-header) {
  display: flex; align-items: center; gap: 8px; padding: 6px 8px 6px 12px;
  background: var(--bg-table-header); border-bottom: 1px solid var(--border-table);
}
:global(.rich-answer .code-lang), :global(.think-body .code-lang) { font-size: 11px; color: var(--text-tertiary); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
:global(.rich-answer .copy-btn), :global(.think-body .copy-btn) {
  margin-left: auto; height: 24px; padding: 0 8px; border: none; background: transparent;
  border-radius: 5px; display: flex; align-items: center; gap: 5px;
  font-size: 11px; color: var(--text-tertiary); cursor: pointer;
}
:global(.rich-answer .copy-btn:hover), :global(.think-body .copy-btn:hover) { background: var(--hover-neutral); color: var(--blue-6); }
:global(.rich-answer pre), :global(.think-body pre) {
  margin: 0; padding: 12px 14px; overflow-x: auto; font-size: 12.5px; line-height: 1.75;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text); font-style: normal;
}
:global(.rich-answer .md-bq), :global(.think-body .md-bq) {
  margin: 0; padding: 8px 14px; border-left: 3px solid var(--blue-2);
  background: var(--surface-info); border-radius: 0 6px 6px 0;
  font-size: 13px; line-height: 1.6; color: var(--text-secondary);
}
:global(.think-block) { border-radius: 8px; }
:global(.think-summary) {
  list-style: none; display: inline-flex; align-items: center; gap: 6px;
  cursor: pointer; user-select: none; font-size: 13px; color: var(--text-tertiary);
}
:global(.think-summary::-webkit-details-marker) { display: none; }
:global(.think-summary:hover) { color: var(--text-secondary); }
:global(.think-body) {
  margin-top: 8px; padding-left: 12px; border-left: 2px solid var(--border-secondary);
  font-size: 13px; line-height: 1.7; color: var(--text-tertiary); font-style: italic;
  display: flex; flex-direction: column; gap: 10px;
}

/* ── Message actions ─────────────────────────────────────────────────────── */
.msg-actions { display: flex; align-items: center; gap: 2px; }
.reply-actions { margin-top: -4px; }
.action-btn {
  width: 26px; height: 26px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary); opacity: 0; transition: opacity 0.12s;
}
.user-bubble-wrap:hover .action-btn,
.agent-content:hover .reply-actions .action-btn { opacity: 1; }
.action-btn:hover { background: var(--hover-neutral); color: var(--blue-6); }

/* ── Streaming caret ─────────────────────────────────────────────────────── */
.caret {
  display: inline-block; width: 7px; height: 15px;
  background: var(--blue-6); vertical-align: -2px; margin-left: 1px;
  animation: octo-blink 1s step-end infinite;
}

/* ── Thinking indicator ──────────────────────────────────────────────────── */
.thinking-indicator {
  display: flex; align-items: center; gap: 10px; min-height: 28px;
  font-size: 14px; color: var(--text-secondary);
}
.dots { display: inline-flex; gap: 3px; align-items: center; }
.dots span {
  width: 4px; height: 4px; border-radius: 9999px;
  background: var(--text-tertiary); animation: octo-dot 1.2s infinite;
}
.think-meta { font-size: 12px; color: var(--text-tertiary); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

/* ── Suggestion ──────────────────────────────────────────────────────────── */
.suggestion-row { display: flex; justify-content: flex-end; }
.suggestion-chip {
  max-width: 80%; height: auto; padding: 7px 14px;
  border: 1px dashed var(--blue-2); background: var(--surface-info);
  border-radius: 10px; display: flex; align-items: center; gap: 8px;
  font-size: 13px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
  text-align: left; line-height: 1.5;
}
.suggestion-chip:hover { border-color: var(--blue-6); color: var(--blue-6); }

/* ── Inline scrollback notice ─────────────────────────────────────────────── */
.notice-avatar {
  background: transparent;
  color: var(--text-tertiary);
}
.notice-avatar[data-level="success"] { color: var(--success); }
.notice-avatar[data-level="error"] { color: var(--error); }
.notice-avatar[data-level="info"] { color: var(--text-secondary); }
.notice-line {
  display: flex; align-items: center; gap: 8px; min-height: 28px;
  font-size: 13px; color: var(--text-secondary);
}
.notice-line[data-level="success"] { color: var(--success); }
.notice-line[data-level="error"] { color: var(--error); }
.notice-line[data-level="info"] { color: var(--text-secondary); }
.notice-line :global(p) { margin: 0; }
.notice-line :global(code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px; background: var(--bg-table-header); border: 1px solid var(--border-table);
  border-radius: 4px; padding: 1px 4px;
}

/* ── Pending steer (mid-turn input) ──────────────────────────────────────── */
.pending-steer-bar {
  max-width: var(--chat-content-max-width); margin: 0 auto; width: 100%;
  padding: 0 24px 10px;
  display: flex; flex-direction: column; gap: 10px;
}
.pending-steer-bubble {
  display: flex;
  justify-content: flex-end;
  align-items: flex-start;
  gap: 10px;
}
.pending-steer-bubble .user-bubble {
  border-radius: 12px 12px 4px 12px;
}
.pending-steer-bubble .user-avatar {
  width: 28px; height: 28px; flex: 0 0 28px; border-radius: 8px;
  background: var(--bg-sidebar); border: 1px solid var(--border);
  display: flex; align-items: center; justify-content: center;
  color: var(--text-secondary);
}

/* ── Fade-in ─────────────────────────────────────────────────────────────── */
.fadein { animation: octo-fadein 0.25s ease; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
