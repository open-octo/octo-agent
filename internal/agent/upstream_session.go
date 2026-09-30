package agent

import "context"

// ctxKeyUpstreamSessionID carries the conversation identity a provider may
// forward to its endpoint (e.g. OpenCode Go's x-opencode-session, which it
// uses for routing and prompt caching). It is stamped per turn by every entry
// point and overridden by the sub-agent spawner, so each child reports its own
// conversation rather than its parent's — the two share no prompt prefix.
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
