import { writable, derived, get } from 'svelte/store'
import type { Session, Skill, ScheduledTask, McpServer, Channel, Memory, Artifact, ArtifactView } from './types'
import * as api from './api'

// First-run gate. 'unknown' until /api/onboard/status resolves (render a splash,
// never flash the main UI); 'key_setup' blocks on the setup panel; 'soul_setup'
// boots normally then auto-launches an /onboard chat; '' means fully configured.
export const onboardPhase = writable<'unknown' | 'key_setup' | 'soul_setup' | ''>('unknown')

// Navigation
export const view = writable('chat')
export const sidebar = writable('full')
export const cmdkOpen = writable(false)
export const mcpModalOpen = writable(false)
// Drives the MCP modal: add a new server, edit an existing one, or paste JSON.
export const mcpModalState = writable<{ mode: 'add' | 'edit' | 'import'; server?: any }>({ mode: 'add' })
export const toast = writable<{ msg: string; type: string } | null>(null)

// Runtime / WS state
export const running = writable(false)
export const wsDown = writable(false)

// Artifacts panel
export const artifactsOpen = writable(false)
export const artifacts = writable<Artifact[]>([])
export const artifactSel = writable(0)
export const artifactView = writable<ArtifactView>('preview')

// Sessions
export const sessions = writable<Session[]>([])
export const activeSessionId = writable<string | null>(null)
// Sidebar session UI state
export const activeSession = writable<string | null>(null)
export const selMode = writable(false)
export const sel = writable<Record<string, boolean>>({})
export const menuFor = writable<string | null>(null)
export const editId = writable<string | null>(null)
export const editDraft = writable('')

// Per-session chat state (keyed by sessionId)
export const chatMessages = writable<Record<string, any[]>>({})
export const chatStreaming = writable<Record<string, boolean>>({})
// Wall-clock start (ms) of the active streaming turn, per session, so the live
// "Thinking" elapsed readout survives view remounts (page switches) instead of
// resetting — a component-local start would restart from ~0 (and briefly read
// negative) every time ChatView is re-created.
export const chatTurnStart = writable<Record<string, number>>({})
export const chatProgress = writable<Record<string, any>>({})
export const chatBgTasks = writable<Record<string, any[]>>({})
export const chatTodos = writable<Record<string, any[]>>({})
export const chatContextUsage = writable<Record<string, any>>({})
// Absolute context-window occupancy in tokens (the uplink size), per session.
// Distinct from chatContextUsage, which is the 0–100% fill.
export const chatContextTokens = writable<Record<string, number>>({})
export const chatWorkingDir = writable<Record<string, string>>({})
export const chatPermMode = writable<Record<string, string>>({})
export const chatReasoningEffort = writable<Record<string, string>>({})
// Effective show-reasoning flag for the current session (global default + per-model override).
export const chatShowReasoning = writable<Record<string, boolean>>({})
export const chatSuggestion = writable<Record<string, string>>({})
// Live thinking buffer (thinking_delta) shown as a Thoughts block while streaming.
export const chatThinking = writable<Record<string, string>>({})
// Live sub-agents, keyed by session. Fed by the sub_agent_event WS stream.
export const chatSubAgents = writable<Record<string, SubAgentState[]>>({})
// Background workflows, keyed by session. Fed by the workflow_event WS stream.
// Unlike sub-agents these persist across turns (a workflow runs detached), so
// the panel is NOT reset on send.
export const chatWorkflows = writable<Record<string, WorkflowRunState[]>>({})

// A slash-command / prompt queued to auto-send once its session's WS
// subscription is confirmed. Set by panel "create / setup" actions that open an
// agent session; consumed by ChatView on the `subscribed` ack. This mirrors the
// old hand-written UI's Sessions.setPendingMessage → ws "subscribed" flush, so
// the agentic-first panels invoke a skill in a fresh chat instead of opening a
// blank session or popping a form.
export const pendingPrompt = writable<{ sessionId: string; content: string } | null>(null)

// Permission/question modals
export const confirmModal = writable<any | null>(null)
export const questionModal = writable<any | null>(null)
export const feedbackModal = writable<any | null>(null)

// Admin data (loaded by views)
export const skills = writable<Skill[]>([])
export const tasks = writable<ScheduledTask[]>([])
export const mcpServers = writable<McpServer[]>([])
export const toolSearchMode = writable('auto')
export const channels = writable<Channel[]>([])
export const memories = writable<Memory[]>([])
export const trashItems = writable<any[]>([])
export const profileSoul = writable<string | null>(null)
export const profileUser = writable<any | null>(null)
export const memTab = writable<'soul' | 'user' | 'memories'>('soul')
export const recallFiles = writable<any[]>([])

// Monotonic id generator — Date.now() collides when called many times in the
// same millisecond (e.g. rendering a page of history synchronously), which
// produces duplicate {#each} keys and crashes the list with each_key_duplicate.
let _idSeq = 0
export function uid(prefix = 'm'): string {
  _idSeq += 1
  return `${prefix}-${_idSeq}`
}

// Helper functions
export function showToast(msg: string, type = 'success') {
  toast.set({ msg, type })
  setTimeout(() => toast.set(null), 3200)
}

export function setActiveSession(id: string) {
  activeSessionId.set(id)
  chatMessages.update(m => ({ ...m, [id]: m[id] || [] }))
}

// Agentic-first entry point shared by the Skills / Tasks / MCP / Channels /
// Profile panels: open a fresh chat session and queue a slash-command to
// auto-send once the WS subscription is confirmed (see pendingPrompt). The
// relevant skill drives the rest of the flow in conversation — no forms.
export async function openAgentSession(content: string, name?: string): Promise<void> {
  const sess = await api.createSession({ source: 'manual', ...(name ? { name } : {}) })
  sessions.update(s => [sess, ...s])
  pendingPrompt.set({ sessionId: sess.id, content })
  activeSessionId.set(sess.id)
  view.set('chat')
}

export function addChatMsg(sessionId: string, msg: any) {
  chatMessages.update(m => ({ ...m, [sessionId]: [...(m[sessionId] || []), msg] }))
}

export function updateLastMsg(sessionId: string, updater: (msg: any) => any) {
  chatMessages.update(m => {
    const msgs = [...(m[sessionId] || [])]
    if (msgs.length) msgs[msgs.length - 1] = updater(msgs[msgs.length - 1])
    return { ...m, [sessionId]: msgs }
  })
}

export function appendToLastAssistant(sessionId: string, content: string) {
  if (!content) return
  chatMessages.update(m => {
    const msgs = [...(m[sessionId] || [])]
    const last = msgs.length - 1
    // Continue the in-flight bubble only if it is the trailing message AND still
    // streaming. Otherwise this delta opens a new segment (turn start, or text
    // following a tool call / thinking block), so finalize any prior streaming
    // bubble and push a fresh one. Without the freshness check a delta would
    // either be dropped (no assistant bubble yet) or merged into a finalized
    // reply from an earlier segment/turn.
    if (last >= 0 && msgs[last].type === 'assistant' && msgs[last].streaming) {
      msgs[last] = { ...msgs[last], content: msgs[last].content + content }
      return { ...m, [sessionId]: msgs }
    }
    for (let i = 0; i < msgs.length; i++) {
      if (msgs[i].type === 'assistant' && msgs[i].streaming) {
        msgs[i] = { ...msgs[i], streaming: false }
      }
    }
    msgs.push({
      id: uid('a'), type: 'assistant', content,
      thinking: '', createdAt: Date.now(), streaming: true, tools: [], todos: [],
    })
    return { ...m, [sessionId]: msgs }
  })
}

export function clearMsgs(sessionId: string) {
  chatMessages.update(m => ({ ...m, [sessionId]: [] }))
}

// Commit a finished reasoning segment as a standalone Thoughts message. Used
// both on history replay (a `thinking` event) and live (when a tool call ends
// the current thinking step). Placing it in the message list — rather than the
// transient live buffer — preserves think → act order and makes the segment a
// tool-group boundary. Whitespace-only thinking is dropped.
export function commitThinking(sessionId: string, text: string) {
  const t = (text ?? '').trim()
  if (!t) return
  chatMessages.update(m => {
    const msgs = [...(m[sessionId] || [])]
    // Finalize any trailing streaming assistant text so its caret stops blinking
    // once the reasoning segment is committed as a separate message.
    for (let i = 0; i < msgs.length; i++) {
      if (msgs[i].type === 'assistant' && msgs[i].streaming) {
        msgs[i] = { ...msgs[i], streaming: false }
      }
    }
    msgs.push({
      id: uid('th'), type: 'thinking', thinking: t,
      createdAt: Date.now(), streaming: false, tools: [], todos: [],
    })
    return { ...m, [sessionId]: msgs }
  })
}

export function addToolCallToGroup(sessionId: string, toolCall: any) {
  chatMessages.update(m => {
    const msgs = [...(m[sessionId] || [])]
    // Group consecutive tools only — append to the running group when it is the
    // LAST message. Any thinking/assistant message pushed since (a reasoning
    // step or LLM text between rounds) ends the group, so separated tools render
    // as their own cards while back-to-back tool calls stay in one.
    const last = msgs[msgs.length - 1]
    if (last && last.type === 'tool_group' && last.streaming) {
      msgs[msgs.length - 1] = { ...last, tools: [...last.tools, toolCall] }
    } else {
      // A tool call starts a new action segment: stop the caret on any trailing
      // streaming assistant text so it doesn't keep blinking behind the tool card.
      for (let i = 0; i < msgs.length; i++) {
        if (msgs[i].type === 'assistant' && msgs[i].streaming) {
          msgs[i] = { ...msgs[i], streaming: false }
        }
      }
      msgs.push({ id: uid('grp'), type: 'tool_group', content: '', streaming: true, tools: [toolCall], todos: [], createdAt: Date.now() })
    }
    return { ...m, [sessionId]: msgs }
  })
}

// Find the tool to update: prefer an exact toolId match (correct for parallel
// tool calls whose results arrive out of order), else the first not-yet-done
// tool, else the last one. Parallel tool_use returns N calls then N results, so
// "last tool" alone mis-assigns every result to the final call.
function pickToolIndex(tools: any[], toolId: string | undefined): number {
  if (toolId) {
    const byId = tools.findIndex((t: any) => t.toolId && t.toolId === toolId)
    if (byId >= 0) return byId
  }
  const pending = tools.findIndex((t: any) => !t.done && !t.error)
  if (pending >= 0) return pending
  return tools.length - 1
}

export function updateToolResult(sessionId: string, toolId: string | undefined, result: any, uiPayload: any) {
  chatMessages.update(m => {
    const msgs = [...(m[sessionId] || [])]
    const lastGroup = msgs.findLastIndex((x: any) => x.type === 'tool_group')
    if (lastGroup >= 0) {
      const tools = [...msgs[lastGroup].tools]
      const idx = pickToolIndex(tools, toolId)
      if (idx >= 0) {
        const started = tools[idx].startedAt
        const elapsed = started ? (Date.now() - started) / 1000 : tools[idx].elapsed
        tools[idx] = { ...tools[idx], result, ui_payload: uiPayload, done: true, elapsed }
      }
      msgs[lastGroup] = { ...msgs[lastGroup], tools }
    }
    return { ...m, [sessionId]: msgs }
  })
}

export function setToolError(sessionId: string, toolId: string | undefined, error: string) {
  chatMessages.update(m => {
    const msgs = [...(m[sessionId] || [])]
    const lastGroup = msgs.findLastIndex((x: any) => x.type === 'tool_group')
    if (lastGroup >= 0) {
      const tools = [...msgs[lastGroup].tools]
      const idx = pickToolIndex(tools, toolId)
      if (idx >= 0) {
        const started = tools[idx].startedAt
        const elapsed = started ? (Date.now() - started) / 1000 : tools[idx].elapsed
        tools[idx] = { ...tools[idx], error, done: true, elapsed }
      }
      msgs[lastGroup] = { ...msgs[lastGroup], tools }
    }
    return { ...m, [sessionId]: msgs }
  })
}

// ── Sub-agents ───────────────────────────────────────────────────────────────
// One live entry per concurrent sub-agent, driven by the sub_agent_event stream
// (kind: started | tool | tool_error | done). The panel is a live view of the
// current turn; resetSubAgents() clears it when a new turn starts.
export interface SubAgentTool {
  name: string
  error: boolean
}
export interface SubAgentState {
  id: string
  description: string
  agentType: string // subagent_type, e.g. "explore" (empty for an untyped fork)
  status: 'running' | 'done'
  lastTool: string
  tools: SubAgentTool[]
  startedAt: number
}

export function resetSubAgents(sessionId: string) {
  chatSubAgents.update(m => ({ ...m, [sessionId]: [] }))
}

// Remove finished sub-agents from the live panel while keeping any that are
// still running (e.g. a sync sub-agent promoted to background). Called on
// `complete` so a done panel doesn't linger until the next page refresh.
export function clearDoneSubAgents(sessionId: string) {
  chatSubAgents.update(m => {
    const remaining = (m[sessionId] || []).filter(a => a.status !== 'done')
    return { ...m, [sessionId]: remaining }
  })
}

export function applySubAgentEvent(
  sessionId: string,
  agentId: string,
  description: string,
  agentType: string,
  kind: string,
  toolName: string,
) {
  chatSubAgents.update(m => {
    const list = [...(m[sessionId] || [])]
    let idx = list.findIndex(a => a.id === agentId)
    if (kind === 'started') {
      const entry: SubAgentState = {
        id: agentId,
        description: description || agentId,
        agentType,
        status: 'running',
        lastTool: '',
        tools: [],
        startedAt: Date.now(),
      }
      if (idx >= 0) list[idx] = entry
      else list.push(entry)
      return { ...m, [sessionId]: list }
    }
    if (idx < 0) {
      // Tool/done arrived before a started event (e.g. resumed agent) — seed it.
      list.push({ id: agentId, description: description || agentId, agentType, status: 'running', lastTool: '', tools: [], startedAt: Date.now() })
      idx = list.length - 1
    }
    const a = { ...list[idx], tools: [...list[idx].tools] }
    if (description && a.description === a.id) a.description = description
    if (agentType && !a.agentType) a.agentType = agentType
    if (kind === 'tool' || kind === 'tool_error') {
      a.tools.push({ name: toolName || 'tool', error: kind === 'tool_error' })
      a.lastTool = toolName || a.lastTool
    } else if (kind === 'done') {
      a.status = 'done'
    }
    list[idx] = a
    return { ...m, [sessionId]: list }
  })
}

// ── Background workflows ─────────────────────────────────────────────────────
// One entry per background workflow run, driven by the workflow_event stream
// (kind: started | progress | done). Runs persist across turns, so this is
// never auto-reset — entries accumulate for the session's lifetime.
const maxWorkflowProgressLines = 200

export interface WorkflowRunState {
  id: string
  description: string
  status: 'running' | 'done' | 'error'
  progress: string[]
  startedAt: number
}

export function applyWorkflowEvent(
  sessionId: string,
  runId: string,
  description: string,
  kind: string,
  line: string,
  status: string,
) {
  if (!runId) return
  chatWorkflows.update(m => {
    const list = [...(m[sessionId] || [])]
    let idx = list.findIndex(r => r.id === runId)
    if (idx < 0) {
      list.push({ id: runId, description: description || runId, status: 'running', progress: [], startedAt: Date.now() })
      idx = list.length - 1
    }
    const r = { ...list[idx], progress: [...list[idx].progress] }
    if (description && (r.description === r.id || !r.description)) r.description = description
    if (kind === 'progress') {
      if (line) {
        r.progress.push(line)
        if (r.progress.length > maxWorkflowProgressLines) {
          r.progress = r.progress.slice(r.progress.length - maxWorkflowProgressLines)
        }
      }
    } else if (kind === 'done') {
      r.status = status === 'error' ? 'error' : 'done'
    }
    list[idx] = r
    return { ...m, [sessionId]: list }
  })
}

// Mark only the tools whose IDs are in the provided set as done. Used after
// loading history so we don't accidentally close tools from a concurrently
// replaying live turn (those tools have different IDs and are not in the set).
export function finishToolsById(sessionId: string, toolIds: Set<string>) {
  if (toolIds.size === 0) return
  chatMessages.update(m => {
    const msgs = (m[sessionId] || []).map((msg: any) => {
      if (msg.type !== 'tool_group') return msg
      const tools = msg.tools.map((t: any) =>
        toolIds.has(t.toolId) && !t.done && !t.error ? { ...t, done: true } : t
      )
      const allDone = tools.every((t: any) => t.done || t.error)
      return { ...msg, streaming: allDone ? false : msg.streaming, tools }
    })
    return { ...m, [sessionId]: msgs }
  })
}

// Mark every still-running tool in the session as done — called on `complete`
// so a finished turn never leaves tools spinning (parallel results that never
// matched, or a tool whose result event was dropped).
export function finishAllTools(sessionId: string) {
  chatMessages.update(m => {
    const msgs = (m[sessionId] || []).map((msg: any) =>
      msg.type === 'tool_group'
        ? {
            ...msg,
            streaming: false,
            tools: msg.tools.map((t: any) => (t.done || t.error ? t : { ...t, done: true })),
          }
        : msg
    )
    return { ...m, [sessionId]: msgs }
  })
}
