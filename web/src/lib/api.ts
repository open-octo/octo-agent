import type { Session, Skill, ScheduledTask, McpServer, Channel, Memory, RecallFile } from './types'

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
  return request<Session>('/api/sessions', { method: 'POST', ...json(opts) })
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

export async function updateSessionModel(id: string, model: string): Promise<void> {
  await request<unknown>(`/api/sessions/${id}/model`, { method: 'PATCH', ...json({ model }) })
}

export async function updateSessionReasoningEffort(id: string, effort: string): Promise<void> {
  await request<unknown>(`/api/sessions/${id}/reasoning_effort`, {
    method: 'PATCH',
    ...json({ reasoning_effort: effort }),
  })
}

export async function updateSessionWorkingDir(id: string, dir: string): Promise<void> {
  await request<unknown>(`/api/sessions/${id}/working_dir`, {
    method: 'PATCH',
    ...json({ working_dir: dir }),
  })
}

// Skills

export async function listSkills(): Promise<Skill[]> {
  return request<Skill[]>('/api/skills')
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

export async function importSkill(file: File): Promise<Skill> {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch('/api/skills/import', { method: 'POST', body: form })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<Skill>
}

// Tasks

export async function listTasks(): Promise<ScheduledTask[]> {
  return request<ScheduledTask[]>('/api/tasks')
}

export async function createTask(req: unknown): Promise<ScheduledTask> {
  return request<ScheduledTask>('/api/tasks', { method: 'POST', ...json(req) })
}

export async function deleteTask(id: string): Promise<void> {
  await request<unknown>(`/api/tasks/${id}`, { method: 'DELETE' })
}

export async function runTask(id: string): Promise<void> {
  await request<unknown>(`/api/tasks/${id}/run`, { method: 'POST' })
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

export interface CreateMcpServerOpts {
  name: string
  command?: string
  args?: string[]
  url?: string
  transport?: string
}

export async function createMcpServer(opts: CreateMcpServerOpts): Promise<void> {
  const { name, command, args, url, ...rest } = opts
  const server: Record<string, unknown> = {}
  if (command) { server.command = command; if (args) server.args = args }
  if (url) server.url = url
  await request<unknown>('/api/mcp/servers', { method: 'POST', ...json({ name, server }) })
}

export async function updateMcpServer(name: string, req: unknown): Promise<McpServer> {
  return request<McpServer>(`/api/mcp/servers/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    ...json(req),
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

export async function updateToolSearch(mode: 'auto' | 'on' | 'off'): Promise<void> {
  await request<unknown>('/api/config/toolsearch', { method: 'PUT', ...json({ enabled: mode }) })
}

// Channels

export async function listChannels(): Promise<Channel[]> {
  return request<Channel[]>('/api/channels')
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
  return request<Memory[]>('/api/memories')
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
  before?: string
}

export async function emptyTrash(opts?: EmptyTrashOpts): Promise<void> {
  await request<unknown>('/api/trash/empty', { method: 'POST', ...json(opts ?? {}) })
}

// Config & Version

export async function getConfig(): Promise<unknown> {
  return request<unknown>('/api/config')
}

export async function getVersion(): Promise<unknown> {
  return request<unknown>('/api/version')
}
