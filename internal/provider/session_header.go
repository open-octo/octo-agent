package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// OpenCodeSessionHeader is the per-conversation header OpenCode Go asks
// clients to send so it can route a conversation's requests to a consistent
// backend and hit its prompt cache.
const OpenCodeSessionHeader = "x-opencode-session"

// SetSessionHeader adds the conversation ID stamped in ctx (see
// agent.WithUpstreamSessionID), hashed, as the session header of endpoints
// known to use one. Other endpoints get nothing, so the ID isn't leaked to vendors that
// never asked for it. Call before applying user-configured headers so a
// config entry can still override the value.
func SetSessionHeader(ctx context.Context, h http.Header, endpointURL string) {
	id := agent.UpstreamSessionIDFrom(ctx)
	if id == "" {
		return
	}
	u, err := url.Parse(endpointURL)
	if err != nil {
		return
	}
	host := strings.ToLower(u.Hostname())
	if host == "opencode.ai" || strings.HasSuffix(host, ".opencode.ai") {
		h.Set(OpenCodeSessionHeader, opaqueSessionID(id))
	}
}

// opaqueSessionID hashes a session ID before it leaves the machine. IDs can
// embed identifiers the endpoint has no business seeing (an IM session's ID
// carries the platform chat and user IDs); the hash is still stable per
// conversation, which is all the endpoint needs.
func opaqueSessionID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}
