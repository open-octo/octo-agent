package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// defaultCallTimeout bounds a single request/response when the caller's context
// carries no deadline of its own. Without it, a server that accepts a request
// but never sends a response frame (and keeps the transport open, so no EOF)
// would block Call — and the agent turn — forever. Handshake/list calls in
// connectOne already impose their own deadlines, so this only affects runtime
// calls (CallTool/ReadResource/GetPrompt) invoked with a deadline-free ctx.
// A var (not const) so tests can shrink it.
var defaultCallTimeout = 60 * time.Second

// Client is a single MCP server connection: one transport, one initialize
// handshake, request/response dispatch by ID. It's safe for concurrent
// callers — Call serialises through the transport via sendMu, and the
// receive loop demultiplexes responses on a per-request channel.
//
// Lifecycle:
//
//	Initialize  →  ListTools / CallTool / ListResources / ... (any order, concurrent OK)  →  Close
//
// The receive goroutine is started in Initialize and stops when the
// transport hits EOF or Close is called. Pending callers waiting on a
// response see their channel closed with an error in that case.
type Client struct {
	transport Transport
	info      Implementation
	// caps is populated after Initialize so callers can branch on which
	// surfaces the server actually advertised. Tools/Resources/Prompts are
	// pointers in the spec; nil means "not supported".
	//
	// metaMu guards all four: they are written by the initialize handshake,
	// which is no longer a once-at-startup event — recovering an expired
	// session re-runs it mid-session, concurrently with readers like
	// Capabilities() or a ListTools call already in flight. A restarted server
	// may legitimately answer with different capabilities than the session we
	// lost, so these really can change under readers.
	metaMu       sync.RWMutex
	caps         ServerCapabilities
	serverInfo   Implementation
	instructions string
	// protocolVersion is what the server answered with in the handshake, not
	// necessarily the ProtocolVersion constant we asked for.
	protocolVersion string

	nextID atomic.Uint64

	// pending tracks in-flight requests. The map is keyed by the request
	// ID we issued (as a string for json.RawMessage compatibility) and
	// holds the response channel the caller is blocked on.
	pendingMu sync.Mutex
	pending   map[string]chan *Message

	// sendMu is the transport's write lock. Stdio transport already
	// serialises but the contract is per-Client, so we hold it explicitly
	// at the Call call site.
	sendMu sync.Mutex

	// onNotification, when set, receives server-initiated notifications by
	// method name. Guarded because it's read from the receive loop and set
	// from whoever owns the client.
	notifyMu       sync.Mutex
	onNotification func(method string)

	rxDone chan struct{} // closed when the receive loop exits
	closed atomic.Bool
}

// NewClient builds a Client around an already-Open transport. The caller
// owns transport lifetime up to Initialize; after Initialize succeeds the
// Client owns it and Close will tear it down.
func NewClient(t Transport, info Implementation) *Client {
	return &Client{
		transport: t,
		info:      info,
		pending:   make(map[string]chan *Message),
		rxDone:    make(chan struct{}),
	}
}

// Initialize performs the MCP handshake: send the initialize request,
// receive the result, then send the initialized notification. Starts the
// background receive loop on the way in so the response to initialize is
// demuxed normally.
func (c *Client) Initialize(ctx context.Context) error {
	// Kick off the receive loop BEFORE issuing the first request so the
	// response can be matched. If the loop fails to start the goroutine
	// would still observe ctx cancellation, but starting it ahead removes
	// a race where Send finishes and the response arrives before we set
	// up the demuxer.
	go c.receiveLoop()

	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      c.info,
	}
	var result InitializeResult
	if err := c.Call(ctx, MethodInitialize, params, &result); err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	c.storeHandshake(result)

	// Transports that carry the negotiated revision on the wire (HTTP, via
	// the MCP-Protocol-Version header) learn it here — before the
	// initialized notification below, so every post-handshake request
	// advertises it. stdio needs nothing and doesn't implement the setter.
	if pv, ok := c.transport.(protocolVersionSetter); ok && result.ProtocolVersion != "" {
		pv.SetProtocolVersion(result.ProtocolVersion)
	}

	// notifications/initialized lets the server know we're done handshaking
	// and ready for normal traffic. Fire-and-forget — the server doesn't
	// reply, and a transport error here surfaces on the next real call.
	if err := c.Notify(ctx, MethodInitialized, nil); err != nil {
		return fmt.Errorf("mcp: send initialized: %w", err)
	}
	return nil
}

// protocolVersionSetter is implemented by transports that must advertise the
// negotiated protocol revision on every request. Only HTTP needs it — stdio
// carries no per-request metadata — so it stays an optional capability
// rather than part of the Transport interface.
type protocolVersionSetter interface {
	SetProtocolVersion(v string)
}

// storeHandshake records what a (re)initialize answered with. Called from
// both the startup handshake and expired-session recovery.
func (c *Client) storeHandshake(r InitializeResult) {
	c.metaMu.Lock()
	c.caps = r.Capabilities
	c.serverInfo = r.ServerInfo
	c.instructions = r.Instructions
	c.protocolVersion = r.ProtocolVersion
	c.metaMu.Unlock()
}

// ServerInfo returns the {name, version} the server advertised in its
// initialize response. Useful for /mcp output.
func (c *Client) ServerInfo() Implementation {
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	return c.serverInfo
}

// ProtocolVersion returns the spec revision the server chose in the
// handshake, which may differ from the ProtocolVersion we requested. Empty
// before Initialize completes.
func (c *Client) ProtocolVersion() string {
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	return c.protocolVersion
}

// Capabilities returns the server's advertised capabilities so callers can
// avoid calling tools/list against a server that doesn't support tools.
func (c *Client) Capabilities() ServerCapabilities {
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	return c.caps
}

// Instructions returns the optional human-readable instructions the server
// included in its initialize response. Some servers use this to tell the
// agent how their tools should be used.
func (c *Client) Instructions() string {
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	return c.instructions
}

// Call issues a request and blocks for the matched response. params is
// json-marshalled; result, if non-nil, is unmarshalled from the response's
// result field. RPC errors (server-returned) become a typed error caller
// can inspect.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	// Bound the call when the caller gave no deadline, so a server that never
	// replies can't wedge the turn indefinitely (stdio's Receive can't be
	// interrupted by ctx once it blocks in Decode — only Close or a deadline
	// unblocks the waiting caller).
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}
	id := c.nextID.Add(1)
	idBytes := []byte(strconv.FormatUint(id, 10))
	idKey := string(idBytes)

	// Register the pending channel BEFORE sending so a fast response can't
	// arrive before the demuxer knows where to deliver it.
	ch := make(chan *Message, 1)
	c.pendingMu.Lock()
	c.pending[idKey] = ch
	c.pendingMu.Unlock()
	defer c.removePending(idKey)

	msg := &Message{
		JSONRPC: "2.0",
		ID:      idBytes,
		Method:  method,
	}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: marshal params: %w", err)
		}
		msg.Params = p
	}

	if err := c.sendRecoveringSession(ctx, msg); err != nil {
		return err
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return errors.New("mcp: connection closed")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("mcp: unmarshal result: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notify sends a one-way notification — no id, no response. params may be
// nil for parameterless notifications (e.g., notifications/initialized).
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	msg := &Message{JSONRPC: "2.0", Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: marshal notify params: %w", err)
		}
		msg.Params = p
	}
	return c.sendRecoveringSession(ctx, msg)
}

// sendRecoveringSession is the single write path to the transport. It holds
// sendMu for the whole attempt, so if the server reports our session gone it
// can run the recovery handshake and replay msg with no other sender able to
// slip a request onto the dead session in between.
//
// The handshake deliberately does NOT go back through Call/Notify: those take
// sendMu, which is not reentrant, so reusing them here would deadlock. It
// talks to the transport directly instead — safe precisely because we already
// hold the lock. Only one recovery is attempted; a second expiry in a row is
// a server we can't keep a session with, and returning the error beats
// looping.
func (c *Client) sendRecoveringSession(ctx context.Context, msg *Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	err := c.transport.Send(ctx, msg)
	if !errors.Is(err, ErrSessionExpired) {
		return err
	}
	if rerr := c.rehandshakeLocked(ctx); rerr != nil {
		// Report the original expiry as the cause — that's what the caller
		// needs to understand — with the recovery failure as context.
		return fmt.Errorf("%w (recovery handshake failed: %v)", err, rerr)
	}
	return c.transport.Send(ctx, msg)
}

// rehandshakeLocked re-runs initialize + initialized on a transport whose
// session the server has forgotten. Caller must hold sendMu; this sends on
// the transport without taking it. Waiting for the initialize response is
// fine under the lock: the receive loop is a separate goroutine and delivers
// it through the pending map, which sendMu doesn't guard.
func (c *Client) rehandshakeLocked(ctx context.Context) error {
	id := c.nextID.Add(1)
	idBytes := []byte(strconv.FormatUint(id, 10))
	idKey := string(idBytes)

	ch := make(chan *Message, 1)
	c.pendingMu.Lock()
	c.pending[idKey] = ch
	c.pendingMu.Unlock()
	defer c.removePending(idKey)

	params, err := json.Marshal(InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      c.info,
	})
	if err != nil {
		return fmt.Errorf("marshal initialize params: %w", err)
	}
	if err := c.transport.Send(ctx, &Message{
		JSONRPC: "2.0", ID: idBytes, Method: MethodInitialize, Params: params,
	}); err != nil {
		return err
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return errors.New("connection closed")
		}
		if resp.Error != nil {
			return resp.Error
		}
		var result InitializeResult
		if len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return fmt.Errorf("unmarshal initialize result: %w", err)
			}
		}
		// A restarted server may answer with different capabilities or even a
		// different protocol revision than the session we lost, so adopt what
		// it says now rather than assuming continuity.
		c.storeHandshake(result)
		if pv, ok := c.transport.(protocolVersionSetter); ok && result.ProtocolVersion != "" {
			pv.SetProtocolVersion(result.ProtocolVersion)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	return c.transport.Send(ctx, &Message{JSONRPC: "2.0", Method: MethodInitialized})
}

// SetNotificationHandler registers fn to receive server-initiated
// notifications by method name (e.g. notifications/tools/list_changed). Pass
// nil to clear. Set it before Initialize if the very first notifications
// matter.
//
// fn is invoked on its own goroutine, one per notification, deliberately: the
// receive loop is the only thing that delivers responses, so a handler that
// called back into the client and waited for a reply would block the loop that
// has to deliver that reply — a deadlock. Spawning means handlers can call the
// client freely, at the cost of no ordering guarantee between concurrent
// notifications, so a handler must tolerate being run more than once
// concurrently.
func (c *Client) SetNotificationHandler(fn func(method string)) {
	c.notifyMu.Lock()
	c.onNotification = fn
	c.notifyMu.Unlock()
}

// receiveLoop runs for the life of the Client, pulling frames off the
// transport and dispatching responses to the matching pending channel.
// Notifications are handed to the registered handler, if any; server-initiated
// requests are still ignored (nothing here implements a server-callable
// surface, and answering with an error would be noise).
func (c *Client) receiveLoop() {
	defer close(c.rxDone)
	ctx := context.Background()
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			c.abortPending(err)
			return
		}
		if msg.IsResponse() {
			c.deliverResponse(msg)
			continue
		}
		if msg.IsNotification() {
			c.notifyMu.Lock()
			fn := c.onNotification
			c.notifyMu.Unlock()
			if fn != nil {
				go fn(msg.Method)
			}
		}
		// Server-initiated request: ignored — see the doc comment above.
	}
}

func (c *Client) deliverResponse(m *Message) {
	c.pendingMu.Lock()
	ch, ok := c.pending[string(m.ID)]
	delete(c.pending, string(m.ID))
	c.pendingMu.Unlock()
	if !ok {
		return // unknown id — server bug or late delivery after timeout
	}
	ch <- m
}

func (c *Client) removePending(id string) {
	c.pendingMu.Lock()
	if ch, ok := c.pending[id]; ok {
		delete(c.pending, id)
		close(ch)
	}
	c.pendingMu.Unlock()
}

func (c *Client) abortPending(_ error) {
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

// Close tears the transport down and unblocks any pending Call(). Safe to
// call multiple times.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	err := c.transport.Close()
	// Wait briefly for the receive loop to notice — best-effort. If the
	// transport's Close didn't unblock Receive (rare), we don't hang here.
	select {
	case <-c.rxDone:
	default:
	}
	c.abortPending(nil)
	return err
}

// ── Typed convenience wrappers ───────────────────────────────────────────

// maxListPages bounds cursor-following so a server that keeps handing back a
// fresh cursor can't spin us forever. Generous enough that hitting it means
// the server is broken, not that someone has a lot of tools.
const maxListPages = 100

// nextPageCursor decides whether to fetch another page. A server signals the
// end with an empty cursor; one that repeats the cursor it just gave us is
// buggy, and following it would loop forever, so treat that as the end too.
func nextPageCursor(next, current string) (string, bool) {
	if next == "" || next == current {
		return "", false
	}
	return next, true
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if c.Capabilities().Tools == nil {
		return nil, nil
	}
	var all []Tool
	cursor := ""
	for range maxListPages {
		var r ListToolsResult
		if err := c.Call(ctx, MethodToolsList, PaginatedParams{Cursor: cursor}, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Tools...)
		next, more := nextPageCursor(r.NextCursor, cursor)
		if !more {
			return all, nil
		}
		cursor = next
	}
	// Erroring rather than returning a truncated list: silently exposing a
	// subset of a server's tools is the failure this pagination exists to fix.
	return nil, fmt.Errorf("mcp: tools/list did not terminate within %d pages", maxListPages)
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	var r CallToolResult
	if err := c.Call(ctx, MethodToolsCall, CallToolParams{Name: name, Arguments: args}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	if c.Capabilities().Resources == nil {
		return nil, nil
	}
	var all []Resource
	cursor := ""
	for range maxListPages {
		var r ListResourcesResult
		if err := c.Call(ctx, MethodResourcesList, PaginatedParams{Cursor: cursor}, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Resources...)
		next, more := nextPageCursor(r.NextCursor, cursor)
		if !more {
			return all, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("mcp: resources/list did not terminate within %d pages", maxListPages)
}

func (c *Client) ReadResource(ctx context.Context, uri string) ([]ResourceContent, error) {
	var r ReadResourceResult
	if err := c.Call(ctx, MethodResourcesRead, ReadResourceParams{URI: uri}, &r); err != nil {
		return nil, err
	}
	return r.Contents, nil
}

func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	if c.Capabilities().Prompts == nil {
		return nil, nil
	}
	var all []Prompt
	cursor := ""
	for range maxListPages {
		var r ListPromptsResult
		if err := c.Call(ctx, MethodPromptsList, PaginatedParams{Cursor: cursor}, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Prompts...)
		next, more := nextPageCursor(r.NextCursor, cursor)
		if !more {
			return all, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("mcp: prompts/list did not terminate within %d pages", maxListPages)
}

func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	var r GetPromptResult
	if err := c.Call(ctx, MethodPromptsGet, GetPromptParams{Name: name, Arguments: args}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
