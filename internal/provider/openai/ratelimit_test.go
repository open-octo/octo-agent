package openai

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
)

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
				Model:    "gpt-4o-mini",
				Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}},
			})
			return err
		}},
		{"SendStream", func(c *Client, ctx context.Context) error {
			_, err := c.SendStream(ctx, provider.Request{
				Model:    "gpt-4o-mini",
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
					_, _ = io.WriteString(w, canonicalOpenAIStream)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"id": "c1",
					"object": "chat.completion",
					"model": "gpt-4o-mini",
					"choices": [{"index": 0, "message": {"role": "assistant", "content": "hi"}, "finish_reason": "stop"}],
					"usage": {"prompt_tokens": 1, "completion_tokens": 1}
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
