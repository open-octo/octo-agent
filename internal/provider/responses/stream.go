package responses

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/open-octo/octo-agent/internal/provider"
	"github.com/open-octo/octo-agent/internal/provider/retry"
)

// toolCallState accumulates one function call's streamed arguments. call_id
// and name arrive only on response.output_item.added; the argument deltas
// that follow carry just the item_id.
type toolCallState struct {
	callID string
	name   string
	args   strings.Builder
}

// SendStream implements provider.StreamingProvider with stream:true.
//
// Events are dispatched on the JSON "type" field. The stream may end either
// with a "data: [DONE]" line or with EOF right after the terminal
// response.completed / response.incomplete event — the docs don't pin down
// which, so both are accepted.
func (c *Client) SendStream(ctx context.Context, req provider.Request, cb provider.StreamCallbacks) (provider.Response, error) {
	payload, err := c.buildRequest(req, true)
	if err != nil {
		return provider.Response{}, err
	}

	release, err := c.Limiter.Acquire(ctx)
	if err != nil {
		return provider.Response{}, err
	}
	defer release()

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	// Retry only request establishment; once tokens stream out a retry would
	// duplicate them.
	resp, err := retry.Do(streamCtx, c.policy(), func(ctx context.Context) (*http.Response, retry.Decision, error) {
		httpReq, err := c.newHTTPRequest(ctx, payload, true)
		if err != nil {
			return nil, retry.Decision{}, err
		}
		resp, err := c.streamingHTTPClient().Do(httpReq)
		if err != nil {
			return nil, retry.Decision{Retry: retry.RetryableErr(ctx, err)}, fmt.Errorf("responses: send stream: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			dec, err := statusError(resp)
			resp.Body.Close()
			return nil, dec, err
		}
		return resp, retry.Decision{}, nil
	})
	if err != nil {
		return provider.Response{}, err
	}
	defer resp.Body.Close()

	var (
		final       apiResponse
		text        strings.Builder
		reasoning   = map[string]*strings.Builder{} // item_id → streamed reasoning text
		tools       = map[string]*toolCallState{}   // item_id → call
		toolOrder   []string
		doneItems   []doneItem
		sawEvent    bool
		sawTerminal bool
	)

	streamBody := retry.IdleTimeoutReader(resp.Body, c.streamIdleTimeout(), cancelStream)
	scanner := bufio.NewScanner(streamBody)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

scan:
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			sawTerminal = true
			break
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return provider.Response{}, retry.AsTransientStream(fmt.Errorf("responses: parse stream event: %w", err))
		}
		sawEvent = true

		switch ev.Type {
		case "response.output_text.delta", "response.refusal.delta":
			text.WriteString(ev.Delta)
			if cb.OnText != nil && ev.Delta != "" {
				cb.OnText(ev.Delta)
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			sb := reasoning[ev.ItemID]
			if sb == nil {
				sb = &strings.Builder{}
				reasoning[ev.ItemID] = sb
			}
			sb.WriteString(ev.Delta)
			if cb.OnThinking != nil && ev.Delta != "" {
				cb.OnThinking(ev.Delta)
			}
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				if _, ok := tools[ev.Item.ID]; !ok {
					toolOrder = append(toolOrder, ev.Item.ID)
				}
				tools[ev.Item.ID] = &toolCallState{callID: ev.Item.CallID, name: ev.Item.Name}
			}
		case "response.function_call_arguments.delta":
			st := tools[ev.ItemID]
			if st == nil {
				st = &toolCallState{}
				tools[ev.ItemID] = st
				toolOrder = append(toolOrder, ev.ItemID)
			}
			st.args.WriteString(ev.Delta)
			if cb.OnToolDelta != nil && ev.Delta != "" && st.callID != "" {
				cb.OnToolDelta(st.callID, st.name, ev.Delta)
			}
		case "response.output_item.done":
			// The done item is authoritative: a reasoning item's
			// encrypted_content can be incomplete on the added event.
			if ev.Item != nil {
				doneItems = append(doneItems, doneItem{ev.OutputIndex, *ev.Item})
			}
		case "response.completed", "response.incomplete":
			if ev.Response != nil {
				final = *ev.Response
			}
			sawTerminal = true
			break scan
		case "response.failed":
			if ev.Response != nil {
				return provider.Response{}, failedError(ev.Response.Error)
			}
			return provider.Response{}, failedError(nil)
		case "error":
			return provider.Response{}, fmt.Errorf("responses: stream error (%s): %s", ev.Code, ev.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return provider.Response{}, retry.AsTransientStream(fmt.Errorf("responses: stream read: %w", err))
	}
	// A clean EOF with no terminal event is a connection dropped on an event
	// boundary mid-generation; handing back the partial turn as success would
	// dispatch truncated tool arguments.
	if !sawTerminal && sawEvent {
		return provider.Response{}, retry.AsTransientStream(errors.New("responses: stream ended without a terminal event (truncated mid-generation)"))
	}

	output := orderedItems(doneItems)
	if len(output) == 0 {
		output = final.Output
	}
	if len(output) == 0 {
		output = synthesizeOutput(text.String(), tools, toolOrder)
	}
	out, err := toResponse(req.Model, final, output)
	if err != nil {
		return provider.Response{}, err
	}
	// Reasoning text that streamed as deltas but isn't on the done item (raw
	// reasoning_text, or a backend that leaves summary empty) is kept for
	// display; replay still depends only on the encrypted content.
	for i := range out.Blocks {
		b := &out.Blocks[i]
		if b.Type == "thinking" && b.Thinking == "" {
			if sb := reasoning[b.ID]; sb != nil {
				b.Thinking = sb.String()
			}
		}
	}
	return out, nil
}

// doneItem is one response.output_item.done payload with its output_index.
type doneItem struct {
	index int
	item  outputItem
}

// orderedItems sorts done items by output_index. The sort is stable and items
// are collected in arrival order, so a backend that omits output_index (every
// item reads as 0) still yields all of them, in the order they streamed.
func orderedItems(done []doneItem) []outputItem {
	if len(done) == 0 {
		return nil
	}
	sort.SliceStable(done, func(i, j int) bool { return done[i].index < done[j].index })
	out := make([]outputItem, len(done))
	for i, d := range done {
		out[i] = d.item
	}
	return out
}

// synthesizeOutput rebuilds output items from the streamed deltas, for a
// backend that sends neither output_item.done events nor the output array on
// the terminal event.
func synthesizeOutput(text string, tools map[string]*toolCallState, order []string) []outputItem {
	var out []outputItem
	if text != "" {
		out = append(out, outputItem{Type: "message", Content: []outputContent{{Type: "output_text", Text: text}}})
	}
	for _, id := range order {
		st := tools[id]
		out = append(out, outputItem{Type: "function_call", ID: id, CallID: st.callID, Name: st.name, Arguments: st.args.String()})
	}
	return out
}

// streamingHTTPClient reuses an injected client's transport but drops its
// end-to-end Timeout, which would otherwise kill a long generation mid-stream;
// cancellation comes from the context and the idle-read guard instead.
func (c *Client) streamingHTTPClient() *http.Client {
	if c.HTTPClient != nil {
		clone := *c.HTTPClient
		clone.Timeout = 0
		return &clone
	}
	return &http.Client{}
}

var _ provider.StreamingProvider = (*Client)(nil)
