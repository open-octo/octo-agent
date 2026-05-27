package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
)

func TestCacheableRequest_SystemBreakpoint(t *testing.T) {
	var body apiRequest
	tools := toAPITools([]agent.ToolDefinition{{Name: "t1"}, {Name: "t2"}})
	cacheableRequest(&body, "you are octo", tools)

	sysJSON, _ := json.Marshal(body.System)
	if !strings.Contains(string(sysJSON), `"ephemeral"`) {
		t.Errorf("system should carry an ephemeral breakpoint: %s", sysJSON)
	}
	// With a system prompt anchoring the breakpoint, tools must NOT be marked
	// (the system breakpoint already caches tools+system).
	for i, tl := range body.Tools {
		if tl.CacheControl != nil {
			t.Errorf("tool[%d] should not be marked when system carries the breakpoint", i)
		}
	}
}

func TestCacheableRequest_ToolFallbackWhenNoSystem(t *testing.T) {
	var body apiRequest
	tools := toAPITools([]agent.ToolDefinition{{Name: "t1"}, {Name: "t2"}})
	cacheableRequest(&body, "", tools)

	if body.System != nil {
		t.Errorf("empty system should serialize as nil/omitted, got %v", body.System)
	}
	// Last tool gets the breakpoint so the tools array still caches.
	if body.Tools[len(body.Tools)-1].CacheControl == nil {
		t.Errorf("last tool should carry the fallback breakpoint")
	}
	if body.Tools[0].CacheControl != nil {
		t.Errorf("only the last tool should be marked, not the first")
	}
}

func TestCacheableRequest_NoSystemNoTools(t *testing.T) {
	var body apiRequest
	cacheableRequest(&body, "", nil)
	if body.System != nil {
		t.Errorf("system should be nil")
	}
	if body.Tools != nil {
		t.Errorf("tools should be nil")
	}
}
