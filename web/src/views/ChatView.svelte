<script lang="ts">
  import { get } from 'svelte/store'
  import {
    activeSessionId,
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
    questionModal,
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
    finishAllTools,
    finishToolsById,
    resetSubAgents,
    clearDoneSubAgents,
    applySubAgentEvent,
    showToast,
    uid,
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

  // Session-level plan panel: collapsed by default so it never occludes the
  // message stream; the user can expand it into a floating dropdown.
  let planExpanded = $state(false)

  // Tracks optimistic UI state for in-flight sends. If the server rejects the
  // message (e.g. the session is bound to another entry), we roll back the
  // pending bubble and restore the streaming flag to its pre-send value.
  // Content and files are kept so a force takeover can retry the same message.
  const pendingSends = new Map<string, { pendingId: string; wasStreaming: boolean; text: string; files?: any[] }>()

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
  function handleHistoryEvent(ev: Record<string, any>) {
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
      // it before the tools it preceded.
      commitThinking(sid, ev.text ?? '')
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
  // rewrote history out of band).
  function loadHistory(sid: string) {
    api.getSessionMessages(sid, { limit: 30 }).then((resp: any) => {
      const events: any[] = resp?.events ?? []
      // Collect the tool_ids that came from history so we only close those,
      // leaving any concurrently-replayed live-turn tools untouched.
      const historyToolIds = new Set<string>()
      for (const ev of events) {
        if (ev.type === 'tool_call' && ev.tool_id) historyToolIds.add(ev.tool_id)
        handleHistoryEvent(ev)
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
    if (!sid) return

    clearMsgs(sid)
    resetArtifacts(sid)
    resetSessionRuntimeState(sid)
    ws.subscribe(sid)
    loadHistory(sid)

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
    // session not found, etc.). Roll back the optimistic pending bubble and
    // restore the streaming flag so the composer doesn't get stuck showing
    // Stop / a phantom steer message.
    cleanups.push(ws.on('send_rejected', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const meta = pendingSends.get(sid)
      if (meta) {
        chatMessages.update(m => ({
          ...m,
          [sid]: (m[sid] || []).filter((msg: any) => msg.id !== meta.pendingId),
        }))
        chatStreaming.update(s => ({ ...s, [sid]: meta.wasStreaming }))
        pendingSends.delete(sid)
      }
      bindRequiredFor = null
      showToast((ev as any).message ?? 'Error', 'error')
    }))

    // The session is bound to another entry but no turn lease is active. Offer
    // a force takeover instead of dropping the message.
    cleanups.push(ws.on('bind_required', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const meta = pendingSends.get(sid)
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
      appendToLastAssistant(sid, txt)
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
      let confirmedPendingId: string | null = null
      chatMessages.update(m => {
        const msgs = [...(m[sid] || [])]
        // If the last user bubble is a pending optimistic echo of the same
        // text, replace it in place (de-dup). Otherwise append a fresh one.
        const lastPending = msgs.findLastIndex((x: any) => x.type === 'user' && x.pending)
        if (lastPending >= 0 && msgs[lastPending].content === content) {
          confirmedPendingId = msgs[lastPending].id
          msgs[lastPending] = { ...msgs[lastPending], id: uid('u'), createdAt, pending: false, images }
        } else {
          msgs.push({ id: uid('u'), type: 'user', content, createdAt, streaming: false, pending: false, tools: [], todos: [], images })
        }
        return { ...m, [sid]: msgs }
      })
      // The server confirmed this optimistic send; stop tracking it for rollback.
      const meta = pendingSends.get(sid)
      if (meta && meta.pendingId === confirmedPendingId) {
        pendingSends.delete(sid)
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

    cleanups.push(ws.on('tool_stdout', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      chatMessages.update(m => {
        const msgs = [...(m[sid] || [])]
        const lastGroup = msgs.findLastIndex((x: any) => x.type === 'tool_group')
        if (lastGroup >= 0) {
          const tools = [...msgs[lastGroup].tools]
          const lastTool = tools.length - 1
          if (lastTool >= 0) {
            tools[lastTool] = { ...tools[lastTool], stdout: [...(tools[lastTool].stdout ?? []), ...((ev as any).lines ?? [])] }
          }
          msgs[lastGroup] = { ...msgs[lastGroup], tools }
        }
        return { ...m, [sid]: msgs }
      })
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
    }))

    cleanups.push(ws.on('session_update', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      chatContextUsage.update(u => ({ ...u, [sid]: (ev as any).context_usage }))
      if (typeof (ev as any).context_tokens === 'number') {
        chatContextTokens.update(u => ({ ...u, [sid]: (ev as any).context_tokens }))
      }
      chatPermMode.update(mm => ({ ...mm, [sid]: (ev as any).permission_mode }))
      chatReasoningEffort.update(r => ({ ...r, [sid]: (ev as any).reasoning_effort }))
      if (typeof (ev as any).show_reasoning === 'boolean') {
        chatShowReasoning.update(r => ({ ...r, [sid]: (ev as any).show_reasoning }))
      }
      chatWorkingDir.update(w => ({ ...w, [sid]: (ev as any).working_dir }))
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
        confId: (ev as any).id,
        sessionId: (ev as any).session_id,
        message: (ev as any).message,
        kind: (ev as any).kind,
      })
    }))

    cleanups.push(ws.on('request_user_question', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      questionModal.set({
        questionId: (ev as any).question_id,
        sessionId: (ev as any).session_id,
        question: (ev as any).question,
        options: (ev as any).options,
        multiSelect: (ev as any).multi_select,
        header: (ev as any).header,
      })
    }))

    cleanups.push(ws.on('dismiss_user_question', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      questionModal.set(null)
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
      chatSuggestion.update(s => ({ ...s, [sid]: (ev as any).text ?? '' }))
    }))

    return () => {
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

    const onScroll = () => {
      stick = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80
    }
    scroller.addEventListener('scroll', onScroll, { passive: true })

    const ro = new ResizeObserver(() => {
      if (stick) scroller.scrollTop = scroller.scrollHeight
    })
    ro.observe(content)

    // Initial pin to bottom after history loads.
    scroller.scrollTop = scroller.scrollHeight

    return () => {
      scroller.removeEventListener('scroll', onScroll)
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
    setupCopyButtons(el)
  }

  // ── edit a prior user message: load it back into the composer for resend ─────
  let composer = $state<{ setText: (v: string) => void } | null>(null)
  function editMessage(content: string) {
    composer?.setText(content)
  }

  // ── export the visible transcript as a markdown file ─────────────────────────
  function exportTranscript() {
    const sid = get(activeSessionId)
    if (!sid) return
    const arr = (get(chatMessages)[sid] ?? []).filter((m: any) => m.type === 'user' || m.type === 'assistant')
    if (!arr.length) { showToast(tr('chat.nothing_to_export'), 'error'); return }
    const title = currentSession?.title ?? currentSession?.name ?? 'session'
    const lines: string[] = [`# ${title}`, '']
    for (const m of arr) {
      lines.push(m.type === 'user' ? '## You' : '## Octo')
      if (m.type === 'assistant' && m.thinking) {
        lines.push('<details><summary>Thoughts</summary>', '', m.thinking, '', '</details>', '')
      }
      lines.push(m.content ?? '', '')
    }
    const blob = new Blob([lines.join('\n')], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${title.replace(/[^\w.-]+/g, '_')}.md`
    a.click()
    URL.revokeObjectURL(url)
  }

  // ── send message ───────────────────────────────────────────────────────────
  function send(text: string, files?: any[]) {
    const sid = get(activeSessionId)
    if (!sid || (!text.trim() && !(files && files.length))) return
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
    // Optimistically show the user bubble, marked pending. The server echoes
    // it back as a history_user_message — that handler replaces this pending
    // bubble (matching by content) instead of appending a duplicate.
    const pendingId = 'pending-' + Date.now()
    pendingSends.set(sid, { pendingId, wasStreaming, text, files })
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
    ws.sendMessage(sid, text, files)
  }

  // ── force bind ─────────────────────────────────────────────────────────────
  // Retry the pending send with force=true, taking over a session bound to
  // another entry as long as no turn lease is active.
  function forceBindAndSend() {
    const sid = bindRequiredFor
    if (!sid) return
    const meta = pendingSends.get(sid)
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

  <!-- Force-bind banner: session is owned by another entry but can be taken over. -->
  {#if bindRequiredFor === id}
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
                <div class="agent-avatar">O</div>
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
                  {#if msg.thinking}
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
                      {@html renderMarkdown(msg.content)}
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

            {:else if msg.type === 'thinking'}
              <!-- Standalone Thoughts segment (reasoning before a tool round) -->
              <div class="msg-agent fadein">
                <div class="agent-avatar">O</div>
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
                <div class="agent-avatar">O</div>
                <div class="agent-content">
                  <ToolGroup tools={msg.tools} streaming={msg.streaming} />
                </div>
              </div>

            {:else if msg.type === 'progress'}
              <!-- Inline progress message -->
              <div class="msg-agent fadein">
                <div class="agent-avatar">O</div>
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
                  <div class="notice-line" data-level={msg.level}>{@html renderMarkdown(msg.content)}</div>
                </div>
              </div>
            {/if}
          {/each}

          <!-- Background workflows panel (persists across turns) -->
          {#if workflows.length > 0}
            <div class="msg-agent fadein">
              <div class="agent-avatar">O</div>
              <div class="agent-content">
                <WorkflowsCard runs={workflows} {now} />
              </div>
            </div>
          {/if}

          <!-- Live sub-agents panel (current turn) -->
          {#if subAgents.length > 0}
            <div class="msg-agent fadein">
              <div class="agent-avatar">O</div>
              <div class="agent-content">
                <SubAgentsCard agents={subAgents} elapsed={subAgentsElapsed} />
              </div>
            </div>
          {/if}

          <!-- Live thinking block while streaming -->
          {#if streaming && thinking}
            <div class="msg-agent fadein">
              <div class="agent-avatar">O</div>
              <div class="agent-content">
                <details class="think-block" open>
                  <summary class="think-summary">
                    <iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>
                    <span>{$t('chat.thinking')}</span>
                    <span class="think-meta mono">{fmtDur(thinkElapsed)}{#if thinkTokens > 0} · ↓ ~{fmtTokens(thinkTokens)} tokens{:else if ctxTokens > 0} · ↑ ~{fmtTokens(ctxTokens)} tokens{:else} · ↑{/if}</span>
                  </summary>
                  <div class="think-body" use:setupAssistantEl>{@html renderMarkdown(thinking)}</div>
                </details>
              </div>
            </div>
          {/if}

          <!-- Live thinking indicator while streaming -->
          {#if streaming && progress}
            <div class="msg-agent fadein">
              <div class="agent-avatar">O</div>
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
              <button class="suggestion-chip" onclick={() => send(suggestion)}>
                <iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>
                {suggestion}
              </button>
            </div>
          {/if}
        </div>
      </div>

      <!-- Background processes tray -->
      {#if bgTasks && bgTasks.length > 0}
        <BackgroundProcesses tasks={bgTasks} />
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
.conversation { flex: 1; display: flex; flex-direction: column; min-width: 0; min-height: 0; }
.messages { flex: 1; overflow-y: auto; min-height: 0; }
.messages-inner {
  max-width: 1080px; margin: 0 auto;
  padding: 24px 24px 16px; display: flex; flex-direction: column; gap: 20px;
}

/* ── User message ────────────────────────────────────────────────────────── */
.msg-user { display: flex; justify-content: flex-end; }
.user-bubble-wrap { display: flex; flex-direction: column; align-items: flex-end; gap: 4px; max-width: 80%; }
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
  background: var(--blue-6); color: #fff;
  display: flex; align-items: center; justify-content: center;
  font-size: 13px; font-weight: 600;
}
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

/* ── Fade-in ─────────────────────────────────────────────────────────────── */
.fadein { animation: octo-fadein 0.25s ease; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>
