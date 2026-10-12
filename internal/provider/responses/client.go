// Package responses implements provider.Provider against OpenAI's Responses API
// (POST /v1/responses). It is a separate protocol from Chat Completions (the
// openai package): tools are flat, the system prompt rides in "instructions",
// tool calls and their outputs are standalone input items, and reasoning
// models return opaque reasoning items that must be replayed on the next
// request.
//
// Requests are always stateless (store:false): the full history is sent each
// turn, and reasoning items round-trip through history with their
// encrypted_content instead of being referenced by id.
package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
	"github.com/open-octo/octo-agent/internal/provider/ratelimit"
	"github.com/open-octo/octo-agent/internal/provider/retry"
	"github.com/open-octo/octo-agent/internal/version"
)

// DefaultBaseURL is OpenAI's API host. The endpoint is BaseURL + ResponsesPath.
const DefaultBaseURL = "https://api.openai.com"

// ResponsesPath is appended to BaseURL (or just "/responses" when BaseURL
// already ends in "/v1").
const ResponsesPath = "/v1/responses"

// DefaultMaxTokens is used when the request leaves MaxTokens unset.
const DefaultMaxTokens = 32768

// minMaxOutputTokens is the smallest max_output_tokens the API accepts.
const minMaxOutputTokens = 16

// DefaultStreamIdleTimeout bounds the gap between two stream reads.
const DefaultStreamIdleTimeout = 5 * time.Minute

// maxErrorBodyBytes caps how much of an error response body is read.
const maxErrorBodyBytes = 4096

// Client is a Responses API client. Construct with New().
type Client struct {
	APIKey     string
	BaseURL    string       // optional override; defaults to DefaultBaseURL
	HTTPClient *http.Client // optional; defaults to http.Client with a 60s timeout
	Retry      retry.Policy // optional; zero value falls back to retry.Default()

	// Limiter throttles calls to this endpoint. Nil means no throttling.
	Limiter *ratelimit.Limiter

	// StreamIdleTimeout overrides DefaultStreamIdleTimeout for SendStream. Zero
	// uses the default; a negative value disables the idle guard.
	StreamIdleTimeout time.Duration

	// Headers are extra HTTP headers sent with every request, applied after
	// the built-in ones so a key here overrides them.
	Headers map[string]string
}

// New constructs a Client. An empty key is accepted and sends no
// Authorization header; whether a key is mandatory is the caller's policy.
func New(apiKey string) (*Client, error) {
	return &Client{
		APIKey:     strings.TrimSpace(apiKey),
		BaseURL:    DefaultBaseURL,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Name implements provider.Provider.
func (c *Client) Name() string { return "openai-responses" }

func (c *Client) policy() retry.Policy {
	if c.Retry.MaxAttempts > 0 {
		return c.Retry
	}
	return retry.Default()
}

func (c *Client) streamIdleTimeout() time.Duration {
	if c.StreamIdleTimeout != 0 {
		return c.StreamIdleTimeout
	}
	return DefaultStreamIdleTimeout
}

// endpointURL accepts a base with or without a trailing "/v1", the same way
// the openai and anthropic clients do.
func (c *Client) endpointURL() string {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/responses"
	}
	return base + ResponsesPath
}

func (c *Client) buildRequest(req provider.Request, stream bool) ([]byte, error) {
	if req.Model == "" {
		return nil, errors.New("responses: req.Model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("responses: at least one message is required")
	}
	body := apiRequest{
		Model:           req.Model,
		Instructions:    req.SystemPrompt,
		Input:           toInput(req.Model, req.Messages),
		Tools:           toAPITools(req.Tools),
		MaxOutputTokens: req.MaxTokens,
		PromptCacheKey:  req.CacheKey,
		Stream:          stream,
		Store:           false,
	}
	switch {
	case body.MaxOutputTokens <= 0:
		body.MaxOutputTokens = DefaultMaxTokens
	case body.MaxOutputTokens < minMaxOutputTokens:
		// The connection test asks for a single token; the API rejects that.
		body.MaxOutputTokens = minMaxOutputTokens
	}
	// Forwarded verbatim: the API's effort enum ("none".."max") covers every
	// level octo uses. reasoning.summary is deliberately not requested — some
	// accounts must be verified before summaries are allowed.
	if req.ReasoningEffort != "" {
		body.Reasoning = &apiReasoning{Effort: req.ReasoningEffort}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("responses: marshal request: %w", err)
	}
	return payload, nil
}

func (c *Client) newHTTPRequest(ctx context.Context, payload []byte, stream bool) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("responses: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", version.UserAgent())
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	provider.SetSessionHeader(ctx, httpReq.Header, c.endpointURL())
	for k, v := range c.Headers {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

// statusError turns a non-2xx response into an error plus its retry decision.
func statusError(resp *http.Response) (retry.Decision, error) {
	dec := retry.Decision{Retry: retry.RetryableStatus(resp.StatusCode), RetryAfter: retry.RetryAfterHeader(resp.Header)}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return dec, fmt.Errorf("responses: HTTP %d: read error response: %w", resp.StatusCode, err)
	}
	var apiErr apiError
	if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Error.Message != "" {
		return dec, fmt.Errorf("responses: HTTP %d (%s): %s", resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
	}
	return dec, fmt.Errorf("responses: HTTP %d: %s", resp.StatusCode, string(respBody))
}

// Send implements provider.Provider.
func (c *Client) Send(ctx context.Context, req provider.Request) (provider.Response, error) {
	payload, err := c.buildRequest(req, false)
	if err != nil {
		return provider.Response{}, err
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	release, err := c.Limiter.Acquire(ctx)
	if err != nil {
		return provider.Response{}, err
	}
	defer release()

	respBody, err := retry.Do(ctx, c.policy(), func(ctx context.Context) ([]byte, retry.Decision, error) {
		httpReq, err := c.newHTTPRequest(ctx, payload, false)
		if err != nil {
			return nil, retry.Decision{}, err
		}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return nil, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("responses: send: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			dec, err := statusError(resp)
			return nil, dec, err
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("responses: read response: %w", err)
		}
		return b, retry.Decision{}, nil
	})
	if err != nil {
		return provider.Response{}, err
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return provider.Response{}, fmt.Errorf("responses: decode response: %w", err)
	}
	return toResponse(req.Model, apiResp, apiResp.Output)
}

// toResponse converts a finished Response object (with its output items) into
// a provider.Response.
func toResponse(model string, r apiResponse, output []outputItem) (provider.Response, error) {
	if r.Status == "failed" {
		return provider.Response{}, failedError(r.Error)
	}
	blocks := toBlocks(model, output)
	out := provider.Response{
		Content:    textFromBlocks(blocks),
		Model:      r.Model,
		StopReason: stopReason(r, blocks),
	}
	if len(blocks) > 0 {
		out.Blocks = blocks
	}
	if r.Usage != nil {
		out.InputTokens = r.Usage.nonCachedInput()
		out.OutputTokens = r.Usage.OutputTokens
		out.CacheReadTokens = r.Usage.InputTokensDetails.CachedTokens
		out.CacheWriteTokens = r.Usage.InputTokensDetails.CacheWriteTokens
	}
	return out, nil
}

func failedError(e *responseError) error {
	if e == nil || e.Message == "" {
		return errors.New("responses: response failed")
	}
	return fmt.Errorf("responses: response failed (%s): %s", e.Code, e.Message)
}

// stopReason maps the Response status onto the agent's canonical stop
// reasons. There is no finish_reason: truncation is status "incomplete" with
// reason "max_output_tokens", and a tool turn is one whose output carries a
// function_call item. A truncated tool call is left as max_tokens, since a
// partial call is unsafe to dispatch.
func stopReason(r apiResponse, blocks []agent.ContentBlock) string {
	if r.Status == "incomplete" && r.IncompleteDetails != nil {
		if r.IncompleteDetails.Reason == "max_output_tokens" {
			return "max_tokens"
		}
		return r.IncompleteDetails.Reason
	}
	for _, b := range blocks {
		if b.Type == "tool_use" {
			return "tool_use"
		}
	}
	return "end_turn"
}

// toBlocks converts output items into content blocks in output order.
// Reasoning items become "thinking" blocks carrying the item id, the
// encrypted content and the model, so toInput can replay them to the same
// model on the next request.
func toBlocks(model string, output []outputItem) []agent.ContentBlock {
	var blocks []agent.ContentBlock
	for _, it := range output {
		switch it.Type {
		case "reasoning":
			var sb strings.Builder
			for _, s := range it.Summary {
				sb.WriteString(s.Text)
			}
			b := agent.NewThinkingBlock(sb.String(), it.EncryptedContent)
			b.ID = it.ID
			b.Model = model
			blocks = append(blocks, b)
		case "message":
			var sb strings.Builder
			for _, p := range it.Content {
				sb.WriteString(p.Text)
				sb.WriteString(p.Refusal)
			}
			if sb.Len() > 0 {
				b := agent.NewTextBlock(sb.String())
				b.Phase = it.Phase
				blocks = append(blocks, b)
			}
		case "function_call":
			blocks = append(blocks, agent.NewToolUseBlockFromJSON(it.CallID, it.Name, it.Arguments))
		}
	}
	return blocks
}

func textFromBlocks(blocks []agent.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

func toAPITools(defs []agent.ToolDefinition) []apiTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]apiTool, len(defs))
	for i, d := range defs {
		out[i] = apiTool{
			Type:        "function",
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
		}
	}
	return out
}

var _ provider.Provider = (*Client)(nil)
