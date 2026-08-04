package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// HTTPTransport speaks JSON-RPC 2.0 over a single HTTP endpoint, per the
// MCP "Streamable HTTP" transport in spec 2024-11-05.
//
// What we implement
//   - POST one JSON-RPC frame per request, body Content-Type application/json.
//   - A plain application/json response body holds exactly one JSON-RPC
//     frame (the response to the request we just sent): treated as a
//     synchronous RPC.
//   - A text/event-stream response is read as a sequence of SSE "data:"
//     frames, each decoded as a JSON-RPC message and queued for Receive in
//     order; reading stops once the frame whose id matches our request
//     arrives (the spec has the server close the stream at that point
//     anyway) or the stream itself closes.
//   - The server-issued Mcp-Session-Id header is captured on the first
//     response and echoed on every subsequent request, so the server can
//     resume per-client state.
//
// What we don't implement (v1 omission)
//   - SSE stream resumption via Last-Event-ID on reconnect.
//   - The optional GET endpoint for server-initiated notifications outside
//     of a request/response cycle.
//   - Bidirectional concurrent sends. The client serialises one request at
//     a time anyway.
//
// Receive is wired through an in-memory channel: each Send queues the
// frame(s) decoded from its response, Receive pops the next one. This
// keeps the same "Send / Receive interleaved" interface stdio uses, so the
// client logic is transport-agnostic.
type HTTPTransport struct {
	url     string
	headers map[string]string
	hc      *http.Client
	oauth   OAuthProvider // nil = no OAuth; static headers only

	// Session id is empty until the first server response sets it via
	// Mcp-Session-Id. Subsequent requests echo it back.
	sessionMu sync.Mutex
	sessionID string

	// inbox queues the response of each Send so Receive can hand it back.
	// Buffered so a Send never blocks (paired with one Receive per Send,
	// the channel never grows unboundedly).
	inbox chan *Message
	// done is closed by Close to signal shutdown. We never close inbox itself:
	// an in-flight doRequest may still be about to send on it, and a send on a
	// closed channel panics (and would crash the process). Producers and Receive
	// select on done instead.
	done   chan struct{}
	closed atomic.Bool
}

// HTTPConfig is the configuration end-users pass via mcp.json — URL plus
// optional static headers (Authorization, custom auth, etc.). Headers
// values are sent verbatim; we don't expand env-var placeholders here, that
// happens in the config layer before we land on this struct.
type HTTPConfig struct {
	URL     string
	Headers map[string]string
	// OAuth, when non-nil, drives bearer-token injection + 401 retry.
	// The transport calls OAuth.Token before each request and
	// OAuth.Invalidate on a 401 response, then retries once.
	OAuth OAuthProvider
}

// NewHTTPTransport builds an HTTPTransport ready to Send / Receive.
// No network I/O happens until the first Send.
func NewHTTPTransport(cfg HTTPConfig) (*HTTPTransport, error) {
	if cfg.URL == "" {
		return nil, errors.New("mcp: http transport: empty URL")
	}
	return &HTTPTransport{
		url:     cfg.URL,
		headers: cfg.Headers,
		oauth:   cfg.OAuth,
		hc:      &http.Client{},
		// 16 is generous — the synchronous Send/Receive pattern only ever
		// has one outstanding response, but headroom protects against any
		// pipelining we might add later.
		inbox: make(chan *Message, 16),
		done:  make(chan struct{}),
	}, nil
}

// Send POSTs msg to the configured URL and queues the decoded response
// frame(s) for Receive. One Send does exactly one HTTP round-trip; a plain
// JSON response yields exactly one queued frame, an SSE response may yield
// several.
//
// When an OAuth provider is configured: every request injects the cached
// access token; a 401 response invalidates the cache and triggers exactly
// one retry (so the user doesn't see two authorization prompts in a row).
func (t *HTTPTransport) Send(ctx context.Context, msg *Message) error {
	if t.closed.Load() {
		return errors.New("mcp: http transport: closed")
	}

	// At most one retry on 401 — the retry uses a freshly-acquired token.
	for attempt := 0; attempt < 2; attempt++ {
		retry, err := t.doRequest(ctx, msg, attempt > 0)
		if !retry {
			return err
		}
		// retry==true means 401 was observed; loop to retry once.
	}
	return errors.New("mcp: http: still unauthorized after retry")
}

// doRequest performs one HTTP round-trip. Returns (retry, err):
//
//   - retry=true means "401 observed, OAuth was invalidated, caller should
//     loop one more time with a freshly-acquired token". err is non-nil
//     here but only as breadcrumb context.
//   - retry=false means "this attempt is terminal — either the response
//     was queued for Receive or err describes a hard failure".
//
// forceFreshToken=true on the retry attempt forces a token refresh path
// even if the cached token still looks unexpired.
func (t *HTTPTransport) doRequest(ctx context.Context, msg *Message, forceFreshToken bool) (retry bool, err error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return false, fmt.Errorf("mcp: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("mcp: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// MCP spec: clients MUST include Accept with both JSON and SSE so the
	// server can pick. We hand-pick the order so a server that supports
	// both prefers plain JSON (which we know how to decode synchronously).
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	t.sessionMu.Lock()
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.sessionMu.Unlock()

	// Bearer-token injection from OAuth provider, if configured.
	if t.oauth != nil {
		if forceFreshToken {
			t.oauth.Invalidate()
		}
		tok, terr := t.oauth.Token(ctx)
		if terr != nil {
			return false, fmt.Errorf("mcp: oauth token: %w", terr)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	resp, err := t.hc.Do(req)
	if err != nil {
		return false, fmt.Errorf("mcp: do: %w", err)
	}
	defer resp.Body.Close()

	// Capture session id on first response (and tolerate the server
	// rotating it on subsequent calls — spec lets them).
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionMu.Lock()
		t.sessionID = sid
		t.sessionMu.Unlock()
	}

	if resp.StatusCode == http.StatusNoContent {
		// 204: server accepted a notification. No body to parse, no inbox
		// queue — the client doesn't expect a response for notifications.
		return false, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// If we have OAuth and this isn't already our second attempt, ask
		// the caller to retry with a fresh token. The provider's
		// Invalidate is what makes the next Token() do real work.
		if t.oauth != nil && !forceFreshToken {
			return true, errors.New("mcp: http 401, will retry")
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("mcp: http 401: %s", bytes.TrimSpace(b))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("mcp: http %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}

	ct := resp.Header.Get("Content-Type")
	switch {
	case ct == "" || isJSONContentType(ct):
		var m Message
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			return false, fmt.Errorf("mcp: decode response: %w", err)
		}
		return false, t.queue(ctx, &m)
	case isEventStreamContentType(ct):
		return false, t.readSSE(ctx, resp.Body, msg.ID)
	default:
		return false, fmt.Errorf("mcp: http: unsupported response Content-Type %q (expected application/json or text/event-stream)", ct)
	}
}

// queue hands a decoded frame to the next Receive call. Shared by the
// plain-JSON and SSE response paths so both honor transport shutdown and
// context cancellation identically.
func (t *HTTPTransport) queue(ctx context.Context, m *Message) error {
	select {
	case t.inbox <- m:
		return nil
	case <-t.done:
		// Transport closed while this request was in flight — drop the response
		// rather than send on a channel teardown is tearing down.
		return errors.New("mcp: http transport: closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// errSSEResponseReceived is an internal sentinel readSSE uses to stop
// scanning once the frame answering our request has been queued — it is
// never returned to the caller.
var errSSEResponseReceived = errors.New("mcp: sse response received")

// maxSSEEventBytes bounds both a single SSE line (via scanner.Buffer) and
// the total size of one event's "data:" lines accumulated across multiple
// lines before a blank line flushes them. Without the latter bound, a
// server that never emits the terminating blank line would grow readSSE's
// buffer unboundedly. A var (not const) so tests can shrink it.
var maxSSEEventBytes = 16 * 1024 * 1024

// readSSE parses a Streamable-HTTP SSE response body: a sequence of events
// separated by blank lines, each carrying its payload on one or more
// "data:" lines (joined with "\n" per the SSE spec). Every decoded frame is
// queued in arrival order — including any notifications the server
// interleaves ahead of the response — so the Client's by-ID demuxer sees
// them exactly as it would over the plain-JSON or stdio transports.
//
// wantID is the id of the request this response body answers; once a
// queued frame is a response carrying that id, we stop reading rather than
// wait for the server to close the stream — which also means we return
// before resp.Body reaches EOF, so Go's http.Transport can't recycle the
// underlying connection (it requires the body be read to completion). We
// accept that cost: waiting for EOF instead would hang forever against a
// server that intentionally keeps the SSE stream open past the response
// (e.g. for later server-initiated messages), which is worse than an extra
// TCP/TLS handshake on the next call. wantID is empty for notifications,
// which expect no response; every frame is still queued as it arrives
// (some servers may emit acks or other notifications on that stream), the
// loop just never exits early and instead runs to stream close.
func (t *HTTPTransport) readSSE(ctx context.Context, body io.Reader, wantID []byte) error {
	scanner := bufio.NewScanner(body)
	// Default 64KiB line limit is too small for a single-line JSON-RPC
	// frame carrying a large tool result; give it room.
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSEEventBytes)

	var data []string
	var dataLen int
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		dataLen = 0
		var m Message
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			return fmt.Errorf("mcp: decode sse frame: %w", err)
		}
		if err := t.queue(ctx, &m); err != nil {
			return err
		}
		if len(wantID) > 0 && m.IsResponse() && bytes.Equal(m.ID, wantID) {
			return errSSEResponseReceived
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				if err == errSSEResponseReceived {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, "data:"):
			field := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			// A server that never emits the blank line ending an event would
			// otherwise grow data unboundedly — bufio.Scanner's per-line cap
			// doesn't help since this accumulates across many lines.
			dataLen += len(field) + 1 // +1 for the "\n" separator flush joins lines with
			if dataLen > maxSSEEventBytes {
				return fmt.Errorf("mcp: sse event exceeds %d bytes without a terminating blank line", maxSSEEventBytes)
			}
			data = append(data, field)
		default:
			// event:, id:, retry:, or a comment line — v1 doesn't act on these.
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("mcp: read sse stream: %w", err)
	}
	// Stream closed without a trailing blank line before EOF: flush
	// whatever event was still buffered.
	if err := flush(); err != nil {
		if err == errSSEResponseReceived {
			return nil
		}
		return err
	}
	if len(wantID) > 0 {
		return fmt.Errorf("mcp: sse stream closed without a response to request id %s", wantID)
	}
	return nil
}

// Receive blocks for the next queued frame. A plain-JSON response queues
// exactly one frame per Send; an SSE response may queue several (server
// notifications ahead of the response), each delivered by its own Receive
// call in arrival order.
func (t *HTTPTransport) Receive(ctx context.Context) (*Message, error) {
	select {
	case m, ok := <-t.inbox:
		if !ok {
			return nil, io.EOF
		}
		return m, nil
	case <-t.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close tears the transport down. Idempotent. After Close, any pending
// Receive caller is unblocked with io.EOF; further Sends return an error.
func (t *HTTPTransport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(t.done)
	return nil
}

// isJSONContentType reports whether ct (a Content-Type header value)
// indicates a JSON-RPC body. Tolerant of the "; charset=utf-8" suffix and
// the legacy "application/json-rpc" alias some servers use.
func isJSONContentType(ct string) bool {
	for _, prefix := range []string{"application/json", "application/json-rpc"} {
		if len(ct) >= len(prefix) && ct[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// isEventStreamContentType reports whether ct indicates an SSE body.
// Tolerant of the "; charset=utf-8" suffix.
func isEventStreamContentType(ct string) bool {
	const prefix = "text/event-stream"
	return len(ct) >= len(prefix) && ct[:len(prefix)] == prefix
}
