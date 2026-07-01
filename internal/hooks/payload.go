package hooks

// Event names the lifecycle points at which the engine dispatches hooks. The
// set and semantics mirror Claude Code's hook model (so a CC hook script ports
// with only field-name changes), but the stdin/stdout field names are
// octo-native (see payload.go / the design doc's "Parity 定位").
type Event string

const (
	// EventSessionStart fires once per logical session opening (see
	// SessionStartSource for the startup/resume/clear discrimination). Its
	// stdout is injected into the first user message and thus persisted.
	EventSessionStart Event = "SessionStart"
	// EventUserPromptSubmit fires before each user turn. Its stdout is folded
	// into that turn's user message (fresh every turn, persisted per-turn).
	EventUserPromptSubmit Event = "UserPromptSubmit"
	// EventPreToolUse fires before each tool dispatch. It can block/allow a
	// tool (blocking protocol lands in a later phase).
	EventPreToolUse Event = "PreToolUse"
	// EventPostToolUse fires after each successful tool result. Its stdout is
	// appended to that tool_result's text.
	EventPostToolUse Event = "PostToolUse"
	// EventStop fires when an assistant turn ends — on success AND on
	// failure/interrupt. Side-effect only (retention); stdout ignored.
	EventStop Event = "Stop"
	// EventSubagentStop fires when a spawned sub-agent finishes. Side-effect
	// only.
	EventSubagentStop Event = "SubagentStop"
	// EventPreCompact fires before history compaction. Side-effect only.
	EventPreCompact Event = "PreCompact"
)

// injects reports whether an event's hook stdout is folded back into the model
// stream. Side-effect events (Stop/SubagentStop/PreCompact) discard stdout.
func (e Event) injects() bool {
	switch e {
	case EventSessionStart, EventUserPromptSubmit, EventPostToolUse:
		return true
	default:
		return false
	}
}

// Payload is the JSON envelope written to a hook's stdin. Every event carries
// the common fields (session_id / cwd / transcript_path / model / transport);
// event-specific fields are populated only for the events that define them and
// omitted otherwise. This is the surface external retrieval layers key on — the
// pre-redesign hooks passed only user_input, which is why a hook couldn't tell
// which session or transport it was serving.
type Payload struct {
	Event          Event  `json:"event"`
	SessionID      string `json:"session_id,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Model          string `json:"model,omitempty"`
	Transport      string `json:"transport,omitempty"`

	// SessionStart.
	Source string `json:"source,omitempty"` // startup | resume | clear

	// UserPromptSubmit / Stop.
	UserInput string `json:"user_input,omitempty"`

	// PreToolUse / PostToolUse.
	ToolName  string         `json:"tool_name,omitempty"`
	ToolInput map[string]any `json:"tool_input,omitempty"`

	// PostToolUse.
	ToolResult string `json:"tool_result,omitempty"`

	// Stop.
	AssistantReply string   `json:"assistant_reply,omitempty"`
	ToolsUsed      []string `json:"tools_used,omitempty"`
	Error          string   `json:"error,omitempty"` // set on a failed/interrupted turn; empty on success
}

// Meta is the per-session identity the agent folds into every hook Payload's
// common envelope. The session-owning layer (CLI/server) sets it on the Agent
// before a run, the way Session.ChunkDir sets the archive dir; the agent then
// stamps the event-specific fields per call. Keeping it in one struct means the
// agent's insertion points don't each re-plumb five identity fields.
type Meta struct {
	SessionID      string
	Transport      string // cli | tui | web | im | subagent
	TranscriptPath string
	Cwd            string
	Model          string
}

// Payload seeds a Payload for event with the common envelope from m. Callers
// fill in the event-specific fields (UserInput, ToolName, …) afterwards.
func (m Meta) Payload(event Event) Payload {
	return Payload{
		Event:          event,
		SessionID:      m.SessionID,
		Cwd:            m.Cwd,
		TranscriptPath: m.TranscriptPath,
		Model:          m.Model,
		Transport:      m.Transport,
	}
}
