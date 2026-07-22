// Mobile chat session wiring.
//
// The desktop ChatView owns a large, DOM- and UI-coupled $effect that wires the
// WebSocket stream into the chat stores. Mobile only needs a CORE SUBSET of that
// stream, and the real message-assembly logic already lives in stores.ts
// (appendToLastAssistant / addToolCallToGroup / updateToolResult / …), shared by
// both. So rather than extract ChatView's coupled wiring, this registers a thin
// mobile-only handler set that calls those same shared store functions.
//
// Deliberately omitted (desktop-only or a later batch): optimistic-send
// bookkeeping / steer queues / rollback, sub-agent & workflow panels, artifact
// auto-open, turn-error banner, next-message suggestions. Mobile chat is a
// simplified view; the detailed tool/progress rendering is batch 2.
import { get } from 'svelte/store'
import { ws } from '../lib/ws'
import * as api from '../lib/api'
import {
  chatMessages,
  chatStreaming,
  chatThinking,
  chatProgress,
  chatLastTextAt,
  chatGoal,
  addChatMsg,
  appendToLastAssistant,
  commitThinking,
  stopTrailingCaret,
  addToolCallToGroup,
  updateToolResult,
  setToolError,
  appendToolStdout,
  finishAllTools,
  finishToolsById,
  clearMsgs,
  showToast,
  uid,
} from '../lib/stores'

// Apply one persisted history event to the stores. A trimmed copy of the
// desktop's handleHistoryEvent (no artifact observation); the message-shaping is
// the shared store helpers, so the two stay behaviourally aligned.
function applyHistoryEvent(sid: string, ev: Record<string, any>, showReasoning: boolean) {
  if (ev.type === 'history_user_message') {
    addChatMsg(sid, {
      id: uid('u'), type: 'user', content: ev.content ?? '', createdAt: ev.created_at ?? Date.now(),
      streaming: false, pending: false, tools: [], todos: [], images: ev.images ?? [],
      messageIndex: ev.message_index,
    })
  } else if (ev.type === 'assistant_message') {
    if (!(ev.content ?? '').trim() && !(ev.thinking ?? '').trim()) return
    addChatMsg(sid, {
      id: uid('a'), type: 'assistant', content: ev.content ?? '', thinking: ev.thinking ?? '',
      createdAt: Date.now(), streaming: false, tools: [], todos: [],
    })
  } else if (ev.type === 'thinking') {
    if (showReasoning) commitThinking(sid, ev.text ?? '')
  } else if (ev.type === 'tool_call') {
    addToolCallToGroup(sid, {
      id: uid('t'), toolId: ev.tool_id ?? '', name: ev.name ?? '', args: ev.args ?? '',
      summary: ev.summary ?? '', done: false, error: null, result: null, stdout: [], diff: null,
    })
  } else if (ev.type === 'tool_result') {
    updateToolResult(sid, ev.tool_id, ev.result, ev.ui_payload)
  }
}

// Fetch and render a session's persisted transcript. Resolves once settled, so
// the caller can subscribe over WS only after history renders (same ordering the
// desktop relies on to avoid replayed tool cards landing before their message).
export function loadMobileHistory(sid: string): Promise<void> {
  api.getSessionGoal(sid)
    .then(resp => chatGoal.update(m => ({ ...m, [sid]: resp?.goal ?? null })))
    .catch(() => {})
  return api.getSessionMessages(sid).then((resp: any) => {
    const events: any[] = resp?.events ?? []
    const showReasoning = resp?.show_reasoning ?? true
    const historyToolIds = new Set<string>()
    for (const ev of events) {
      if (ev.type === 'tool_call' && ev.tool_id) historyToolIds.add(ev.tool_id)
      applyHistoryEvent(sid, ev, showReasoning)
    }
    finishToolsById(sid, historyToolIds)
  }).catch(() => {})
}

// Optimistically echo the user's message, mark the turn streaming, and send.
// history_user_message reconciles the echo (see the handler below).
export function sendMobile(sid: string, text: string, files?: unknown[]) {
  const t = text.trim()
  if (!t) return
  addChatMsg(sid, {
    id: uid('u'), type: 'user', content: t, createdAt: Date.now(),
    streaming: false, pending: true, tools: [], todos: [], images: [],
  })
  chatStreaming.update(s => ({ ...s, [sid]: true }))
  ws.sendMessage(sid, t, files)
}

// Register the core WS handlers for a session. Returns a cleanup that removes
// them all. Pair with ws.subscribe/unsubscribe in the caller's effect.
export function wireMobileSession(sid: string): () => void {
  const forSid = (ev: any) => !ev.session_id || ev.session_id === sid
  const cleanups: Array<() => void> = []

  cleanups.push(ws.on('text_delta', (ev: any) => {
    if (!forSid(ev)) return
    const txt = ev.text ?? ''
    const pendingThinking = get(chatThinking)[sid] ?? ''
    appendToLastAssistant(sid, txt, pendingThinking)
    if (pendingThinking) chatThinking.update(tt => ({ ...tt, [sid]: '' }))
    if (txt) chatLastTextAt.update(tt => ({ ...tt, [sid]: Date.now() }))
  }))

  cleanups.push(ws.on('thinking_delta', (ev: any) => {
    if (!forSid(ev)) return
    if (!(get(chatThinking)[sid] ?? '')) stopTrailingCaret(sid)
    chatThinking.update(tt => ({ ...tt, [sid]: (tt[sid] ?? '') + (ev.text ?? '') }))
  }))

  cleanups.push(ws.on('assistant_message', (ev: any) => {
    if (!forSid(ev)) return
    const think = ev.thinking ?? ''
    chatThinking.update(tt => ({ ...tt, [sid]: '' }))
    const cur = get(chatMessages)[sid] ?? []
    const streaming = get(chatStreaming)[sid] ?? false
    if (streaming && cur.length > 0 && cur[cur.length - 1]?.type === 'assistant') {
      chatMessages.update(m => {
        const arr = [...(m[sid] || [])]
        const last = arr.length - 1
        if (last >= 0) arr[last] = { ...arr[last], content: ev.content ?? arr[last].content, thinking: think || arr[last].thinking, streaming: false }
        return { ...m, [sid]: arr }
      })
    } else {
      addChatMsg(sid, { id: uid('a'), type: 'assistant', content: ev.content ?? '', thinking: think, createdAt: Date.now(), streaming: false, tools: [], todos: [] })
    }
  }))

  cleanups.push(ws.on('history_user_message', (ev: any) => {
    if (!forSid(ev)) return
    const content = ev.content ?? ''
    const createdAt = ev.created_at ?? Date.now()
    const images = ev.images ?? []
    chatMessages.update(m => {
      const msgs = [...(m[sid] || [])]
      // Reconcile the optimistic echo: replace the trailing pending user bubble
      // of the same text, else append. (Mobile has no steer path.)
      const lastPending = msgs.findLastIndex((x: any) => x.type === 'user' && x.pending)
      if (lastPending >= 0 && msgs[lastPending].content === content) {
        msgs[lastPending] = { ...msgs[lastPending], id: uid('u'), createdAt, pending: false, images, messageIndex: ev.message_index }
      } else {
        msgs.push({ id: uid('u'), type: 'user', content, createdAt, streaming: false, pending: false, tools: [], todos: [], images, messageIndex: ev.message_index })
      }
      return { ...m, [sid]: msgs }
    })
  }))

  cleanups.push(ws.on('tool_call', (ev: any) => {
    if (!forSid(ev)) return
    commitThinking(sid, get(chatThinking)[sid] ?? '')
    chatThinking.update(tt => ({ ...tt, [sid]: '' }))
    addToolCallToGroup(sid, {
      id: uid('t'), toolId: ev.tool_id ?? '', name: ev.name ?? '', args: ev.args ?? '',
      summary: ev.summary ?? '', startedAt: Date.now(), done: false, error: null, result: null, stdout: [], diff: null,
    })
  }))

  cleanups.push(ws.on('tool_result', (ev: any) => {
    if (!forSid(ev)) return
    updateToolResult(sid, ev.tool_id, ev.result, ev.ui_payload)
  }))

  cleanups.push(ws.on('tool_error', (ev: any) => {
    if (!forSid(ev)) return
    setToolError(sid, ev.tool_id, ev.error ?? 'error')
  }))

  cleanups.push(ws.on('tool_stdout', (ev: any) => {
    if (!forSid(ev)) return
    appendToolStdout(sid, ev.tool_id, ev.lines ?? [])
  }))

  cleanups.push(ws.on('progress', (ev: any) => {
    if (!forSid(ev)) return
    chatProgress.update(p => ({ ...p, [sid]: { message: ev.message || 'Thinking', phase: ev.phase } }))
    if (ev.phase === 'active') chatStreaming.update(s => ({ ...s, [sid]: true }))
  }))

  cleanups.push(ws.on('complete', (ev: any) => {
    if (!forSid(ev)) return
    chatStreaming.update(s => ({ ...s, [sid]: false }))
    chatProgress.update(p => ({ ...p, [sid]: null }))
    chatMessages.update(m => {
      const msgs = (m[sid] || []).map((x: any) => (x.streaming ? { ...x, streaming: false } : x))
      return { ...m, [sid]: msgs }
    })
    finishAllTools(sid)
  }))

  cleanups.push(ws.on('turn_error', (ev: any) => {
    if (!forSid(ev)) return
    addChatMsg(sid, {
      id: uid('err'), type: 'notice', content: `错误: ${ev.error ?? 'request failed'}`,
      level: 'error', createdAt: Date.now(), streaming: false, tools: [], todos: [],
    })
    chatStreaming.update(s => ({ ...s, [sid]: false }))
  }))

  cleanups.push(ws.on('toast', (ev: any) => {
    if (ev.session_id !== sid) return
    showToast(ev.message ?? '', ev.level ?? 'info')
  }))

  // Operation errors surfaced over WS (retry-while-running, session-not-found…).
  cleanups.push(ws.on('error', (ev: any) => {
    if (!forSid(ev)) return
    showToast(ev.message ?? 'Error', 'error')
  }))

  // The server rejected the send (session bound to another client, not found…).
  // Without this the optimistic bubble and the streaming flag would hang forever
  // — the server sends no idle snapshot after a rejection. Roll the echo back,
  // clear streaming, and surface why. (No steer/force bookkeeping — mobile has
  // no optimistic-steer path; force-takeover UI is a later batch.)
  const rollbackSend = (ev: any) => {
    chatMessages.update(m => {
      const msgs = [...(m[sid] || [])]
      const lastPending = msgs.findLastIndex((x: any) => x.type === 'user' && x.pending)
      if (lastPending >= 0) msgs.splice(lastPending, 1)
      return { ...m, [sid]: msgs }
    })
    chatStreaming.update(s => ({ ...s, [sid]: false }))
    showToast(ev.message ?? '发送失败', 'error')
  }
  cleanups.push(ws.on('send_rejected', (ev: any) => {
    if (!forSid(ev)) return
    rollbackSend(ev)
  }))
  cleanups.push(ws.on('bind_required', (ev: any) => {
    if (!forSid(ev)) return
    // Mobile has no force-takeover UI yet, so treat it as a rejection rather
    // than leaving the turn stuck: roll back and tell the user it's in use.
    rollbackSend({ message: ev.message ?? '会话正被其他端占用' })
  }))

  cleanups.push(ws.on('session_update', (ev: any) => {
    if (!forSid(ev)) return
    // Mobile only needs the idle snapshot to clear a stale streaming indicator.
    if (ev.status === 'idle') {
      chatStreaming.update(s => ({ ...s, [sid]: false }))
      chatProgress.update(p => ({ ...p, [sid]: null }))
      chatThinking.update(t => ({ ...t, [sid]: '' }))
    }
  }))

  cleanups.push(ws.on('history_reload', (ev: any) => {
    if (!forSid(ev)) return
    clearMsgs(sid)
    loadMobileHistory(sid)
  }))

  return () => cleanups.forEach(fn => fn())
}
