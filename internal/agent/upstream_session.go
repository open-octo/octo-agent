package agent

import "context"

// ctxKeyUpstreamSessionID carries the conversation identity a provider may
// forward to its endpoint (e.g. OpenCode Go's x-opencode-session, which it
// uses for routing and prompt caching). Agent entry points stamp it from
// Agent.UpstreamSessionID.
type ctxKeyUpstreamSessionID struct{}

// WithUpstreamSessionID stamps the conversation ID providers may forward.
func WithUpstreamSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyUpstreamSessionID{}, id)
}

// UpstreamSessionIDFrom resolves the stamped conversation ID, "" when none.
func UpstreamSessionIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyUpstreamSessionID{}).(string)
	return id
}

// withUpstreamSession stamps a.UpstreamSessionID over whatever ctx carries:
// the agent's own identity wins, so a sub-agent run on its parent's ctx still
// reports itself. An agent with none leaves ctx as is.
func (a *Agent) withUpstreamSession(ctx context.Context) context.Context {
	if a.UpstreamSessionID == "" {
		return ctx
	}
	return WithUpstreamSessionID(ctx, a.UpstreamSessionID)
}
