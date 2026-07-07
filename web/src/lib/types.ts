export type View = 'chat' | 'skills' | 'workflows' | 'tasks' | 'mcp' | 'channels' | 'settings' | 'profile' | 'files'
export type SidebarMode = 'full' | 'rail' | 'hidden'
export type MemTab = 'soul' | 'user' | 'memories'
export type ArtifactView = 'preview' | 'code'
export type TagStatus = 'success' | 'info' | 'warning' | 'error' | 'default'

// Session matches the server-side session item returned by the REST API and
// broadcast over the WebSocket. The UI stores it as-is; title/name may differ
// depending on which endpoint produced the record.
export interface Session {
  id: string
  name: string
  title: string
  created_at: string
  updated_at: string
  model: string
  model_id: string
  status: 'idle' | 'working' | string
  source: string
  agent_profile: string
  pinned: boolean
  total_tasks: number
  turn_count: number
  working_dir: string
  permission_mode: 'interactive' | 'auto' | 'strict' | string
  reasoning_effort: 'low' | 'medium' | 'high' | string
  show_reasoning?: boolean
  context_usage: number
  // Optional UI-only fields carried by some broadcasts.
  time?: string
  icon?: string
}

export interface Skill {
  name: string
  desc: string
  icon: string
  tagStatus: TagStatus
  tagLabel: string
  enabled: boolean
  source: string
}

export interface Workflow {
  name: string
  desc: string
  icon: string
  tagStatus: TagStatus
  tagLabel: string
  source: string
}

export interface ScheduledTask {
  name: string
  target: string
  cron: string
  nextRun: string
  tagStatus: TagStatus
  tagLabel: string
}

export interface McpServer {
  name: string
  command: string
  transport: string
  tools: number
  icon: string
  tagStatus: TagStatus
  tagLabel: string
  enabled: boolean
}

export interface Channel {
  name: string
  handle: string
  activity: string
  icon: string
  tagStatus: TagStatus
  tagLabel: string
  enabled: boolean
}

export interface Memory {
  name: string
  path: string
  size: number
  updated_at: string
  source: string
}

export interface RecallFile {
  name: string
  path: string
  size: string
  age: string
  icon: string
  orphan: boolean
}

export interface Artifact {
  name: string
  type: string
  ver: string
  short: string
  icon: string
  code: string
  preview: string
  path: string
}

// SkillInfo matches the Go server skill struct
export interface SkillInfo {
  name: string
  description: string
  source: string
  enabled: boolean
}

// McpServerInfo matches the Go server MCP server struct
export interface McpServerInfo {
  name: string
  transport: string
  source: string
  disabled: boolean
  invalid: boolean
  command: string
  args: string[]
  env: Record<string, string>
  url: string
  headers: Record<string, string>
  auth: string
  status: 'connected' | 'error' | 'disabled' | 'invalid' | 'disconnected'
  error: string
  tools: string[]
}

// McpTool matches a single advertised tool from a connected MCP server.
export interface McpTool {
  name: string
  description?: string
  inputSchema?: Record<string, any>
}

// McpServerDetail matches the Go server mcpServerDetail struct.
export interface McpServerDetail {
  name: string
  transport: string
  source: string
  disabled: boolean
  invalid?: string
  command?: string
  args?: string[]
  env?: Record<string, string>
  url?: string
  headers?: Record<string, string>
  auth?: string
  status: 'connected' | 'error' | 'disabled' | 'invalid' | 'disconnected'
  error?: string
  tools: number
  tool_list?: McpTool[]
}

// ToolSearchSettings matches the Go server tool search settings struct
export interface ToolSearchSettings {
  enabled: 'auto' | 'on' | 'off'
  threshold_pct: number
  search_default_limit: number
  max_search_limit: number
}

// WsSessionInfo matches the Go server WebSocket session info struct
export interface WsSessionInfo {
  id: string
  name: string
  status: string
  created_at: string
  model: string
  total_turns: number
  working_dir: string
  permission_mode: 'interactive' | 'auto' | 'strict'
  reasoning_effort: 'low' | 'medium' | 'high'
  show_reasoning?: boolean
  context_usage: number
}

// WebSocket event interfaces
export interface WsEventSessionList {
  type: 'session_list'
  sessions: WsSessionInfo[]
}

export interface WsEventOutput {
  type: 'output'
  content: string
}

export interface WsEventHistoryUserMessage {
  type: 'history_user_message'
  content: string
  created_at: string
  images: string[]
}

export interface WsEventAssistantMessage {
  type: 'assistant_message'
  content: string
  files: string[]
  thinking: string
}

export interface WsEventToolCall {
  type: 'tool_call'
  name: string
  args: string
  summary: string
}

export interface WsEventToolResult {
  type: 'tool_result'
  result: string
  ui_payload: any
}

export interface WsEventToolError {
  type: 'tool_error'
  error: string
}

export interface WsEventToolStdout {
  type: 'tool_stdout'
  lines: string[]
}

export interface WsEventProgress {
  type: 'progress'
  message: string
  progress_type: string
  phase: string
  status: string
  started_at: string
  elapsed: number
}

export interface WsEventComplete {
  type: 'complete'
  iterations: number
  duration: number
  awaiting_user_feedback: boolean
}

export interface WsEventSessionUpdate {
  type: 'session_update'
  status: string
  tasks: number
  latency: number
  context_usage: number
  working_dir: string
  permission_mode: 'interactive' | 'auto' | 'strict'
  reasoning_effort: 'low' | 'medium' | 'high'
  show_reasoning: boolean
}

export interface WsEventTodoUpdate {
  type: 'todo_update'
  todos: any[]
}

export interface WsEventSessionDeleted {
  type: 'session_deleted'
  session_id: string
}

export interface WsEventRequestFeedback {
  type: 'request_feedback'
  session_id: string
  question: string
  context: string
  options: string[]
}

export interface WsEventRequestConfirmation {
  type: 'request_confirmation'
  session_id: string
  id: string
  message: string
  kind: string
  // #1105: what's actually being approved. At most one is set.
  tool_name?: string
  command?: string // terminal
  diff?: string // edit_file
  input?: string // other tools
}

export interface WsEventRequestUserQuestion {
  type: 'request_user_question'
  session_id: string
  question_id: string
  question: string
  options: string[]
  multi_select: boolean
  header: string
}

export interface WsEventBackgroundTaskUpdate {
  type: 'background_task_update'
  session_id: string
  running: boolean
  tasks: any[]
}

export interface WsEventDiff {
  type: 'diff'
  old_size: number
  new_size: number
  diff: string
  truncated: boolean
}

export interface WsEventFilePreview {
  type: 'file_preview'
  path: string
  operation: string
  is_new_file: boolean
}

export interface WsEventShellPreview {
  type: 'shell_preview'
  command: string
}

export interface WsEventNextMessageSuggestion {
  type: 'next_message_suggestion'
  text: string
}

// Discriminated union of all WebSocket event types
export type WsEvent =
  | WsEventSessionList
  | WsEventOutput
  | WsEventHistoryUserMessage
  | WsEventAssistantMessage
  | WsEventToolCall
  | WsEventToolResult
  | WsEventToolError
  | WsEventToolStdout
  | WsEventProgress
  | WsEventComplete
  | WsEventSessionUpdate
  | WsEventTodoUpdate
  | WsEventSessionDeleted
  | WsEventRequestFeedback
  | WsEventRequestConfirmation
  | WsEventRequestUserQuestion
  | WsEventBackgroundTaskUpdate
  | WsEventDiff
  | WsEventFilePreview
  | WsEventShellPreview
  | WsEventNextMessageSuggestion

// ChatMessage is the UI-layer chat message type
export interface ChatMessage {
  id: string
  type: 'user' | 'assistant' | 'progress' | 'tool_group'
  content: string
  streaming: boolean
  createdAt: number
  tools: any[]
  todos: any[]
}
