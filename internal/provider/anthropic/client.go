package anthropic

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

// DefaultBaseURL is Anthropic's API base. The actual Messages endpoint is
// BaseURL + "/v1/messages". Override Client.BaseURL when pointing at an
// Anthropic-compatible third-party (DeepSeek, Kimi, OpenRouter's Anthropic
// shim, …) — for example DeepSeek exposes the protocol at
// `https://api.deepseek.com/anthropic`.
const DefaultBaseURL = "https://api.anthropic.com"

// MessagesPath is the path appended to BaseURL for every send.
const MessagesPath = "/v1/messages"

// DefaultAPIVersion is the value sent as the `anthropic-version` header.
// Pinned to the GA version used by the Messages API since 2023-06-01; bump
// only when a new feature requires it.
const DefaultAPIVersion = "2023-06-01"

// DefaultMaxTokens caps response length when a caller doesn't specify one.
// 32768 leaves room for the large code edits agent turns routinely emit while
// staying well below the model ceilings; truncation escalation (65536) sits on
// top. See cmd/octo escalateMaxTokensAnthropic and dev-docs/truncation-recovery.md.
const DefaultMaxTokens = 32768

// DefaultStreamIdleTimeout bounds how long a streaming response may go silent
// (no bytes received) before SendStream aborts it as a stall. The Messages API
// emits periodic `ping` events to keep streams alive, so a healthy stream never
// idles this long. 5 minutes is generous enough to ride out a slow first token
// at a large context or a briefly congested third-party endpoint (some
// Anthropic-compatible proxies don't relay pings) while still catching a server
// that stops sending without closing. A stall that does trip it is recovered by
// the agent loop (see isTransientStreamErr), not surfaced as a turn error.
const DefaultStreamIdleTimeout = 5 * time.Minute

// maxErrorBodyBytes caps how much of a non-2xx response body we read for an
// error message. Provider error bodies are usually small JSON objects; this
// avoids memory pressure if a misbehaving endpoint streams a huge HTML page.
const maxErrorBodyBytes = 4096

// Client talks to an Anthropic-compatible Messages API. Construct via New();
// zero values are not valid because APIKey is required.
//
// BaseURL is the host + protocol-prefix only (no /v1/messages suffix); the
// client appends MessagesPath itself. This makes pointing at compatible
// endpoints painless — set `BaseURL = "https://api.deepseek.com/anthropic"`
// and the rest works unchanged.
type Client struct {
	APIKey     string
	BaseURL    string       // optional override; defaults to DefaultBaseURL
	APIVersion string       // optional override; defaults to DefaultAPIVersion
	HTTPClient *http.Client // optional; defaults to http.Client with a 60s timeout
	Retry      retry.Policy // optional; zero value falls back to retry.Default()

	// StreamIdleTimeout overrides DefaultStreamIdleTimeout for SendStream. Zero
	// uses the default; a negative value disables the idle guard entirely.
	StreamIdleTimeout time.Duration
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
		return nil, errors.New("anthropic: API key is required")
	}
	return &Client{
		APIKey:     apiKey,
		BaseURL:    DefaultBaseURL,
		APIVersion: DefaultAPIVersion,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Name implements provider.Provider.
func (c *Client) Name() string { return "anthropic-messages" }

// Send implements provider.Provider against Anthropic's Messages API.
//
// Non-2xx responses are decoded as apiError and wrapped into a descriptive
// error containing the HTTP status and the upstream error message.
func (c *Client) Send(ctx context.Context, req provider.Request) (provider.Response, error) {
	if req.Model == "" {
		return provider.Response{}, errors.New("anthropic: req.Model is required")
	}
	if len(req.Messages) == 0 {
		return provider.Response{}, errors.New("anthropic: at least one message is required")
	}

	msgs, err := toAPIMessages(req.Messages)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: serialize messages: %w", err)
	}

	body := apiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  msgs,
	}
	cacheableRequest(&body, req.SystemPrompt, toAPITools(req.Tools), c.staticPrefixCache())
	if body.MaxTokens <= 0 {
		body.MaxTokens = DefaultMaxTokens
	}
	applyReasoning(&body, req.Model, req.ReasoningEffort, req.ThinkingBudget)

	payload, err := json.Marshal(body)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	// Retry the request on transient failures (429/5xx/529/network). The
	// payload is fixed across attempts; each attempt gets a fresh body reader.
	return retry.Do(ctx, c.policy(), func(ctx context.Context) (provider.Response, retry.Decision, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(payload))
		if err != nil {
			return provider.Response{}, retry.Decision{}, fmt.Errorf("anthropic: build request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", version.UserAgent())
		httpReq.Header.Set("x-api-key", c.APIKey)
		apiVer := c.APIVersion
		if apiVer == "" {
			apiVer = DefaultAPIVersion
		}
		httpReq.Header.Set("anthropic-version", apiVer)

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return provider.Response{}, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("anthropic: send: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
			if err != nil {
				return provider.Response{}, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("anthropic: read error response: %w", err)
			}

			dec := retry.Decision{Retry: retry.RetryableStatus(resp.StatusCode), RetryAfter: retry.RetryAfterHeader(resp.Header)}
			var apiErr apiError
			if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Error.Message != "" {
				return provider.Response{}, dec, fmt.Errorf(
					"anthropic: HTTP %d (%s): %s",
					resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message,
				)
			}
			return provider.Response{}, dec, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return provider.Response{}, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("anthropic: read response: %w", err)
		}

		var apiResp apiResponse
		if err := json.Unmarshal(respBody, &apiResp); err != nil {
			return provider.Response{}, retry.Decision{}, fmt.Errorf("anthropic: decode response: %w", err)
		}

		blocks := fromAPIContentBlocks(apiResp.Content)
		stopReason := apiResp.StopReason
		// A response carrying tool_use blocks is a tool-use turn regardless of
		// stop_reason. Real Anthropic always pairs them with "tool_use", but a
		// misbehaving Anthropic-compatible gateway may report "end_turn"; trusting
		// stop_reason alone would silently drop the call. A genuine truncation
		// ("max_tokens") is left intact. Mirrors the streaming path and the
		// openai adapter.
		if stopReason != "max_tokens" && hasToolUseBlock(blocks) {
			stopReason = "tool_use"
		}
		return provider.Response{
			Content:          joinTextBlocks(apiResp.Content),
			Blocks:           blocks,
			Model:            apiResp.Model,
			StopReason:       stopReason,
			InputTokens:      apiResp.Usage.InputTokens,
			OutputTokens:     apiResp.Usage.OutputTokens,
			CacheReadTokens:  apiResp.Usage.CacheReadInputTokens,
			CacheWriteTokens: apiResp.Usage.CacheCreationInputTokens,
		}, retry.Decision{}, nil
	})
}

// hasToolUseBlock reports whether any block is a tool_use block.
func hasToolUseBlock(blocks []agent.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

// endpointURL returns BaseURL + MessagesPath, applying defaults and trimming
// any trailing slash on BaseURL so the join is exactly one slash.
func (c *Client) endpointURL() string {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	return strings.TrimRight(base, "/") + MessagesPath
}

// staticPrefixCache picks the cache_control marker for the static system+tools
// prefix. The extended one-hour TTL is a documented GA feature only on the
// official Anthropic endpoint; Anthropic-compatible gateways (Kimi …/coding,
// DeepSeek …/anthropic, self-hosted shims set via BaseURL) either don't
// implement it or may reject the unknown `ttl` field — so they fall back to the
// default 5-minute marker. The rolling message breakpoints stay 5m everywhere.
func (c *Client) staticPrefixCache() *cacheControl {
	if c.isOfficialEndpoint() {
		return ephemeral1h
	}
	return ephemeral
}

// isOfficialEndpoint reports whether the client targets api.anthropic.com (the
// zero value defaults there too). Anything else is a compatible third party.
func (c *Client) isOfficialEndpoint() bool {
	base := strings.TrimRight(c.BaseURL, "/")
	return base == "" || base == DefaultBaseURL
}

// buildSystem returns the value for the request's `system` field, placing a
// cache breakpoint on it when non-empty. Because the cache prefix order is
// tools → system → messages, a breakpoint on the system block also caches the
// (stable, every-turn-identical) tools array that precedes it — capturing the
// bulk of the per-turn input-token cost of an agentic loop. cc selects the
// breakpoint's TTL. Returns nil for an empty prompt so the field is omitted.
func buildSystem(prompt string, cc *cacheControl) any {
	if prompt == "" {
		return nil
	}
	return []apiSystemBlock{{Type: "text", Text: prompt, CacheControl: cc}}
}

// markToolsCacheable puts a cache breakpoint (with cc's TTL) on the LAST tool so
// the tools array is cached even with no system prompt to anchor on. No-op when
// there are no tools.
func markToolsCacheable(tools []apiTool, cc *cacheControl) []apiTool {
	if len(tools) > 0 {
		tools[len(tools)-1].CacheControl = cc
	}
	return tools
}

// cacheableRequest places all cache breakpoints on a request: the system/
// tools prefix (via buildSystem / markToolsCacheable) and the conversation
// history (via markMessagesCacheable). staticCC is the marker for the static
// prefix — ephemeral1h on the official endpoint, ephemeral elsewhere; the
// message breakpoints are always 5-minute. Shared by Send and SendStream so
// both paths cache identically. body.Messages must already be populated.
func cacheableRequest(body *apiRequest, systemPrompt string, tools []apiTool, staticCC *cacheControl) {
	body.System = buildSystem(systemPrompt, staticCC)
	if body.System == nil {
		tools = markToolsCacheable(tools, staticCC) // no system block to anchor on
	}
	body.Tools = tools
	markMessagesCacheable(body.Messages)
}

// markMessagesCacheable places cache breakpoints on the last two messages so
// the conversation-history prefix is cached turn-over-turn (not just the
// static system+tools prefix). Two consecutive markers — rather than one —
// keep a cache anchor alive across the "old tail / new tail" boundary as
// history grows and if the final message is dropped on a retry; the older of
// the two still matches the prior request's prefix. The system breakpoint plus
// these two stays within Anthropic's 4-breakpoint budget.
func markMessagesCacheable(msgs []apiMessage) {
	n := len(msgs)
	for i := n - 1; i >= 0 && i >= n-2; i-- {
		msgs[i] = markMessageCacheable(msgs[i])
	}
}

// markMessageCacheable returns m with a cache_control breakpoint on its last
// content block. A bare-string content is promoted to a single text block so
// it can carry the marker (Anthropic only allows cache_control on blocks). On
// any unexpected shape the message is returned unchanged.
func markMessageCacheable(m apiMessage) apiMessage {
	var blocks []map[string]any
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		var s string
		if json.Unmarshal(m.Content, &s) != nil {
			return m // neither array nor string — leave it alone
		}
		blocks = []map[string]any{{"type": "text", "text": s}}
	}
	if len(blocks) == 0 {
		return m
	}
	blocks[len(blocks)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return m
	}
	m.Content = raw
	return m
}

// applyReasoning enables thinking on the request for the requested effort.
//
// Modern Claude models (Opus 4.6/4.7/4.8, Sonnet 4.6, Fable 5, …) removed the
// budget_tokens extended-thinking API — sending it 400s on Opus 4.7+. They use
// adaptive thinking plus the GA output_config.effort control instead. Older
// Claude models (Haiku 4.5, Sonnet 4.5) and Anthropic-protocol-compatible
// backends (Kimi for coding) don't speak adaptive/effort, so they keep the
// budget_tokens path. The model name selects which.
//
// On either path max_tokens is bumped to leave room above the reasoning budget
// (the budget value doubles as a max_tokens floor on the adaptive path).
func applyReasoning(body *apiRequest, model, effort string, budget int) {
	if usesAdaptiveEffort(model) {
		if effort == "" {
			return
		}
		body.Thinking = &apiThinking{Type: "adaptive"}
		body.OutputConfig = &apiOutputConfig{Effort: clampEffort(model, effort)}
		if budget > 0 && body.MaxTokens <= budget {
			body.MaxTokens = budget + DefaultMaxTokens
		}
		return
	}
	// Legacy budget_tokens path (older Claude, Kimi-for-coding): triggered by a
	// positive budget, independent of the effort string.
	if budget <= 0 {
		return
	}
	body.Thinking = &apiThinking{Type: "enabled", BudgetTokens: budget}
	if body.MaxTokens <= budget {
		body.MaxTokens = budget + DefaultMaxTokens
	}
}

// usesAdaptiveEffort reports whether a model takes adaptive thinking +
// output_config.effort (true) rather than the legacy thinking.budget_tokens
// (false). True for the Claude models that removed or deprecated budget_tokens
// in favour of adaptive thinking; false for older Claude (Haiku 4.5, Sonnet
// 4.5) and non-Claude Anthropic-protocol backends (e.g. kimi-for-coding).
func usesAdaptiveEffort(model string) bool {
	m := strings.ToLower(model)
	for _, s := range []string{"opus-4-6", "opus-4-7", "opus-4-8", "sonnet-4-6", "fable-5", "mythos-5"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// clampEffort lowers "xhigh" to "high" for models that don't support it — Opus
// 4.7 introduced "xhigh", so Opus 4.6 and Sonnet 4.6 would reject it. Every
// adaptive model accepts "max".
func clampEffort(model, effort string) string {
	if effort != "xhigh" || supportsXHigh(model) {
		return effort
	}
	return "high"
}

// supportsXHigh reports whether a model accepts the "xhigh" effort level
// (introduced with Opus 4.7).
func supportsXHigh(model string) bool {
	m := strings.ToLower(model)
	for _, s := range []string{"opus-4-7", "opus-4-8", "fable-5", "mythos-5"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// toAPITools converts []agent.ToolDefinition to []apiTool.
// Anthropic uses "input_schema" where the agent layer uses "parameters".
func toAPITools(defs []agent.ToolDefinition) []apiTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]apiTool, len(defs))
	for i, d := range defs {
		out[i] = apiTool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.Parameters,
		}
	}
	return out
}

// toAPIMessages converts agent.Message slice into the Anthropic wire format.
// The system role is filtered out here because Anthropic carries it as a
// top-level Request.SystemPrompt — sending role:"system" inside the
// messages array would 400.
//
// Messages with Blocks are serialized as a []apiContentBlock JSON array;
// plain-text messages are serialized as a JSON string for compatibility.
//
// Consecutive user messages are merged into one so that a tool_use block is
// immediately followed by its tool_result block with no intervening user
// message boundary. This satisfies Anthropic's API requirement that every
// tool_use id must have a matching tool_result in the next message.
func toAPIMessages(in []agent.Message) ([]apiMessage, error) {
	out := make([]apiMessage, 0, len(in))
	for _, m := range in {
		if m.Role == agent.RoleSystem {
			continue
		}

		// Merge consecutive user messages: Anthropic requires strict
		// user/assistant alternation, and a tool_use must be immediately
		// followed by a tool_result in the next message. A steer message
		// appended after the tool_result would break this pairing.
		if m.Role == agent.RoleUser && len(out) > 0 && out[len(out)-1].Role == "user" {
			merged, err := mergeUserMessages(out[len(out)-1], m)
			if err != nil {
				return nil, err
			}
			out[len(out)-1] = merged
			continue
		}

		if len(m.Blocks) > 0 {
			// Serialize as a content-block array.
			blocks, err := marshalBlocks(m.Blocks)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(blocks)
			if err != nil {
				return nil, err
			}
			out = append(out, apiMessage{Role: string(m.Role), Content: raw})
		} else {
			// Plain string content.
			raw, err := json.Marshal(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, apiMessage{Role: string(m.Role), Content: raw})
		}
	}
	return out, nil
}

// mergeUserMessages combines two user-role messages into one. Both may be
// plain string content or block-array content; the result is always a block
// array so heterogeneous merges are uniform.
func mergeUserMessages(prev apiMessage, next agent.Message) (apiMessage, error) {
	// Decode prev content into blocks.
	var prevBlocks []map[string]any
	if err := json.Unmarshal(prev.Content, &prevBlocks); err != nil {
		// prev was a plain string — promote to a single text block.
		var s string
		if err := json.Unmarshal(prev.Content, &s); err != nil {
			return apiMessage{}, fmt.Errorf("anthropic: unmarshal prev user message: %w", err)
		}
		prevBlocks = []map[string]any{{"type": "text", "text": s}}
	}

	// Decode next content into blocks.
	var nextBlocks []map[string]any
	if len(next.Blocks) > 0 {
		var err error
		nextBlocks, err = marshalBlocks(next.Blocks)
		if err != nil {
			return apiMessage{}, err
		}
	} else {
		nextBlocks = []map[string]any{{"type": "text", "text": next.Content}}
	}

	merged := append(prevBlocks, nextBlocks...)
	raw, err := json.Marshal(merged)
	if err != nil {
		return apiMessage{}, fmt.Errorf("anthropic: marshal merged user message: %w", err)
	}
	return apiMessage{Role: "user", Content: raw}, nil
}

// marshalBlocks converts agent.ContentBlock slice to []apiContentBlock.
// tool_result blocks are serialized with a nested "content" field per the
// Anthropic wire format.
//
// IMPORTANT: When a tool_result is followed by non-tool_result blocks (image,
// text), those blocks are nested INTO the tool_result's content array. This
// preserves the Anthropic API requirement that tool_use must be immediately
// followed by tool_result with no intervening blocks. Without this nesting,
// image blocks between two tool_result blocks would break the tool_use/
// tool_result pairing and cause HTTP 400 errors.
//
// Example transformation:
//
//	Input:  [tool_result(id1), image, tool_result(id2), image]
//	Output: [tool_result(id1, content:[text, image]), tool_result(id2, content:[text, image])]
func marshalBlocks(blocks []agent.ContentBlock) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(blocks))
	i := 0
	for i < len(blocks) {
		b := blocks[i]
		switch b.Type {
		case "text":
			out = append(out, map[string]any{
				"type": "text",
				"text": b.Text,
			})
		case "thinking":
			// Replayed verbatim with its signature; required before tool_use
			// when thinking is enabled, or the API rejects the request.
			out = append(out, map[string]any{
				"type":      "thinking",
				"thinking":  b.Thinking,
				"signature": b.Signature,
			})
		case "tool_use":
			m := map[string]any{
				"type":  "tool_use",
				"id":    b.ID,
				"name":  b.Name,
				"input": b.Input,
			}
			out = append(out, m)
		case "tool_result":
			// Collect following non-tool_result blocks to nest inside this
			// tool_result's content. This ensures image blocks from read_file
			// (or any tool returning images) are properly associated with
			// their tool_result rather than appearing as sibling blocks.
			var nestedContent []map[string]any
			if b.Result != "" {
				nestedContent = append(nestedContent, map[string]any{
					"type": "text",
					"text": b.Result,
				})
			}
			j := i + 1
			for j < len(blocks) && blocks[j].Type != "tool_result" && blocks[j].Type != "tool_use" {
				switch blocks[j].Type {
				case "image":
					if blocks[j].Image == nil {
						return nil, fmt.Errorf("anthropic: image block missing Image data")
					}
					nestedContent = append(nestedContent, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": blocks[j].Image.MIMEType,
							"data":       encodeBase64(blocks[j].Image.Data),
						},
					})
				case "text":
					nestedContent = append(nestedContent, map[string]any{
						"type": "text",
						"text": blocks[j].Text,
					})
				}
				j++
			}

			m := map[string]any{
				"type":        "tool_result",
				"tool_use_id": b.ToolUseID,
			}
			// Use content array when there are nested blocks (images), otherwise
			// use plain string for simpler tool results.
			if len(nestedContent) == 1 && nestedContent[0]["type"] == "text" {
				m["content"] = b.Result
			} else if len(nestedContent) > 0 {
				m["content"] = nestedContent
			} else {
				m["content"] = b.Result
			}
			if b.IsError {
				m["is_error"] = true
			}
			out = append(out, m)
			i = j - 1 // advance past consumed blocks (-1 because loop does i++)
		case "image":
			// Standalone image block (not following a tool_result).
			if b.Image == nil {
				return nil, fmt.Errorf("anthropic: image block missing Image data")
			}
			out = append(out, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": b.Image.MIMEType,
					"data":       encodeBase64(b.Image.Data),
				},
			})
		default:
			return nil, fmt.Errorf("anthropic: unknown block type %q", b.Type)
		}
		i++
	}
	return out, nil
}

// encodeBase64 returns a standard base64 string.
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// fromAPIContentBlocks converts the response content blocks to agent.ContentBlock.
func fromAPIContentBlocks(blocks []apiContentBlock) []agent.ContentBlock {
	out := make([]agent.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, agent.NewTextBlock(b.Text))
		case "tool_use":
			out = append(out, agent.NewToolUseBlock(b.ID, b.Name, b.Input))
		case "thinking":
			out = append(out, agent.NewThinkingBlock(b.Thinking, b.Signature))
		}
	}
	return out
}

// joinTextBlocks concatenates the text from every "text" content block.
// Non-text blocks (tool_use) are skipped.
func joinTextBlocks(blocks []apiContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}
