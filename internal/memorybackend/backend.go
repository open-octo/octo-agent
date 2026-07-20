// Package memorybackend adapts octo to optional, user-configured external
// semantic memory services (hindsight, mem0, agentmemory). It is separate
// from and does not touch internal/memory, which owns the markdown
// MEMORY.md/topic-file layer — see dev-docs/memory-design.md for why that
// layer is deliberately untyped. This package is the opposite: a typed
// store/recall client for services that do their own extraction and
// retrieval server-side.
package memorybackend

import (
	"context"
	"fmt"
)

// Config selects and configures a single backend. Only one backend can be
// active at a time.
type Config struct {
	// Type is "hindsight", "mem0", or "agentmemory".
	Type string
	// BaseURL is the backend's REST endpoint, e.g. http://localhost:8888.
	BaseURL string
	// APIKey is optional; whether it's required depends on the backend and
	// how it was deployed (e.g. mem0 requires auth by default, hindsight
	// does not).
	APIKey string
	// Namespace scopes stored/recalled memories (hindsight bank_id, mem0
	// user_id, agentmemory project). Defaults to "default" when empty.
	Namespace string
	// Mode selects between deployment variants of the same Type. Currently
	// only meaningful for "mem0": "cloud" talks to the hosted Platform API
	// (api.mem0.ai); "" or "self_hosted" (the default) talks to the
	// self-hosted server/ OSS stack. Ignored by other backend types.
	Mode string
}

func (c Config) namespace() string {
	if c.Namespace == "" {
		return "default"
	}
	return c.Namespace
}

// Result is one recalled memory, normalized across backends.
type Result struct {
	ID      string
	Content string
	Score   float64
}

// Backend is the normalized interface every adapter implements.
type Backend interface {
	// Name identifies the backend, e.g. "hindsight".
	Name() string
	// Store saves a piece of free-form text. Backends that do their own
	// extraction (mem0, agentmemory) are expected to receive raw
	// conversational content and extract/dedupe it themselves.
	Store(ctx context.Context, content string) error
	// Recall searches for memories relevant to query.
	Recall(ctx context.Context, query string) ([]Result, error)
}

// New builds the Backend for cfg.Type. It only validates the type and
// required fields; it does not verify connectivity — errors from an
// unreachable or misconfigured backend surface on the first Store/Recall
// call.
func New(cfg Config) (Backend, error) {
	// "cloud" mode auto-fills the fixed hosted base_url when the user leaves it
	// blank. mem0 and hindsight each run a single managed Platform, so there's
	// one canonical URL per backend (unlike self-hosted, which needs an explicit
	// address). agentmemory has no hosted service, so it's not listed here.
	if cfg.Mode == "cloud" && cfg.BaseURL == "" {
		switch cfg.Type {
		case "mem0":
			cfg.BaseURL = mem0CloudBaseURL
		case "hindsight":
			cfg.BaseURL = hindsightCloudBaseURL
		}
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("memorybackend: base_url is required")
	}
	switch cfg.Type {
	case "hindsight":
		return newHindsight(cfg), nil
	case "mem0":
		return newMem0(cfg), nil
	case "agentmemory":
		return newAgentMemory(cfg), nil
	default:
		return nil, fmt.Errorf("memorybackend: unknown type %q (want hindsight, mem0, or agentmemory)", cfg.Type)
	}
}
