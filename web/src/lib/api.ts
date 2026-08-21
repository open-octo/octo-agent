import type { Session, SessionGroup, Skill, Workflow, ScheduledTask, McpServer, McpServerDetail, Channel, Memory, RecallFile, TagStatus } from './types'

// TaskResponse matches the Go server task struct.
export interface TaskResponse {
  id: string
  name: string
  cron: string
  prompt: string
  model: string
  agent: string
  agent_id?: string
  notify: string
  enabled: boolean
  created_at: string
  last_run: string
  next_run: string
  session_id: string
  session_group_id?: string
}

// #1109: every caller below throws through here, and every error toast in
// Settings/MCP/Skills/Tasks/Channels/Profile/FileRecall renders e.message —
// which used to be just the HTTP status line ("500 Internal Server Error")
// because the server's JSON error body ({"error": "..."}, see writeError in
// internal/server/server.go) was discarded. One fix here fixes every toast.
//
// Exported so callers that can't go through request() — because they need
// the raw Response (e.g. SkillsView.handleExport, which reads a Blob on
// success) — parse a failing response's error body the same way, instead of
// re-implementing (and drifting from) the same fallback logic inline.
export async function readErrorMessage(res: Response, fallback: string): Promise<string> {
  try {
    const body = await res.json()
    if (typeof body?.error === 'string' && body.error) return body.error
    if (typeof body?.message === 'string' && body.message) return body.message
  } catch {
    // Not JSON (proxy error page, empty body, …) — fall back to the status line.
  }
  return fallback
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init)
  if (!res.ok) {
    throw new Error(await readErrorMessage(res, `${res.status} ${res.statusText}`))
  }
  return res.json() as Promise<T>
}

function json(body: unknown): RequestInit {
  return {
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

// Sessions

export interface SessionsResponse {
  sessions: Session[]
  has_more: boolean
  cron_count: number
}

export async function listSessions(): Promise<SessionsResponse> {
  return request<SessionsResponse>('/api/sessions')
}

export interface CreateSessionOpts {
  name?: string
  model?: string
  source?: string
  agent_profile?: string
  // Files the session under an existing group (the sidebar's per-group "+").
  // For a project, the server skips seeding a default working dir so the
  // session runs purely in the project's directory.
  group_id?: string
}

export async function createSession(opts: CreateSessionOpts): Promise<Session> {
  // The create endpoint wraps the record as { session: {...} }; unwrap so
  // callers get a Session with a usable .id (sidebar push + activeSessionId).
  const d = await request<{ session?: Session } & Session>('/api/sessions', { method: 'POST', ...json(opts) })
  return (d.session ?? d) as Session
}

export async function deleteSession(id: string): Promise<void> {
  await request<unknown>(`/api/sessions/${id}`, { method: 'DELETE' })
}

export async function deleteSessions(ids: string[]): Promise<void> {
  await request<unknown>('/api/sessions/delete', { method: 'POST', ...json({ ids }) })
}

export async function updateSession(id: string, patch: { name?: string }): Promise<Session> {
  return request<Session>(`/api/sessions/${id}`, { method: 'PATCH', ...json(patch) })
}

// ─── Session groups (Web-UI sidebar organisation) ───────────────────────────

// listSessionGroups returns the sidebar groups plus the pinned- and
// collapsed-session IDs; they share one registry file and one endpoint.
export async function listSessionGroups(): Promise<{ groups: SessionGroup[]; pinned: string[]; collapsed: string[] }> {
  const d = await request<{ groups: SessionGroup[]; pinned_session_ids: string[]; collapsed_session_ids: string[] }>('/api/session-groups')
  return { groups: d.groups ?? [], pinned: d.pinned_session_ids ?? [], collapsed: d.collapsed_session_ids ?? [] }
}

// Every created group is a project: the server generates its workspace, and
// source_dirs are the external folders it references (0..N is fine).
export async function createSessionGroup(name: string, project?: { source_dirs?: string[] }): Promise<SessionGroup> {
  const d = await request<{ group: SessionGroup }>('/api/session-groups', { method: 'POST', ...json({ name, ...project }) })
  return d.group
}

// The workspace (working_dir) is fixed at creation and not patchable;
// source_dirs replaces the mount set wholesale, output_dir marks one of them
// ('' clears the marker).
export async function updateSessionGroup(
  id: string,
  patch: { name?: string; collapsed?: boolean; source_dirs?: string[]; output_dir?: string },
): Promise<SessionGroup> {
  const d = await request<{ group: SessionGroup }>(`/api/session-groups/${id}`, { method: 'PATCH', ...json(patch) })
  return d.group
}

// Delete a project. withSessions deletes its sessions along with it, in the same
// request — so a failure cannot leave the project standing over sessions that are
// already gone.
export async function deleteSessionGroup(id: string, withSessions = false): Promise<void> {
  const q = withSessions ? '?sessions=delete' : ''
  await request<unknown>(`/api/session-groups/${id}${q}`, { method: 'DELETE' })
}

// Persist a new group order. Pass the full group-ID list in the desired order.
export async function reorderSessionGroups(ids: string[]): Promise<void> {
  await request<unknown>('/api/session-groups/order', { method: 'PUT', ...json({ ids }) })
}

// Pin a session to the top of the sidebar, or unpin it.
export async function setSessionPinned(sessionId: string, pinned: boolean): Promise<void> {
  await request<unknown>(`/api/sessions/${sessionId}/pin`, { method: 'PUT', ...json({ pinned }) })
}

// Collapse a session into the sidebar's folded panel, or restore it. The
// server rejects collapsing a pinned or grouped session (409).
export async function setSessionCollapsed(sessionId: string, collapsed: boolean): Promise<void> {
  await request<unknown>(`/api/sessions/${sessionId}/collapse`, { method: 'PUT', ...json({ collapsed }) })
}

// Copy a session's history into a new session. messageIndex is an exclusive
// count of messages to keep, so it must land on a turn boundary (right after a
// completed assistant reply) or the server rejects it. Returns the new session.
export async function branchSession(sessionId: string, messageIndex: number): Promise<Session> {
  const d = await request<{ session: Session }>(`/api/sessions/${sessionId}/branch`, {
    method: 'POST',
    ...json({ message_index: messageIndex }),
  })
  return d.session
}

// Edit a user message and regenerate from it. The server interrupts any
// in-flight turn, truncates history to just before the message, and reruns
// with the new content (keeping the original image attachments) — the caller
// must NOT resend; the rerun re-appends the prompt itself.
export async function editMessage(sessionId: string, messageIndex: number, newContent: string): Promise<void> {
  await request<unknown>(`/api/sessions/${sessionId}/edit_message`, {
    method: 'POST',
    ...json({ message_index: messageIndex, new_content: newContent }),
  })
}

// The server returns a session's full persisted transcript in one shot — it
// has no limit/before pagination (GET /api/sessions/:id/messages ignores
// those params entirely and always returns everything), so there is nothing
// to page through here.
export async function getSessionMessages(id: string): Promise<unknown> {
  return request<unknown>(`/api/sessions/${id}/messages`)
}

// The outstanding permission confirmation for a session, if any. The mobile feed
// isn't subscribed to sessions (request_confirmation only reaches subscribers),
// so ApprovalDetail fetches the pending ask over REST. Returns { pending: false }
// when there is none.
export interface SessionConfirmation {
  pending: boolean
  id?: string
  message?: string
  kind?: string
  tool_name?: string
  command?: string
  diff?: string
  input?: string
}
export async function getSessionConfirmation(id: string): Promise<SessionConfirmation> {
  return request<SessionConfirmation>(`/api/sessions/${id}/confirmation`)
}

// The server keys session model by the named entry id (or "default" / a raw
// model string), read from the `model_id` field — not `model`.
export async function updateSessionModel(id: string, modelId: string): Promise<{ model: string; model_id: string }> {
  return request<{ model: string; model_id: string }>(`/api/sessions/${id}/model`, {
    method: 'PATCH',
    ...json({ model_id: modelId }),
  })
}

export async function updateSessionReasoningEffort(id: string, effort: string): Promise<void> {
  await request<unknown>(`/api/sessions/${id}/reasoning_effort`, {
    method: 'PATCH',
    ...json({ reasoning_effort: effort }),
  })
}

export async function updateSessionShowReasoning(id: string, show: boolean): Promise<void> {
  await request<unknown>(`/api/sessions/${id}/show_reasoning`, {
    method: 'PATCH',
    ...json({ show_reasoning: show }),
  })
}

export async function getSessionGoal(id: string): Promise<{ goal: any | null }> {
  return request<{ goal: any | null }>(`/api/sessions/${id}/goal`)
}

export async function updateSessionPermissionMode(id: string, mode: string): Promise<void> {
  await request<unknown>(`/api/sessions/${id}/permission_mode`, {
    method: 'PATCH',
    ...json({ permission_mode: mode }),
  })
}

export interface NativePickResult {
  path: string
  cancelled: boolean
}

// Desktop shell only: open the OS file dialog and return the chosen path. The
// caller attaches it by real path (no upload); the agent reads it in place.
export async function nativePickFile(startDir?: string): Promise<NativePickResult> {
  return request<NativePickResult>('/api/native/pick-file', {
    method: 'POST',
    ...json({ start_dir: startDir ?? '' }),
  })
}

// Desktop shell only: open the OS folder dialog and return the chosen path.
// Available only when /api/version reports native:true (a NativeBridge is
// wired); calling it under plain `octo serve` 404s. The caller then sets the
// path via updateSessionWorkingDir, same as the in-app picker.
export async function nativePickFolder(startDir?: string): Promise<NativePickResult> {
  return request<NativePickResult>('/api/native/pick-folder', {
    method: 'POST',
    ...json({ start_dir: startDir ?? '' }),
  })
}

// Desktop shell only: show the OS save dialog seeded with name, write content
// to the chosen path, and return it. Used for the artifact download, which the
// octo-served webview can't do with an in-page blob download. cancelled is
// true when the user dismissed the dialog.
export async function nativeSaveFile(name: string, content: string): Promise<{ path: string; cancelled: boolean }> {
  return request<{ path: string; cancelled: boolean }>('/api/native/save-file', {
    method: 'POST',
    ...json({ name, content }),
  })
}

// Desktop shell only: open the OS print dialog for the window. Backs the PDF
// export, which prints the live transcript through the print stylesheet instead
// of building a PDF client-side. Resolves once the dialog is up — on macOS it is
// a window sheet — so the caller must keep the transcript rendered afterwards.
export async function nativePrint(): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>('/api/native/print', { method: 'POST' })
}

// Desktop shell variant for binary payloads. The content is base64-encoded; the
// server decodes it to bytes before writing, so zips (and any other non-UTF-8
// blob) survive the JSON round-trip intact.
export async function nativeSaveBinary(name: string, b64: string): Promise<{ path: string; cancelled: boolean }> {
  return request<{ path: string; cancelled: boolean }>('/api/native/save-file', {
    method: 'POST',
    ...json({ name, content: b64, encoding: 'base64' }),
  })
}

// Desktop shell only: raise an OS-native notification. Used in place of the
// browser Notification API, which native webviews don't implement. Best-effort.
// When sessionId is provided, clicking the notification focuses the app and
// jumps to that session.
export async function nativeNotify(title: string, body: string, sessionId?: string): Promise<void> {
  await request<{ ok: boolean }>('/api/native/notify', {
    method: 'POST',
    ...json({ title, body, session_id: sessionId }),
  })
}

// Desktop shell only: maximise/restore the window — the double-click-titlebar
// zoom, which the draggable header can't do itself (no Wails runtime on the
// octo-served page). Best-effort.
export async function nativeToggleMaximise(): Promise<void> {
  await request<{ ok: boolean }>('/api/native/window/toggle-maximise', { method: 'POST' })
}

// Desktop shell only: minimise the window to the taskbar/dock. Best-effort.
export async function nativeMinimise(): Promise<void> {
  await request<{ ok: boolean }>('/api/native/window/minimise', { method: 'POST' })
}

// Desktop shell only: close the window (the app's ShouldQuit decides whether the
// hub actually terminates or keeps running in the tray). Best-effort.
export async function nativeClose(): Promise<void> {
  await request<{ ok: boolean }>('/api/native/window/close', { method: 'POST' })
}

// Desktop shell only: query whether the window is currently maximised. Lets the
// frontend keep its maximise icon in sync after Aero Snap, keyboard shortcuts,
// etc. Returns false if the native bridge is unavailable (e.g. web, pre-window).
export async function nativeWindowState(): Promise<boolean> {
  try {
    const r = await request<{ maximised: boolean }>('/api/native/window/state')
    return r.maximised
  } catch {
    return false
  }
}

// Desktop shell only: open a URL with the OS default handler. The update badge
// calls this in installer mode to reach the release download page (the desktop
// build updates through its installer, not an in-place swap); chat links use it
// too. http(s)/mailto/tel only — the server rejects other schemes. Available
// only when /api/version reports native:true and the caller is loopback; remote
// peers fall back to window.open.
export async function openExternal(url: string): Promise<void> {
  await request<{ ok: boolean }>('/api/native/open-external', {
    method: 'POST',
    ...json({ url }),
  })
}

// Desktop shell only: reveal a session's or a project's working directory in
// the OS file manager. Backs the sidebar's "Open folder" action — a browser tab
// has no way to do this, so it is offered only when native:true.
//
// The row is named by id, not by path: the server resolves where that session
// or project actually works (and refuses an id it doesn't know), so this can't
// become a way to open any directory on the host.
export async function openFolder(target: { sessionId?: string; groupId?: string }): Promise<void> {
  await request<{ ok: boolean; path: string }>('/api/native/open-folder', {
    method: 'POST',
    ...json({ session_id: target.sessionId, group_id: target.groupId }),
  })
}

// Desktop shell only: start the in-place update flow — the native updater
// window takes over (download → verify → restart). Available when /api/version
// reports self_update:true and the caller is loopback; the badge falls back to
// the download page on failure.
export async function selfUpdate(): Promise<void> {
  await request<{ ok: boolean }>('/api/native/self-update', { method: 'POST' })
}

// Desktop shell only: launch-at-login state.
export async function getAutostart(): Promise<boolean> {
  const r = await request<{ enabled: boolean }>('/api/native/autostart')
  return r.enabled
}
export async function setAutostart(enabled: boolean): Promise<void> {
  await request<{ enabled: boolean }>('/api/native/autostart', {
    method: 'PUT',
    ...json({ enabled }),
  })
}

export interface FsEntry {
  name: string
  is_dir: boolean
  is_symlink: boolean
}

export interface FsListing {
  path: string
  parent: string
  entries: FsEntry[]
  truncated: boolean
  // True for the synthetic "This PC" drive list a Windows client sees above
  // a drive root (C:\) — `path` is an opaque token, not a real directory, so
  // it can't be picked; other platforms never set this.
  is_this_pc: boolean
}

// Read-only directory listing for the folder picker. Omit `path` to start at
// the user's home directory. A 403 (thrown here as an Error with the server's
// message) means the request didn't come from the local machine — the picker
// surfaces that message and the user falls back to typing a path.
export async function fsList(path?: string): Promise<FsListing> {
  const q = path ? `?path=${encodeURIComponent(path)}` : ''
  return request<FsListing>(`/api/fs/list${q}`)
}

export async function updateSessionWorkingDir(id: string, dir: string): Promise<{ working_dir: string }> {
  // The server expands ~ and returns the canonical absolute dir it stored.
  return request<{ working_dir: string }>(`/api/sessions/${id}/working_dir`, {
    method: 'PATCH',
    ...json({ working_dir: dir }),
  })
}

// Only accepted server-side while the session still has zero turns — once a
// turn has run, agent_profile is fixed for the life of the session (a turn
// may already have applied the old profile's system prompt/tool allowlist).
export async function updateSessionAgentProfile(id: string, agentProfile: string): Promise<{ agent_profile: string }> {
  return request<{ agent_profile: string }>(`/api/sessions/${id}/agent_profile`, {
    method: 'PATCH',
    ...json({ agent_profile: agentProfile }),
  })
}

// Skills

// The server returns { skills: [{name, description, source, enabled}] }. Map it
// to the display shape the table expects (desc/icon/tag), since the server has
// no icon/version/status of its own.
// Agent profile management (multi-agent system)
export interface Agent {
  id: string
  name: string
  description: string
  model?: string
  tools?: string[]
  tool_skills?: string[]
  system_prompt?: string
  channel_bindings?: { platform: string; adapter_id?: string; chat_id: string }[]
  // Gallery display metadata — populated for curated (source: 'default')
  // experts, empty/absent for ordinary user agents.
  category?: string
  tags?: string[]
  tags_en?: string[]
  example_prompts?: string[]
  example_prompts_en?: string[]
  icon?: string
  name_en?: string
  description_en?: string
  // Always present: 'default' (officially curated) or 'user'.
  source?: 'default' | 'user'
  // Visibility of a curated expert: false once the user hides it. User agents
  // are always enabled.
  enabled?: boolean
}

export async function listAgents(): Promise<Agent[]> {
  return request<Agent[]>('/api/agents')
}

export async function getAgent(id: string): Promise<Agent> {
  return request<Agent>(`/api/agents/${id}`)
}

export async function createAgent(data: Omit<Agent, 'id'>): Promise<Agent> {
  return request<Agent>('/api/agents', { method: 'POST', ...json(data) })
}

export async function updateAgent(id: string, data: Partial<Agent>): Promise<Agent> {
  return request<Agent>(`/api/agents/${id}`, { method: 'PUT', ...json(data) })
}

export async function deleteAgent(id: string): Promise<void> {
  await request<unknown>(`/api/agents/${id}`, { method: 'DELETE' })
}

// Hide or re-show a curated (source: 'default') expert in the gallery. Only
// valid for curated experts — the server rejects it for user agents.
export async function toggleAgent(id: string): Promise<{ id: string; enabled: boolean }> {
  return request<{ id: string; enabled: boolean }>(`/api/agents/${id}/toggle`, { method: 'PATCH' })
}

export async function bindAgent(id: string, platform: string, chatId: string): Promise<Agent> {
  return request<Agent>(`/api/agents/${id}/bind`, { method: 'POST', ...json({ platform, chat_id: chatId }) })
}

export async function unbindAgent(id: string, platform: string, chatId: string): Promise<Agent> {
  return request<Agent>(`/api/agents/${id}/bind`, { method: 'DELETE', ...json({ platform, chat_id: chatId }) })
}

// Transfer a cron task's ownership to another agent.
export async function transferTask(taskId: string, agentId: string): Promise<unknown> {
  return request<unknown>(`/api/tasks/${taskId}/transfer`, { method: 'PUT', ...json({ agent_id: agentId }) })
}

interface SkillInfoRaw {
  name: string
  description?: string
  source?: string
  enabled?: boolean
}
export async function listSkills(): Promise<Skill[]> {
  const d = await request<{ skills: SkillInfoRaw[] }>('/api/skills')
  return (d.skills ?? []).map((s): Skill => {
    // Server source is "default" (built-in/system) | "user".
    const src = s.source ?? 'user'
    const tag: { tagStatus: TagStatus; tagLabel: string } = src === 'default'
      ? { tagStatus: 'default', tagLabel: 'System' }
      : { tagStatus: 'success', tagLabel: 'User' }
    return {
      name: s.name,
      desc: s.description ?? '',
      icon: 'ant-design:thunderbolt-outlined',
      tagStatus: tag.tagStatus,
      tagLabel: tag.tagLabel,
      enabled: s.enabled ?? false,
      source: src,
    }
  })
}

export async function toggleSkill(name: string, enabled: boolean): Promise<void> {
  await request<unknown>(`/api/skills/${encodeURIComponent(name)}/toggle`, {
    method: 'PATCH',
    ...json({ enabled }),
  })
}

// Workflows

export interface NamedWorkflow {
  name: string
  description: string
  source: string
}

// Raw list, used by the Composer's /wf autocomplete (name + description only).
export async function listWorkflows(): Promise<NamedWorkflow[]> {
  const d = await request<{ workflows: NamedWorkflow[] }>('/api/workflows')
  return d.workflows ?? []
}

// Display-mapped list for the management panel — mirrors listSkills's
// source→tag mapping so the two panels read as one system.
export async function listWorkflowsView(): Promise<Workflow[]> {
  const named = await listWorkflows()
  return named.map((w): Workflow => {
    const src = w.source || 'user'
    const tag: { tagStatus: TagStatus; tagLabel: string } = src === 'default'
      ? { tagStatus: 'default', tagLabel: 'System' }
      : { tagStatus: 'success', tagLabel: 'User' }
    return {
      name: w.name,
      desc: w.description ?? '',
      icon: 'ant-design:partition-outlined',
      tagStatus: tag.tagStatus,
      tagLabel: tag.tagLabel,
      source: src,
    }
  })
}

export interface WorkflowDetail {
  name: string
  description: string
  source: string
  script: string
}

export async function getWorkflow(name: string): Promise<WorkflowDetail> {
  return request<WorkflowDetail>(`/api/workflows/${encodeURIComponent(name)}`)
}

export async function deleteWorkflow(name: string): Promise<void> {
  await request<unknown>(`/api/workflows/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export async function deleteSkill(name: string): Promise<void> {
  await request<unknown>(`/api/skills/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export interface ImportSkillResult {
  ok: boolean
  conflict?: boolean   // 409: a same-named skill exists — retry with force
  name?: string
  error?: string
}

// Install a skill from a GitHub URL, owner/repo[/sub/path] shorthand, a local
// path, or an /api/uploads/<name> URL (from uploadFile). The server endpoint is
// JSON-only — it does NOT accept a multipart file directly; uploads go through
// /api/upload first. Mirrors the old web import + `octo skills add`.
export async function importSkill(source: string, force = false): Promise<ImportSkillResult> {
  const res = await fetch('/api/skills/import', { method: 'POST', ...json({ source, force }) })
  if (res.status === 409) return { ok: false, conflict: true }
  const d = await res.json().catch(() => ({} as any))
  if (!res.ok) return { ok: false, error: d.error ?? `${res.status} ${res.statusText}` }
  return { ok: true, name: d.name }
}

// Upload a local file, returning the /api/uploads/<name> URL to feed importSkill.
export async function uploadFile(file: File): Promise<string> {
  const form = new FormData()
  form.append('files', file)
  const res = await fetch('/api/upload', { method: 'POST', body: form })
  const d = await res.json().catch(() => ({} as any))
  if (!res.ok) throw new Error(d.error ?? `${res.status} ${res.statusText}`)
  const url = d.files?.[0]?.url
  if (!url) throw new Error(d.files?.[0]?.error ?? 'upload failed')
  return url
}

// Tasks

export async function listTasks(): Promise<TaskResponse[]> {
  return request<TaskResponse[]>('/api/tasks')
}

export async function createTask(req: unknown): Promise<ScheduledTask> {
  return request<ScheduledTask>('/api/tasks', { method: 'POST', ...json(req) })
}

export async function deleteTask(id: string): Promise<void> {
  await request<unknown>(`/api/tasks/${id}`, { method: 'DELETE' })
}

// Edit any subset of a task's fields (cron, prompt, model, agent, directory,
// notify, enabled) via the single task PATCH endpoint, keyed by task id.
export async function updateTask(id: string, patch: unknown): Promise<void> {
  await request<unknown>(`/api/tasks/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    ...json(patch),
  })
}

export interface RunTaskResult {
  status: string
  id: string
  session_id: string
}

export async function runTask(id: string): Promise<RunTaskResult> {
  return request<RunTaskResult>(`/api/tasks/${id}/run`, { method: 'POST' })
}

// Pause (enabled:false) or resume (enabled:true) a scheduled task — a thin
// wrapper over the task PATCH endpoint.
export async function toggleTask(id: string, enabled: boolean): Promise<void> {
  await updateTask(id, { enabled })
}

// MCP Servers

export interface ToolSearchInfo {
  enabled: 'auto' | 'on' | 'off'
  threshold_pct: number
}

export interface McpServersResponse {
  servers: McpServer[]
  tool_search: ToolSearchInfo
}

export async function listMcpServers(): Promise<McpServersResponse> {
  const [serversData, tsData] = await Promise.all([
    request<{ servers: McpServer[] }>('/api/mcp/servers'),
    request<ToolSearchInfo>('/api/config/toolsearch'),
  ])
  return { servers: serversData.servers, tool_search: tsData }
}

// Full reload from disk: picks up hand-edited config files and retries every
// failed connection, unlike listMcpServers() which just re-reads cached state.
export async function reloadMcpServers(): Promise<McpServersResponse> {
  const [serversData, tsData] = await Promise.all([
    request<{ servers: McpServer[] }>('/api/mcp/reload', { method: 'POST' }),
    request<ToolSearchInfo>('/api/config/toolsearch'),
  ])
  return { servers: serversData.servers, tool_search: tsData }
}

export async function getMcpServer(name: string): Promise<McpServerDetail> {
  return request<McpServerDetail>(`/api/mcp/servers/${encodeURIComponent(name)}`)
}

// Bulk-import servers from a pasted JSON config: either a full
// { mcpServers: { name: {...} } } document or a bare { name: {...} } map.
// This is also the only way to add a server through the API — adding or
// editing a single one by hand goes through the mcp-creator skill instead,
// which edits the config file directly (see McpView's askAgentToEdit).
export async function importMcpServers(servers: Record<string, unknown>): Promise<void> {
  await request<unknown>('/api/mcp/servers', { method: 'POST', ...json({ mcpServers: servers }) })
}

export async function deleteMcpServer(name: string): Promise<void> {
  await request<unknown>(`/api/mcp/servers/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export async function toggleMcpServer(name: string, enabled: boolean): Promise<void> {
  await request<unknown>(`/api/mcp/servers/${encodeURIComponent(name)}/toggle`, {
    method: 'PATCH',
    ...json({ disabled: !enabled }),
  })
}

export async function reconnectMcpServer(name: string): Promise<void> {
  await request<unknown>(`/api/mcp/servers/${encodeURIComponent(name)}/reconnect`, {
    method: 'POST',
  })
}

// MCP OAuth Authorization Code + PKCE flow. start launches it in the
// background and returns the initial snapshot; poll status until the state
// settles (connected | failed). While authorizing, authorize_url is the page
// to open (a new tab) — the server's callback route resolves the flow once
// the browser redirects back.
export interface McpOAuthState {
  state: 'starting' | 'authorizing' | 'connected' | 'failed'
  authorize_url?: string
  error?: string
}

export async function startMcpOAuth(name: string): Promise<McpOAuthState> {
  return request<McpOAuthState>(`/api/mcp/servers/${encodeURIComponent(name)}/oauth/start`, {
    method: 'POST',
  })
}

export async function mcpOAuthStatus(name: string): Promise<McpOAuthState> {
  return request<McpOAuthState>(`/api/mcp/servers/${encodeURIComponent(name)}/oauth/status`)
}

export async function updateToolSearch(mode: 'auto' | 'on' | 'off'): Promise<void> {
  await request<unknown>('/api/config/toolsearch', { method: 'PUT', ...json({ enabled: mode }) })
}

// Channels

export async function listChannels(): Promise<Channel[]> {
  const d = await request<{ channels: Channel[] }>('/api/channels')
  return d.channels ?? []
}

export interface AvailableChannel {
  platform: string
  label: string
  fields: string[]
}

// The supported platforms (telegram/discord/feishu/dingtalk/wecom/weixin),
// shown as cards even before they're configured.
export async function listAvailableChannels(): Promise<AvailableChannel[]> {
  const d = await request<{ channels: AvailableChannel[] }>('/api/channels/available')
  return d.channels ?? []
}

export async function saveChannel(platform: string, cfg: unknown): Promise<void> {
  await request<unknown>(`/api/channels/${encodeURIComponent(platform)}`, {
    method: 'POST',
    ...json(cfg),
  })
}

export async function deleteChannel(platform: string): Promise<void> {
  await request<unknown>(`/api/channels/${encodeURIComponent(platform)}`, { method: 'DELETE' })
}

export async function testChannel(platform: string): Promise<void> {
  await request<unknown>(`/api/channels/${encodeURIComponent(platform)}/test`, { method: 'POST' })
}

// Profile & Memories

export async function getProfileSoul(): Promise<string> {
  return request<string>('/api/profile/soul')
}

export async function getProfileUser(): Promise<unknown> {
  return request<unknown>('/api/profile/user')
}

export async function getMemories(): Promise<Memory[]> {
  // The server returns { files: [...] } for the memory listing.
  const d = await request<{ files: Memory[] }>('/api/memories')
  return d.files ?? []
}

// Single memory detail — the list endpoint omits content, so the body is
// fetched on demand when a row is expanded.
export async function getMemory(name: string, source?: string): Promise<{ content: string; path: string }> {
  // source disambiguates a filename that exists in both the project and the
  // inherited (home) memory dirs.
  const qs = source ? `?source=${encodeURIComponent(source)}` : ''
  return request<{ content: string; path: string }>(`/api/memories/${encodeURIComponent(name)}${qs}`)
}

// #1109: ProfileView.forgetMemory used a raw fetch() with no res.ok check —
// a failing DELETE (404/expired session/etc) still reported "Memory removed"
// and the entry reappeared on reload. Routing through request() makes a
// non-2xx throw instead of silently succeeding.
export async function deleteMemory(name: string, source: string): Promise<void> {
  await request<unknown>(`/api/memories/${encodeURIComponent(name)}?source=${encodeURIComponent(source)}`, { method: 'DELETE' })
}

// ── Light Apps ─────────────────────────────────────────────────────────────

export interface LightApp {
  slug: string
  name: string
  description: string
  icon: string
  created_at: string
}

export interface LightAppDetail {
  manifest: LightApp
  html: string
}

// The list response also carries the Light Apps directory itself — the web UI
// uses it to detect session artifacts that already live inside it.
export interface LightAppList {
  apps: LightApp[]
  dir: string
}

export async function listLightApps(): Promise<LightApp[]> {
  const d = await request<LightAppList>('/api/light-apps')
  return d.apps ?? []
}

// Absolute path of the Light Apps directory (~/.octo/light-apps). Cached so
// repeated lookups (per artifact selection) cost one request per session.
let lightAppsDirCache: string | null = null

export async function getLightAppsDir(): Promise<string> {
  if (lightAppsDirCache) return lightAppsDirCache
  const d = await request<LightAppList>('/api/light-apps')
  lightAppsDirCache = d.dir || ''
  return lightAppsDirCache
}

export async function getLightApp(slug: string): Promise<LightAppDetail> {
  return request<LightAppDetail>(`/api/light-apps/${encodeURIComponent(slug)}`)
}

export async function createLightApp(opts: { name: string; description?: string; html: string }): Promise<LightApp> {
  return request<LightApp>('/api/light-apps', { method: 'POST', ...json({ ...opts, icon: '📄' }) })
}

export async function deleteLightApp(slug: string): Promise<void> {
  await request<unknown>(`/api/light-apps/${encodeURIComponent(slug)}`, { method: 'DELETE' })
}

// Trash

export async function listTrash(): Promise<RecallFile[]> {
  return request<RecallFile[]>('/api/trash')
}

export type RestoreResult =
  | { ok: true; restoredTo: string; backedUpExisting: boolean }
  | { ok: false; conflict: true }

// restoreTrash restores an entry. onConflict picks how an occupied original
// path is handled: undefined → abort (server replies 409 → { conflict:true });
// 'backup' → trash the current file first; 'rename' → restore as a sibling.
export async function restoreTrash(id: string, onConflict?: 'backup' | 'rename'): Promise<RestoreResult> {
  // The id ends in the original basename, which can contain URL-significant
  // characters (# ? % space) — encode it so the path segment survives.
  const q = onConflict ? `?on_conflict=${onConflict}` : ''
  const res = await fetch(`/api/trash/${encodeURIComponent(id)}/restore${q}`, { method: 'POST' })
  if (res.status === 409) return { ok: false, conflict: true }
  if (!res.ok) throw new Error(await readErrorMessage(res, `${res.status} ${res.statusText}`))
  const d = await res.json()
  return { ok: true, restoredTo: d.restored_to ?? '', backedUpExisting: !!d.backed_up_existing }
}

export async function deleteTrashItem(id: string): Promise<void> {
  await request<unknown>(`/api/trash/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export interface EmptyTrashOpts {
  mode?: 'all' | 'old' | 'orphans'
}

export async function emptyTrash(opts?: EmptyTrashOpts): Promise<void> {
  await request<unknown>('/api/trash/empty', { method: 'POST', ...json(opts ?? {}) })
}

// Config & Version

// Mirrors server modelConfig (onboard_config_handlers.go). api_key is returned
// masked; type is "default" | "lite" | "" .
export interface ModelEntry {
  id: string
  model: string
  type?: string
  base_url?: string
  api_key_masked?: string
  provider?: string
  anthropic_format?: boolean
  permission_mode?: string
  reasoning_effort?: string
  show_reasoning?: boolean
  vision?: boolean
}
export interface ConfigResponse {
  // PR5: Models/DefaultModelIdx deleted (flat Models field gone). The
  // two-level endpoint view is served by GET /api/config/endpoints.
  font_size?: string
  language?: string
  show_reasoning?: boolean
  coauthor?: boolean
  workspace_dir?: string
  // workspace_dir_default is the resolved directory sessions actually get
  // when workspace_dir is empty (~/Octo) — display this, not a bare "auto".
  workspace_dir_default?: string
  reasoning_effort?: string   // PR5: global reasoning effort
  permission_mode?: string    // PR6: global permission mode (was per-default-entry)
}

export async function getConfig(): Promise<ConfigResponse> {
  return request<ConfigResponse>('/api/config')
}

// PR4b (design §10.1): two-level endpoint view. Mirrors server endpointsResponse
// (onboard_config_handlers.go). has_api_key is the only key-related field — the
// server never echoes the key itself. models is the per-endpoint model list.
// Read-only in PR4b; CRUD lands in PR5.
export interface EndpointModel {
  model: string
  vision: boolean
}
export interface EndpointConfig {
  id: string
  name?: string
  provider: string
  base_url?: string
  protocol?: string
  has_api_key: boolean
  lite_model?: string
  headers?: Record<string, string>
  models: EndpointModel[]
}
export interface EndpointsResponse {
  endpoints: EndpointConfig[]
  default?: string
  lite?: string
  vision_helper?: string
}

export async function getEndpoints(): Promise<EndpointsResponse> {
  return request<EndpointsResponse>('/api/config/endpoints')
}

// ─── Endpoint CRUD (PR6, design §10.2) ─────────────────────────────────────
//
// PR5 shipped the backend routes; PR6 wires the Web UI to them. The old
// /api/config/models* routes are gone (PR5 deleted them), so saveModel below
// is re-routed to createEndpoint — the FirstRunSetup wizard and the (hidden)
// flat AI Models section both call saveModel with a ModelConfigInput; PR6
// keeps that call site working by projecting the flat input onto a single-
// model endpoint.

export interface EndpointModelInput {
  model: string
  vision: boolean
}

export interface EndpointConfigInput {
  id: string
  name?: string
  provider: string
  base_url?: string
  api_key?: string
  protocol?: string
  lite_model?: string
  headers?: Record<string, string>
  models?: EndpointModelInput[]
}

// Mirrors server endpointJSONOut — the response shape from create/update.
export interface EndpointMutationResult {
  id: string
  name?: string
  provider: string
  base_url?: string
  protocol?: string
  has_api_key: boolean
  lite_model?: string
  headers?: Record<string, string>
  models: EndpointModel[]
}

export async function createEndpoint(req: EndpointConfigInput): Promise<EndpointMutationResult> {
  return request<EndpointMutationResult>('/api/config/endpoints', { method: 'POST', ...json(req) })
}

// updateEndpoint takes the current id + a partial patch. When new_id is set,
// the server triggers RenameEndpoint (Default/Lite cascade + sender cache
// invalidation on the old id).
export interface EndpointUpdateInput {
  new_id?: string
  name?: string
  provider?: string
  base_url?: string
  api_key?: string
  protocol?: string
  // headers is a full-replacement patch, not a merge: omit the key entirely to
  // leave existing headers untouched, or send (possibly {}) to replace them
  // wholesale. Unlike the string fields above, an empty object is NOT the
  // same as "unchanged" — see EndpointsSection.svelte's submitForm.
  headers?: Record<string, string>
}

export async function updateEndpoint(id: string, req: EndpointUpdateInput): Promise<EndpointMutationResult> {
  return request<EndpointMutationResult>(`/api/config/endpoints/${encodeURIComponent(id)}`, { method: 'PATCH', ...json(req) })
}

export async function deleteEndpoint(id: string): Promise<void> {
  await request<unknown>(`/api/config/endpoints/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function addEndpointModel(id: string, model: string, vision: boolean): Promise<EndpointMutationResult> {
  return request<EndpointMutationResult>(`/api/config/endpoints/${encodeURIComponent(id)}/models`, { method: 'POST', ...json({ model, vision }) })
}

export async function deleteEndpointModel(id: string, model: string): Promise<void> {
  await request<unknown>(`/api/config/endpoints/${encodeURIComponent(id)}/models/${encodeURIComponent(model)}`, { method: 'DELETE' })
}

export async function setEndpointDefault(id: string, model?: string): Promise<{ ok: boolean; default: string }> {
  const qs = model ? `?model=${encodeURIComponent(model)}` : ''
  return request<{ ok: boolean; default: string }>(`/api/config/endpoints/${encodeURIComponent(id)}/default${qs}`, { method: 'POST' })
}

export async function setEndpointLite(id: string, model?: string): Promise<{ ok: boolean; lite: string }> {
  const qs = model ? `?model=${encodeURIComponent(model)}` : ''
  return request<{ ok: boolean; lite: string }>(`/api/config/endpoints/${encodeURIComponent(id)}/lite${qs}`, { method: 'POST' })
}

export async function unsetEndpointLite(id: string): Promise<{ ok: boolean; lite: string }> {
  return request<{ ok: boolean; lite: string }>(`/api/config/endpoints/${encodeURIComponent(id)}/lite`, { method: 'DELETE' })
}

// The vision helper describes images for text-only models. Only a model with
// vision: true may be assigned; the server rejects anything else with 400.
export async function setEndpointVisionHelper(id: string, model?: string): Promise<{ ok: boolean; vision_helper: string }> {
  const qs = model ? `?model=${encodeURIComponent(model)}` : ''
  return request<{ ok: boolean; vision_helper: string }>(`/api/config/endpoints/${encodeURIComponent(id)}/vision_helper${qs}`, { method: 'POST' })
}

export async function unsetEndpointVisionHelper(id: string): Promise<{ ok: boolean; vision_helper: string }> {
  return request<{ ok: boolean; vision_helper: string }>(`/api/config/endpoints/${encodeURIComponent(id)}/vision_helper`, { method: 'DELETE' })
}

export async function updateShowReasoning(showReasoning: boolean): Promise<{ ok: boolean; show_reasoning?: boolean }> {
  return request<{ ok: boolean; show_reasoning?: boolean }>('/api/config/show_reasoning', {
    method: 'PUT',
    ...json({ show_reasoning: showReasoning }),
  })
}

export async function updateCoauthor(coauthor: boolean): Promise<{ ok: boolean; coauthor?: boolean }> {
  return request<{ ok: boolean; coauthor?: boolean }>('/api/config/coauthor', {
    method: 'PUT',
    ...json({ coauthor }),
  })
}

export async function updateLanguage(language: string): Promise<{ ok: boolean; language?: string }> {
  return request<{ ok: boolean; language?: string }>('/api/config/language', {
    method: 'PUT',
    ...json({ language }),
  })
}

export async function updateWorkspaceDir(workspaceDir: string): Promise<{ ok: boolean; workspace_dir?: string }> {
  return request<{ ok: boolean; workspace_dir?: string }>('/api/config/workspace_dir', {
    method: 'PUT',
    ...json({ workspace_dir: workspaceDir }),
  })
}

export async function updateReasoningEffort(effort: string): Promise<{ ok: boolean; reasoning_effort?: string }> {
  return request<{ ok: boolean; reasoning_effort?: string }>('/api/config/reasoning_effort', {
    method: 'PUT',
    ...json({ reasoning_effort: effort }),
  })
}

export async function updatePermissionMode(mode: string): Promise<{ ok: boolean; permission_mode?: string }> {
  return request<{ ok: boolean; permission_mode?: string }>('/api/config/permission_mode', {
    method: 'PUT',
    ...json({ permission_mode: mode }),
  })
}

export async function getVersion(): Promise<unknown> {
  return request<unknown>('/api/version', { cache: 'no-store' })
}

// Managed tunnel (mobile pairing)

export interface TunnelPairing {
  enabled: boolean
  pair_url?: string
  relay?: string
  tunnel_id?: string
}

// getTunnelPairing returns the managed-tunnel pairing material, or
// { enabled: false } when `octo serve --tunnel` is not active.
export async function getTunnelPairing(): Promise<TunnelPairing> {
  return request<TunnelPairing>('/api/tunnel/pairing', { cache: 'no-store' })
}

// Browser automation setup

export interface BrowserStatus {
  configured: boolean
  connected: boolean
  port: number
  attach_running: boolean
  chrome_available: boolean
}

export interface BrowserVerifyResult {
  ok: boolean
  port: number
  detail: string
  saved: boolean
}

export async function getBrowserStatus(): Promise<BrowserStatus> {
  return request<BrowserStatus>('/api/browser/status', { cache: 'no-store' })
}

// verifyBrowser probes the CDP endpoint (the chrome://inspect path) and, on
// success, persists connect_port — the web equivalent of `octo browser setup`.
export async function verifyBrowser(port?: number): Promise<BrowserVerifyResult> {
  return request<BrowserVerifyResult>('/api/browser/verify', {
    method: 'POST',
    ...json(port ? { port } : {}),
  })
}

// Browser recordings = the editable YAML workflows produced by record_stop and
// replayed by the browser tool's replay action.
export interface BrowserRecording {
  name: string
  description?: string
  steps: number
  params?: string[]
}

export async function listBrowserRecordings(): Promise<BrowserRecording[]> {
  const d = await request<{ recordings: BrowserRecording[] }>('/api/browser/recordings', { cache: 'no-store' })
  return d.recordings ?? []
}

export async function getBrowserRecording(name: string): Promise<{ name: string; yaml: string }> {
  return request<{ name: string; yaml: string }>(`/api/browser/recordings/${encodeURIComponent(name)}`, { cache: 'no-store' })
}

export async function saveBrowserRecording(name: string, yaml: string): Promise<void> {
  await request<unknown>(`/api/browser/recordings/${encodeURIComponent(name)}`, { method: 'PUT', ...json({ yaml }) })
}

export async function deleteBrowserRecording(name: string): Promise<void> {
  await request<unknown>(`/api/browser/recordings/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// Onboard / first-run

export interface OnboardStatus {
  needs_onboard: boolean
  phase: '' | 'key_setup' | 'soul_setup'
}

export async function getOnboardStatus(): Promise<OnboardStatus> {
  // no-store: the desktop webview (WKWebView) heuristically caches GET 200s, so
  // without this the second window open would replay the FIRST load's stale
  // 'soul_setup' from cache — never hitting the server — and re-launch /onboard
  // even after the marker is set (#1660). Mirrors /api/version, /api/browser/status.
  return request<OnboardStatus>('/api/onboard/status', { cache: 'no-store' })
}

export async function completeOnboard(): Promise<void> {
  await request<unknown>('/api/onboard/complete', { method: 'POST' })
}

// Persists that the soul_setup auto-nudge fired, before the /onboard chat
// starts — so an interrupted first attempt doesn't retrigger it on the next
// load (#1660).
export async function markOnboardAttempted(): Promise<void> {
  await request<unknown>('/api/onboard/attempt', { method: 'POST' })
}

// Provider presets (GET /api/providers) — mirrors server providerPreset.
export interface EndpointVariant {
  label: string
  label_key?: string
  base_url: string
  region?: string
}
export interface ProviderPreset {
  id: string
  name: string
  base_url: string
  api: string                // "anthropic-messages" ⇒ anthropic protocol
  default_model: string
  models?: string[]
  model_vision?: Record<string, boolean>  // model id → accepts image input, for pre-filling the vision toggle
  lite_model?: string
  endpoint_variants?: EndpointVariant[]
  website_url?: string
  custom_endpoint?: boolean
}

export async function listProviders(): Promise<ProviderPreset[]> {
  const d = await request<{ providers: ProviderPreset[] }>('/api/providers')
  return d.providers ?? []
}

// Model config mutations. The request shape mirrors server saveModelRequest;
// an empty/masked api_key keeps the stored key on the server side.
export interface ModelConfigInput {
  type?: string
  model: string
  base_url: string
  api_key?: string
  provider?: string
  anthropic_format?: boolean
  permission_mode?: string
  reasoning_effort?: string
  show_reasoning?: boolean
  vision?: boolean
}

export interface TestConfigResult {
  ok: boolean
  message?: string
}

export async function testConfig(req: ModelConfigInput & { index?: number }): Promise<TestConfigResult> {
  return request<TestConfigResult>('/api/config/test', { method: 'POST', ...json(req) })
}

// saveModel is the flat-input shim kept for the FirstRunSetup wizard and the
// (PR5-hidden) flat AI Models section. PR5 deleted /api/config/models, so
// saveModel now projects the flat ModelConfigInput onto a single-model
// endpoint. The endpoint id is a human-readable name derived from the
// provider — "anthropic" for Anthropic, "openai" for OpenAI, "custom" for
// Custom — so the user sees a recognisable name instead of the
// "legacy-<host>-<n>" form reserved for Load's migration of old flat configs.
//
// A re-run over the same provider + base_url reuses the same id — but
// POST /api/config/endpoints (createEndpoint) 400s on an id collision by
// design (it must not silently clobber an unrelated endpoint that happens to
// share an id). So when generateEndpointID resolves to an EXISTING endpoint,
// this updates that endpoint in place via PATCH + the models sub-route
// instead of re-POSTing; only a genuinely new id goes through createEndpoint.
export async function saveModel(req: ModelConfigInput): Promise<{ ok: boolean; id?: string }> {
  const provider = req.provider || 'custom'
  const protocol = req.anthropic_format ? 'anthropic' : (provider === 'custom' ? 'openai' : undefined)
  // Resolve existing endpoints to generate a unique, readable id.
  const ep = await getEndpoints().catch(() => ({ endpoints: [], default: '', lite: '' }))
  const endpointID = generateEndpointID(provider, req.base_url || '', ep.endpoints)
  const existing = ep.endpoints.some(e => e.id === endpointID)
  if (existing) {
    await updateEndpoint(endpointID, {
      provider,
      base_url: req.base_url || undefined,
      api_key: req.api_key || undefined, // empty = keep the stored key (server-side "unchanged" rule)
      protocol,
    })
    await addEndpointModel(endpointID, req.model, req.vision ?? false)
  } else {
    await createEndpoint({
      id: endpointID,
      provider,
      base_url: req.base_url || undefined,
      api_key: req.api_key || undefined,
      protocol,
      models: [{ model: req.model, vision: req.vision ?? false }],
    })
  }
  // Set as default, pinned to the exact model just saved — neither path above
  // auto-sets Default, and an existing endpoint may already carry other
  // models, so the no-model fallback (first model) could point elsewhere.
  // The hidden flat section's "add model" path also lands here, but PR6's
  // new endpoint editor uses createEndpoint directly with its own default
  // toggle.
  try {
    await setEndpointDefault(endpointID, req.model)
  } catch {
    // non-fatal: endpoint was created/updated, default just didn't stick
  }
  return { ok: true, id: req.model }
}

// generateEndpointID produces a human-readable endpoint id for the wizard:
// the provider id for a named vendor ("anthropic", "openai", ...), or "custom"
// for the Custom vendor. The "legacy-<host>-<n>" form is reserved for Load's
// migration of old flat configs; new wizard entries get a name the user can
// recognise in the endpoint list.
//
// Re-running the wizard against the same provider + base_url reuses the
// existing endpoint id (overwrite) rather than creating a duplicate. If the
// natural id is already taken by an unrelated endpoint, "-1", "-2", ... are
// appended until free.
export function generateEndpointID(provider: string, baseURL: string, existing: EndpointConfig[]): string {
  const base = (provider && provider !== 'custom') ? provider : 'custom'
  // Overwrite candidate: same provider AND same base_url (both empty counts
  // as equal — named vendors resolve base_url at runtime, so an empty
  // base_url is the canonical "this provider's default endpoint").
  for (const ep of existing) {
    if (ep.provider === provider && (ep.base_url ?? '') === baseURL) {
      return ep.id
    }
  }
  const taken = (id: string) => existing.some(e => e.id === id)
  if (!taken(base)) return base
  for (let n = 1; ; n++) {
    const id = `${base}-${n}`
    if (!taken(id)) return id
  }
}

// freshEndpointID picks an unused id for a NEW endpoint. Unlike
// generateEndpointID it never reuses an existing endpoint's id — the wizard's
// overwrite semantics don't apply to an explicit "Add endpoint" form, where a
// reused id would just make the create request fail with an id conflict.
export function freshEndpointID(provider: string, existing: EndpointConfig[]): string {
  const base = (provider && provider !== 'custom') ? provider : 'custom'
  const taken = (id: string) => existing.some(e => e.id === id)
  if (!taken(base)) return base
  for (let n = 1; ; n++) {
    const id = `${base}-${n}`
    if (!taken(id)) return id
  }
}

// The four flat-Models mutations below are STUBS. PR5 deleted their backend
// routes (/api/config/models*), so calling them throws. They're kept only so
// the (PR5-hidden, {#if false}) flat AI Models section in SettingsModal still
// compiles — Slice 6.3 replaces that section with an endpoint editor and
// deletes these stubs along with it.
export async function updateModel(_id: string, _req: ModelConfigInput): Promise<void> {
  throw new Error('updateModel is removed — use updateEndpoint (PR5 deleted /api/config/models)')
}
export async function deleteModel(_id: string): Promise<void> {
  throw new Error('deleteModel is removed — use deleteEndpoint (PR5 deleted /api/config/models)')
}
export async function setDefaultModel(_id: string): Promise<void> {
  throw new Error('setDefaultModel is removed — use setEndpointDefault (PR5 deleted /api/config/models)')
}
export async function setLiteModel(_id: string): Promise<{ ok: boolean; lite_model: string }> {
  throw new Error('setLiteModel is removed — use setEndpointLite (PR5 deleted /api/config/models)')
}
