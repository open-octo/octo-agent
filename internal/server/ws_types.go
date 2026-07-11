package server

import "encoding/json"

// ─── Client → Server messages (inbound) ─────────────────────────────────────

// wsInMessage is the base envelope for every client-sent message.
type wsInMessage struct {
	Type string `json:"type"`
}

type wsMsgListSessions struct{}

type wsMsgSubscribe struct {
	SessionID string `json:"session_id"`
}

type wsMsgUnsubscribe struct {
	SessionID string `json:"session_id"`
}

type wsMsgUserMessage struct {
	SessionID string          `json:"session_id"`
	Content   json.RawMessage `json:"content"` // string or array (multipart)
	Files     []wsUserFile    `json:"files,omitempty"`
	// Force allows the web UI to take over a session bound to another entry.
	// The server still refuses if the other entry holds an active turn lease.
	Force bool `json:"force,omitempty"`
}

type wsUserFile struct {
	Name string `json:"name"`
	// DataURL carries an image attachment inline (base64 data URL).
	DataURL string `json:"data_url,omitempty"`
	// Path references a document already uploaded via POST /api/upload
	// (an /api/uploads/<name> URL).
	Path string `json:"path,omitempty"`
	// NativePath is a real absolute path on the local machine, chosen via the
	// desktop shell's native file dialog — no upload. Honored only in the
	// desktop build (a NativeBridge is present); ignored under `octo serve` so
	// a remote client can't make the agent read arbitrary server files.
	NativePath string `json:"native_path,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
}

type wsMsgInterrupt struct {
	SessionID string `json:"session_id"`
}

type wsMsgConfirmation struct {
	// The frontend answers with `id` (app.js showConfirmModal); `conf_id`
	// is accepted as a fallback for anything speaking the older shape.
	ConfID       string `json:"id"`
	LegacyConfID string `json:"conf_id"`
	Result       string `json:"result"`
}

type wsMsgUserQuestionAnswer struct {
	QuestionID string   `json:"question_id"`
	Choices    []string `json:"choices,omitempty"`
	Custom     string   `json:"custom,omitempty"`
	Cancelled  bool     `json:"cancelled,omitempty"`
}

type wsMsgRetry struct {
	SessionID string `json:"session_id"`
}

type wsMsgRollback struct {
	SessionID string `json:"session_id"`
}

type wsMsgRunTask struct {
	SessionID string `json:"session_id"`
}

type wsMsgUpdateSettings struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// ─── Server → Client events (outbound) ────────────────────────────────────

// wsOutEvent is the base envelope for every server-sent event.
type wsOutEvent struct {
	Type string `json:"type"`
}

// wsEventSessionList carries the full session list sent on connect + refresh.
type wsEventSessionList struct {
	Type     string          `json:"type"`
	Sessions []wsSessionInfo `json:"sessions"`
}

type wsSessionInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status,omitempty"` // "idle" | "working"
	CreatedAt       int64  `json:"created_at"`       // unix ms
	Source          string `json:"source,omitempty"` // "manual" | "cron" | "channel" | "setup"
	Model           string `json:"model,omitempty"`
	TotalTurns      int    `json:"total_turns,omitempty"`
	WorkingDir      string `json:"working_dir,omitempty"`
	PermissionMode  string `json:"permission_mode,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ShowReasoning   *bool  `json:"show_reasoning,omitempty"`
	ContextUsage    int    `json:"context_usage,omitempty"`
	PendingQuestion bool   `json:"pending_question,omitempty"`
}

type wsEventHistoryUserMessage struct {
	Type      string   `json:"type"`
	Content   string   `json:"content"`
	CreatedAt int64    `json:"created_at,omitempty"`
	Images    []string `json:"images,omitempty"`
}

type wsEventAssistantMessage struct {
	Type    string   `json:"type"`
	Content string   `json:"content"`
	Files   []string `json:"files,omitempty"`
}

type wsEventToolCall struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Args    any    `json:"args"`
	Summary string `json:"summary,omitempty"`
}

type wsEventToolResult struct {
	Type      string `json:"type"`
	Result    string `json:"result"`
	UIPayload any    `json:"ui_payload,omitempty"`
}

type wsEventToolError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

type wsEventToolStdout struct {
	Type  string   `json:"type"`
	Lines []string `json:"lines"`
}

type wsEventOutput struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type wsEventProgress struct {
	Type         string  `json:"type"`
	Message      string  `json:"message,omitempty"`
	ProgressType string  `json:"progress_type,omitempty"`
	Phase        string  `json:"phase"` // "active" | "done"
	Status       string  `json:"status,omitempty"`
	StartedAt    int64   `json:"started_at,omitempty"` // unix ms
	Elapsed      float64 `json:"elapsed,omitempty"`
}

type wsEventComplete struct {
	Type                 string  `json:"type"`
	Iterations           int     `json:"iterations"`
	Duration             float64 `json:"duration,omitempty"`
	CacheStats           any     `json:"cache_stats,omitempty"`
	AwaitingUserFeedback bool    `json:"awaiting_user_feedback,omitempty"`
}

type wsEventSessionUpdate struct {
	Type            string  `json:"type"`
	Status          string  `json:"status,omitempty"`
	Tasks           int     `json:"tasks,omitempty"`
	Latency         float64 `json:"latency,omitempty"`
	ContextUsage    int     `json:"context_usage,omitempty"` // 0–100 context window %
	WorkingDir      string  `json:"working_dir,omitempty"`
	PermissionMode  string  `json:"permission_mode,omitempty"`
	ReasoningEffort string  `json:"reasoning_effort,omitempty"`
}

type wsEventTodoUpdate struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Todos     any    `json:"todos"`
}

type wsEventSessionDeleted struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

type wsEventRequestFeedback struct {
	Type string `json:"type"`
	// SessionID is required: the ws-dispatcher drops any event whose session_id
	// doesn't match the active session, so without it a request_feedback event
	// would always be discarded — the same session-less footgun #613 fixed for
	// request_confirmation.
	SessionID string   `json:"session_id"`
	Question  string   `json:"question"`
	Context   string   `json:"context,omitempty"`
	Options   []string `json:"options,omitempty"`
}

type wsEventRequestConfirmation struct {
	Type string `json:"type"`
	// SessionID and the `id` tag match what the dispatcher actually reads
	// (ev.session_id filter, ev.id answer key) — the old conf_id-only,
	// session-less shape made the dispatcher drop the event, so the modal
	// never appeared and every web permission ask timed out to deny.
	SessionID string `json:"session_id"`
	ConfID    string `json:"id"`
	Message   string `json:"message"`
	Kind      string `json:"kind"` // "yes_no" | "yes_no_always" | "ok"

	// Detail fields for #1105: the modal used to show only Message ("Allow
	// terminal?") with no indication of what's actually being approved. At
	// most one of these is set, chosen by tool kind in permissionAskFrom.
	ToolName string `json:"tool_name,omitempty"`
	Command  string `json:"command,omitempty"` // terminal: the full command
	Diff     string `json:"diff,omitempty"`    // edit_file: removed/added preview (tools.EditUIDiff)
	Input    string `json:"input,omitempty"`   // other tools: sorted "key: value" lines
}

// wsEventConfirmationComplete tells all connected clients that a permission
// confirmation has been answered (by any client). It lets secondary tabs close
// the modal so the user isn't asked again for the same action.
type wsEventConfirmationComplete struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	ConfID    string `json:"id"`
	Result    string `json:"result"`
}

type wsEventRequestUserQuestion struct {
	Type string `json:"type"`
	// SessionID is required by the dispatcher's session filter — without
	// it the browser drops the event and the question modal never shows.
	SessionID   string   `json:"session_id"`
	QuestionID  string   `json:"question_id"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	MultiSelect bool     `json:"multi_select"`
	Header      string   `json:"header,omitempty"`
}

// wsEventDismissUserQuestion tells the browser to close the question modal
// that was opened by request_user_question. Sent when the question times out
// or the agent context is cancelled before the user answers.
type wsEventDismissUserQuestion struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id"`
	QuestionID string `json:"question_id"`
}

// wsEventSessionActivity is a lightweight cross-session signal broadcast
// globally (wsHub.broadcast("", ev)) rather than to a session's subscribers.
// request_user_question / session_update / complete only reach tabs
// currently subscribed to that exact session, so a tab looking at session B
// never learns session A got a question or finished its turn. This event
// carries no payload beyond "what happened, to which session" — the sidebar
// badge and desktop-notification logic key off Kind, and don't need the full
// question text or turn stats that already went out on the per-session path.
type wsEventSessionActivity struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"` // "question_pending" | "question_resolved" | "turn_complete"
}

type wsEventBackgroundTaskUpdate struct {
	Type      string             `json:"type"`
	SessionID string             `json:"session_id"`
	Running   int                `json:"running"`
	Tasks     []wsBackgroundTask `json:"tasks"`
}

type wsBackgroundTask struct {
	HandleID string `json:"handle_id"`
	Command  string `json:"command"`
	Elapsed  int    `json:"elapsed"`
}

type wsEventBackgroundTaskNotice struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
	HandleID  string `json:"handle_id"`
	Status    string `json:"status"`
}

type wsEventSubAgentNotice struct {
	Type        string `json:"type"`
	SessionID   string `json:"session_id"`
	AgentID     string `json:"agent_id"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
}

type wsEventUserMessageQueueStatus struct {
	Type    string `json:"type"`
	Pending int    `json:"pending"`
}

type wsEventNextMessageSuggestion struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wsEventDiff struct {
	Type      string `json:"type"`
	OldSize   int    `json:"old_size"`
	NewSize   int    `json:"new_size"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated,omitempty"`
}

type wsEventFilePreview struct {
	Type      string `json:"type"`
	Path      string `json:"path"`
	Operation string `json:"operation"`
	IsNewFile bool   `json:"is_new_file,omitempty"`
}

type wsEventShellPreview struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// wsInPromoteSyncTerminal is sent by the browser when the user clicks the
// "Background" button on a running terminal tool card.
type wsInPromoteSyncTerminal struct {
	SessionID string `json:"session_id"`
}

// wsInPromoteSyncSubAgent is sent by the browser when the user clicks the
// "Background" button on a running synchronous sub_agent tool card.
type wsInPromoteSyncSubAgent struct {
	SessionID string `json:"session_id"`
}
