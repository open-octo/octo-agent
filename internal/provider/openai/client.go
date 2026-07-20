package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
	"github.com/open-octo/octo-agent/internal/provider/retry"
	"github.com/open-octo/octo-agent/internal/version"
)

// DefaultBaseURL is OpenAI's API host. The actual Chat Completions endpoint
// is BaseURL + ChatCompletionsPath. Override Client.BaseURL when pointing at
// an OpenAI-compatible third party — e.g. DeepSeek at https://api.deepseek.com,
// Kimi at https://api.moonshot.cn, vLLM, OpenRouter, Together, etc.
const DefaultBaseURL = "https://api.openai.com"

// ChatCompletionsPath is the path appended to BaseURL for every send.
const ChatCompletionsPath = "/v1/chat/completions"

// DefaultMaxTokens caps response length when a caller doesn't specify one.
// 32768 mirrors the Anthropic provider's default so behaviour stays consistent
// across backends. OpenAI itself treats max_tokens as optional (model
// maximum if omitted); we send 32768 anyway for predictability.
const DefaultMaxTokens = 32768

// DialectDeepSeek selects DeepSeek's reasoning quirks (the thinking on/off
// toggle) on a Client. Assign it to Client.Dialect for the "deepseek" vendor;
// any other value leaves the request shaped as generic OpenAI.
const DialectDeepSeek = "deepseek"

// DialectOpenAI selects OpenAI's effort value set ("max" → "xhigh", since
// gpt-5.x reasoning_effort tops out at "xhigh", not "max"). Assign it to
// Client.Dialect for the "openai" vendor.
const DialectOpenAI = "openai"

// DialectOpenRouter selects OpenRouter's reasoning shape: a nested
// `reasoning: {effort: ...}` object rather than the flat reasoning_effort
// field every other OpenAI-compatible backend here uses. OpenRouter's schema
// has no reasoning_effort field, so sending it there — as the generic
// fallback below does — is silently ignored and the effort setting has no
// effect. Assign it to Client.Dialect for the "openrouter" vendor. See
// https://openrouter.ai/docs/use-cases/reasoning-tokens.
const DialectOpenRouter = "openrouter"

// DialectBailian selects DashScope's (Alibaba Bailian) reasoning shape: a
// plain top-level enable_thinking boolean — there is no reasoning_effort
// field at all, flat or nested, so the generic fallback's clamped
// reasoning_effort is silently ignored. Assign it to Client.Dialect for the
// "bailian" vendor. See
// https://www.alibabacloud.com/help/en/model-studio/deep-thinking.
const DialectBailian = "bailian"

// DialectKimi selects Moonshot Kimi's reasoning shape, which is split by
// model generation rather than uniform across the vendor:
//   - k2.6 / k2.5: the same nested {type: "enabled"|"disabled"} toggle as
//     DeepSeek — no reasoning_effort field.
//   - k2.7-code: thinking is permanently on; sending "disabled" errors, so
//     the toggle is always {type: "enabled"}.
//   - k3: a top-level reasoning_effort field, but the only value it currently
//     accepts is "max" — the generic fallback's clamp-to-"high" would send an
//     unsupported value.
//
// Assign it to Client.Dialect for the "kimi" vendor (kimi-coding-plan speaks
// the Anthropic protocol and never reaches this client). See
// https://platform.kimi.ai/docs/api/chat.
const DialectKimi = "kimi"

// DefaultStreamIdleTimeout bounds how long a streaming response may go silent
// (no bytes received) before SendStream aborts it as a stall. Chat Completions
// backends stream chunks continuously while generating, so a healthy stream
// never idles this long. 5 minutes is generous enough to ride out a slow first
// token at a large context or a briefly congested endpoint while still catching
// a server that stops sending without closing. A stall that does trip it is
// recovered by the agent loop (see isTransientStreamErr), not surfaced as a
// turn error.
const DefaultStreamIdleTimeout = 5 * time.Minute

// maxErrorBodyBytes caps how much of a non-2xx response body we read for an
// error message. Provider error bodies are usually small JSON objects; this
// avoids memory pressure if a misbehaving endpoint streams a huge HTML page.
const maxErrorBodyBytes = 4096

// Client talks to an OpenAI-compatible Chat Completions API. Construct via
// New(); zero values are not valid because APIKey is required.
//
// BaseURL is the host + protocol-prefix only (no /v1/chat/completions
// suffix); the client appends ChatCompletionsPath itself, so pointing at
// compatible endpoints stays painless — `BaseURL = "https://api.deepseek.com"`
// and the rest works unchanged.
type Client struct {
	APIKey     string
	BaseURL    string       // optional override; defaults to DefaultBaseURL
	HTTPClient *http.Client // optional; defaults to http.Client with a 60s timeout
	Retry      retry.Policy // optional; zero value falls back to retry.Default()

	// StreamIdleTimeout overrides DefaultStreamIdleTimeout for SendStream. Zero
	// uses the default; a negative value disables the idle guard entirely.
	StreamIdleTimeout time.Duration

	// Dialect selects vendor-specific request quirks within the OpenAI protocol.
	// Empty (the default) is generic OpenAI-compatible. See applyReasoning for
	// what each DialectXxx constant changes. Set at construction (see
	// internal/app).
	Dialect string
}

// applyReasoning populates the reasoning fields of body for the given effort
// ("" | "low" | "medium" | "high" | "xhigh" | "max"). The accepted value set
// differs by backend, so each dialect normalises before sending:
//
//   - DeepSeek: forwards verbatim — DeepSeek accepts "high"/"max", maps
//     "low"/"medium" up to "high" and "xhigh" up to "max" — and additionally
//     sets the thinking on/off toggle (enabling thinking and tuning effort are
//     separate switches, and thinking stays on by default, so "off" must be
//     sent explicitly or it would still think).
//   - OpenAI: gpt-5.x reasoning_effort accepts "low".."high" and "xhigh" but
//     not "max", so "max" maps to "xhigh" (the real top tier).
//   - OpenRouter: forwards verbatim into the nested reasoning object — its
//     effort enum ("none".."max") is a superset of ours, so no clamping is
//     needed. Omitted (nil) entirely when effort is "".
//   - Bailian (DashScope): no reasoning_effort field at all — only a plain
//     enable_thinking boolean, sent explicitly either way (true/false) since
//     per-model defaults vary.
//   - Kimi: model-dependent (see DialectKimi) — k2.6/k2.5 get the same
//     nested toggle as DeepSeek with no reasoning_effort; k2.7-code always
//     gets the toggle forced to "enabled"; k3 gets a top-level
//     reasoning_effort clamped to its only supported value, "max".
//   - Generic OpenAI-compatible: top out at "high" and reject unknown enums, so
//     both "xhigh" and "max" clamp to "high"; "thinking" is never sent.
func (c *Client) applyReasoning(body *apiRequest, effort string) {
	switch c.Dialect {
	case DialectDeepSeek:
		// fall through to the toggle logic below.
	case DialectOpenAI:
		if effort == "max" {
			effort = "xhigh"
		}
		body.ReasoningEffort = effort
		return
	case DialectOpenRouter:
		if effort != "" {
			body.Reasoning = &apiReasoning{Effort: effort}
		}
		return
	case DialectBailian:
		// Sent explicitly both ways since per-model defaults vary. Note: a
		// handful of DashScope models (e.g. qwq-plus, deepseek-r1) are
		// thinking-only and reject enable_thinking:false outright — none of
		// the vendor's catalogued models are (see internal/app/provider.go's
		// "bailian" entry), so this only bites a hand-typed custom model id.
		enabled := effort != ""
		body.EnableThinking = &enabled
		return
	case DialectKimi:
		m := strings.ToLower(body.Model)
		switch {
		case strings.Contains(m, "k3"):
			if effort != "" {
				body.ReasoningEffort = "max"
			}
		case strings.Contains(m, "k2.7-code") || strings.Contains(m, "k2-7-code"):
			body.Thinking = &apiThinking{Type: "enabled"}
		default: // k2.6, k2.5, and any other kimi model
			if effort == "" {
				body.Thinking = &apiThinking{Type: "disabled"}
			} else {
				body.Thinking = &apiThinking{Type: "enabled"}
			}
		}
		return
	default:
		if effort == "xhigh" || effort == "max" {
			effort = "high"
		}
		body.ReasoningEffort = effort
		return
	}
	body.ReasoningEffort = effort
	if effort == "" {
		body.Thinking = &apiThinking{Type: "disabled"}
	} else {
		body.Thinking = &apiThinking{Type: "enabled"}
	}
}

// policy returns the configured retry policy, or the package default when the
// caller left Client.Retry zero.
func (c *Client) policy() retry.Policy {
	if c.Retry.MaxAttempts > 0 {
		return c.Retry
	}
	return retry.Default()
}

// streamIdleTimeout returns the configured streaming idle timeout, or the
// package default when the caller left Client.StreamIdleTimeout zero. A
// negative value is passed through to disable the guard.
func (c *Client) streamIdleTimeout() time.Duration {
	if c.StreamIdleTimeout != 0 {
		return c.StreamIdleTimeout
	}
	return DefaultStreamIdleTimeout
}

// New constructs a Client with the given API key and the standard defaults.
// Returns an error when apiKey is empty so misconfiguration is caught at
// startup rather than at the first request.
func New(apiKey string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openai: API key is required")
	}
	return &Client{
		APIKey:     apiKey,
		BaseURL:    DefaultBaseURL,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Name implements provider.Provider.
func (c *Client) Name() string { return "openai-completions" }

// Send implements provider.Provider against OpenAI's Chat Completions API.
//
// Non-2xx responses are decoded as apiError and wrapped into a descriptive
// error containing the HTTP status and the upstream error message.
func (c *Client) Send(ctx context.Context, req provider.Request) (provider.Response, error) {
	if req.Model == "" {
		return provider.Response{}, errors.New("openai: req.Model is required")
	}
	if len(req.Messages) == 0 {
		return provider.Response{}, errors.New("openai: at least one message is required")
	}

	msgs, err := toAPIMessages(req.SystemPrompt, req.Messages)
	if err != nil {
		return provider.Response{}, fmt.Errorf("openai: serialize messages: %w", err)
	}

	body := apiRequest{
		Model:          req.Model,
		MaxTokens:      req.MaxTokens,
		Messages:       msgs,
		Tools:          toAPITools(req.Tools),
		PromptCacheKey: c.promptCacheKey(req.CacheKey),
	}
	c.applyReasoning(&body, req.ReasoningEffort)
	if body.MaxTokens <= 0 {
		body.MaxTokens = DefaultMaxTokens
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return provider.Response{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	// Retry request establishment + body read on transient failures; parse the
	// (fixed) response body below. Each attempt gets a fresh body reader.
	respBody, err := retry.Do(ctx, c.policy(), func(ctx context.Context) ([]byte, retry.Decision, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(payload))
		if err != nil {
			return nil, retry.Decision{}, fmt.Errorf("openai: build request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", version.UserAgent())
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return nil, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("openai: send: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
			if err != nil {
				return nil, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("openai: read error response: %w", err)
			}

			dec := retry.Decision{Retry: retry.RetryableStatus(resp.StatusCode), RetryAfter: retry.RetryAfterHeader(resp.Header)}
			var apiErr apiError
			if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Error.Message != "" {
				return nil, dec, fmt.Errorf(
					"openai: HTTP %d (%s): %s",
					resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message,
				)
			}
			return nil, dec, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("openai: read response: %w", err)
		}
		return respBody, retry.Decision{}, nil
	})
	if err != nil {
		return provider.Response{}, err
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return provider.Response{}, fmt.Errorf("openai: decode response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return provider.Response{}, errors.New("openai: response has no choices")
	}
	first := apiResp.Choices[0]

	// Convert tool calls to agent.ContentBlock.
	var blocks []agent.ContentBlock
	var stopReason = first.FinishReason
	// Normalise the output-cap truncation signal to the canonical sentinel the
	// agent loop checks (matches Anthropic's "max_tokens"), so truncation
	// recovery is provider-agnostic. Independent of whether a tool call is present.
	if stopReason == "length" {
		stopReason = "max_tokens"
	}
	if len(first.Message.ToolCalls) > 0 {
		for _, tc := range first.Message.ToolCalls {
			var input map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			}
			blocks = append(blocks, agent.NewToolUseBlock(tc.ID, tc.Function.Name, input))
		}
		// A response carrying tool calls is a tool-use turn — dispatch them
		// regardless of finish_reason. The expected signal is "tool_calls", but
		// some OpenAI-compatible backends (e.g. a gateway proxying Gemini) emit
		// the calls while reporting "stop"; trusting finish_reason alone would
		// silently drop the call. Leave a genuine truncation ("max_tokens")
		// intact, since a partial tool call is unsafe to dispatch.
		if stopReason != "max_tokens" {
			stopReason = "tool_use"
		}
	}
	if first.Message.Content != "" {
		blocks = append([]agent.ContentBlock{agent.NewTextBlock(first.Message.Content)}, blocks...)
	}
	// Stash the reasoning trace on the tool_use block so it round-trips back to
	// the API on the follow-up request (required by thinking models).
	attachReasoning(blocks, first.Message.ReasoningContent)

	return provider.Response{
		Content:      first.Message.Content,
		Blocks:       blocks,
		Model:        apiResp.Model,
		StopReason:   stopReason,
		InputTokens:  apiResp.Usage.nonCachedInput(),
		OutputTokens: apiResp.Usage.CompletionTokens,
		// OpenAI/DeepSeek report only cached (read) input; no write count.
		CacheReadTokens: apiResp.Usage.cachedTokens(),
	}, nil
}

// MistralBaseURL is Mistral's official API host. It speaks the OpenAI protocol
// and, unlike the auto-caching backends (DeepSeek, GLM, Qwen-implicit, …),
// caches a prompt prefix ONLY when the request carries a stable prompt_cache_key
// — so the key must be forwarded there too, not just to OpenAI.
//
// The Mistral *vendor* was removed from the registry, but this wire-level entry
// stays: a `custom`/openai config entry can still point at this host, and it
// must keep forwarding the cache key. Not dead code.
const MistralBaseURL = "https://api.mistral.ai"

// promptCacheKeyEndpoints is the set of base URLs known to accept the
// prompt_cache_key field. prompt_cache_key originated as an OpenAI field; most
// OpenAI-compatible gateways either ignore it or cache automatically without it,
// and some that proxy other backends (e.g. AWS Bedrock for Anthropic models)
// reject unknown body fields with a 400 ("Extra inputs are not permitted") — so
// the key is sent only to endpoints that are known to honour it. Mistral is
// included because it does not cache at all without the key.
var promptCacheKeyEndpoints = map[string]bool{
	DefaultBaseURL: true, // official OpenAI: prompt-cache routing
	MistralBaseURL: true, // Mistral: prefix caching requires the key
}

// promptCacheKey returns the prompt-cache routing key only when the client
// targets an endpoint known to accept the prompt_cache_key field (see
// promptCacheKeyEndpoints); it is omitted everywhere else to avoid a 400 from
// strict gateways.
func (c *Client) promptCacheKey(key string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL // zero value defaults to official OpenAI
	}
	if promptCacheKeyEndpoints[base] {
		return key
	}
	return ""
}

// endpointURL returns BaseURL + ChatCompletionsPath, applying defaults and
// trimming any trailing slash on BaseURL so the join is exactly one slash.
//
// OpenAI-compatible gateway conventions split on where the "/v1" goes: OpenAI
// itself and Alibaba Bailian bake "/v1" into the documented base (e.g.
// "https://dashscope.aliyuncs.com/compatible-mode/v1"), then the client only
// appends "/chat/completions". Other gateways (api.deepseek.com, api.moonshot.cn,
// …) ship a bare host and expect the client to append the full
// "/v1/chat/completions". To accept both without making the user trim, detect
// a trailing "/v1" on the base and drop the redundant segment.
func (c *Client) endpointURL() string {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	suffix := ChatCompletionsPath // "/v1/chat/completions"
	if strings.HasSuffix(base, "/v1") {
		suffix = "/chat/completions"
	}
	return base + suffix
}

// toAPITools converts []agent.ToolDefinition to []apiTool (function format).
func toAPITools(defs []agent.ToolDefinition) []apiTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]apiTool, len(defs))
	for i, d := range defs {
		out[i] = apiTool{
			Type: "function",
			Function: apiFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		}
	}
	return out
}

// toAPIMessages converts agent.Message slice into the OpenAI wire format.
//
// OpenAI carries the system prompt as the FIRST element of the messages
// array with role:"system". Tool result messages in agent History have
// role=user + Blocks containing tool_result blocks — these are exploded into
// individual role="tool" messages (one per result) because that's what the
// OpenAI protocol expects.
func toAPIMessages(systemPrompt string, in []agent.Message) ([]apiMessage, error) {
	out := make([]apiMessage, 0, len(in)+1)
	if strings.TrimSpace(systemPrompt) != "" {
		out = append(out, apiMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range in {
		if m.Role == agent.RoleSystem {
			continue
		}

		// Assistant message with tool calls.
		if m.Role == agent.RoleAssistant && len(m.Blocks) > 0 {
			msg := apiMessage{Role: "assistant"}
			for _, b := range m.Blocks {
				switch b.Type {
				case "text":
					msg.Content = b.Text
				case "tool_use":
					if b.Reasoning != "" {
						msg.ReasoningContent = b.Reasoning
					}
					msg.ToolCalls = append(msg.ToolCalls, apiToolCall{
						ID:   b.ID,
						Type: "function",
						Function: apiToolCallFunction{
							Name:      b.Name,
							Arguments: marshalInput(b.Input),
						},
					})
				}
			}
			out = append(out, msg)
			continue
		}

		// User message with tool results — explode into individual role="tool"
		// messages. A trailing text block (a mid-turn "steer" the agent folded
		// into the tool_result message — see dev-docs/tui-input-modes-design.md
		// §5) can't ride on a role="tool" message, so it's emitted as a separate
		// role="user" message AFTER the tool outputs, which is the OpenAI-shaped
		// equivalent of Anthropic's [tool_result…, text] user message.
		//
		// Image blocks (following a tool_result or standalone) become image_url
		// parts on a single trailing role="user" message — a role="tool" message
		// can't carry image content in the OpenAI protocol (DashScope rejects it
		// with "Unexpected item type in content", and real OpenAI ignores it).
		// Deferring images past all tool outputs keeps the tool_call→tool_result
		// messages contiguous, which the API requires.
		if m.Role == agent.RoleUser && len(m.Blocks) > 0 {
			var steerText strings.Builder
			var userImageParts []apiContentPart
			i := 0
			for i < len(m.Blocks) {
				b := m.Blocks[i]
				switch b.Type {
				case "tool_result":
					// Tool messages carry STRING content only. Any image blocks the
					// tool produced fall through to the "image" case below and are
					// emitted as a trailing role="user" message — nesting an image
					// part into a role="tool" message is rejected by the protocol.
					out = append(out, apiMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: b.Result})
					i++
				case "text":
					if steerText.Len() > 0 {
						steerText.WriteString("\n\n")
					}
					steerText.WriteString(b.Text)
					i++
				case "image":
					// Standalone image (not nested into a preceding tool_result) —
					// e.g. an image pasted into the TUI input. Carry it as an
					// image_url part so vision models actually receive it instead
					// of a "[image]" placeholder.
					if b.Image != nil {
						dataURL := fmt.Sprintf("data:%s;base64,%s", b.Image.MIMEType, base64.StdEncoding.EncodeToString(b.Image.Data))
						userImageParts = append(userImageParts, apiContentPart{
							Type: "image_url",
							ImageURL: &struct {
								URL string `json:"url"`
							}{URL: dataURL},
						})
					}
					i++
				default:
					i++
				}
			}
			// Emit the user turn. With images, use the content-parts array
			// (text first, then images); otherwise a plain text message.
			switch {
			case len(userImageParts) > 0:
				parts := make([]apiContentPart, 0, 1+len(userImageParts))
				if steerText.Len() > 0 {
					parts = append(parts, apiContentPart{Type: "text", Text: steerText.String()})
				}
				parts = append(parts, userImageParts...)
				out = append(out, apiMessage{Role: "user", ContentParts: parts})
			case steerText.Len() > 0:
				out = append(out, apiMessage{Role: "user", Content: steerText.String()})
			}
			continue
		}

		// Plain text message.
		out = append(out, apiMessage{Role: string(m.Role), Content: m.Content})
	}
	return out, nil
}

// attachReasoning records a thinking model's reasoning trace on the first
// tool_use block so it survives in history and is re-sent on the follow-up
// request. No-op when reasoning is empty or there is no tool_use block — which
// keeps reasoning_content off plain text turns (some reasoning models reject it
// there).
func attachReasoning(blocks []agent.ContentBlock, reasoning string) {
	if reasoning == "" {
		return
	}
	for i := range blocks {
		if blocks[i].Type == "tool_use" {
			blocks[i].Reasoning = reasoning
			return
		}
	}
}

// marshalInput encodes a tool input map back to a compact JSON string.
// Returns "{}" on nil/empty input so the wire value is always valid JSON.
func marshalInput(input map[string]any) string {
	if len(input) == 0 {
		return "{}"
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(b)
}
