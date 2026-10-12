package responses

// Wire types for POST /v1/responses. Field names follow the official OpenAPI
// spec (github.com/openai/openai-openapi, components.schemas CreateResponse,
// FunctionTool, ReasoningItem, ResponseUsage, ResponseStreamEvent).

type apiRequest struct {
	Model           string        `json:"model"`
	Instructions    string        `json:"instructions,omitempty"`
	Input           []any         `json:"input"`
	Tools           []apiTool     `json:"tools,omitempty"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Reasoning       *apiReasoning `json:"reasoning,omitempty"`
	PromptCacheKey  string        `json:"prompt_cache_key,omitempty"`
	Stream          bool          `json:"stream,omitempty"`
	Store           bool          `json:"store"`
}

// apiTool is a flat function tool — Responses does not nest it under a
// "function" key the way Chat Completions does.
type apiTool struct {
	Type        string         `json:"type"` // "function"
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	// Strict is sent as an explicit false: when it is omitted the API tries to
	// rewrite the schema into strict mode, which our tool schemas aren't
	// written for.
	Strict bool `json:"strict"`
}

type apiReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// Input items. Each is marshalled as its own JSON object inside Input.

type inputMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content any    `json:"content"`
	// Phase is resent on assistant messages as the model returned it.
	Phase string `json:"phase,omitempty"`
}

type inputPart struct {
	Type     string `json:"type"` // "input_text" | "input_image"
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type functionCallItem struct {
	Type      string `json:"type"` // "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type functionCallOutputItem struct {
	Type   string `json:"type"` // "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type reasoningItem struct {
	Type             string        `json:"type"` // "reasoning"
	ID               string        `json:"id"`
	Summary          []summaryPart `json:"summary"`
	EncryptedContent string        `json:"encrypted_content"`
}

type summaryPart struct {
	Type string `json:"type"` // "summary_text"
	Text string `json:"text"`
}

// apiResponse is the Response object, returned whole by a non-streaming call
// and carried by the response.completed / incomplete / failed stream events.
type apiResponse struct {
	Model             string             `json:"model"`
	Status            string             `json:"status"`
	Output            []outputItem       `json:"output"`
	IncompleteDetails *incompleteDetails `json:"incomplete_details"`
	Error             *responseError     `json:"error"`
	Usage             *apiUsage          `json:"usage"`
}

type incompleteDetails struct {
	Reason string `json:"reason"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// outputItem covers the output item types we consume: "message",
// "function_call" and "reasoning". Built-in tool calls are ignored.
type outputItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// message
	Content []outputContent `json:"content"`
	Phase   string          `json:"phase"`

	// function_call
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`

	// reasoning
	Summary          []summaryPart `json:"summary"`
	EncryptedContent string        `json:"encrypted_content"`
}

type outputContent struct {
	Type    string `json:"type"` // "output_text" | "refusal"
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

// apiUsage reports input_tokens INCLUDING the cached and cache-write parts
// (OpenAI's prompt-caching guide computes ordinary input as
// input_tokens - cached_tokens - cache_write_tokens).
type apiUsage struct {
	InputTokens        int `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens int `json:"output_tokens"`
}

// nonCachedInput is the uncached remainder, so InputTokens and the cache
// buckets stay non-overlapping as the agent expects.
func (u apiUsage) nonCachedInput() int {
	n := u.InputTokens - u.InputTokensDetails.CachedTokens - u.InputTokensDetails.CacheWriteTokens
	if n < 0 {
		return 0
	}
	return n
}

// apiError is the body of a 4xx/5xx response.
type apiError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// streamEvent is one SSE data payload. Dispatch is on Type; the other fields
// are populated depending on the event.
type streamEvent struct {
	Type        string       `json:"type"`
	OutputIndex int          `json:"output_index"`
	ItemID      string       `json:"item_id"`
	Delta       string       `json:"delta"`
	Item        *outputItem  `json:"item"`
	Response    *apiResponse `json:"response"`

	// "error" event fields sit at the top level.
	Code    string `json:"code"`
	Message string `json:"message"`
}
