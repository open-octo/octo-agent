// Package octoagent exports the public Go API for building agentic applications
// on top of octo-agent's core loop without importing internal packages.
//
// All types in this package are aliases (type X = agent.X) rather than new
// types, so values can be passed freely between octo-agent's own binaries and
// external consumers without conversion.
//
// # Concurrency safety
//
// Several exported types embed a sync.Mutex or sync.RWMutex and must never be
// copied by value. Always pass *Session, *History, and *Inbox as pointers.
//
// # What's not exported here
//
// Hook engine types (Agent.Hooks, Agent.HookMeta) live in internal/hooks; see
// pkg/octoagent/hooks for their aliases. Leave both fields zero if you don't
// need custom hooks. The MCP client registry is not exported yet; using MCP
// tools from a library consumer requires a separate design pass.
package octoagent

import "github.com/open-octo/octo-agent/internal/agent"

// Agent aliases the core agent loop owner.
//
// Do not copy Agent by value; it contains internal mutexes and accumulators.
type Agent = agent.Agent

// Session is a durable conversation record. It also implements GoalAccountant.
//
// Do not copy Session by value; it embeds sync.Mutex and is only safe to use
// through a pointer. External consumers should generally avoid Session.Save()/Load()
// because those write to ~/.octo/sessions by default. Use Session for its field
// shape or as a GoalAccountant, and persist Messages yourself.
type Session = agent.Session

// History is the in-memory conversation log.
//
// Do not copy History by value; it embeds sync.RWMutex and is only safe to use
// through a pointer.
type History = agent.History

// GoalAccountant receives per-turn token deltas for goal tracking.
type GoalAccountant = agent.GoalAccountant

// Goal is a session's persistent objective.
type Goal = agent.Goal

// GoalStatus is the lifecycle state of a goal.
type GoalStatus = agent.GoalStatus

// Inbox holds user messages that arrived while a turn was running.
//
// Do not copy Inbox by value; it embeds sync.Mutex and is only safe to use
// through a pointer.
type Inbox = agent.Inbox

// InboxItem is one queued user message.
type InboxItem = agent.InboxItem

// Message is a single turn in the conversation.
type Message = agent.Message

// Role is the message author role.
type Role = agent.Role

// ContentBlock is a rich content block carried in a Message.
type ContentBlock = agent.ContentBlock

// ToolDefinition describes a tool the LLM may invoke.
type ToolDefinition = agent.ToolDefinition

// ToolResult is the return value from a tool execution.
type ToolResult = agent.ToolResult

// ToolExecutor dispatches tool calls on behalf of the agentic loop.
type ToolExecutor = agent.ToolExecutor

// Sender is the minimal provider interface the Agent depends on.
type Sender = agent.Sender

// StreamingSender extends Sender with streaming delivery.
type StreamingSender = agent.StreamingSender

// ToolSender extends Sender with tool-aware requests.
type ToolSender = agent.ToolSender

// ToolStreamingSender extends Sender with streaming tool-aware requests.
type ToolStreamingSender = agent.ToolStreamingSender

// LowEffortSender can produce a cheaper variant of itself for throwaway calls.
type LowEffortSender = agent.LowEffortSender

// PermissionGate decides whether a tool call may proceed.
type PermissionGate = agent.PermissionGate

// EventHandler receives AgentEvents from RunStream.
type EventHandler = agent.EventHandler

// AgentEvent is the union shape carried over the EventHandler callback.
type AgentEvent = agent.AgentEvent

// EventKind tags an AgentEvent by what happened.
type EventKind = agent.EventKind

// Reply is the agent-level view of a provider response.
type Reply = agent.Reply

// New constructs an Agent with a fresh History.
func New(sender Sender, model string) *Agent { return agent.New(sender, model) }

// NewUserMessage constructs a Message with RoleUser and a current timestamp.
func NewUserMessage(content string) Message { return agent.NewUserMessage(content) }

// NewAssistantMessage constructs a Message with RoleAssistant and a current timestamp.
func NewAssistantMessage(content string) Message { return agent.NewAssistantMessage(content) }

// NewSystemMessage constructs a Message with RoleSystem.
func NewSystemMessage(content string) Message { return agent.NewSystemMessage(content) }

// NewToolUseMessage constructs an assistant Message carrying tool_use blocks.
func NewToolUseMessage(blocks []ContentBlock) Message { return agent.NewToolUseMessage(blocks) }

// NewToolResultMessage constructs a user Message carrying tool_result blocks.
func NewToolResultMessage(results []ContentBlock) Message { return agent.NewToolResultMessage(results) }

// NewTextBlock creates a ContentBlock with Type=="text".
func NewTextBlock(text string) ContentBlock { return agent.NewTextBlock(text) }

// NewToolUseBlock creates a ContentBlock with Type=="tool_use".
func NewToolUseBlock(id, name string, input map[string]any) ContentBlock {
	return agent.NewToolUseBlock(id, name, input)
}

// NewToolResultBlock creates a ContentBlock with Type=="tool_result".
func NewToolResultBlock(toolUseID, result string, isError bool) ContentBlock {
	return agent.NewToolResultBlock(toolUseID, result, isError)
}

// NewImageBlock creates a ContentBlock with Type=="image".
func NewImageBlock(mimeType string, data []byte) ContentBlock {
	return agent.NewImageBlock(mimeType, data)
}

// NewThinkingBlock creates a ContentBlock with Type=="thinking".
func NewThinkingBlock(thinking, signature string) ContentBlock {
	return agent.NewThinkingBlock(thinking, signature)
}

// Role constants.
const (
	RoleSystem    = agent.RoleSystem
	RoleUser      = agent.RoleUser
	RoleAssistant = agent.RoleAssistant
)

// Stop reason sentinels returned in Reply.StopReason.
const (
	StopReasonMaxTurns    = agent.StopReasonMaxTurns
	StopReasonInterrupted = agent.StopReasonInterrupted
	StopReasonMaxTokens   = agent.StopReasonMaxTokens
	StopReasonStuck       = agent.StopReasonStuck
)

// Event kinds for RunStream handlers.
const (
	EventTextDelta       = agent.EventTextDelta
	EventThinkingDelta   = agent.EventThinkingDelta
	EventToolInputDelta  = agent.EventToolInputDelta
	EventToolStarted     = agent.EventToolStarted
	EventToolProgress    = agent.EventToolProgress
	EventToolDone        = agent.EventToolDone
	EventToolError       = agent.EventToolError
	EventTurnDone        = agent.EventTurnDone
	EventTurnError       = agent.EventTurnError
	EventSteerInjected   = agent.EventSteerInjected
	EventCompactStarted  = agent.EventCompactStarted
	EventCompactProgress = agent.EventCompactProgress
	EventCompactDone     = agent.EventCompactDone
	EventGoalUpdated     = agent.EventGoalUpdated
)

// EventToolOutputCap is the maximum length of the Output field emitted on
// EventToolDone / EventToolError.
const EventToolOutputCap = agent.EventToolOutputCap

// Goal lifecycle states.
const (
	GoalActive        = agent.GoalActive
	GoalPaused        = agent.GoalPaused
	GoalBlocked       = agent.GoalBlocked
	GoalUsageLimited  = agent.GoalUsageLimited
	GoalBudgetLimited = agent.GoalBudgetLimited
	GoalComplete      = agent.GoalComplete
)
