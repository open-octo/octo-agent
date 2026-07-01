// Package app is the single place that constructs provider clients and wires
// them into an agent. CLI, HTTP server, and IM bridge all bootstrap through
// here, so the provider wire packages are imported in exactly one spot and the
// one-directional dependency graph (provider → agent) stays intact.
//
// This file owns provider-client construction and the provider.Provider →
// agent.Sender adapter. Higher-level session assembly (executor, gate, MCP,
// sub-agents) lands in bootstrap.go as the migration proceeds.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
	"github.com/open-octo/octo-agent/internal/provider/anthropic"
	"github.com/open-octo/octo-agent/internal/provider/openai"
)

// Provider name constants (legacy).  New code should use vendor IDs directly.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// SenderOptions is everything needed to build an agent.Sender for a vendor.
// Key resolution and any user-facing help text stay with the caller (a CLI
// prints setup hints; a server returns an error) — this layer only constructs.
type SenderOptions struct {
	Provider string // vendor ID, e.g. "kimi", "deepseek", "anthropic", "openai"
	APIKey   string
	BaseURL  string // optional endpoint override; empty uses the vendor default
	// Protocol ("anthropic" | "openai") is required only for the Custom vendor,
	// which has no registry-pinned wire format; named vendors ignore it.
	Protocol string

	// CacheKey is forwarded as the provider's prompt-cache key, stable across a
	// conversation's turns so the backend routes them to the same cache.
	CacheKey string
	// ThinkingBudget > 0 enables Anthropic extended thinking with this trace
	// budget; ignored by OpenAI-protocol vendors.
	ThinkingBudget int
	// ReasoningEffort ("low"|"medium"|"high"|"xhigh"|"max") is forwarded to OpenAI-protocol
	// vendors as reasoning_effort; ignored by Anthropic-protocol vendors
	// (which use ThinkingBudget).
	ReasoningEffort string
	// ShowReasoning gates whether the reasoning/thinking trace is surfaced to
	// the agent event stream.
	ShowReasoning bool
}

// AnthropicThinkingBudget maps a unified reasoning-effort level to an Anthropic
// thinking-token figure. "" (off) yields 0, which disables thinking. On modern
// Claude models (adaptive thinking + output_config.effort) the provider uses
// this only as a max_tokens floor; on older Claude / Kimi-for-coding it is the
// literal thinking.budget_tokens. The provider bumps max_tokens to fit a value
// larger than it, so the higher levels can outrun the default cap safely.
func AnthropicThinkingBudget(effort string) int {
	switch effort {
	case "low":
		return 4096
	case "medium":
		return 16384
	case "high":
		return 32768
	case "xhigh":
		return 48000
	case "max":
		return 64000
	}
	return 0
}

// NewSender builds the provider client for opts and wraps it as an agent.Sender.
// It is the single entry point through which every transport obtains a sender.
func NewSender(opts SenderOptions) (agent.Sender, error) {
	p, err := buildClient(opts.Provider, opts.APIKey, opts.BaseURL, opts.Protocol)
	if err != nil {
		return nil, err
	}
	// Anthropic-protocol models on the legacy budget path (older Claude,
	// Kimi-for-coding) enable thinking only from a positive ThinkingBudget and
	// ignore the effort string entirely. Callers that set only ReasoningEffort
	// (e.g. the server, which carries the persisted reasoning_effort) would
	// otherwise never enable thinking — derive the budget from the effort here
	// so every transport behaves like the CLI. An explicit budget wins; the
	// figure is harmless to OpenAI-protocol vendors, which ignore it.
	thinkingBudget := opts.ThinkingBudget
	if thinkingBudget == 0 {
		thinkingBudget = AnthropicThinkingBudget(opts.ReasoningEffort)
	}
	return sender{
		p:               p,
		cacheKey:        opts.CacheKey,
		thinkingBudget:  thinkingBudget,
		reasoningEffort: opts.ReasoningEffort,
		showReasoning:   opts.ShowReasoning,
	}, nil
}

// DefaultBaseURL returns the vendor's built-in API endpoint for the given
// provider ID, or "" for an unknown one. Exposed so callers can show the
// effective endpoint without importing the vendor packages themselves.
//
// Deprecated: prefer VendorBaseURL(id) from provider.go.
func DefaultBaseURL(providerName string) string {
	return VendorBaseURL(providerName)
}

// buildClient constructs the vendor client and applies an optional base-URL
// override. The caller is responsible for having resolved a non-empty key.
// protocol is used only for vendors with no registry-pinned wire format (the
// Custom catch-all); named vendors ignore it and use their own protocol.
func buildClient(name, apiKey, baseURL, protocol string) (provider.Provider, error) {
	v := vendorByID(name)
	if v == nil {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	if v.CustomEndpoint && baseURL == "" {
		return nil, fmt.Errorf("provider %q requires a base URL (set %s_BASE_URL or base_url in config)",
			name, strings.ToUpper(name))
	}
	// An empty override means "the vendor's own endpoint". The wire clients'
	// zero-value defaults point at api.openai.com / api.anthropic.com, which is
	// wrong for every other vendor speaking the same protocol — so resolve the
	// registry endpoint here instead of leaving it to the client.
	if baseURL == "" {
		baseURL = v.DefaultBaseURL
	}

	// A registry-pinned protocol always wins; the Custom vendor leaves it empty
	// and relies on the caller-supplied protocol from the model entry.
	proto := v.Protocol
	if proto == "" {
		proto = protocol
	}
	if proto == "" {
		return nil, fmt.Errorf("provider %q requires a protocol (\"anthropic\" or \"openai\")", name)
	}

	switch proto {
	case "anthropic":
		client, err := anthropic.New(apiKey)
		if err != nil {
			return nil, err
		}
		if baseURL != "" {
			client.BaseURL = baseURL
		}
		return client, nil
	case "openai":
		client, err := openai.New(apiKey)
		if err != nil {
			return nil, err
		}
		if baseURL != "" {
			client.BaseURL = baseURL
		}
		// Pass the vendor id as the dialect so the client can apply
		// vendor-specific quirks (DeepSeek's thinking-mode toggle); names the
		// client doesn't recognise leave the request in its generic shape.
		client.Dialect = name
		return client, nil
	default:
		return nil, fmt.Errorf("unknown protocol %q for provider %q", proto, name)
	}
}

// sender adapts a provider.Provider into agent.Sender. Keeping the adapter here
// means the agent package never imports provider — a one-directional dep graph
// that pays off as more provider implementations land.
type sender struct {
	p               provider.Provider
	cacheKey        string
	thinkingBudget  int
	reasoningEffort string
	showReasoning   bool
}

// reasoningSink returns the OnThinking callback to hand the provider: the
// agent's onThinking when reasoning display is enabled, else nil so the
// provider skips surfacing reasoning entirely.
func (s sender) reasoningSink(onThinking func(string)) func(string) {
	if !s.showReasoning || onThinking == nil {
		return nil
	}
	return onThinking
}

func (s sender) SendMessages(ctx context.Context, model, system string, msgs []agent.Message, maxTokens int) (agent.Reply, error) {
	if s.p == nil {
		return agent.Reply{}, errors.New("app: provider is nil")
	}
	resp, err := s.p.Send(ctx, provider.Request{
		Model:           model,
		SystemPrompt:    system,
		Messages:        msgs,
		MaxTokens:       maxTokens,
		CacheKey:        s.cacheKey,
		ThinkingBudget:  s.thinkingBudget,
		ReasoningEffort: s.reasoningEffort,
	})
	if err != nil {
		return agent.Reply{}, err
	}
	return replyFromResponse(resp), nil
}

// StreamMessages delegates to the provider's SendStream when it implements
// provider.StreamingProvider, else falls back to the buffered Send path and
// synthesises a single onChunk call with the full content.
func (s sender) StreamMessages(
	ctx context.Context,
	model, system string,
	msgs []agent.Message,
	maxTokens int,
	onChunk func(string),
	onThinking func(string),
) (agent.Reply, error) {
	if s.p == nil {
		return agent.Reply{}, errors.New("app: provider is nil")
	}
	req := provider.Request{
		Model:           model,
		SystemPrompt:    system,
		Messages:        msgs,
		MaxTokens:       maxTokens,
		CacheKey:        s.cacheKey,
		ThinkingBudget:  s.thinkingBudget,
		ReasoningEffort: s.reasoningEffort,
	}
	if sp, ok := s.p.(provider.StreamingProvider); ok {
		resp, err := sp.SendStream(ctx, req, provider.StreamCallbacks{
			OnText:     onChunk,
			OnThinking: s.reasoningSink(onThinking),
		})
		if err != nil {
			return agent.Reply{}, err
		}
		return replyFromResponse(resp), nil
	}

	resp, err := s.p.Send(ctx, req)
	if err != nil {
		return agent.Reply{}, err
	}
	if onChunk != nil && resp.Content != "" {
		onChunk(resp.Content)
	}
	return replyFromResponse(resp), nil
}

// SendMessagesWithTools implements agent.ToolSender.
func (s sender) SendMessagesWithTools(
	ctx context.Context,
	model, system string,
	msgs []agent.Message,
	maxTokens int,
	toolDefs []agent.ToolDefinition,
) (agent.Reply, error) {
	if s.p == nil {
		return agent.Reply{}, errors.New("app: provider is nil")
	}
	resp, err := s.p.Send(ctx, provider.Request{
		Model:           model,
		SystemPrompt:    system,
		Messages:        msgs,
		MaxTokens:       maxTokens,
		CacheKey:        s.cacheKey,
		Tools:           toolDefs,
		ThinkingBudget:  s.thinkingBudget,
		ReasoningEffort: s.reasoningEffort,
	})
	if err != nil {
		return agent.Reply{}, err
	}
	return replyFromResponse(resp), nil
}

// StreamMessagesWithTools implements agent.ToolStreamingSender.
func (s sender) StreamMessagesWithTools(
	ctx context.Context,
	model, system string,
	msgs []agent.Message,
	maxTokens int,
	toolDefs []agent.ToolDefinition,
	onChunk func(string),
	onToolDelta agent.ToolInputDeltaFunc,
	onThinking agent.ThinkingDeltaFunc,
) (agent.Reply, error) {
	if s.p == nil {
		return agent.Reply{}, errors.New("app: provider is nil")
	}
	req := provider.Request{
		Model:           model,
		SystemPrompt:    system,
		Messages:        msgs,
		MaxTokens:       maxTokens,
		CacheKey:        s.cacheKey,
		Tools:           toolDefs,
		ThinkingBudget:  s.thinkingBudget,
		ReasoningEffort: s.reasoningEffort,
	}
	if sp, ok := s.p.(provider.StreamingProvider); ok {
		resp, err := sp.SendStream(ctx, req, provider.StreamCallbacks{
			OnText:      onChunk,
			OnToolDelta: onToolDelta,
			OnThinking:  s.reasoningSink(onThinking),
		})
		if err != nil {
			return agent.Reply{}, err
		}
		return replyFromResponse(resp), nil
	}

	// Buffered fallback.
	resp, err := s.p.Send(ctx, req)
	if err != nil {
		return agent.Reply{}, err
	}
	if onChunk != nil && resp.Content != "" {
		onChunk(resp.Content)
	}
	return replyFromResponse(resp), nil
}

func replyFromResponse(resp provider.Response) agent.Reply {
	return agent.Reply{
		Content:          resp.Content,
		Blocks:           resp.Blocks,
		Model:            resp.Model,
		StopReason:       resp.StopReason,
		InputTokens:      resp.InputTokens,
		OutputTokens:     resp.OutputTokens,
		CacheReadTokens:  resp.CacheReadTokens,
		CacheWriteTokens: resp.CacheWriteTokens,
	}
}

// TestConnection pings the provider with a minimal request to verify that
// the API key, base URL, and model all work. It returns a descriptive error
// on failure (auth, model not found, network, etc.).
func TestConnection(ctx context.Context, providerName, apiKey, baseURL, model, protocol string) error {
	p, err := buildClient(providerName, apiKey, baseURL, protocol)
	if err != nil {
		return err
	}
	_, err = p.Send(ctx, provider.Request{
		Model:     model,
		Messages:  []agent.Message{{Role: agent.RoleUser, Content: "hi"}},
		MaxTokens: 1,
	})
	return err
}

// Compile-time assertions: sender satisfies all agent sender interfaces.
var (
	_ agent.Sender              = sender{}
	_ agent.StreamingSender     = sender{}
	_ agent.ToolSender          = sender{}
	_ agent.ToolStreamingSender = sender{}
)
