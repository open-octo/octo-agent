<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { get } from 'svelte/store'
  import { fade } from 'svelte/transition'
  import {
    activeSessionId,
    activeAgent,
    pendingModel,
    pendingAgent,
    pendingWorkingDir,
    pendingGroupId,
    pendingReasoningEffort,
    pendingPermissionMode,
    pendingShowReasoning,
    globalReasoningEffort,
    resolveProjectForDir,
    prependSession,
    sessions,
    sessionGroups,
    chatMessages,
    chatStreaming,
    chatLastTextAt,
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
    confirmModals,
    questionModals,
    feedbackModal,
    pendingPrompt,
    artifacts,
    addChatMsg,
    clearMsgs,
    appendToLastAssistant,
    addToolCallToGroup,
    commitThinking,
    stopTrailingCaret,
    updateToolResult,
    setToolError,
    appendToolStdout,
    finishAllTools,
    finishToolsById,
    resetSubAgents,
    clearDoneSubAgents,
    removeSubAgent,
    applySubAgentEvent,
    recordAgentTrailEvent,
    recordWorkflowTrailEvent,
    hydrateAgentRuns,
    markSubAgentFinished,
    showToast,
    uid,
    agenticSessions,
    chatGoal,
    nativeShell,
    chatHeaderSnippet,
  } from '../lib/stores'
  import { ws, wsState, wsReconnect } from '../lib/ws'
  import * as api from '../lib/api'
  import { observeArtifact, resetArtifacts } from '../lib/artifacts'
  import { renderMarkdown, escapeHtml, setupCopyButtons } from '../lib/markdown'
  import { t, tr, pickLocalized } from '../lib/i18n'
  import { insertPendingSend, takeConfirmedSend } from '../lib/pendingSendOrder'
  import { inlineSlashCommand } from '../lib/inlineSlash'
  import { exportModeStore, selectedMessagesStore } from '../lib/exportStore'
  import { filenameStem } from '../lib/filename'
  import { anchorBgTasks } from '../lib/bgTaskAnchor'
  import DOMPurify from 'dompurify'
  import ToolGroup from '../components/chat/ToolGroup.svelte'
  import SubAgentsCard from '../components/chat/SubAgentsCard.svelte'
  import WorkflowsCard from '../components/chat/WorkflowsCard.svelte'
  import BackgroundProcesses from '../components/chat/BackgroundProcesses.svelte'
  import Composer from '../components/chat/Composer.svelte'
  import OctoLogo from '../components/layout/OctoLogo.svelte'
import QuestionModal from '../components/overlays/QuestionModal.svelte'
  import GenuiBlock from '../components/genui/GenuiBlock.svelte'
  import { splitOctoUiFences, type Segment as GenuiSegment } from '../lib/genui/fence-split'
  import type { GenuiActionEvent } from '../lib/genui/context'
  import { projectPanels, isAnchor, type PanelProjection } from '../lib/genui/projection'
  import {
    silentActionPanel,
    isSilentPairAt,
    precedingSaid,
    couldBeSilentReply,
    parseActionEnvelope,
  } from '../lib/genui/silent-turn'

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

  // Message-bubble identity: a session bound to a non-default agent_profile
  // (e.g. a summoned expert) should show that agent's own name/icon instead
  // of the generic Octo mark. Mirrors the same lookup Composer's "@agent"
  // chip and Sidebar's per-session label already do (api.listAgents() +
  // find-by-id) — no shared store for this exists yet, so it's fetched here
  // too, refreshed on the same 'agents_changed' WS event Sidebar listens for.
  let agents = $state<api.Agent[]>([])
  let boundAgent = $derived.by(() => {
    const id = (currentSession as any)?.agent_profile
    if (!id || id === 'default') return null
    return agents.find(a => a.id === id) ?? null
  })
  let boundAgentName = $derived(boundAgent ? pickLocalized(boundAgent.name, boundAgent.name_en) : '')

  onMount(async () => {
    try { agents = await api.listAgents() } catch { /* agents list is optional */ }
  })
  const unsubAgentsChanged = ws.on('agents_changed', async () => {
    try { agents = await api.listAgents() } catch { /* ignore */ }
  })
  onDestroy(() => { unsubAgentsChanged() })

  // Register this view's title/status/actions with the page-spanning title
  // bar (Header.svelte) — it renders whatever's here without knowing what it
  // is. The snippet closes over this component's own reactive state, so a
  // single registration on mount stays live; only the reference itself needs
  // clearing so a different view's Header doesn't keep rendering a stale one.
  onMount(() => {
    chatHeaderSnippet.set(chatHeader)
    return () => chatHeaderSnippet.set(null)
  })

  function agentAvatarColor(name: string): string {
    const colors = ['#1677ff', '#722ed1', '#13c2c2', '#52c41a', '#eb2f96', '#fa8c16', '#2f54eb', '#a0d911']
    let hash = 0
    for (let i = 0; i < name.length; i++) hash = name.charCodeAt(i) + ((hash << 5) - hash)
    return colors[Math.abs(hash) % colors.length]
  }
  let artifactCount  = $derived($artifacts.length)
  let wsDisconnected = $derived($wsState === 'disconnected')
  let showReasoning  = $derived($chatShowReasoning[$activeSessionId ?? ''] ?? currentSession?.show_reasoning ?? true)

  // Session-level plan panel: collapsed by default so it never occludes the
  // message stream; the user can expand it into a floating dropdown.
  let planExpanded = $state(false)

  // Turn-level LLM error, shown as a persistent red banner above the composer.
  // Set on turn_error WS event, cleared when the user sends a new message or
  // dismisses it manually.
  let turnError = $state<string | null>(null)
  // Vision helper progress: the roundtrip that turns an image into text
  // happens before the model replies, so it needs its own status line.
  let describingImage = $state<{ name: string; index: number; total: number } | null>(null)

  // Tracks optimistic UI state for in-flight sends. A FIFO queue per session
  // supports multiple messages (e.g. consecutive steer messages mid-turn); if
  // the server rejects one, we roll back only that pending bubble and restore
  // the streaming flag to its pre-send value. Content and files are kept so a
  // force takeover can retry the same message.
  const pendingSends = new Map<string, { pendingId: string; wasStreaming: boolean; text: string; files?: any[]; queued?: boolean }[]>()

  // The message that started the running turn, kept for the whole turn so a
  // turn_error can put it back in the composer. pendingSends can't serve that:
  // the server confirms the user bubble (history_user_message) BEFORE it calls
  // the LLM, so the pending entry is already shifted out of the queue by the
  // time a provider error lands. Written on every fresh (non-steer) send and
  // dropped when the turn ends — a turn started elsewhere (another tab, cron,
  // a wakeup) must never restore this tab's stale text.
  const turnInput = new Map<string, { text: string; files?: any[] }>()

  // Pending auto-dismiss timers for background sub-agents that finished with no
  // active turn (keyed by `${sid}\x00${agentId}`). A turn-less `done` has no
  // `complete` event to clear it, so we show it briefly then drop it. Cleared
  // per-session on the effect teardown.
  const subAgentDismissTimers = new Map<string, ReturnType<typeof setTimeout>>()
  const SUB_AGENT_DISMISS_MS = 2000

  function scheduleSubAgentDismiss(sid: string, agentId: string) {
    const key = `${sid}\x00${agentId}`
    const existing = subAgentDismissTimers.get(key)
    if (existing) clearTimeout(existing)
    subAgentDismissTimers.set(key, setTimeout(() => {
      subAgentDismissTimers.delete(key)
      // Guard against the agent having restarted (a new `started` would flip it
      // back to running): only drop it if it's still present and finished.
      const cur = (get(chatSubAgents)[sid] || []).find(a => a.id === agentId)
      if (cur && cur.status !== 'running') removeSubAgent(sid, agentId)
    }, SUB_AGENT_DISMISS_MS))
  }

  // Pending steer messages typed while a turn is running. They are shown above
  // the composer as ghost user bubbles until the server drains the inbox and
  // confirms them in the scrollback.
  let pendingSteers = $state<Record<string, { pendingId: string; text: string; files?: any[]; retracting?: boolean; queued?: boolean }[]>>({})

  // Set when the server reports a recoverable binding conflict. The UI shows a
  // banner with a "Force bind" button; clicking it retries the pending send
  // with force=true, matching the IM /bind --force semantics.
  let bindRequiredFor = $state<string | null>(null)
  let bindRequiredMessage = $state('')

  // ── branch: fork the transcript at a reply into its own session ─────────────
  let branchBusy = $state(false)

  let lightboxSrc = $state<string | null>(null)

  // ── export mode ─────────────────────────────────────────────────────────────
  let inExportMode = $derived($exportModeStore[$activeSessionId ?? ''] ?? false)
  let selectedIds   = $derived($selectedMessagesStore[$activeSessionId ?? ''] ?? new Set<string>())
  let exportBusy    = $state(false)
  let exportIncludeTools = $state(false)

  let pendingSteerList = $state<{ pendingId: string; text: string; files?: any[]; retracting?: boolean; queued?: boolean }[]>([])

  // Sync pendingSteerList with the current session's pending steers. Uses $effect
  // instead of $derived to ensure correct reactivity when indexing a $state Record
  // with a $store value (keyed lookup).
  $effect(() => {
    pendingSteerList = pendingSteers[$activeSessionId ?? ''] ?? []
  })

  // How long after the last text_delta the reply caret keeps blinking.
  const CARET_IDLE_MS = 1200

  // Sub-agents card elapsed time + reconnect countdown both tick off `now`.
  let now = $state(Date.now())
  $effect(() => {
    const h = setInterval(() => { now = Date.now() }, 1000)
    return () => clearInterval(h)
  })
  let subAgentsStart = $derived(subAgents.length ? Math.min(...subAgents.map(a => a.startedAt)) : 0)
  let subAgentsElapsed = $derived(subAgentsStart ? (now - subAgentsStart) / 1000 : 0)
  let reconnectIn = $derived($wsReconnect ? Math.max(0, Math.ceil(($wsReconnect.nextAt - now) / 1000)) : 0)

  // All sub-agents finished ("all done"): fade the panel out after a brief beat.
  // This covers the mid-turn window where sub-agents have all completed but the
  // main turn is still streaming a reply — `complete`'s clear hasn't fired yet and
  // the per-agent dismiss only runs when the turn is already idle, so without
  // this the panel lingers indefinitely. Starts a 2s timer; cancels if a new
  // sub-agent starts before it fires (effect re-runs).
  $effect(() => {
    const sid = $activeSessionId ?? ''
    if (!sid) return
    if (subAgents.length === 0) return
    if (subAgents.some(a => a.status === 'running')) return
    const timer = setTimeout(() => clearDoneSubAgents(sid), SUB_AGENT_DISMISS_MS)
    return () => clearTimeout(timer)
  })

  // Fade the task panel once the whole plan is done: 3s after every task reads
  // completed, drop it. Cancels if a task reverts to incomplete or a new one is
  // added (the effect re-runs on any todos change). Mirrors the sub-agent
  // dismiss above; the server skips replaying a fully-completed plan so a
  // refresh after the fade doesn't bring it back.
  const TODO_DISMISS_MS = 3000
  $effect(() => {
    const sid = $activeSessionId ?? ''
    if (!sid) return
    if (todos.length === 0) return
    if (todos.some(t => t.status !== 'completed')) return
    const timer = setTimeout(() => chatTodos.update(t => ({ ...t, [sid]: [] })), TODO_DISMISS_MS)
    return () => clearTimeout(timer)
  })

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
  // The reply caret is a typewriter cursor: show it only while text is actively
  // arriving. Once deltas stop (the model went silent to generate tool calls /
  // reasoning), it fades within CARET_IDLE_MS even though the bubble stays
  // `streaming` until the next segment boundary. `now` ticks every 1s, so the
  // real hide latency is CARET_IDLE_MS..CARET_IDLE_MS+1s.
  let lastTextAt = $derived($chatLastTextAt[$activeSessionId ?? ''] ?? 0)
  let typingActive = $derived(streaming && lastTextAt > 0 && (now - lastTextAt) < CARET_IDLE_MS)
  // Output-token estimate (~chars/4), derived from persisted stores — the live
  // assistant text plus the reasoning buffer — so it survives view remounts
  // alongside the elapsed clock instead of resetting to 0.
  let turnOutChars = $derived(
    ((msgs.find((m: any) => m.streaming && m.type === 'assistant')?.content?.length) ?? 0) + thinking.length
  )
  let thinkTokens = $derived(Math.floor(turnOutChars / 4))

  const THINKING_KEYS = ['chat.thinking_0', 'chat.thinking_1', 'chat.thinking_2', 'chat.thinking_3']
  let thinkingLabel = $derived(
    (progress && progress.message && progress.message !== 'Thinking')
      ? progress.message
      : $t(THINKING_KEYS[Math.floor(thinkElapsed / 3) % THINKING_KEYS.length])
  )
  // Uplink size: the context being sent up (last known occupancy in tokens).
  let ctxTokens = $derived(Number($chatContextTokens[$activeSessionId ?? ''] ?? 0))
  function fmtDur(s: number): string {
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${s % 60}s`
  }
  function fmtTokens(n: number): string {
    return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`
  }
  // HH:MM for the message meta row; only optimistic sends carry createdAt, so
  // the row simply omits the time for replayed history.
  function fmtTime(ts: number): string {
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  // ── history handler ────────────────────────────────────────────────────────
  // sid is the session the fetch was started for — NOT re-read from
  // activeSessionId, which may have moved on while the fetch was in flight
  // (issue #2090); the mobile port (chatWiring.ts) threads it the same way.
  function handleHistoryEvent(sid: string, ev: Record<string, any>, historyShowReasoning: boolean) {
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
        // Position in the backend's persisted Messages array. Tool_result-only
        // bookkeeping messages are skipped during replay, so this can differ
        // from the rendered index — the branch feature relies on it.
        messageIndex: ev.message_index,
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
        // Present only on a reply that closes a turn — an intermediate
        // (tool-round) bubble is not a valid branch point, so the Branch
        // action keys off this being set.
        messageIndex: ev.message_index,
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
  //
  // isStale is checked after the fetch resolves: the user may have switched
  // sessions while it was in flight, and a stale response must not write into
  // the stores at all — even keyed by its own sid, a late append would
  // duplicate the transcript when the user switches straight back and the
  // fresh effect's own loadHistory appends the same events again (#2090).
  function loadHistory(sid: string, isStale: () => boolean): Promise<void> {
    // Seed the goal chip for this session; failures (older server, goals
    // disabled) just leave the chip hidden.
    api.getSessionGoal(sid)
      .then(resp => chatGoal.update(m => ({ ...m, [sid]: resp?.goal ?? null })))
      .catch(() => {})
    // Seed the persisted sub-agent / workflow trails so the transcript's tool
    // cards can render finished runs after a reload; failures (older server)
    // just leave the cards on their plain text result.
    api.getAgentRuns(sid)
      .then((resp: any) => { if (!isStale()) hydrateAgentRuns(sid, resp) })
      .catch(() => {})
    return api.getSessionMessages(sid).then((resp: any) => {
      if (isStale()) return
      const events: any[] = resp?.events ?? []
      // Server-resolved, so it's correct even before $sessions has loaded —
      // see the comment on the 'thinking' branch in handleHistoryEvent.
      const historyShowReasoning = resp?.show_reasoning ?? true
      // Collect the tool_ids that came from history so we only close those,
      // leaving any concurrently-replayed live-turn tools untouched.
      const historyToolIds = new Set<string>()
      for (const ev of events) {
        if (ev.type === 'tool_call' && ev.tool_id) historyToolIds.add(ev.tool_id)
        handleHistoryEvent(sid, ev, historyShowReasoning)
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
  // keepRunningSubAgents preserves in-flight background sub-agents (which
  // outlive turns) while still clearing turn-scoped state. The server's idle
  // snapshot replays running sub-agents just before it, so a full resetSubAgents
  // there would wipe the very entries that were just restored (flicker).
  function resetSessionRuntimeState(sid: string, keepRunningSubAgents = false) {
    chatStreaming.update(s => ({ ...s, [sid]: false }))
    chatProgress.update(p => ({ ...p, [sid]: null }))
    chatThinking.update(t => ({ ...t, [sid]: '' }))
    chatTurnStart.update(m => { const n = { ...m }; delete n[sid]; return n })
    if (keepRunningSubAgents) clearDoneSubAgents(sid)
    else resetSubAgents(sid)
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
      turnError = null
      // The landing is not a session, so it inherits none of one's artifacts.
      // Leaving them would keep the panel open over a blank chat, still
      // showing whichever session the user just left — the same stale-panel
      // problem switching between two sessions already resets away. The empty
      // marker matches no in-flight fetch, so nothing lands here afterwards.
      resetArtifacts('')
      return
    }

    clearMsgs(sid)
    resetArtifacts(sid)
    resetSessionRuntimeState(sid)
    // Stale turn-error banner from a previous session must not carry over.
    turnError = null
    describingImage = null
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
      loadHistory(sid, () => cancelled).then(() => {
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
        send(pend.content, pend.files)
      }
    }))

    // History rewritten out of band (/clear, /compact): drop the rendered
    // transcript and re-fetch the persisted one.
    cleanups.push(ws.on('goal_updated', (ev) => {
      const gsid = (ev as any).session_id
      if (!gsid) return
      chatGoal.update(m => ({ ...m, [gsid]: (ev as any).goal ?? null }))
    }))

    // The goal's scrollback lines, mirroring the TUI's "● Goal …" notices: the
    // /goal reply (kind "command"), a status change (kind "status"), and the
    // start of a continuation turn (kind "start"/"continue"). The continuation
    // prompt itself is hidden server-side — StripSystemReminders drops the
    // <goal_context> span, so no user bubble is broadcast — which is exactly
    // why the turn needs this line: without it the output looks unprompted.
    cleanups.push(ws.on('goal_notice', (ev) => {
      if ((ev as any).session_id !== sid) return
      const kind = (ev as any).kind ?? ''
      // Only start/continue are textless by design; an unknown textless kind is
      // a newer server talking to this build, and mislabelling it "continues"
      // would be worse than dropping it.
      const fixed: Record<string, string> = {
        start: 'Goal starts — /goal pause to stop',
        continue: 'Goal continues — /goal pause to stop',
      }
      const text = (ev as any).text || fixed[kind]
      if (!text) return
      addChatMsg(sid, {
        id: uid('note'),
        type: 'notice',
        // marked collapses single newlines, and the bare `/goal` summary is
        // genuinely multi-line (status, usage, command hints) — promote its
        // line breaks to markdown hard breaks so it reads as it does in the TUI.
        content: text.replace(/\n/g, '  \n'),
        level: (ev as any).level ?? 'info',
        createdAt: Date.now(),
        streaming: false,
        tools: [],
        todos: [],
      })
    }))

    cleanups.push(ws.on('history_reload', (ev) => {
      if ((ev as any).session_id !== sid) return
      clearMsgs(sid)
      resetArtifacts(sid)
      loadHistory(sid, () => cancelled)
    }))

    // The transcript tail was stripped server-side (retry / rollback): re-render
    // from the persisted history before any new events stream in. Same effect as
    // history_reload — different trigger.
    cleanups.push(ws.on('history_rollback', (ev) => {
      if ((ev as any).session_id !== sid) return
      clearMsgs(sid)
      resetArtifacts(sid)
      loadHistory(sid, () => cancelled)
    }))

    // Transient server-side notice (command result, error).
    cleanups.push(ws.on('toast', (ev) => {
      if ((ev as any).session_id !== sid) return
      showToast((ev as any).message ?? '', (ev as any).level ?? 'info')
    }))

    // Another entry stole this session's binding (force bind from Web, TUI
    // --take-over, …). Informational only: the turn is not blocked and the
    // notice never lands in the persisted history.
    cleanups.push(ws.on('session_taken_over', (ev) => {
      if ((ev as any).session_id !== sid) return
      showToast(tr('chat.session_taken_over').replace('{entry}', (ev as any).entry ?? ''), 'info')
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
          pendingSteers = { ...pendingSteers, [sid]: pendingSteers[sid]?.filter(s => s.pendingId !== meta.pendingId) ?? [] }
        } else {
          chatMessages.update(m => ({
            ...m,
            [sid]: (m[sid] || []).filter((msg: any) => msg.id !== meta.pendingId),
          }))
        }
        chatStreaming.update(s => ({ ...s, [sid]: meta.wasStreaming }))
      }
      bindRequiredFor = null
      // No turn started, so no turn_error/complete will consume the restore
      // slot — drop it here or it would outlive this send and be handed to a
      // later turn's failure.
      turnInput.delete(sid)
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

    // A pending steer was successfully pulled back out of the running turn's
    // inbox: drop its ghost bubble, forget its rollback entry (it will never be
    // confirmed now), and reload its text + attachments into the composer.
    cleanups.push(ws.on('steer_retracted', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const pendingId = (ev as any).pending_id
      const s = pendingSteers[sid]?.find(x => x.pendingId === pendingId)
      pendingSteers = { ...pendingSteers, [sid]: pendingSteers[sid]?.filter(x => x.pendingId !== pendingId) ?? [] }
      const queue = pendingSends.get(sid)
      if (queue) {
        const next = queue.filter(m => m.pendingId !== pendingId)
        if (next.length) pendingSends.set(sid, next)
        else pendingSends.delete(sid)
      }
      if (s) composer?.restore(s.text, s.files)
    }))

    // The turn already consumed the steer before the retract landed, so it's
    // committed and on its way. Un-mark the bubble and let the user know instead
    // of stranding the text.
    cleanups.push(ws.on('steer_retract_failed', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const pendingId = (ev as any).pending_id
      pendingSteers = { ...pendingSteers, [sid]: pendingSteers[sid]?.map(x => x.pendingId === pendingId ? { ...x, retracting: false } : x) ?? [] }
      showToast(tr('chat.steer_retract_failed'), 'info')
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

    // An in-session loop wakeup fired and started a fresh turn. Render a "Loop
    // tick" scrollback notice, mirroring the TUI line — the loop prompt itself
    // is suppressed server-side (wrapped as a <system-reminder>), so it no
    // longer duplicates as a user-message bubble on every tick.
    cleanups.push(ws.on('loop_tick_notice', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      addChatMsg(sid, {
        id: uid('note'),
        type: 'notice',
        content: 'Loop tick',
        level: 'info',
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
      // Stamp the arrival so the reply caret only blinks while text is flowing
      // (see chatLastTextAt). Empty deltas carry no visible text, so ignore them.
      if (txt) chatLastTextAt.update(tt => ({ ...tt, [sid]: Date.now() }))
    }))

    cleanups.push(ws.on('thinking_delta', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      // The server only emits thinking_delta when show_reasoning is on for the
      // session's sender (it's off by default and never surfaced to the
      // terminal), so any delta that reaches the Web UI is meant to be shown.
      const txt = (ev as any).text ?? ''
      // An empty buffer means this delta opens a new reasoning segment (the
      // previous turn's reply is done), so stop its caret the same way
      // commitThinking/addToolCallToGroup do — otherwise it keeps blinking
      // behind the new live-thinking block.
      if (!(get(chatThinking)[sid] ?? '')) stopTrailingCaret(sid)
      chatThinking.update(tt => ({ ...tt, [sid]: (tt[sid] ?? '') + txt }))
    }))

    cleanups.push(ws.on('sub_agent_event', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const kind = (ev as any).kind ?? ''
      const agentId = (ev as any).agent_id ?? ''
      applySubAgentEvent(sid, ev as any)
      recordAgentTrailEvent(sid, ev as any)
      // A background sub-agent that finishes while no turn is streaming has no
      // `complete` to clean it up — it would linger until an unrelated event
      // (next send / reconnect). Show its done state briefly, then auto-dismiss.
      // During an active turn we leave it: `complete` clears the whole trail.
      if (kind === 'done' && agentId && !(get(chatStreaming)[sid] ?? false)) {
        scheduleSubAgentDismiss(sid, agentId)
      }
    }))

    cleanups.push(ws.on('sub_agent_notice', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const status = (ev as any).status ?? ''
      const description = (ev as any).description ?? ''
      const agentId = (ev as any).agent_id ?? ''
      const label = description || agentId || 'sub-agent'
      let level: 'success' | 'warning' | 'error' = 'error'
      let text = `Sub-agent \`${label}\` failed`
      let finishedStatus: 'done' | 'error' | 'cancelled' = 'error'
      if (status === 'success') {
        level = 'success'
        text = `Sub-agent \`${label}\` completed`
        finishedStatus = 'done'
      } else if (status === 'warning') {
        level = 'warning'
        text = `Sub-agent \`${label}\` incomplete`
        finishedStatus = 'done'
      } else if (status === 'cancelled') {
        level = 'warning'
        text = `Sub-agent \`${label}\` cancelled`
        finishedStatus = 'cancelled'
      }
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
      // The notice is the definitive completion signal; use it to force the live
      // panel to a finished status and then auto-dismiss, in case the final
      // `sub_agent_event` was delayed, lost, or arrived while the turn was still
      // streaming.
      if (agentId) {
        markSubAgentFinished(sid, agentId, finishedStatus)
        if (!(get(chatStreaming)[sid] ?? false)) {
          scheduleSubAgentDismiss(sid, agentId)
        }
      }
    }))

    cleanups.push(ws.on('workflow_event', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const kind = (ev as any).kind ?? ''
      const runId = (ev as any).run_id ?? ''
      const description = (ev as any).description ?? ''
      const status = (ev as any).status ?? ''
      applyWorkflowEvent(sid, ev as any)
      recordWorkflowTrailEvent(sid, ev as any)
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
      // Entries ahead of the confirmed one never got a confirmation of their
      // own. Retiring them here — instead of letting the queue stay short —
      // keeps one lost confirmation from misreading every send after it.
      const { meta, dropped } = takeConfirmedSend(queue ?? [], content)
      if (queue && queue.length === 0) pendingSends.delete(sid)
      if (dropped.length) {
        const stale = new Set(dropped.map(d => d.pendingId))
        pendingSteers = { ...pendingSteers, [sid]: pendingSteers[sid]?.filter(s => !stale.has(s.pendingId)) ?? [] }
      }
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
          const confirmedMsg = { id: uid('u'), type: 'user', content, createdAt, streaming: false, pending: false, tools: [], todos: [], images, messageIndex: (ev as any).message_index }
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
            msgs[lastPending] = { ...msgs[lastPending], id: uid('u'), createdAt, pending: false, images, messageIndex: (ev as any).message_index }
          } else {
            msgs.push({ id: uid('u'), type: 'user', content, createdAt, streaming: false, pending: false, tools: [], todos: [], images, messageIndex: (ev as any).message_index })
          }
        }
        return { ...m, [sid]: msgs }
      })
      if (meta?.queued) {
        // This queued message is now the input of a turn of its own, so it takes
        // over the restore slot the fresh-send path fills in send().
        turnInput.set(sid, { text: meta.text, files: meta.files })
        // A queued message starts its own turn, and the previous turn's
        // `complete` already flipped streaming off. The `progress phase=active`
        // that doAgentTurn broadcasts right after this event flips it back — this
        // just closes the frame in between, so the composer doesn't blink to idle.
        chatStreaming.update(s => ({ ...s, [sid]: true }))
      }
      if (isSteer && meta) {
        // The server drained this steer from the inbox; drop the ghost bubble.
        pendingSteers = { ...pendingSteers, [sid]: pendingSteers[sid]?.filter(s => s.pendingId !== meta.pendingId) ?? [] }
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

    // A text-only model is having an image described for it. "started" shows
    // the line; anything else clears it — a failure surfaces in the transcript
    // as the fallback text the model itself receives, so no banner is needed.
    cleanups.push(ws.on('image_describing', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const e = ev as any
      describingImage = e.status === 'started'
        ? { name: e.image_name ?? 'image', index: e.image_index ?? 1, total: e.image_total ?? 1 }
        : null
    }))

    // Turn-level failure (sender/tool setup or the LLM call itself errored).
    // It belongs to no tool card, so render it as a standalone error notice in
    // the transcript instead of dropping it. Also persist it as turnError so the
    // banner above the composer stays visible until dismissed or a new message.
    cleanups.push(ws.on('turn_error', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const msg = (ev as any).error ?? 'request failed'
      turnError = msg
      addChatMsg(sid, {
        id: uid('err'),
        type: 'notice',
        content: `**Error:** ${msg}`,
        level: 'error',
        createdAt: Date.now(),
        streaming: false,
        tools: [],
        todos: [],
      })
      // A first-round failure (usage limit, network, bad key) rolls the user
      // message back past history on the server (see the history_reload that
      // follows), and send() already cleared the composer optimistically — so
      // the text the user typed is gone from both places. Pull it back into the
      // composer so a long prompt isn't lost to a transient error; the user can
      // fix the cause and resend. Only when the server says the message was
      // rolled back: a failure after the first round keeps the bubble in the
      // transcript, and restoring there would resend it twice.
      // The slot is consumed either way, so a failure that kept the message
      // (or one whose turn never reaches `complete`) can't leak this text into a
      // later turn's error.
      // Never overwrite a message already being composed (typed while the turn
      // ran): the failed one is still one ↑ away in the input history, while
      // half-typed text clobbered here would be unrecoverable.
      const lostInput = turnInput.get(sid)
      turnInput.delete(sid)
      if ((ev as any).input_rolled_back && lostInput && composer?.isEmpty()) {
        composer.restore(lostInput.text, lostInput.files)
      }
      // Forget any send still tracked for rollback (a message the server never
      // confirmed — e.g. reminder-only text that gets no history_user_message).
      // Leaving it would misalign the FIFO for the next turn's confirmation.
      // Steers are left alone: their text belongs to the running turn and
      // steer_retracted owns that path.
      const errQueue = pendingSends.get(sid)
      const errMeta = errQueue?.find(m => !m.wasStreaming)
      if (errQueue && errMeta) {
        const next = errQueue.filter(m => m.pendingId !== errMeta.pendingId)
        if (next.length) pendingSends.set(sid, next)
        else pendingSends.delete(sid)
      }
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
      // Belt-and-braces: the describing line normally clears on its own
      // done/failed event, but a WS reconnect between started and done would
      // otherwise leave it hanging forever.
      describingImage = null
      // The turn is over: turn_error (which arrives before this) already had its
      // chance to restore the input, so drop it rather than letting it leak into
      // a later turn this tab didn't compose.
      turnInput.delete(sid)
      chatStreaming.update(s => ({ ...s, [sid]: false }))
      chatProgress.update(p => ({ ...p, [sid]: null }))
      // A turn that ends without an assistant_message (interrupt / error) would
      // otherwise leave the live bubble's streaming caret blinking forever, so
      // clear any lingering per-message streaming flags here. Same for a user
      // bubble still marked pending: its confirmation is broadcast at turn
      // start, so by the time the turn ends it either arrived or never will —
      // a spinner that outlives its own turn is only ever a lie.
      // The server sends the reply's persisted index when the turn ended on a
      // branchable one; stamp it onto the bubble the turn just produced so
      // Branch lights up there without waiting for a transcript reload.
      const replyIndex = (ev as any).message_index
      chatMessages.update(m => {
        const msgs = (m[sid] || []).map((x: any) =>
          x.streaming || x.pending ? { ...x, streaming: false, pending: false } : x)
        if (typeof replyIndex === 'number') {
          for (let k = msgs.length - 1; k >= 0; k--) {
            if (msgs[k].type !== 'assistant') continue
            // Only an unstamped bubble: a turn that ended without producing one
            // (interrupted before any text) must not relabel the previous
            // reply with an index that reaches past it.
            if (typeof msgs[k].messageIndex !== 'number') {
              msgs[k] = { ...msgs[k], messageIndex: replyIndex }
            }
            break
          }
        }
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
      // cache_pct is omitted (not 0) when the backend reported no cache
      // activity, so cache-less providers keep the two-part line.
      const durationMs = (ev as any).duration_ms
      const tokens = (ev as any).tokens
      const cachePct = (ev as any).cache_pct
      if (typeof durationMs === 'number' && typeof tokens === 'number') {
        // A silent panel-update turn draws nothing in the transcript, so a
        // summary notice would float anchored to nothing. Hand the stats to
        // the panel's own status chip instead (GenuiBlock, via panelTurnStats)
        // — the turn still cost tokens, and hiding that entirely would make
        // panel actions look free.
        let handed = false
        const list = get(chatMessages)[sid] || []
        for (let k = list.length - 1; k >= 0; k--) {
          if (list[k].type !== 'assistant') continue
          const actionPanel = silentActionPanel(precedingSaid(list, k))
          if (actionPanel) {
            if (isSilentPairAt(list, k)) {
              panelTurnStats[`${sid}\x00${actionPanel}`] =
                `${fmtDur(Math.round(durationMs / 1000))} · ${fmtTokens(tokens)} tokens`
              handed = true
            } else {
              // Degraded silent turn: the model chose to answer with a visible
              // bubble instead of updating the panel. The user who fired the
              // action skipped the usual pin-to-bottom, so without this the
              // reply lands off-screen and the click reads as a dead button.
              pinToBottom()
            }
          }
          break
        }
        if (!handed) {
          addChatMsg(sid, {
            id: uid('sum'),
            type: 'notice',
            content: `⏱ ${fmtDur(Math.round(durationMs / 1000))}, ${fmtTokens(tokens)} tokens${typeof cachePct === 'number' ? `, cache ${cachePct}%` : ''}`,
            level: 'info',
            createdAt: Date.now(),
            streaming: false,
            tools: [],
            todos: [],
          })
        }
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
      // An idle snapshot from the server clears a stale thinking indicator, but
      // must keep still-running background sub-agents: replayLiveState pushes
      // their events immediately before this snapshot, so wiping them here made
      // the panel flash out and back in as the next sub_agent_event re-added it.
      if ((ev as any).status === 'idle') {
        resetSessionRuntimeState(sid, true)
      }
    }))

    cleanups.push(ws.on('todo_update', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      chatTodos.update(t => ({ ...t, [sid]: (ev as any).todos ?? [] }))
    }))

    cleanups.push(ws.on('background_tasks_update', (ev) => {
      if ((ev as any).session_id && (ev as any).session_id !== sid) return
      const at = Date.now()
      chatBgTasks.update(b => ({ ...b, [sid]: anchorBgTasks(b[sid] ?? [], (ev as any).tasks ?? [], at) }))
    }))

    cleanups.push(ws.on('request_confirmation', (ev) => {
      const csid = (ev as any).session_id
      if (!csid) return
      confirmModals.update(m => ({
        ...m,
        [csid]: {
          id: (ev as any).id,
          sessionId: csid,
          message: (ev as any).message,
          kind: (ev as any).kind,
          // #1105: detail fields so the modal shows what it's approving
          // instead of just "Allow <tool>?".
          toolName: (ev as any).tool_name,
          command: (ev as any).command,
          diff: (ev as any).diff,
          input: (ev as any).input,
        },
      }))
    }))

    cleanups.push(ws.on('confirmation_complete', (ev) => {
      const csid = (ev as any).session_id
      if (!csid) return
      confirmModals.update(m => {
        const current = m[csid]
        if (!current) return m
        // Only close if this completion is for the confirmation currently
        // shown for that session; otherwise leave any unrelated modal untouched.
        if ((ev as any).id === current.id) {
          const n = { ...m }
          delete n[csid]
          return n
        }
        return m
      })
    }))

    cleanups.push(ws.on('request_user_question', (ev) => {
      // Global broadcast: the question may be for ANY session, not just this
      // tab's. Store it under its own session_id; QuestionModal decides whether
      // to show it as a modal (active session) or a note (non-active).
      const qsid = (ev as any).session_id
      if (!qsid) return
      questionModals.update(m => ({
        ...m,
        [qsid]: {
          questionId: (ev as any).question_id,
          sessionId: qsid,
          questions: (ev as any).questions ?? [],
          secret: (ev as any).secret === true,
          dismissed: false,
        },
      }))
    }))

    cleanups.push(ws.on('dismiss_user_question', (ev) => {
      const qsid = (ev as any).session_id
      if (!qsid) return
      questionModals.update(m => {
        const n = { ...m }
        delete n[qsid]
        return n
      })
    }))

    cleanups.push(ws.on('dismiss_confirmation', (ev) => {
      const csid = (ev as any).session_id
      if (!csid) return
      confirmModals.update(m => {
        const n = { ...m }
        delete n[csid]
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
      // A turn is in flight: drop the event. The previous turn's Suggest call
      // can still resolve after a new one starts (fire-and-forget goroutine on
      // the server); applying its stale suggestion would resurrect text that
      // send() just cleared. Mirrors the TUI's "only apply suggestion while
      // idle" rule. The new turn's own Suggest (or its empty-timeout event)
      // will carry the authoritative follow-up.
      if (get(chatStreaming)[sid]) return
      chatSuggestion.update(s => ({ ...s, [sid]: (ev as any).text ?? '' }))
    }))

    return () => {
      cancelled = true
      ws.unsubscribe(sid)
      for (const cleanup of cleanups) cleanup()
      // Switching away tears down the handlers that would have consumed the
      // restore slot, so drop it: on the way back the turn that owned it is
      // long over, and a later failure must not push its text at the composer.
      turnInput.delete(sid)
      // Drop this session's pending sub-agent dismiss timers so they can't fire
      // against a session no longer shown in this view.
      for (const [key, timer] of subAgentDismissTimers) {
        if (key.startsWith(`${sid}\x00`)) {
          clearTimeout(timer)
          subAgentDismissTimers.delete(key)
        }
      }
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
    //
    // The synchronous unstick latches, though — and a macOS trackpad emits a
    // few tiny negative deltas at the tail of a *downward* flick (momentum
    // decay / rubber-band bounce) right as the user lands at the bottom. That
    // noise latched stick off at the exact bottom, where no downward 'scroll'
    // event ever follows to re-arm it, so the ResizeObserver stopped pinning
    // for good and new cards piled up below the fold with the last one
    // half-hidden behind the composer. Disengage immediately as before (the
    // #1069 guard needs it), but if the whole upward gesture settles with
    // barely any net movement it never meant to leave the bottom — re-arm.
    let unstickTimer: ReturnType<typeof setTimeout> | null = null
    let gestureStartTop: number | null = null
    let lastWheelUpAt = 0
    const onWheel = (e: WheelEvent) => {
      if (e.deltaY >= 0) return
      stick = false
      const now = performance.now()
      // A new gesture starts after a quiet gap; track where it began so the
      // settle check measures the gesture's TOTAL travel, not one tick's —
      // a genuinely gentle scroll-up accumulates many px and stays disengaged.
      if (now - lastWheelUpAt > 300 || gestureStartTop === null) gestureStartTop = scroller.scrollTop
      lastWheelUpAt = now
      if (unstickTimer) clearTimeout(unstickTimer)
      unstickTimer = setTimeout(() => {
        if (!interacting && gestureStartTop !== null && scroller.scrollTop >= gestureStartTop - 4) stick = true
      }, 150)
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
    // Belt-and-braces for the same stuck-true hazard: a pointerup swallowed
    // by the OS (context menu dismissed with Esc, an HTML5 drag) never
    // reaches the window either. Any pointer motion with no buttons held
    // proves the press is over, so clear the flag on the next move.
    const onPointerMove = (e: PointerEvent) => { if (interacting && e.buttons === 0) interacting = false }
    scroller.addEventListener('pointermove', onPointerMove, { passive: true })

    const ro = new ResizeObserver(() => {
      // Sitting at the very bottom with no pointer held down is objective
      // proof the user is following the stream, not reading history — re-arm
      // stick even if stray wheel noise latched it off, so auto-scroll can
      // never die while the view is pinned to the latest message.
      const atBottom = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight <= 1
      if (atBottom && !interacting) stick = true
      if (stick && !interacting) scroller.scrollTop = scroller.scrollHeight
    })
    ro.observe(content)

    // Initial pin to bottom after history loads.
    scroller.scrollTop = scroller.scrollHeight

    return () => {
      scroller.removeEventListener('scroll', onScroll)
      scroller.removeEventListener('wheel', onWheel)
      scroller.removeEventListener('pointerdown', onPointerDown)
      scroller.removeEventListener('pointermove', onPointerMove)
      window.removeEventListener('pointerup', onPointerUp)
      window.removeEventListener('pointercancel', onPointerUp)
      window.removeEventListener('blur', onPointerUp)
      if (unstickTimer) clearTimeout(unstickTimer)
      ro.disconnect()
    }
  })

  // When the active session changes, re-pin to the bottom.
  $effect(() => {
    void $activeSessionId
    stick = true
  })

  // ── user-message rail ───────────────────────────────────────────────────────
  // A minimap-style rail of ticks, one per user message, positioned by actual
  // rendered offset within the scrollable content (not evenly spaced) so it
  // tracks where each message really sits, DeepSeek-style.
  //
  // Split into two effects on purpose:
  //  - The recompute itself is throttled (like throttledMarkdown below, #1114)
  //    since it walks every user message doing layout-forcing reads, and
  //    appendToLastAssistant (stores.ts) hands msgs a new array on every
  //    streamed delta.
  //  - Driving that throttle from a ResizeObserver ALONE isn't reliable:
  //    Chrome defers ResizeObserver callbacks for backgrounded/invisible tabs,
  //    so a message added while the tab isn't in the foreground would leave
  //    the rail stale indefinitely. A tracked `msgs` effect below re-arms the
  //    throttle on every change regardless of tab visibility; the
  //    ResizeObserver stays only as a supplementary trigger for layout shifts
  //    that don't touch msgs (viewport resize, Artifacts panel toggle).
  let railTicks = $state<{ id: string; preview: string }[]>([])
  // Which node is "current" (last user message scrolled past the 0.32 line) and
  // how far the blue progress line has filled (0–100, scroll position). Both are
  // driven by the scroll listener below. The nodes are evenly spaced and
  // vertically centered (per the timeline design); this layer expresses where
  // the viewport currently sits within the conversation.
  let railActive = $state(0)
  let railFillPct = $state(0)
  // How much the node column is compressed versus its natural length (1 = not
  // at all). Drives --rail-scale so the dots shrink along with the spacing on
  // short panes instead of staying full-size while crammed together.
  let railScale = $state(1)

  const RAIL_THROTTLE_MS = 100
  let railTimer: ReturnType<typeof setTimeout> | null = null
  let railLastRun = 0

  function recomputeRailTicks() {
    const ticks: { id: string; preview: string }[] = []
    for (const m of msgs) {
      if (m.type !== 'user') continue
      if (!document.getElementById(`msg-${m.id}`)) continue
      ticks.push({ id: m.id, preview: (m.content || '').slice(0, 160) })
    }
    railTicks = ticks
    // Compression factor: available rail height (pane minus the 8px top/bottom
    // insets of .msg-rail) over the column's natural length (28px per node
    // minus the trailing gap). Clamped to 1 so uncompressed panes are unscaled.
    const natural = ticks.length * 28 - 20
    const avail = (messagesEl?.clientHeight ?? 0) - 16
    railScale = ticks.length > 1 && avail > 0 ? Math.min(1, avail / natural) : 1
    syncRailScroll()
  }

  // Recompute active node + fill percentage from the scroll container. Called on
  // every scroll (rAF-throttled) and whenever the tick set changes.
  function syncRailScroll() {
    const sc = messagesEl
    if (!sc || railTicks.length === 0) return
    const max = sc.scrollHeight - sc.clientHeight
    // Not actually scrollable: the whole thread is visible from the top, so the
    // first message is "current" and nothing has been scrolled past. Without
    // this guard `max - scrollTop < 4` is trivially true and would pin active
    // to the last node even though you're looking at the top.
    if (max <= 4) {
      if (railFillPct !== 0) railFillPct = 0
      if (railActive !== 0) railActive = 0
      return
    }
    const fill = Math.min(100, Math.max(0, (sc.scrollTop / max) * 100))
    const scTop = sc.getBoundingClientRect().top
    const line = sc.clientHeight * 0.32
    let active = 0
    railTicks.forEach((tk, i) => {
      const el = document.getElementById(`msg-${tk.id}`)
      if (!el) return
      if (el.getBoundingClientRect().top - scTop <= line) active = i
    })
    if (max - sc.scrollTop < 4) active = railTicks.length - 1
    if (fill !== railFillPct) railFillPct = fill
    if (active !== railActive) railActive = active
  }

  let railScrollRaf = 0
  function onRailScroll() {
    if (railScrollRaf) return
    railScrollRaf = requestAnimationFrame(() => {
      railScrollRaf = 0
      syncRailScroll()
    })
  }

  function scheduleRailRecompute() {
    const elapsed = Date.now() - railLastRun
    if (elapsed >= RAIL_THROTTLE_MS) {
      railLastRun = Date.now()
      recomputeRailTicks()
    } else if (!railTimer) {
      railTimer = setTimeout(() => {
        railTimer = null
        railLastRun = Date.now()
        recomputeRailTicks()
      }, RAIL_THROTTLE_MS - elapsed)
    }
  }

  $effect(() => {
    void msgs
    scheduleRailRecompute()
  })

  $effect(() => {
    const content = innerEl
    if (!content) return
    const ro = new ResizeObserver(scheduleRailRecompute)
    ro.observe(content)
    // Also watch the scroll pane itself: a height-only viewport resize changes
    // the rail's available height (railScale) without reflowing the content,
    // so observing the content alone would miss it.
    if (messagesEl) ro.observe(messagesEl)
    return () => ro.disconnect()
  })

  $effect(() => {
    const sc = messagesEl
    if (!sc) return
    sc.addEventListener('scroll', onRailScroll, { passive: true })
    syncRailScroll()
    return () => sc.removeEventListener('scroll', onRailScroll)
  })

  // Smooth-scroll the conversation so the target message lands ~90px below the
  // top of the scroll area. Deliberately not scrollIntoView (per the timeline
  // design): scrollIntoView can nudge ancestor scrollers and gives no offset
  // control. Honors prefers-reduced-motion.
  function jumpToMessage(id: string) {
    const sc = messagesEl
    const el = document.getElementById(`msg-${id}`)
    if (!sc || !el) return
    const target = Math.max(0, el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop - 90)
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    sc.scrollTo({ top: target, behavior: reduce ? 'auto' : 'smooth' })
  }

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

  // ── throttled ```octo-ui fence segmentation while streaming ────────────────
  // splitOctoUiFences() is a single O(n) scan (see fence-split.ts), but it is
  // still called from the render body of every assistant message, so it hits
  // the exact same call-frequency hazard #1114 was about (see
  // throttledMarkdown above): a streaming message's content grows on every
  // delta, and re-deriving segments on every one of those renders would be
  // wasted work on the hot path. Mirror throttledMarkdown's cache shape
  // exactly — same per-message key, same content-equality/time-window
  // reuse, same "always fresh once streaming stops" guarantee.
  const segmentCache = new Map<string, { segments: GenuiSegment[]; at: number; content: string }>()
  function throttledSegments(cacheKey: string, content: string, streaming: boolean): GenuiSegment[] {
    const cached = segmentCache.get(cacheKey)
    if (streaming && cached && (content === cached.content || Date.now() - cached.at < RENDER_THROTTLE_MS)) {
      return cached.segments
    }
    const segments = splitOctoUiFences(content)
    segmentCache.set(cacheKey, { segments, at: Date.now(), content })
    return segments
  }

  // ── octo-ui inline action feedback → synthetic user turn ───────────────────
  // Builds the "[octo-ui-action] {...}" convention message and sends it
  // exactly like an ordinary typed message (send(), below) — a plain new
  // user turn, no force/queue flags, no backend change: it rides the
  // existing user_message WS frame verbatim.
  // A `panel` key marks the turn silent: neither this message nor the reply
  // that answers it renders as a bubble (see lib/genui/silent-turn.ts). An
  // action from an anonymous panel omits it and keeps the visible chip.
  function sendGenuiAction(event: GenuiActionEvent, panelId?: string) {
    const body: Record<string, unknown> = { action: event.action, fields: event.fields }
    if (panelId) body.panel = panelId
    if (event.payload !== undefined) body.payload = event.payload
    // Clear the previous turn's stats up front: if this turn ends without a
    // fresh entry (interrupt, error), the chip must not resurface last
    // round's numbers on a later update.
    if (panelId) delete panelTurnStats[`${id ?? ''}\x00${panelId}`]
    // A silent action's reply updates the panel in place, so the view must
    // stay where the user is acting — scrolled up at the panel, not at the
    // bottom. An anonymous panel's action keeps the pin: its reply is an
    // ordinary bubble at the bottom.
    send(`[octo-ui-action] ${JSON.stringify(body)}`, undefined, false, !panelId)
  }

  // ── panel projection ───────────────────────────────────────────────────────
  // Which version of each addressable panel is live, and where it renders.
  // projectPanels is O(total message text) and this runs on every streamed
  // delta, so it inherits the same cache-and-throttle discipline #1114 forced
  // on markdown rendering: reuse the previous result while the last message is
  // still growing, and always recompute once it settles.
  let panelCache: { sig: string; panels: Map<string, PanelProjection>; at: number } | null = null
  const panels = $derived.by(() => {
    const list = msgs
    const last = list[list.length - 1]
    const sig = `${list.length}:${last?.id ?? ''}:${last?.content?.length ?? 0}:${last?.streaming ?? false}`
    if (panelCache) {
      if (panelCache.sig === sig) return panelCache.panels
      if (last?.streaming && Date.now() - panelCache.at < RENDER_THROTTLE_MS) return panelCache.panels
    }
    const next = projectPanels(list)
    panelCache = { sig, panels: next, at: Date.now() }
    return next
  })

  /**
   * The panel waiting on a silent turn, if any.
   *
   * Gated on the session actually running a turn rather than on the shape of
   * the transcript alone. A turn that errors or is interrupted before
   * producing any assistant message would otherwise leave the action as the
   * last thing in history — and a panel disabled forever, surviving reloads,
   * since history is what it was derived from. The running flag comes from
   * the server, so it clears on every ending a turn can have.
   */
  const pendingPanel = $derived.by(() => {
    if (!streaming) return null
    for (let i = msgs.length - 1; i >= 0; i--) {
      const m = msgs[i]
      if (m.type === 'assistant') {
        const p = silentActionPanel(precedingSaid(msgs, i))
        return p && couldBeSilentReply(m.content) ? p : null
      }
      if (m.type === 'user') return silentActionPanel(m)
      // Tool groups and progress rows are skipped: the model calling a tool
      // before answering is the normal shape of a panel action that needs
      // data, and the panel should stay pending across it.
    }
    return null
  })

  // Per-panel stats of the last completed silent turn, keyed "sid\0panelId".
  // Written by the turn-done handler, shown briefly by the panel's status
  // chip. Client-session state only — it dies with the tab, like the per-turn
  // summary notices.
  const panelTurnStats = $state<Record<string, string>>({})

  /** True when this assistant message is the hidden half of a silent pair. */
  function isHiddenReply(index: number): boolean {
    const m = msgs[index]
    if (!m || m.type !== 'assistant') return false
    // While it is still streaming, hide it optimistically for as long as it
    // could still turn out to be a silent update. The predicate is monotone,
    // so once prose appears the bubble shows and stays shown.
    if (m.streaming) {
      const p = silentActionPanel(precedingSaid(msgs, index))
      return p !== null && couldBeSilentReply(m.content)
    }
    return isSilentPairAt(msgs, index)
  }

  // The chip rendering below parses the same envelope the silent-turn
  // classifier does, so it uses that parser rather than a second copy — two
  // implementations of one wire convention would drift.

  // ── edit a prior user message: load it back into the composer for resend ─────
  let composer = $state<{ setText: (v: string) => void; restore: (v: string, files?: any[]) => void; isEmpty: () => boolean } | null>(null)
  function editMessage(content: string) {
    composer?.setText(content)
  }

  // ── retract a pending steer message (web equivalent of the TUI's ↑ recall) ───
  // Ask the server to pull it back out of the running turn's inbox; the bubble
  // stays (marked "retracting") until the server answers. steer_retracted then
  // reloads it into the composer; steer_retract_failed means the turn already
  // consumed it — keep the bubble and tell the user it's on its way.
  function retractSteer(pendingId: string) {
    const sid = get(activeSessionId)
    if (!sid) return
    const s = pendingSteers[sid]?.find(x => x.pendingId === pendingId)
    if (!s || s.retracting) return
    pendingSteers = { ...pendingSteers, [sid]: pendingSteers[sid]?.map(x => x.pendingId === pendingId ? { ...x, retracting: true } : x) ?? [] }
    ws.retractSteer(sid, pendingId, s.text)
  }

  // ── suggestion chip: fill the composer, don't fire (mirrors the TUI's Tab) ──
  function fillSuggestion(text: string) {
    composer?.setText(text)
    const sid = get(activeSessionId)
    if (sid) chatSuggestion.update(s => ({ ...s, [sid]: '' }))
  }

  // ── landing starter cards ──────────────────────────────────────────────────
  // Four ways in, one per kind of work Octo is for. Clicking one loads its
  // prompt into the composer without sending, so the working directory / model
  // pickers still apply and the text stays editable — same contract as the
  // suggestion chip above.
  const starters = [
    { icon: 'ant-design:code-outlined',   key: 'explore'  },
    { icon: 'ant-design:tool-outlined',   key: 'build'    },
    { icon: 'ant-design:global-outlined', key: 'research' },
    { icon: 'ant-design:form-outlined',   key: 'write'    },
  ]

  // ── export mode helpers ────────────────────────────────────────────────────

  function enterExportMode() {
    const sid = get(activeSessionId)
    if (!sid) return
    const msgsForSession = get(chatMessages)[sid] ?? []
    const selectable = msgsForSession
      .filter((m: any) => m.type === 'user' || m.type === 'assistant')
      .map((m: any) => m.id)
    selectedMessagesStore.initForSession(sid, selectable)
    exportModeStore.enter(sid)
  }

  function exitExportMode() {
    const sid = get(activeSessionId)
    if (!sid) return
    exportModeStore.exit(sid)
    selectedMessagesStore.clear(sid)
    exportBusy = false
    exportIncludeTools = false
  }

  function toggleSelect(msgId: string) {
    const sid = get(activeSessionId)
    if (!sid) return
    selectedMessagesStore.toggle(sid, msgId)
  }

  // Filter server events to only those corresponding to selected local messages.
  // User/assistant events are matched by running index against local msgs of the
  // same type; tool_call, tool_result and thinking events always ride along.
  //
  // handleHistoryEvent skips creating a local bubble for an assistant_message
  // whose content AND thinking are both empty (tool-only rounds) — see its
  // comment above. That predicate is duplicated here so the index walk stays
  // aligned: without it, any such event would consume a uaIdx slot that no
  // local message ever occupied, permanently shifting every match after it.
  function filterEventsBySelection(events: any[]): any[] {
    const sid = get(activeSessionId)
    if (!sid) return events
    const selected = selectedMessagesStore.getForSession(sid)
    const allLocal = get(chatMessages)[sid] ?? []
    const localUA = allLocal.filter((m: any) => m.type === 'user' || m.type === 'assistant')
    let uaIdx = 0

    const result: any[] = []
    for (const ev of events) {
      const etype = ev.type ?? ''
      const isEmptyAssistant = etype === 'assistant_message' &&
        !(ev.content ?? '').trim() && !(ev.thinking ?? '').trim()
      if ((etype === 'history_user_message' || etype === 'assistant_message') && !isEmptyAssistant) {
        const local = localUA[uaIdx]
        uaIdx++
        if (local && selected.has(local.id)) result.push(ev)
      } else {
        result.push(ev)
      }
    }
    return result
  }

  async function fetchEvents(): Promise<{ events: any[]; sid: string } | null> {
    const sid = get(activeSessionId)
    if (!sid) return null
    let events: any[] = []
    try {
      const data = await api.getSessionMessages(sid)
      events = (data as { events?: any[] }).events ?? []
    } catch (e) {
      console.error('Export failed:', e)
      showToast(tr('chat.export_failed'), 'error')
      return null
    }
    if (!events.length) {
      showToast(tr('chat.nothing_to_export'), 'error')
      return null
    }
    return { events, sid }
  }

  function triggerDownload(content: string, filename: string, mime: string) {
    const blob = new Blob([content], { type: mime })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
  }

  // Hand the built transcript to the user. Every format must go through here:
  // the octo-served desktop webview has no download delegate, so a blob
  // <a download> click there silently does nothing and the bytes have to take
  // the native save dialog instead. Returns false when the save was cancelled
  // or failed, so the caller keeps export mode (and the selection) open.
  async function deliverExport(content: string, filename: string, mime: string): Promise<boolean> {
    if (get(nativeShell)) {
      try {
        const r = await api.nativeSaveFile(filename, content)
        return !r.cancelled
      } catch {
        showToast(tr('chat.export_failed'), 'error')
        return false
      }
    }
    triggerDownload(content, filename, mime)
    return true
  }

  // Returns whether the export actually completed, so the caller only exits
  // export mode (and drops the user's checkbox selection) on success — not on
  // "nothing selected" or a cancelled/failed native save.
  async function exportAsMarkdown(events: any[], title: string): Promise<boolean> {
    const filtered = filterEventsBySelection(events)
    if (!filtered.length) { showToast(tr('chat.nothing_to_export'), 'error'); return false }

    const lines: string[] = [`# ${title}`, '']
    let omittedToolEvents = false

    for (const ev of filtered) {
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
        lines.push('<!-- Thinking -->', ev.text, '')
      } else if (type === 'tool_call' || type === 'tool_result') {
        if (exportIncludeTools) {
          if (type === 'tool_call') {
            lines.push(`- **Tool call**: ${ev.tool_name ?? ev.name ?? 'unknown'}`, '')
          } else {
            lines.push(`- **Tool result**: ${typeof ev.result === 'string' ? ev.result.slice(0, 500) : '(non-text result)'}`, '')
          }
        } else {
          omittedToolEvents = true
        }
      }
    }

    const title_safe = filenameStem(title)
    const content = lines.join('\n')
    if (!(await deliverExport(content, `${title_safe}.md`, 'text/markdown'))) return false

    if (omittedToolEvents) {
      showToast(tr('chat.export_tools_omitted'), 'info')
    }
    return true
  }

  async function exportAsJSON(events: any[], title: string): Promise<boolean> {
    const title_safe = filenameStem(title)
    const json = JSON.stringify(events, null, 2)
    return deliverExport(json, `${title_safe}.json`, 'application/json')
  }

  // PDF prints the conversation that is already on screen rather than building a
  // document: the print stylesheet in app.css strips the chrome and un-clips the
  // scroll box, and the engine paginates. That is what keeps this export free of
  // a PDF library and an embedded CJK font.
  //
  // Unlike MD and JSON this never reports success, because there is nothing to
  // report: the print dialog outlives the call (on macOS it is a window sheet),
  // and it reads the live DOM — so export mode has to stay up, selection and all,
  // until the user dismisses it themselves.
  async function exportAsPDF(): Promise<void> {
    if (get(nativeShell)) {
      await api.nativePrint()
      return
    }
    window.print()
  }

  // ── PNG / HTML export ───────────────────────────────────────────────────
  // PNG captures an off-screen render of the conversation as a single long
  // image (html2canvas, dynamically imported so it never touches the main
  // bundle). HTML builds the same self-contained document and hands it to
  // deliverExport, so it rides the native save dialog in the desktop webview
  // just like MD and JSON. Both honour the checkbox selection via
  // filterEventsBySelection, and only carry user/assistant turns — tool calls
  // stay in MD/JSON where they belong.
  const EXPORT_CAPTURE_WIDTH = 960

  function getExportLocale(): string {
    return document.documentElement.lang || navigator.language || 'en'
  }

  function triggerBlobDownload(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
  }

  function exportConversationStyles(): string {
    return `
      :root { color-scheme: light; }
      * { box-sizing: border-box; }
      body {
        margin: 0; padding: 32px 20px 40px;
        background: #f8fafc; color: #111827;
        font: 14px/1.6 Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      }
      .export-shell { width: min(960px, 100%); margin: 0 auto; }
      .export-header {
        margin-bottom: 20px; padding: 20px 24px;
        border: 1px solid #e5e7eb; border-radius: 18px;
        background: #ffffff; box-shadow: 0 2px 8px rgba(0,0,0,0.06);
      }
      .export-title { margin: 0; font-size: 24px; line-height: 1.25; }
      .export-meta { margin-top: 8px; color: #64748b; font-size: 13px; }
      .conversation { display: flex; flex-direction: column; gap: 14px; }
      .msg {
        display: flex; flex-direction: column; gap: 8px;
        padding: 16px 18px; border: 1px solid #e5e7eb; border-radius: 18px;
        background: #f9fafb; box-shadow: 0 2px 8px rgba(0,0,0,0.06);
      }
      .msg.user { background: #eff6ff; }
      .msg.assistant { background: #f8fafc; }
      .msg-head {
        display: flex; align-items: center; gap: 12px;
        color: #64748b; font-size: 12px;
        text-transform: uppercase; letter-spacing: 0.08em;
      }
      .msg-label { font-weight: 700; }
      .msg-body { min-width: 0; overflow-wrap: anywhere; }
      .msg-body > :first-child, .msg-thinking > :first-child { margin-top: 0; }
      .msg-body > :last-child, .msg-thinking > :last-child { margin-bottom: 0; }
      .msg-thinking-wrap { border-top: 1px solid #e5e7eb; padding-top: 10px; }
      .msg-thinking-label {
        margin-bottom: 8px; color: #64748b; font-size: 12px; font-weight: 700;
        text-transform: uppercase; letter-spacing: 0.08em;
      }
      .msg-thinking {
        padding: 12px 14px; border: 1px solid #e5e7eb; border-radius: 14px;
        background: #f1f5f9;
      }
      p, ul, ol, pre, blockquote { margin: 0 0 12px; }
      pre {
        overflow: auto; padding: 14px; border-radius: 14px;
        background: #f1f5f9; border: 1px solid #e5e7eb;
        white-space: pre-wrap; word-break: break-word;
      }
      code { font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; }
      a { color: #2563eb; text-decoration: none; }
      blockquote {
        margin-left: 0; padding-left: 14px;
        border-left: 3px solid rgba(37,99,235,0.45); color: #475569;
      }
    `
  }

  // Build the inner conversation markup from filtered server events. Only
  // user/assistant turns are rendered — tool calls are intentionally absent
  // (MD is the lossless format; PNG/HTML are for sharing a readable snapshot).
  function buildExportConversation(events: any[]): string {
    return events
      .map((ev) => {
        const type = ev.type ?? ''
        if (type === 'history_user_message') {
          return `<article class="msg user"><div class="msg-head"><span class="msg-label">You</span></div><div class="msg-body">${escapeHtml(ev.content ?? '').replace(/\n/g, '<br>')}</div></article>`
        }
        if (type === 'assistant_message') {
          const thinking = ev.thinking
            ? `<div class="msg-thinking-wrap"><div class="msg-thinking-label">Thoughts</div><div class="msg-thinking">${renderMarkdown(ev.thinking, true)}</div></div>`
            : ''
          return `<article class="msg assistant"><div class="msg-head"><span class="msg-label">Octo</span></div><div class="msg-body">${renderMarkdown(ev.content ?? '', true)}</div>${thinking}</article>`
        }
        return ''
      })
      .filter(Boolean)
      .join('\n')
  }

  // Self-contained HTML document shared by the HTML export and the PNG render
  // root. DOMPurify sanitises the whole document so a session title or message
  // can't inject script or event handlers.
  function buildExportHTMLDocument(events: any[], title: string, locale: string): string {
    const zh = locale.startsWith('zh')
    const exportTime = new Intl.DateTimeFormat(zh ? 'zh-CN' : 'en-US', {
      dateStyle: 'medium', timeStyle: 'short',
    }).format(new Date())
    const dirty = `<!DOCTYPE html>
<html lang="${zh ? 'zh' : 'en'}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${escapeHtml(title)} - Octo Export</title>
  <style>${exportConversationStyles()}</style>
</head>
<body>
  <main class="export-shell">
    <header class="export-header">
      <h1 class="export-title">${escapeHtml(title)}</h1>
      <div class="export-meta">${escapeHtml(exportTime)} · Exported from octo</div>
    </header>
    <section class="conversation">
      ${buildExportConversation(events)}
    </section>
  </main>
</body>
</html>`
    return DOMPurify.sanitize(dirty, { WHOLE_DOCUMENT: true, ADD_ATTR: ['target', 'rel', 'class'] })
  }

  // Render the conversation off-screen so html2canvas can capture it without
  // the live chat chrome. The host is parked far off the left edge and never
  // visible to the user.
  function createExportRenderRoot(events: any[], title: string, locale: string): { root: HTMLElement; cleanup: () => void } {
    const htmlDoc = buildExportHTMLDocument(events, title, locale)
    const parsed = new DOMParser().parseFromString(htmlDoc, 'text/html')
    const styleText = parsed.querySelector('style')?.textContent ?? ''
    const shell = parsed.querySelector('.export-shell')?.cloneNode(true) as HTMLElement | null
    const host = document.createElement('div')
    host.style.cssText = 'position:fixed;left:-10000px;top:0;width:960px;padding:0;margin:0;z-index:-1;pointer-events:none'
    // The style element from the parsed document head is not part of the
    // cloned shell, so append it separately or html2canvas captures unstyled markup.
    const styleEl = document.createElement('style')
    styleEl.textContent = styleText
    host.appendChild(styleEl)
    if (shell) host.appendChild(shell)
    document.body.appendChild(host)
    return { root: host, cleanup: () => host.remove() }
  }

  async function exportAsPNG(events: any[], title: string): Promise<boolean> {
    const filtered = filterEventsBySelection(events)
    if (!filtered.length) { showToast(tr('chat.nothing_to_export'), 'error'); return false }
    const { default: html2canvas } = await import('html2canvas')
    const { root, cleanup } = createExportRenderRoot(filtered, title, getExportLocale())
    try {
      const canvas = await html2canvas(root, {
        scale: 2, useCORS: true, backgroundColor: '#ffffff',
        windowWidth: EXPORT_CAPTURE_WIDTH,
      })
      const blob = await new Promise<Blob | null>(r => canvas.toBlob(r, 'image/png'))
      if (!blob) throw new Error('png_blob_failed')
      const filename = `${filenameStem(title)}.png`
      if (get(nativeShell)) {
        // Desktop webview has no <a download> delegate — base64-encode the
        // PNG and route it through the native binary save dialog.
        const b64 = await new Promise<string>(r => {
          const fr = new FileReader()
          fr.onload = () => r((fr.result as string).split(',')[1] ?? '')
          fr.readAsDataURL(blob)
        })
        try {
          const res = await api.nativeSaveBinary(filename, b64)
          if (res.cancelled) return false
        } catch {
          showToast(tr('chat.export_failed'), 'error')
          return false
        }
      } else {
        triggerBlobDownload(blob, filename)
      }
      return true
    } finally { cleanup() }
  }

  async function exportAsHTML(events: any[], title: string): Promise<boolean> {
    const filtered = filterEventsBySelection(events)
    if (!filtered.length) { showToast(tr('chat.nothing_to_export'), 'error'); return false }
    const content = buildExportHTMLDocument(filtered, title, getExportLocale())
    return deliverExport(content, `${filenameStem(title)}.html`, 'text/html')
  }

  async function exportByFormat(format: string) {
    if (exportBusy) return
    exportBusy = true
    try {
      // PDF is the odd one out: it prints the rendered DOM, so it needs no
      // server transcript and leaves export mode open (see exportAsPDF).
      if (format === 'pdf') {
        await exportAsPDF()
        return
      }
      const title = currentSession?.title ?? currentSession?.name ?? 'session'
      // Both MD and JSON fetch the full server transcript. MD filters by
      // selection internally (filterEventsBySelection); JSON is always
      // lossless and ignores the checkbox selection. PNG and HTML ride the
      // same fetch+filter path so they honour the checkboxes too.
      const result = await fetchEvents()
      if (!result) { exportBusy = false; return }
      let ok = true
      switch (format) {
        case 'md':
          ok = await exportAsMarkdown(result.events, title)
          break
        case 'json':
          ok = await exportAsJSON(result.events, title)
          break
        case 'png':
          ok = await exportAsPNG(result.events, title)
          break
        case 'html':
          ok = await exportAsHTML(result.events, title)
          break
      }
      if (ok) exitExportMode()
    } catch (e: any) {
      console.error('Export error:', e)
      showToast(tr('chat.export_failed'), 'error')
    } finally {
      exportBusy = false
    }
  }

  // ensureActiveSession returns the session to send into, creating one first
  // if none is active. This is the only place the ORDINARY new-session flow
  // creates one: the sidebar "+", the command palette, the desktop tray and ⌘N
  // all just open this blank landing and park their picks in the pending*
  // stores, so a session exists on disk and in the sidebar only once something
  // is actually said in it. (The panel-launched flows in stores.ts —
  // openAgentSession, summonAgent — create their own up front, since they always
  // have a prompt to send.) `created` reports whether this call is what created it: the
  // caller must not send straight into a session it just created (see send()).
  // In-flight creation, shared by concurrent callers. Creating a session is now
  // up to two round-trips (a project, then the session), and a second send
  // arriving inside that window would see activeSessionId still unset and start
  // its own — two sessions, one message in each. It also protects
  // resolveProjectForDir from building the same project twice.
  let creating: Promise<{ id: string; created: boolean } | null> | null = null

  async function ensureActiveSession(): Promise<{ id: string; created: boolean } | null> {
    const existing = get(activeSessionId)
    if (existing) return { id: existing, created: false }
    if (creating) return creating
    creating = createSessionForFirstMessage().finally(() => { creating = null })
    return creating
  }

  async function createSessionForFirstMessage(): Promise<{ id: string; created: boolean } | null> {
    try {
      // A model picked in the composer before any session existed rides the
      // pendingModel store (composite "<endpoint>::<model>" id — the create
      // handler resolves it via cfg.EntryByModel); consumed here so it only
      // applies to the session it was picked for. Same for the agent, which
      // the sidebar's "+" caret can pin per new session, falling back to the
      // globally active one.
      const model = get(pendingModel)
      const opts: api.CreateSessionOpts = {
        source: 'manual',
        agent_profile: get(pendingAgent) || get(activeAgent),
        ...(model ? { model } : {}),
      }
      // Where the session lands. An explicitly chosen group (the sidebar's
      // per-group "+") wins outright. Otherwise a working directory picked on
      // the landing page files the session under the project that owns that
      // directory — creating the project on the spot if there isn't one yet.
      // With neither, it stays an ungrouped task.
      const groupId = get(pendingGroupId)
      const dir = get(pendingWorkingDir)
      // Track whether THIS call is what created the project, so a failure to
      // create the session can take it back down again. Otherwise a server
      // restart or a rejected create leaves an empty project in the sidebar
      // that the user has to notice and delete by hand.
      let createdProject = ''
      if (groupId) {
        opts.group_id = groupId
      } else if (dir) {
        const before = get(sessionGroups).length
        opts.group_id = await resolveProjectForDir(dir)
        if (get(sessionGroups).length > before) createdProject = opts.group_id
      }
      let created: any
      try {
        created = await api.createSession(opts) as any
      } catch (e) {
        if (createdProject) await discardProject(createdProject)
        throw e
      }
      pendingModel.set('')
      pendingAgent.set('')
      pendingGroupId.set('')
      pendingWorkingDir.set('')
      const reasoningPick = get(pendingReasoningEffort)
      pendingReasoningEffort.set('')
      const permPick = get(pendingPermissionMode)
      pendingPermissionMode.set('')
      const showReasoningPick = get(pendingShowReasoning)
      pendingShowReasoning.set(null)
      const newSess = created.session ?? created
      prependSession(newSess)
      // The server filed it under the group; mirror that locally so the
      // sidebar shows the row under its project without waiting for a refetch.
      if (opts.group_id) {
        const gid = opts.group_id
        sessionGroups.update(gs => gs.map(g => g.id === gid
          ? { ...g, session_ids: [newSess.id, ...g.session_ids.filter(id => id !== newSess.id)] }
          : g))
      }
      activeSessionId.set(newSess.id)
      // Reasoning effort has no create-time field of its own (it's a global
      // setting every session inherits — see the PATCH endpoint's own PR5
      // note), so a landing-page pick can only take effect as a follow-up
      // call once the session is real. Best-effort: the session already
      // exists at this point, so a failure here shouldn't undo it — just
      // leave the global default in place and tell the user.
      if (reasoningPick) {
        try {
          await api.updateSessionReasoningEffort(newSess.id, reasoningPick)
          chatReasoningEffort.update(r => ({ ...r, [newSess.id]: reasoningPick }))
          globalReasoningEffort.set(reasoningPick)
          if (reasoningPick === 'off') {
            chatShowReasoning.update(r => ({ ...r, [newSess.id]: false }))
          }
        } catch (e: any) {
          showToast(e.message ?? 'Failed to apply reasoning effort', 'error')
        }
      }
      // Same story as reasoning effort above: permission mode and the
      // show-reasoning toggle have no create-time field either, so a
      // landing-page pick only takes effect as a follow-up PATCH once the
      // session is real.
      if (permPick) {
        try {
          await api.updateSessionPermissionMode(newSess.id, permPick)
          chatPermMode.update(m => ({ ...m, [newSess.id]: permPick }))
        } catch (e: any) {
          showToast(e.message ?? 'Failed to apply permission mode', 'error')
        }
      }
      // Reasoning effort 'off' already forced show_reasoning off locally
      // above; skip so a stale pending toggle doesn't fight that.
      if (showReasoningPick !== null && reasoningPick !== 'off') {
        try {
          await api.updateSessionShowReasoning(newSess.id, showReasoningPick)
          chatShowReasoning.update(r => ({ ...r, [newSess.id]: showReasoningPick }))
        } catch (e: any) {
          showToast(e.message ?? 'Failed to apply reasoning visibility', 'error')
        }
      }
      return { id: newSess.id, created: true }
    } catch (e: any) {
      showToast(e.message, 'error')
      return null
    }
  }

  // Undo a project this send created moments ago, after the session it was
  // created for failed. Best-effort: a failure here is not worth a second toast
  // on top of the one the caller already showed, and the project is visible in
  // the sidebar for the user to remove.
  async function discardProject(groupId: string) {
    try {
      await api.deleteSessionGroup(groupId)
      sessionGroups.update(gs => gs.filter(g => g.id !== groupId))
    } catch { /* leave it; the user can delete it from the sidebar */ }
  }

  // ── send message ───────────────────────────────────────────────────────────
  // The previous turn's leftovers, cleared before anything that can start the
  // next one: finished sub-agents, the thinking buffer, the error banner, and
  // the follow-up suggestion (it belongs to a conversation that has now moved
  // on and must not re-render when this turn ends).
  function clearLastTurnUi(sid: string) {
    turnError = null
    clearDoneSubAgents(sid)
    chatThinking.update(tt => ({ ...tt, [sid]: '' }))
    chatSuggestion.update(s => ({ ...s, [sid]: '' }))
  }

  // `pin` — whether sending re-pins the view to the bottom. True for anything
  // the user typed; false for a silent panel action, whose reply renders into
  // the panel the user is currently looking at rather than into the scrollback.
  async function send(text: string, files?: any[], queued = false, pin = true) {
    if (!text.trim() && !(files && files.length)) return
    const active = await ensureActiveSession()
    if (!active) {
      // The composer cleared its input before calling us, so without this the
      // user's first message is simply gone — and this path now runs for EVERY
      // new session's first message, across two requests that can each fail,
      // not just the rare post-delete case it started as.
      composer?.restore(text, files)
      return
    }
    const sid = active.id
    if (active.created) {
      // Nothing is subscribed to a session that existed a millisecond ago: the
      // effect above only issues the subscribe once this call returns. Sending
      // now would have the server broadcast this message's confirmation to an
      // empty room, and a turn's own history_user_message — unlike the steers
      // that follow it — is not in the replay buffer, so a later subscribe
      // never sees it. The bubble would spin for good and the queue would sit
      // one entry ahead for the rest of the tab's life. Hand the text to the
      // flush-on-subscribe path the panel-opened sessions already use.
      pendingPrompt.set({ sessionId: sid, content: text, files })
      return
    }
    // Steering: a message sent while a turn is already running rides the
    // running turn's Inbox on the server. It must NOT reset the live UI —
    // the sub-agents panel and thinking buffer belong to the turn in flight.
    const wasStreaming = get(chatStreaming)[sid] ?? false
    const steering = wasStreaming

    // Server-inline commands (/clear, /compact, /reload, /goal …) are applied
    // by the server and never enter history, so no history_user_message ever
    // comes back for them. Everything that echo retires — the optimistic
    // bubble, its pendingSends entry, the ghost steer — has to be skipped, or
    // it hangs there forever. The server speaks for these itself: a goal_notice
    // line, a history_reload, a toast.
    const inline = inlineSlashCommand(text, !!(files && files.length))
    if (inline) {
      if (!steering) {
        clearLastTurnUi(sid)
        // Only /compact is a long server-side job with no other busy signal
        // (its own session_update idle clears this again). /goal may start a
        // continuation turn, whose events flip streaming on for real; the rest
        // finish in a single round-trip. Claiming "streaming" for those would
        // stick — nothing would ever flip it back.
        if (inline === 'compact') chatStreaming.update(s => ({ ...s, [sid]: true }))
      }
      ws.sendMessage(sid, text, files, false, false)
      pinToBottom()
      return
    }

    if (!steering) {
      // A fresh turn starts.
      clearLastTurnUi(sid)
      // Remember what started this turn so turn_error can hand it back.
      turnInput.set(sid, { text, files })
      chatStreaming.update(s => ({ ...s, [sid]: true }))
    }
    // Queueing only means anything mid-turn: idle there is no turn to wait for,
    // so the server starts one immediately and this is an ordinary send.
    const isQueued = queued && steering
    const pendingId = 'pending-' + Date.now()
    // Insert in server-confirmation order, not send order — see pendingSendOrder.
    const queue = insertPendingSend(pendingSends.get(sid) ?? [], {
      pendingId, wasStreaming, text, files, queued: isQueued,
    })
    pendingSends.set(sid, queue)
    if (steering) {
      // Mid-turn input: show above the composer, not in the scrollback, until
      // the server drains it into the running turn (mirrors TUI pendingSteer).
      pendingSteers = { ...pendingSteers, [sid]: [...(pendingSteers[sid] ?? []), { pendingId, text, files, queued: isQueued }] }
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
    ws.sendMessage(sid, text, files, false, isQueued)
    if (pin) pinToBottom()
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
    ws.sendMessage(sid, meta.text, meta.files, true, meta.queued)
    pinToBottom()
  }

  // Re-pin the scroll view to the bottom: reset the stick flag so the
  // ResizeObserver keeps us there, and scroll immediately in case the observer
  // doesn't fire (e.g. content height hasn't changed yet).
  function pinToBottom() {
    stick = true
    requestAnimationFrame(() => {
      if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight
    })
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

  // ── inline edit: turn a user message into an input, truncate history, resend ──
  let editingIndex = $state<number | null>(null)
  let editingDraft = $state('')
  let editingBusy = $state(false)

  function startEdit(index: number) {
    if (editingBusy) return
    const m = msgs[index]
    if (!m) return
    editingIndex = index
    editingDraft = m.content
  }

  function cancelEdit() {
    editingIndex = null
    editingDraft = ''
  }

  async function saveEdit() {
    const sid = get(activeSessionId)
    if (editingIndex == null || !sid || editingBusy) return
    const idx = editingIndex
    const content = editingDraft.trim()
    if (!content) return
    editingBusy = true
    try {
      // The server interrupts any in-flight turn, truncates history to just
      // before the message, and reruns with the edited prompt itself — no
      // resend from here (a resend would append the prompt a second time).
      await api.editMessage(sid, msgs[idx].messageIndex, content)
      // Server truncated history and reran — re-pin so the new reply streams
      // into view even if the user had scrolled up before editing.
      pinToBottom()
      editingIndex = null
      editingDraft = ''
    } catch (e: any) {
      showToast(e.message, 'error')
    } finally {
      editingBusy = false
    }
  }

  // Branch: copy the transcript through this reply into a fresh session and
  // switch to it. Nothing is sent — the fork just sits there waiting for
  // whatever the user wants to try instead.
  async function branchFrom(messageIndex: number) {
    const sid = get(activeSessionId)
    if (!sid || branchBusy) return
    branchBusy = true
    try {
      // message_index is an exclusive count, so +1 keeps this reply in the copy.
      const newSess = await api.branchSession(sid, messageIndex + 1)
      sessions.update(ss => [newSess, ...ss])
      activeSessionId.set(newSess.id)
    } catch (e: any) {
      showToast(e.message, 'error')
    } finally {
      branchBusy = false
    }
  }
</script>

<!-- Title/status/actions render inline in the page-spanning title bar
     (Header.svelte), not as this view's own row — registered via a store so
     that generic layout component can render a snippet it knows nothing
     about the internals of. -->
{#snippet chatHeader()}
  <div class="chat-header">
    <div class="title-row">
      <span class="session-title">
        {#if !id}{$t('nav.new_session')}{:else}{currentSession?.title ?? currentSession?.name ?? 'Chat'}{/if}
      </span>
      {#if currentSession?.branched_from}
        {@const src = $sessions.find(s => s.id === currentSession!.branched_from)}
        <span class="branched-label" title={src?.title ?? src?.name ?? currentSession.branched_from}>
          <iconify-icon icon="lucide:git-branch" width="12"></iconify-icon>
          {$t('chat.branched_from')} {src?.title ?? src?.name ?? currentSession.branched_from}
        </span>
      {/if}
      {#if streaming}
        <span class="state-pill running"><span class="state-dot"></span>{$t('status.running')}</span>
      {:else}
        <span class="state-pill"><span class="state-dot"></span>{$t('status.idle')}</span>
      {/if}
    </div>
    <div class="header-actions">
      <button class="hdr-btn" title={$t('chat.compact_tooltip')} disabled={!id || streaming} onclick={() => send('/compact')}>
        <iconify-icon icon="ant-design:compress-outlined" width="13"></iconify-icon>
        <span class="btn-label">{$t('chat.compact')}</span>
      </button>
      <button class="hdr-btn" title={$t('chat.export')} disabled={!id} onclick={enterExportMode}>
        <iconify-icon icon="ant-design:export-outlined" width="13"></iconify-icon>
        <span class="btn-label">{$t('chat.export')}</span>
      </button>
    </div>
  </div>
{/snippet}

<div class="chat-view" class:print-omit-tools={!exportIncludeTools}>
  <!-- Export mode bar: sticky format selector -->
  {#if inExportMode}
    <div class="export-bar">
      <div class="export-formats">
        <button class="export-fmt-btn" title={$t('chat.export_md')} disabled={exportBusy} onclick={() => exportByFormat('md')}>
          <iconify-icon icon="ant-design:file-markdown-outlined" width="16"></iconify-icon>
          <span>MD</span>
        </button>
        <button class="export-fmt-btn" title="{$t('chat.export_json')} — {$t('chat.export_json_full')}" disabled={exportBusy} onclick={() => exportByFormat('json')}>
          <iconify-icon icon="ant-design:file-text-outlined" width="16"></iconify-icon>
          <span>JSON</span>
        </button>
        <button class="export-fmt-btn" title="{$t('chat.export_pdf')} — {$t('chat.export_pdf_hint')}" disabled={exportBusy} onclick={() => exportByFormat('pdf')}>
          <iconify-icon icon="ant-design:file-pdf-outlined" width="16"></iconify-icon>
          <span>PDF</span>
        </button>
        <button class="export-fmt-btn" title={$t('chat.export_png')} disabled={exportBusy} onclick={() => exportByFormat('png')}>
          <iconify-icon icon="ant-design:file-image-outlined" width="16"></iconify-icon>
          <span>PNG</span>
        </button>
        <button class="export-fmt-btn" title={$t('chat.export_html')} disabled={exportBusy} onclick={() => exportByFormat('html')}>
          <iconify-icon icon="ant-design:html5-outlined" width="16"></iconify-icon>
          <span>HTML</span>
        </button>
      </div>
      <span class="export-count">{$t('chat.export_selected_count').replace('{n}', String(selectedIds.size))}</span>
      <label class="export-tools-toggle" title={$t('chat.export_include_tools')}>
        <input type="checkbox" bind:checked={exportIncludeTools} />
        <span>{$t('chat.export_include_tools')}</span>
      </label>
      <span style="margin-left:auto"></span>
      <button class="export-cancel-btn" onclick={exitExportMode} disabled={exportBusy}>{$t('chat.export_cancel')}</button>
    </div>
  {/if}

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
      <div class="messages-wrap">
      <div class="messages" bind:this={messagesEl}>
        <div class="messages-inner" class:no-session={!id} bind:this={innerEl}>

          <!-- Meta row for an assistant turn: rendered on the first assistant
               chunk after a user message (text/thinking/tools are separate
               entries, so later chunks of the same turn skip it). Time shows
               only when the entry carries createdAt — replayed history doesn't. -->
          {#snippet agentMeta(show: boolean, ts?: number)}
            {#if show}
              <div class="msg-meta">
                {#if boundAgent}
                  <span
                    class="meta-avatar expert"
                    aria-hidden="true"
                    style="background-color:{agentAvatarColor(boundAgentName)}22;color:{agentAvatarColor(boundAgentName)}"
                  >
                    {#if boundAgent.icon}
                      <iconify-icon icon={boundAgent.icon} width="13"></iconify-icon>
                    {:else}
                      {boundAgentName.charAt(0).toUpperCase()}
                    {/if}
                  </span>
                  <span class="meta-name">{boundAgentName}</span>
                {:else}
                  <span class="meta-avatar bot" aria-hidden="true"><OctoLogo size={22} /></span>
                  <span class="meta-name">Octo</span>
                {/if}
                {#if ts}<span class="meta-time">{fmtTime(ts)}</span>{/if}
              </div>
            {/if}
          {/snippet}

          <!-- Landing page. No session exists yet: the composer below collects
               the model, agent and working directory, and the first message is
               what actually creates the session (ensureActiveSession). -->
          {#if !id}
            <div class="landing">
              <span class="landing-mark"><OctoLogo size={44} /></span>
              <h1 class="landing-title">{$t('chat.landing_title')}</h1>
              <p class="landing-sub">{$t('chat.landing_sub')}</p>
              <div class="landing-cards">
                {#each starters as card (card.key)}
                  <button
                    type="button"
                    class="landing-card"
                    onclick={() => composer?.setText($t(`chat.starter_${card.key}_prompt`))}
                    title={$t(`chat.starter_${card.key}_prompt`)}
                  >
                    <span class="landing-card-icon" aria-hidden="true">
                      <iconify-icon icon={card.icon} width="17"></iconify-icon>
                    </span>
                    <span class="landing-card-title">{$t(`chat.starter_${card.key}_title`)}</span>
                  </button>
                {/each}
              </div>
            </div>
          {/if}

          {#each msgs as msg, i (msg.id)}
            {@const silentPanel = msg.type === 'user' ? silentActionPanel(msg) : null}
            {#if silentPanel}
              <!-- The user half of a silent turn. Acting on a panel is
                   manipulation of an object, not a thing said, so it draws
                   nothing in the transcript at all — feedback lives on the
                   panel itself (GenuiBlock's status chip). The message still
                   exists in history and in every export; only this view
                   elides it. -->
            {:else if isHiddenReply(i)}
              <!-- The model half of a silent turn: its single octo-ui fence is
                   projected into the panel above instead of drawn here. -->
            {:else if msg.type === 'user'}
              <!-- Right-aligned user bubble -->
              <div class="msg-row" class:export-mode={inExportMode} class:export-unselected={inExportMode && !selectedIds.has(msg.id)}>
                {#if inExportMode}
                  <label class="msg-checkbox">
                    <input type="checkbox" checked={selectedIds.has(msg.id)} onchange={() => toggleSelect(msg.id)} />
                  </label>
                {/if}
                <div class="msg-user fadein" id={`msg-${msg.id}`}>
                <div class="msg-meta">
                  <span class="meta-avatar user" aria-hidden="true">
                    <iconify-icon icon="ant-design:user-outlined" width="13"></iconify-icon>
                  </span>
                  <span class="meta-name">{$t('chat.you')}</span>
                  {#if msg.createdAt}<span class="meta-time">{fmtTime(msg.createdAt)}</span>{/if}
                </div>
                <div class="user-card-wrap">
                  <div class="user-card" class:pending={msg.pending}>
                    {#if msg.files && msg.files.length > 0}
                      <div class="msg-attachments">
                        {#each msg.files as f}
                          {#if f.mime_type?.startsWith('image/')}
                            <img src={f.data_url} alt={f.name} class="msg-image" onclick={() => { lightboxSrc = f.data_url }} />
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
                            <img src={ref} alt="attachment" class="msg-image" onclick={() => { lightboxSrc = ref }} />
                          {/if}
                        {/each}
                      </div>
                    {/if}
                    {#if editingIndex === i}
                      <textarea
                        class="inline-edit-input"
                        bind:value={editingDraft}
                        rows={Math.max(2, editingDraft.split('\n').length)}
                        disabled={editingBusy}
                        onkeydown={(e) => {
                          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); saveEdit() }
                          if (e.key === 'Escape') { e.preventDefault(); cancelEdit() }
                        }}
                      ></textarea>
                    {:else}
                      {@const genuiAction = msg.content ? parseActionEnvelope(msg.content) : null}
                      {#if genuiAction}
                        <!-- Compact chip for a synthetic [octo-ui-action] turn — the
                             raw JSON convention text would otherwise leak into the
                             bubble verbatim. Falls back to plain text above if the
                             prefix matches but parsing fails, so a hand-typed
                             message that happens to start the same way never shows
                             a broken chip. -->
                        <details class="genui-action-chip">
                          <summary>
                            <iconify-icon icon="ant-design:thunderbolt-outlined" width="12"></iconify-icon>
                            {$t('chat.genui_action_triggered')}: {genuiAction.action}
                          </summary>
                          <pre class="genui-action-json">{JSON.stringify(genuiAction, null, 2)}</pre>
                        </details>
                      {:else if msg.content}
                        {msg.content}
                      {/if}
                    {/if}
                    {#if msg.pending}<span class="pending-spinner" title={$t('status.running')}></span>{/if}
                  </div>
                  {#if editingIndex === i}
                    <div class="msg-actions editing-actions">
                      <button class="action-btn" title={$t('chat.cancel')} onclick={cancelEdit} disabled={editingBusy}>
                        <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
                      </button>
                      <button class="action-btn" title={$t('chat.send')} onclick={saveEdit} disabled={editingBusy || !editingDraft.trim()}>
                        <iconify-icon icon="ant-design:check-outlined" width="13"></iconify-icon>
                      </button>
                    </div>
                  {:else}
                    <div class="msg-actions">
                      <!-- Editable even mid-stream: confirming the edit has the
                           server interrupt the in-flight turn before rerunning,
                           so the button needs no streaming gate. -->
                      <button class="action-btn" title={$t('chat.edit')} onclick={() => startEdit(i)}>
                        <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
                      </button>
                      <button class="action-btn" title={$t('chat.copy')} onclick={() => navigator.clipboard.writeText(msg.content)}>
                        <iconify-icon icon="ant-design:copy-outlined" width="13"></iconify-icon>
                      </button>
                    </div>
                  {/if}
                </div>
              </div>
              </div>

            {:else if msg.type === 'assistant'}
              <!-- Assistant message with avatar -->
              <div class="msg-row" class:export-mode={inExportMode} class:export-unselected={inExportMode && !selectedIds.has(msg.id)}>
                {#if inExportMode}
                  <label class="msg-checkbox">
                    <input type="checkbox" checked={selectedIds.has(msg.id)} onchange={() => toggleSelect(msg.id)} />
                  </label>
                {/if}
                <div class="msg-agent fadein">
                {@render agentMeta(i === 0 || msgs[i - 1]?.type === 'user', msg.createdAt)}
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

                  <!-- Rendered markdown / inline octo-ui content. A message with
                       no ```octo-ui fence yields exactly one markdown segment
                       (splitOctoUiFences' no-op path), so this renders
                       byte-identically to before for every pre-existing message. -->
                  {#if msg.content}
                    <div
                      class="rich-answer"
                      use:setupAssistantEl
                    >
                      {#each throttledSegments(msg.id, msg.content, msg.streaming) as seg, segIdx (segIdx)}
                        {#if seg.kind === 'markdown'}
                          {@html throttledMarkdown(`${msg.id}:${segIdx}`, seg.text, msg.streaming, showReasoning)}
                        {:else if seg.spec && isAnchor(panels, seg.spec, msg.id, segIdx)}
                          <!-- An anonymous panel renders its own spec where it
                               sits. An addressable one renders the newest
                               version of itself, which a silent turn may have
                               replaced since this message was written. -->
                          {@const live = seg.spec.id ? (panels.get(seg.spec.id)?.spec ?? seg.spec) : seg.spec}
                          <GenuiBlock
                            spec={live}
                            interactive={seg.complete}
                            pending={!!seg.spec.id && pendingPanel === seg.spec.id}
                            stats={seg.spec.id ? panelTurnStats[`${id ?? ''}\x00${seg.spec.id}`] : undefined}
                            sessionId={id ?? ''}
                            onaction={(a) => sendGenuiAction(a, seg.spec?.id)}
                          />
                        {/if}
                        <!-- seg.kind === 'octo-ui' && seg.spec === null: the guard
                             rejected everything in this fence, or nothing is
                             safe to render yet — the design's documented
                             fallback is to render nothing, not raw text. -->
                      {/each}
                    </div>
                  {/if}

                  <!-- Streaming caret — only while text is actively arriving
                       (see typingActive), so it doesn't blink under a finished
                       reply while the model silently generates the next step. -->
                  {#if msg.streaming && typingActive}
                    <span class="caret"></span>
                  {/if}

                  <!-- Message actions -->
                  {#if !msg.streaming}
                    <div class="msg-actions reply-actions">
                      <!-- Branch only on a reply that closed a turn: an
                           intermediate tool-round bubble carries no
                           messageIndex, and forking there would orphan the
                           tool calls it hasn't answered yet. -->
                      {#if typeof msg.messageIndex === 'number'}
                        <button class="action-btn" title={$t('chat.branch')} disabled={branchBusy} onclick={() => branchFrom(msg.messageIndex)}>
                          <iconify-icon icon="lucide:git-branch" width="14"></iconify-icon>
                        </button>
                      {/if}
                      <button class="action-btn" title={$t('chat.copy')} onclick={() => navigator.clipboard.writeText(msg.content)}>
                        <iconify-icon icon="ant-design:copy-outlined" width="14"></iconify-icon>
                      </button>
                    </div>
                  {/if}
                </div>
              </div>
              </div>

            {:else if msg.type === 'thinking' && showReasoning}
              <!-- Standalone Thoughts segment (reasoning before a tool round) -->
              <div class="msg-agent fadein">
                {@render agentMeta(i === 0 || msgs[i - 1]?.type === 'user', msg.createdAt)}
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
                {@render agentMeta(i === 0 || msgs[i - 1]?.type === 'user', msg.createdAt)}
                <div class="agent-content">
                  <ToolGroup tools={msg.tools} streaming={msg.streaming} />
                </div>
              </div>

            {:else if msg.type === 'progress'}
              <!-- Inline progress message -->
              <div class="msg-agent fadein">
                <div class="thinking-indicator">
                  <iconify-icon icon="ant-design:loading-outlined" width="15" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                  <span>{msg.content || $t('chat.thinking')}</span>
                </div>
              </div>

            {:else if msg.type === 'notice'}
              <!-- Inline scrollback notice (background process completion, etc.) -->
              <div class="msg-agent fadein">
                <div class="notice-row">
                  <span class="notice-avatar" data-level={msg.level}>
                    <iconify-icon icon="lucide:info" width="14"></iconify-icon>
                  </span>
                  <div class="notice-line" data-level={msg.level}>{@html renderMarkdown(msg.content, showReasoning)}</div>
                </div>
              </div>
            {/if}
          {/each}

          <!-- Live sub-agents panel (current turn) -->
          {#if subAgents.length > 0}
            <!-- |global so the fade plays when this whole {#if} block unmounts
                 (the common auto-dismiss case: the last background sub-agent is
                 removed and the array empties). A default local transition would
                 only play on an {#each}-item removal inside a still-mounted card. -->
            <div class="msg-agent fadein" out:fade|global={{ duration: 250 }}>
              <div class="agent-content">
                <SubAgentsCard agents={subAgents} elapsed={subAgentsElapsed} />
              </div>
            </div>
          {/if}

          <!-- Live thinking block while streaming -->
          {#if streaming && thinking && showReasoning}
            <div class="msg-agent fadein">
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
              <div class="thinking-indicator">
                <iconify-icon icon="ant-design:loading-outlined" width="15" style="color:var(--blue-6);animation:octo-spin 0.8s linear infinite"></iconify-icon>
                <span>{thinkingLabel}</span>
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

        <!-- Pending steer messages (mid-turn input) — shown above the composer
             as ghost text lines so they don't break the chronological order
             of the scrollback while waiting to be drained. -->
        {#if pendingSteerList.length > 0}
          <div class="pending-steer-bar fadein">
            {#each pendingSteerList as s}
              <div class="pending-steer-line">
                <!-- Only a queued message is labelled: it waits for the turn AFTER
                     this one, so without the badge it would be indistinguishable
                     from a steer about to be folded into the reply being written. -->
                {#if s.queued}
                  <span class="steer-kind queued">{$t('chat.pending_queued')}</span>
                {/if}
                <span class="steer-text">
                  {#if s.files && s.files.length > 0}
                    <span class="steer-attachments">
                      {#each s.files as f}
                        {f.name}
                      {/each}
                    </span>
                  {/if}
                  {#if s.text}{s.text}{/if}
                </span>
                <span class="pending-spinner" title={$t('status.running')}></span>
                <button
                  class="steer-retract"
                  title={$t('chat.steer_retract')}
                  disabled={s.retracting}
                  onclick={() => retractSteer(s.pendingId)}
                >
                  <iconify-icon icon="ant-design:edit-outlined" width="12"></iconify-icon>
                </button>
              </div>
            {/each}
          </div>
        {/if}
      </div>

      {#if railTicks.length > 1}
        <div class="msg-rail">
          <div class="msg-rail-inner" style="height:min(100%, {railTicks.length * 28 - 20}px);--rail-scale:{railScale}">
            <div class="msg-rail-track"></div>
            <div class="msg-rail-fill" style="height:calc((100% - 8px) * {railFillPct / 100})"></div>
            {#each railTicks as tick, i (tick.id)}
              <button
                type="button"
                class="msg-rail-node"
                class:passed={i < railActive}
                class:active={i === railActive}
                onclick={() => jumpToMessage(tick.id)}
                aria-current={i === railActive ? 'true' : undefined}
                aria-label={tick.preview ? `${$t('chat.jump_to_message')}: ${tick.preview}` : $t('chat.jump_to_message')}
              >
                <span class="msg-rail-dot"></span>
                <span class="msg-rail-tip" role="tooltip">
                  <span class="msg-rail-tip-text">{tick.preview}</span>
                </span>
              </button>
            {/each}
          </div>
        </div>
      {/if}
      </div>

      <!-- Background workflows panel (persists across turns, pinned above composer) -->
      {#if workflows.length > 0}
        <div class="workflows-bar fadein">
          <WorkflowsCard runs={workflows} {now} />
        </div>
      {/if}

      <!-- Background processes: a text trigger opening a popover list -->
      {#if bgTasks && bgTasks.length > 0}
        <BackgroundProcesses tasks={bgTasks} {now} />
      {/if}

      <!-- Question banner (aligned with composer) -->
      <QuestionModal />

      <!-- Vision helper progress: transient, cleared by done/failed -->
      {#if describingImage}
        <div class="vision-describing">
          <iconify-icon icon="ant-design:eye-outlined" width="14"></iconify-icon>
          <span>
            {describingImage.total > 1
              ? tr('chat.describing_image_n')
                  .replace('{name}', describingImage.name)
                  .replace('{index}', String(describingImage.index))
                  .replace('{total}', String(describingImage.total))
              : tr('chat.describing_image').replace('{name}', describingImage.name)}
          </span>
        </div>
      {/if}

      <!-- Turn-error banner: persists until dismissed or new message -->
      {#if turnError}
        <div class="turn-error-banner transition:fade|100">
          <span class="turn-error-text">{turnError}</span>
          <button class="turn-error-dismiss" onclick={() => turnError = null}
            aria-label="{$t('common.close')}">
            <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
          </button>
        </div>
      {/if}

      <!-- Composer -->
      <Composer bind:this={composer} onSend={send} />
    </div>
  </div>
</div>

<!-- Image lightbox: click any message attachment thumbnail to view full-size -->
{#if lightboxSrc}
  <div class="lightbox-overlay" onclick={() => { lightboxSrc = null }}>
    <button class="lightbox-close" aria-label={$t('chat.image_close')} onclick={() => { lightboxSrc = null }}>
      <iconify-icon icon="ant-design:close-outlined" width="18"></iconify-icon>
    </button>
    <img src={lightboxSrc} alt="" class="lightbox-image" onclick={() => { lightboxSrc = null }} />
  </div>
{/if}

<svelte:window onkeydown={(e) => {
  if (e.key === 'Escape' && inExportMode) { e.preventDefault(); exitExportMode(); return }
  if (e.key === 'Escape' && lightboxSrc) lightboxSrc = null
}} />

<style>
/* ── Layout ──────────────────────────────────────────────────────────────── */
.chat-view { flex: 1; display: flex; flex-direction: column; min-height: 0; }

/* ── Header ──────────────────────────────────────────────────────────────── */
/* Rendered inside the main column's own top row (Header.svelte's slot), so it
   carries no border or background of its own — the layout's only lines are the
   vertical dividers between columns. Horizontal padding is 0 for the same
   reason: that row already pads itself. */
.chat-header {
  flex: 0 0 auto;
  padding: 7px 0; display: flex; align-items: center; justify-content: space-between; gap: 16px;
  container-type: inline-size;
}
.title-row { display: flex; align-items: center; gap: 10px; min-width: 0; }
.session-title {
  font-size: 15px; font-weight: 600; color: var(--text-heading);
  min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.state-pill {
  display: inline-flex; align-items: center; gap: 5px; padding: 2px 9px;
  border-radius: var(--radius-pill); background: var(--hover-neutral);
  font-size: 11px; font-weight: 500; color: var(--text-secondary); white-space: nowrap;
}
.state-pill .state-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--text-secondary); }
.state-pill.running { background: var(--active-blue-bg); color: var(--blue-6); }
.state-pill.running .state-dot { background: var(--blue-6); animation: octo-pulse 1.2s infinite; }
@keyframes octo-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
.header-actions { display: flex; align-items: center; gap: 8px; flex: none; }
.hdr-btn {
  height: 30px; padding: 0 11px; border: none; background: transparent;
  border-radius: var(--radius-sm); display: flex; align-items: center; gap: 6px; white-space: nowrap;
  font-size: 12px; font-weight: 500; color: var(--text-secondary); cursor: pointer; font-family: inherit;
  transition: 0.12s;
}
/* Not enough room for labels: keep the icons (tooltips carry the meaning). */
@container (max-width: 680px) {
  .btn-label { display: none; }
  .hdr-btn { padding: 0 8px; }
}
.hdr-btn:hover { background: var(--hover-neutral); color: var(--text); }
.hdr-btn:disabled { opacity: 0.5; cursor: not-allowed; color: var(--text-quaternary); }

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

/* ── Export bar ──────────────────────────────────────────────────────────── */
.export-bar {
  flex: 0 0 auto; display: flex; align-items: center; gap: 12px;
  padding: 8px 20px; background: var(--bg-layout); border-bottom: 1px solid var(--border);
  position: sticky; top: 0; z-index: 10;
}
.export-formats { display: flex; align-items: center; gap: 4px; }
.export-fmt-btn {
  display: flex; align-items: center; gap: 4px; padding: 5px 10px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: var(--radius-sm); font-size: 12px; font-weight: 500;
  color: var(--text); cursor: pointer; font-family: inherit;
  transition: 0.12s;
}
.export-fmt-btn:hover { background: var(--bg-table-header); border-color: var(--text-quaternary); }
.export-fmt-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.export-count {
  font-size: 12px; color: var(--text-secondary); white-space: nowrap;
}
.export-tools-toggle {
  display: flex; align-items: center; gap: 5px; font-size: 12px;
  color: var(--text-secondary); cursor: pointer; user-select: none;
}
.export-tools-toggle input[type="checkbox"] { cursor: pointer; }
.export-cancel-btn {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: var(--radius-sm); font-size: 12px; font-weight: 500;
  color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.export-cancel-btn:hover { border-color: var(--text-quaternary); color: var(--text); }

/* ── Export mode message rows ────────────────────────────────────────────── */
.msg-row { display: flex; align-items: flex-start; gap: 0; }
.msg-row.export-mode { gap: 8px; }
.msg-row > :not(.msg-checkbox) { flex: 1; min-width: 0; }
.msg-checkbox {
  flex: none; display: flex; align-items: flex-start; padding-top: 28px;
  cursor: pointer;
}
.msg-checkbox input[type="checkbox"] {
  width: 16px; height: 16px; cursor: pointer; accent-color: var(--blue-6);
}

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
  max-height: min(320px, calc(40vh / var(--font-zoom)));
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
  /* Safety net: on short viewports the header/banners/plan-card/composer
     (all flex: 0 0 auto) can together exceed the available height even after
     .messages shrinks to 0. Without this, the excess silently overflows past
     every ancestor up to .app's `overflow: hidden` and clips the composer
     with no way to recover it. This lets the conversation column itself
     scroll into view when that happens. */
  overflow-y: auto;
}
.workflows-bar {
  flex: 0 0 auto;
  max-width: var(--chat-content-max-width); margin: 0 auto; width: 100%;
  padding: 0 24px 12px;
}
.messages-wrap {
  flex: 1; display: flex; min-height: 0; position: relative;
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

/* ── User-message timeline rail ──────────────────────────────────────────── */
/* A floating vertical timeline in the right-hand gutter: one node per user
   message at its real rendered offset, a grey track behind them, and a blue
   progress line that fills to the current scroll position. Hovering a node
   reveals a dark preview card; clicking smooth-scrolls to that message. */
.msg-rail {
  /* Sit in the middle of the empty gutter to the right of the centered message
     column — halfway between the column edge and the pane edge — so it has
     breathing room on both sides instead of crowding the bubbles. Falls back
     to a fixed offset on narrow panes, staying clear of the native scrollbar
     track (app.css sets it to 8px). Vertically centers the node column within
     the conversation area. */
  position: absolute; top: 8px; bottom: 8px; width: 20px;
  right: max(22px, calc((100% - var(--chat-content-max-width)) / 4));
  pointer-events: none; z-index: 8;
  display: flex; align-items: center; justify-content: center;
}
/* Evenly-spaced node column, centered; track + fill are scoped to its height.
   Height is set inline to min(100%, natural length) — natural = 8px per node +
   20px per gap (28 * N - 20) — so on short panes the column compresses evenly
   via space-between instead of keeping its natural length and spilling past
   the conversation area. */
.msg-rail-inner {
  position: relative;
  display: flex; flex-direction: column; align-items: center;
  justify-content: space-between;
}
/* Background track (node-column height) + blue progress fill (top → scroll). */
.msg-rail-track, .msg-rail-fill {
  position: absolute; left: 50%; top: 4px; width: 2px;
  transform: translateX(-50%); border-radius: 9999px;
}
.msg-rail-track { bottom: 4px; background: var(--border); }
.msg-rail-fill {
  background: linear-gradient(var(--blue-5), var(--blue-6));
  transition: height 0.14s linear;
}
/* Node = generous 20×8 hit area wrapping a small dot, laid out in the flow of
   the evenly-spaced column. */
.msg-rail-node {
  position: relative; width: 20px; height: 8px;
  /* Allow shrinking below the 8px hit area on extremely short panes with many
     nodes (flex's min-height:auto would otherwise floor each node at 8px and
     let the column overflow the pane again). Dots may overlap; the column
     stays bounded. */
  min-height: 0;
  padding: 0; margin: 0; border: none; background: none;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; pointer-events: auto; z-index: 2;
}
.msg-rail-node:focus-visible { outline: none; }
/* Dots shrink with the column's compression (--rail-scale, 1 on panes tall
   enough for the natural length) so a crammed rail reads as a smaller minimap
   instead of full-size dots touching. Floored so they stay visible. */
.msg-rail-dot {
  width: max(6px, calc(10px * var(--rail-scale, 1)));
  height: max(6px, calc(10px * var(--rail-scale, 1)));
  border-radius: 9999px;
  background: var(--bg-container);
  /* Border and (below) the active ring scale too: at the 6px floor a fixed 2px
     border would leave almost no fill, and a fixed 4px ring would cover
     neighboring dots. */
  border: max(1.5px, calc(2px * var(--rail-scale, 1))) solid var(--border);
  transition: width 0.14s ease, height 0.14s ease, background 0.14s ease,
    border-color 0.14s ease, box-shadow 0.14s ease, opacity 0.14s ease;
}
/* Passed + active nodes are filled blue; tone them down slightly so they do
   not dominate the message column. */
.msg-rail-node.passed .msg-rail-dot {
  background: var(--blue-5); border-color: var(--blue-5);
}
.msg-rail-node.active .msg-rail-dot {
  background: var(--blue-6); border-color: var(--blue-6); opacity: 0.9;
  width: max(8px, calc(12px * var(--rail-scale, 1)));
  height: max(8px, calc(12px * var(--rail-scale, 1)));
  box-shadow: 0 0 0 max(2px, calc(4px * var(--rail-scale, 1))) var(--focus-ring);
}
/* Hover/focus preview card, to the left of the rail, with a caret pointing back
   at the dot. --terminal-bg is the DS's intentionally-dark surface in both
   themes, matching the design's #1F1F1F tooltip. */
.msg-rail-tip {
  position: absolute; right: calc(100% + 14px); top: 50%;
  transform: translateY(-50%);
  display: none; flex-direction: column;
  background: var(--terminal-bg); color: var(--terminal-text);
  border-radius: 10px; padding: 9px 13px;
  max-width: 260px; width: max-content;
  box-shadow: 0 8px 24px rgba(15,23,42,0.18);
  pointer-events: none; z-index: 20;
  animation: tlfade 0.14s ease;
}
.msg-rail-node:hover .msg-rail-tip,
.msg-rail-node:focus-visible .msg-rail-tip { display: flex; }
.msg-rail-tip::after {
  content: ''; position: absolute; left: 100%; top: 50%;
  transform: translateY(-50%);
  border: 6px solid transparent; border-left-color: var(--terminal-bg);
}
.msg-rail-tip-text {
  font-size: 12.5px; line-height: 1.5;
  display: -webkit-box; -webkit-line-clamp: 4; line-clamp: 4; -webkit-box-orient: vertical;
  overflow: hidden; word-break: break-word;
}
@keyframes tlfade {
  from { opacity: 0; transform: translateY(-50%) translateX(6px); }
  to { opacity: 1; transform: translateY(-50%) translateX(0); }
}
@media (prefers-reduced-motion: reduce) {
  .msg-rail-fill, .msg-rail-dot { transition: none; }
  .msg-rail-tip { animation: none; }
}
.messages-inner {
  max-width: var(--chat-content-max-width); margin: 0 auto;
  padding: 24px 24px 16px; display: flex; flex-direction: column; gap: 20px;
}
/* Landing only: nothing to scroll yet, so fill the scroll viewport instead of
   collapsing to the content height — that is what lets .landing centre itself
   vertically rather than sitting pinned to the top with all the slack below. */
.messages-inner.no-session { min-height: 100%; box-sizing: border-box; }

/* ── Landing page (no session yet) ───────────────────────────────────────── */
/* Sits in the empty scroll area above the composer rather than replacing it,
   so the composer stays exactly where it is once the first message lands and
   the conversation starts scrolling in above it. */
.landing {
  flex: 1 1 auto; min-height: 0;
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 10px; padding: 48px 0 24px; text-align: center;
}
/* OctoLogo's plate is fill="currentColor" while the octopus itself is a hard
   #fff, so this has to name a colour: inheriting --text made the plate near-white
   in dark mode and swallowed the mark. Brand blue, same as the sidebar logo. */
.landing-mark { display: flex; color: var(--blue-6); }
.landing-title { margin: 0; font-size: 26px; font-weight: 600; color: var(--text); }
.landing-sub { margin: 0; font-size: 13px; color: var(--text-tertiary); max-width: 460px; }

/* Starter cards: a grid of ways in, so the blank page suggests what Octo is
   for instead of showing an empty column above the composer. */
.landing-cards {
  display: grid; grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px; width: 100%; margin-top: 14px;
}
.landing-card {
  display: flex; flex-direction: column; align-items: flex-start; gap: 12px;
  min-height: 92px; padding: 13px 14px;
  background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius-card);
  box-shadow: var(--card-shadow);
  font-family: inherit; text-align: left; cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}
.landing-card:hover { border-color: var(--blue-2); background: var(--surface-info); }
.landing-card:focus-visible { outline: 2px solid var(--blue-6); outline-offset: 1px; }
.landing-card-icon { display: flex; color: var(--blue-6); }
.landing-card-title {
  font-size: 13px; font-weight: 500; line-height: 1.45; color: var(--text);
}
@media (max-width: 620px) {
  .landing-cards { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

/* ── Message meta row (avatar · name · time) ─────────────────────────────── */
.msg-meta { display: flex; align-items: center; gap: 8px; }
.meta-avatar {
  width: 22px; height: 22px; flex: 0 0 22px; border-radius: 6px;
  display: grid; place-items: center; color: var(--on-accent); overflow: hidden;
}
.meta-avatar.user { background: var(--text-quaternary); }
/* The logo is already an app-icon style mark (blue rounded square); no well. */
.meta-avatar.bot { background: transparent; color: var(--blue-6); }
.meta-avatar.bot :global(svg) { width: 22px; height: 22px; }
.meta-avatar.expert { font-size: 11px; font-weight: 600; }
.meta-name { font-size: 13px; font-weight: 600; color: var(--text-heading); }
.meta-time { font-size: 11px; color: var(--text-secondary); }

/* ── User message ────────────────────────────────────────────────────────── */
.msg-user { display: flex; flex-direction: column; gap: 9px; align-items: flex-end; }
/* Right-side chat layout: meta row reads "time · name · avatar" left→right. */
.msg-user .msg-meta { flex-direction: row-reverse; }
.user-card-wrap { display: flex; flex-direction: column; gap: 4px; max-width: 80%; }
.user-card {
  /* No border, no shadow: the fill against the page's own --bg-layout is
     enough contrast to read as a distinct block without a drawn edge. */
  background: var(--bg-container);
  border-radius: 14px; padding: 14px 16px;
  font-size: 13px; line-height: 1.65; color: var(--text);
  white-space: pre-wrap; word-break: break-word;
  display: flex; flex-direction: column; gap: 8px;
}
/* Pending (queued) card — an optimistic echo, or a steer message waiting to
   be drained into the running turn. Dimmed with a small spinner until the
   server confirms it via history_user_message. */
.user-card.pending { opacity: 0.65; }
/* Compact chip for a synthetic [octo-ui-action] turn (see
   parseActionEnvelope) — the JSON body sits behind a click-to-expand
   <details>, so a genui button click doesn't leak raw JSON into the
   transcript at a glance. */
.genui-action-chip { font-size: 13px; color: var(--text); }
.genui-action-chip summary {
  display: inline-flex; align-items: center; gap: 5px; cursor: pointer;
  color: var(--text-secondary); list-style: none;
}
.genui-action-chip summary::-webkit-details-marker { display: none; }
.genui-action-json {
  margin: 6px 0 0; padding: 8px 10px; border-radius: var(--radius-xs, 6px);
  background: var(--bg-layout); border: 1px solid var(--border);
  font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  white-space: pre-wrap; word-break: break-word; color: var(--text-secondary);
}
.pending-spinner {
  display: inline-block; width: 10px; height: 10px; margin-left: 6px;
  vertical-align: -1px; border-radius: 50%;
  border: 1.5px solid var(--blue-2); border-top-color: var(--blue-6);
  animation: octo-spin 0.8s linear infinite;
}

/* ── Inline attachments inside user cards ────────────────────────────────── */
.msg-attachments { display: flex; flex-wrap: wrap; gap: 8px; }
.msg-image { max-width: 100%; max-height: 320px; border-radius: 8px; border: 1px solid var(--border); cursor: zoom-in; }

.lightbox-overlay {
  position: fixed; inset: 0; background: var(--scrim); z-index: 2000;
  display: flex; align-items: center; justify-content: center; padding: 40px;
}
.lightbox-image {
  max-width: 100%; max-height: 100%; border-radius: 8px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4); cursor: zoom-out;
}
.lightbox-close {
  position: fixed; top: 20px; right: 20px; background: rgba(0, 0, 0, 0.4); border: none;
  color: #fff; width: 36px; height: 36px; border-radius: 50%; cursor: pointer;
  display: flex; align-items: center; justify-content: center;
}
.lightbox-close:hover { background: rgba(0, 0, 0, 0.6); }

/* ── Agent message ───────────────────────────────────────────────────────── */
.msg-agent { display: flex; flex-direction: column; gap: 12px; }
.agent-content { min-width: 0; display: flex; flex-direction: column; gap: 12px; }

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
:global(.rich-answer :not(pre) > code), :global(.think-body :not(pre) > code) {
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
/* Markdown tables: marked emits plain <table>/<th>/<td> (no classes), so style
   the elements directly. Keep wide tables scrollable inside the bubble instead
   of blowing up the flex column, and give cells real padding + borders so they
   don't read as one squeezed wall of text. */
:global(.rich-answer .table-scroll), :global(.think-body .table-scroll) { overflow-x: auto; }
:global(.rich-answer table), :global(.think-body table) {
  width: max-content; min-width: 100%; max-width: none;
  border-collapse: collapse; border-spacing: 0; font-size: 13.5px; line-height: 1.55;
}
:global(.rich-answer th), :global(.rich-answer td),
:global(.think-body th), :global(.think-body td) {
  padding: 7px 14px; text-align: left; vertical-align: top;
  border: 1px solid var(--border-table);
}
:global(.rich-answer th), :global(.think-body th) {
  background: var(--bg-table-header); font-weight: 600; color: var(--text-heading); white-space: nowrap;
}
:global(.rich-answer tbody tr:nth-child(even) td), :global(.think-body tbody tr:nth-child(even) td) {
  background: var(--bg-zebra);
}
/* Reasoning card — a bordered fold matching the design's tool-card look. */
:global(.think-block) {
  border: 1px solid var(--border); border-radius: 10px;
  background: var(--bg-container); box-shadow: var(--card-shadow);
}
:global(.think-summary) {
  list-style: none; display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; cursor: pointer; user-select: none;
  font-size: 12px; color: var(--text-secondary);
}
:global(.think-summary > span:first-of-type) { font-weight: 600; color: var(--text); font-size: 13px; }
:global(.think-summary::-webkit-details-marker) { display: none; }
:global(.think-summary:hover) { background: var(--hover-neutral); border-radius: 10px; }
:global(.think-body) {
  margin: 0 12px 10px; padding-left: 12px; border-left: 2px solid var(--border-secondary);
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
.user-card-wrap:hover .action-btn,
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
.notice-row { display: flex; align-items: flex-start; gap: 10px; }
.notice-avatar {
  width: 22px; height: 22px; flex: 0 0 22px;
  display: grid; place-items: center;
  background: transparent;
  color: var(--text-tertiary);
}
.notice-avatar[data-level="success"] { color: var(--success); }
.notice-avatar[data-level="warning"] { color: var(--warning); }
.notice-avatar[data-level="error"] { color: var(--error); }
.notice-avatar[data-level="info"] { color: var(--text-secondary); }
.notice-line {
  display: flex; align-items: center; gap: 8px; min-height: 28px;
  font-size: 13px; color: var(--text-secondary);
}
.notice-line[data-level="success"] { color: var(--success); }
.notice-line[data-level="warning"] { color: var(--warning); }
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
  position: sticky; bottom: 0; z-index: 2;
  max-width: var(--chat-content-max-width); margin: 0 auto; width: 100%;
  padding: 0 24px 10px;
  display: flex; flex-direction: column;
  gap: 4px;
}
.pending-steer-line {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  line-height: 1.3;
  color: var(--text-tertiary);
  opacity: 0.7;
}
.steer-text {
  white-space: pre-wrap; word-break: break-word;
}
.steer-kind {
  flex-shrink: 0;
  font-size: 11px;
  padding: 1px 5px;
  border-radius: 4px;
  border: 1px solid var(--border-secondary);
  color: var(--text-quaternary);
}
.steer-kind.queued {
  border-color: var(--blue-6);
  color: var(--blue-6);
}
.steer-retract {
  width: 20px; height: 20px; border: none; background: transparent;
  border-radius: 4px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-quaternary); flex-shrink: 0;
}
.steer-retract:hover { color: var(--text-secondary); background: var(--hover-neutral); }
.steer-attachments {
  font-size: 12px;
  color: var(--text-quaternary);
}

/* ── Fade-in ─────────────────────────────────────────────────────────────── */
.fadein { animation: octo-fadein 0.25s ease; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

/* ── Inline message edit ───────────────────────────────────────────────── */
.inline-edit-input {
  width: 100%; min-width: 720px; border: 1px solid var(--blue-5); border-radius: 8px;
  padding: 10px 12px; font-size: 14px; font-family: inherit; resize: vertical;
  background: var(--bg-container); color: var(--text); box-sizing: border-box;
  outline: none;
}
.inline-edit-input:focus { box-shadow: 0 0 0 2px var(--blue-5-alpha, rgba(59,130,246,0.2)); }
.editing-actions { opacity: 1 !important; }

/* ── Branched-from label ────────────────────────────────────────────────── */
.branched-label {
  display: inline-flex; align-items: center; gap: 4px; font-size: 12px;
  color: var(--text-tertiary); cursor: help;
  min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}

/* ── Turn-error banner ──────────────────────────────────────────────────── */
.vision-describing {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 24px;
  margin: 0 24px 10px;
  background: var(--bg-subtle, var(--active-blue-bg));
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.5;
  max-width: var(--chat-content-max-width);
  margin-left: auto; margin-right: auto;
  width: 100%;
  box-sizing: border-box;
}
.turn-error-banner {
  display: flex; align-items: center; gap: 10px;
  padding: 10px 24px;
  margin: 0 24px 10px;
  background: var(--error-bg);
  border: 1px solid var(--error-border);
  border-radius: 8px;
  font-size: 13px;
  color: var(--error);
  line-height: 1.5;
  max-width: var(--chat-content-max-width);
  margin-left: auto; margin-right: auto;
  width: 100%;
  box-sizing: border-box;
}
.turn-error-text {
  flex: 1;
  word-break: break-word;
}
.turn-error-dismiss {
  display: flex; align-items: center; justify-content: center;
  background: none; border: none; cursor: pointer;
  color: var(--error); opacity: 0.6; padding: 2px;
  flex-shrink: 0;
}
.turn-error-dismiss:hover { opacity: 1; }
</style>
