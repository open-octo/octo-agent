import type { Session, Skill, ScheduledTask, McpServer, McpServerDetail, Channel, Memory, RecallFile, TagStatus } from './types'

// TaskResponse matches the Go server task struct.
export interface TaskResponse {
  id: string
  name: string
  cron: string
  prompt: string
  model: string
  agent: string
  notify: string
  enabled: boolean
  created_at: string
  last_run: string
  session_id: string
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init)
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText}`)
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

export interface MessagesOpts {
  limit?: number
  before?: string
}

export async function getSessionMessages(id: string, opts?: MessagesOpts): Promise<unknown> {
  const params = new URLSearchParams()
  if (opts?.limit !== undefined) params.set('limit', String(opts.limit))
  if (opts?.before !== undefined) params.set('before', opts.before)
  const qs = params.toString()
  return request<unknown>(`/api/sessions/${id}/messages${qs ? `?${qs}` : ''}`)
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

export async function updateSessionWorkingDir(id: string, dir: string): Promise<void> {
  await request<unknown>(`/api/sessions/${id}/working_dir`, {
    method: 'PATCH',
    ...json({ working_dir: dir }),
  })
}

// Skills

// The server returns { skills: [{name, description, source, enabled}] }. Map it
// to the display shape the table expects (desc/icon/tag), since the server has
// no icon/version/status of its own.
interface SkillInfoRaw {
  name: string
  description?: string
  source?: string
  enabled?: boolean
}
export async function listSkills(): Promise<Skill[]> {
  const d = await request<{ skills: SkillInfoRaw[] }>('/api/skills')
  return (d.skills ?? []).map((s): Skill => {
    // Server source is "default" (built-in/system) | "project" | "user".
    const src = s.source ?? 'user'
    const tag: { tagStatus: TagStatus; tagLabel: string } = src === 'project'
      ? { tagStatus: 'info', tagLabel: 'Project' }
      : src === 'default'
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
  search_default_limit: number
  max_search_limit: number
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

export async function getMcpServer(name: string): Promise<McpServerDetail> {
  return request<McpServerDetail>(`/api/mcp/servers/${encodeURIComponent(name)}`)
}

export interface CreateMcpServerOpts {
  name: string
  command?: string
  args?: string[]
  url?: string
  transport?: string
  allowArbitraryCommand?: boolean
}

export async function createMcpServer(opts: CreateMcpServerOpts): Promise<void> {
  const { name, command, args, url, allowArbitraryCommand, ...rest } = opts
  const server: Record<string, unknown> = {}
  if (command) { server.command = command; if (args) server.args = args }
  if (url) server.url = url
  const body: Record<string, unknown> = { name, server }
  if (allowArbitraryCommand) body.allow_arbitrary_command = true
  await request<unknown>('/api/mcp/servers', { method: 'POST', ...json(body) })
}

// Bulk-import servers from a pasted JSON config: either a full
// { mcpServers: { name: {...} } } document or a bare { name: {...} } map.
export async function importMcpServers(servers: Record<string, unknown>): Promise<void> {
  await request<unknown>('/api/mcp/servers', { method: 'POST', ...json({ mcpServers: servers }) })
}

export interface UpdateMcpServerOpts {
  server: Record<string, unknown>
  allowArbitraryCommand?: boolean
}

export async function updateMcpServer(name: string, req: UpdateMcpServerOpts): Promise<McpServer> {
  const body: Record<string, unknown> = { server: req.server }
  if (req.allowArbitraryCommand) body.allow_arbitrary_command = true
  return request<McpServer>(`/api/mcp/servers/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    ...json(body),
  })
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

// MCP OAuth device flow. start launches it in the background and returns the
// initial snapshot; poll status until the state settles (connected | failed).
export interface McpOAuthState {
  state: 'starting' | 'authorizing' | 'connected' | 'failed'
  user_code?: string
  verification_uri?: string
  verification_uri_complete?: string
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

// Trash

export async function listTrash(): Promise<RecallFile[]> {
  return request<RecallFile[]>('/api/trash')
}

export async function restoreTrash(id: string): Promise<void> {
  await request<unknown>(`/api/trash/${id}/restore`, { method: 'POST' })
}

export async function deleteTrashItem(id: string): Promise<void> {
  await request<unknown>(`/api/trash/${id}`, { method: 'DELETE' })
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
}
export interface ConfigResponse {
  models?: ModelEntry[]
  default_model_idx?: number
  font_size?: string
  language?: string
  show_reasoning?: boolean
}

export async function getConfig(): Promise<ConfigResponse> {
  return request<ConfigResponse>('/api/config')
}

export async function updateShowReasoning(showReasoning: boolean): Promise<{ ok: boolean; show_reasoning?: boolean }> {
  return request<{ ok: boolean; show_reasoning?: boolean }>('/api/config/show_reasoning', {
    method: 'PUT',
    ...json({ show_reasoning: showReasoning }),
  })
}

export async function getVersion(): Promise<unknown> {
  return request<unknown>('/api/version', { cache: 'no-store' })
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

// Onboard / first-run

export interface OnboardStatus {
  needs_onboard: boolean
  phase: '' | 'key_setup' | 'soul_setup'
}

export async function getOnboardStatus(): Promise<OnboardStatus> {
  return request<OnboardStatus>('/api/onboard/status')
}

export async function completeOnboard(): Promise<void> {
  await request<unknown>('/api/onboard/complete', { method: 'POST' })
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
}

export interface TestConfigResult {
  ok: boolean
  message?: string
}

export async function testConfig(req: ModelConfigInput & { index?: number }): Promise<TestConfigResult> {
  return request<TestConfigResult>('/api/config/test', { method: 'POST', ...json(req) })
}

export async function saveModel(req: ModelConfigInput): Promise<{ ok: boolean; id?: string }> {
  return request<{ ok: boolean; id?: string }>('/api/config/models', { method: 'POST', ...json(req) })
}

export async function updateModel(id: string, req: ModelConfigInput): Promise<void> {
  await request<unknown>(`/api/config/models/${encodeURIComponent(id)}`, { method: 'PATCH', ...json(req) })
}

export async function deleteModel(id: string): Promise<void> {
  await request<unknown>(`/api/config/models/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function setDefaultModel(id: string): Promise<void> {
  await request<unknown>(`/api/config/models/${encodeURIComponent(id)}/default`, { method: 'POST' })
}

export async function setLiteModel(id: string): Promise<{ ok: boolean; lite_model: string }> {
  return request<{ ok: boolean; lite_model: string }>(`/api/config/models/${encodeURIComponent(id)}/lite`, { method: 'POST' })
}
