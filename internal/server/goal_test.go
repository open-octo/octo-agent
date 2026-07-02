package server

import (
	"errors"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestIsRateLimitErr(t *testing.T) {
	for _, err := range []error{
		errors.New("anthropic: HTTP 429: rate limited"),
		errors.New("openai: HTTP 429 (insufficient_quota): You exceeded your current quota"),
		errors.New("agent: loop[3]: send: Rate limit reached for requests"),
		errors.New("gateway: too many requests"),
	} {
		if !isRateLimitErr(err) {
			t.Errorf("should classify as rate limit: %v", err)
		}
	}
	for _, err := range []error{
		nil,
		errors.New("anthropic: HTTP 500: overloaded"),
		errors.New("context deadline exceeded"),
	} {
		if isRateLimitErr(err) {
			t.Errorf("should NOT classify as rate limit: %v", err)
		}
	}
}

func TestSteerPending(t *testing.T) {
	s := &Server{steerQueues: make(map[string][]agent.InboxItem)}
	if s.steerPending("sid") {
		t.Error("empty queue should not be pending")
	}
	s.enqueueSteer("sid", agent.InboxItem{Text: "hello"})
	if !s.steerPending("sid") {
		t.Error("queued steer should be pending")
	}
	s.drainSteer("sid")
	if s.steerPending("sid") {
		t.Error("drained queue should not be pending")
	}
}
