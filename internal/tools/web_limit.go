package tools

import (
	"context"

	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/provider/ratelimit"
)

// webLimiters gates web_fetch and web_search for the whole process. Every
// session, sub-agent and workflow step runs its own agent loop, so only a
// process-wide gate bounds the total: a fan-out of sub-agents that each fire
// several fetches would otherwise open all of them at once (#2710). The
// registry keys limiters by the configured value, so an edited
// tools.web_max_concurrency applies from the next call without a restart.
var webLimiters ratelimit.Registry

// acquireWebSlot blocks until a web request may start under
// tools.web_max_concurrency (0 = unlimited). The returned release is never
// nil, so `defer release()` is always safe.
func acquireWebSlot(ctx context.Context) (func(), error) {
	cfg, _ := config.LoadCached()
	return webLimiters.For("web", 0, cfg.Tools.WebMaxConcurrency).Acquire(ctx)
}
