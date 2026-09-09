package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/provider"
	"github.com/open-octo/octo-agent/internal/provider/ratelimit"
	"github.com/open-octo/octo-agent/internal/provider/retry"
)

// TestRetriesShareOneRPMPermit pins a deliberate contract: retries inside one
// provider call don't re-enter the gate. The call holds its slot across them
// and spends a single RPM permit for the lot, with the retry layer's backoff
// spacing them out. With rpm=1 on a real 60-second window, a second permit
// would park the retry for a minute — so this also proves the retry isn't
// silently paying the window's price.
//
// If someone later decides retries SHOULD be counted (the endpoint does count
// them), this test is the thing that will fail and force the decision to be
// explicit rather than accidental.
func TestRetriesShareOneRPMPermit(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_01",
			"type": "message",
			"role": "assistant",
			"model": "claude-haiku-4-5-20251001",
			"content": [{"type": "text", "text": "hi"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`))
	}))
	defer srv.Close()

	c, err := New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = srv.URL
	c.Limiter = ratelimit.New(1, 1) // one per minute, one at a time
	c.Retry = retry.Policy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}

	start := time.Now()
	if _, err := c.Send(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5-20251001",
		Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("server saw %d attempts, want 2 (one 429 then the retry)", got)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("call took %v — the retry waited for a second RPM permit instead of reusing the call's", elapsed)
	}
}

// TestLimiterSerialisesCalls checks the wiring, which no unit test of the
// limiter itself can: the gate must sit around the whole provider call on
// both the buffered and the streaming path. A streaming gate placed around
// request establishment alone would pass every limiter test and still let
// eight streams run at once.
func TestLimiterSerialisesCalls(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Client, context.Context) error
	}{
		{"Send", func(c *Client, ctx context.Context) error {
			_, err := c.Send(ctx, provider.Request{
				Model:    "claude-haiku-4-5-20251001",
				Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}},
			})
			return err
		}},
		{"SendStream", func(c *Client, ctx context.Context) error {
			_, err := c.SendStream(ctx, provider.Request{
				Model:    "claude-haiku-4-5-20251001",
				Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}},
			}, provider.StreamCallbacks{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inFlight, peak atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := inFlight.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				defer inFlight.Add(-1)
				// Hold the request open long enough that an ungated caller
				// would overlap with it.
				time.Sleep(10 * time.Millisecond)

				if r.Header.Get("Accept") == "text/event-stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, canonicalStream)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"id": "msg_01",
					"type": "message",
					"role": "assistant",
					"model": "claude-haiku-4-5-20251001",
					"content": [{"type": "text", "text": "hi"}],
					"stop_reason": "end_turn",
					"usage": {"input_tokens": 1, "output_tokens": 1}
				}`))
			}))
			defer srv.Close()

			c, err := New("test-key")
			if err != nil {
				t.Fatal(err)
			}
			c.BaseURL = srv.URL
			c.Limiter = ratelimit.New(0, 1)

			var wg sync.WaitGroup
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := tc.call(c, context.Background()); err != nil {
						t.Errorf("call: %v", err)
					}
				}()
			}
			wg.Wait()

			if got := peak.Load(); got != 1 {
				t.Errorf("peak concurrent requests at the server = %d, want 1", got)
			}
		})
	}
}
