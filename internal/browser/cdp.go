// Package browser is octo's owned, in-binary browser automation backend. It
// drives a real Chrome over the Chrome DevTools Protocol (CDP) — websocket +
// JSON — with no Node and no external proxy. CDP is the only native surface;
// the Chrome itself is the sole runtime dependency.
//
// cdp.go holds the transport: a minimal JSON-RPC client over a single
// websocket, multiplexing commands and events across flattened target
// sessions. Higher-level actions (navigate, click, …) live in browser.go.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// cdpClient is a JSON-RPC client over one CDP websocket. Commands carry an
// auto-incrementing id and an optional sessionId (flatten mode); responses are
// routed back by id, events are fanned out to subscribers by (method,
// sessionId).
type cdpClient struct {
	conn *websocket.Conn

	writeMu sync.Mutex // gorilla requires serialized writes
	nextID  int64

	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponse

	subsMu  sync.Mutex
	subs    map[int]*subscription
	nextSub int

	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

type rpcRequest struct {
	ID        int64  `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type rpcResponse struct {
	ID        int64           `json:"id"`
	Result    json.RawMessage `json:"result"`
	Error     *cdpError       `json:"error"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	SessionID string          `json:"sessionId"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message) }

// subscription receives events whose method and (when set) sessionId match.
type subscription struct {
	method    string
	sessionID string
	ch        chan rpcResponse
}

func newCDPClient(conn *websocket.Conn) *cdpClient {
	c := &cdpClient{
		conn:    conn,
		pending: make(map[int64]chan rpcResponse),
		subs:    make(map[int]*subscription),
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *cdpClient) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.shutdown(err)
			return
		}
		var msg rpcResponse
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != 0 {
			c.pendingMu.Lock()
			ch := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.pendingMu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		// Event: fan out to matching subscribers without blocking the reader.
		c.subsMu.Lock()
		for _, s := range c.subs {
			if s.method != msg.Method {
				continue
			}
			if s.sessionID != "" && s.sessionID != msg.SessionID {
				continue
			}
			select {
			case s.ch <- msg:
			default:
			}
		}
		c.subsMu.Unlock()
	}
}

func (c *cdpClient) shutdown(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.closed)
		c.conn.Close()
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
	})
}

// call sends a command and waits for its response. sessionID may be empty for
// browser-level commands.
func (c *cdpClient) call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	req := rpcRequest{ID: id, Method: method, Params: params, SessionID: sessionID}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, payload)
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("cdp write %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-c.closed:
		return nil, fmt.Errorf("cdp connection closed: %w", c.closeErr)
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("cdp connection closed: %w", c.closeErr)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, resp.Error)
		}
		return resp.Result, nil
	}
}

// subscribe registers interest in an event method (optionally scoped to a
// session) and returns the channel plus an unsubscribe func. The buffer is
// generous; the reader drops on overflow rather than blocking, so callers must
// subscribe before triggering the action that emits the event.
func (c *cdpClient) subscribe(method, sessionID string) (<-chan rpcResponse, func()) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	id := c.nextSub
	c.nextSub++
	s := &subscription{method: method, sessionID: sessionID, ch: make(chan rpcResponse, 32)}
	c.subs[id] = s
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			// Delete then close under the same lock the reader holds while sending,
			// so a `for ev := range ch` consumer actually terminates (unsub used to
			// only delete, leaving such goroutines blocked forever). readLoop only
			// sends to subs still in the map and only under subsMu, so closing here
			// can never race a send onto the closed channel. sync.Once guards against
			// a double unsub closing twice.
			c.subsMu.Lock()
			delete(c.subs, id)
			close(s.ch)
			c.subsMu.Unlock()
		})
	}
}

func (c *cdpClient) close() { c.shutdown(fmt.Errorf("closed by caller")) }

// done returns a channel closed when the connection shuts down, so long-lived
// event consumers (e.g. the OOPIF target watcher) can exit instead of blocking
// forever on a subscription channel the shutdown never closes.
func (c *cdpClient) done() <-chan struct{} { return c.closed }
