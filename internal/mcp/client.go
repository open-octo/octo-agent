package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/open-octo/octo-agent/internal/panics"
)

// defaultCallTimeout bounds a single request/response when the caller's context
// carries no deadline of its own. Without it, a server that accepts a request
// but never sends a response frame (and keeps the transport open, so no EOF)
// would block Call — and the agent turn — forever. Handshake/list calls in
// connectOne already impose their own deadlines, so this only affects runtime
// calls (CallTool/ReadResource/GetPrompt) invoked with a deadline-free ctx.
// A var (not const) so tests can shrink it.
var defaultCallTimeout = 60 * time.Second

// maxProgressCallTimeout caps a CallToolProgress call no matter how steadily
// the server keeps reporting progress. Each report restarts the
// defaultCallTimeout idle window, as the spec allows, but the spec also asks
// for an absolute ceiling so a server that reports forever can't hold a turn
// forever. A var (not const) so tests can shrink it.
var maxProgressCallTimeout = 30 * time.Minute

// progressBuffer is how many reports a single call can have queued before
// the receive loop starts dropping them. The receive loop must never block on
// a slow consumer — it is also what delivers every response.
const progressBuffer = 64

// Client is a single MCP server connection: one transport, one initialize
// handshake, request/response dispatch by ID. It's safe for concurrent
// callers — Call serialises writes through a one-slot send semaphore, and the
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

	// sendSem is the transport's write lock, held while a request is written
	// and its response headers read (not for a streamed body — see
	// sendRecoveringSession) so a session recovery can't have another request
	// interleaved onto the dead session. A 1-buffered channel rather than a
	// Mutex because acquisition has to be abandonable: a recovery handshake
	// holds it for a full round-trip, and sync.Mutex.Lock ignores context — so
	// queued callers could not be released by their own deadline or by an
	// interrupt. Waiting on a channel lets them select on ctx instead.
	sendSem chan struct{}

	// onNotification, when set, receives server-initiated notifications by
	// method name. Guarded because it's read from the receive loop and set
	// from whoever owns the client.
	notifyMu       sync.Mutex
	onNotification func(method string)

	// progress maps the progressToken of each in-flight CallToolProgress to
	// the channel its caller drains.
	progressMu sync.Mutex
	progress   map[string]chan ProgressParams

	rxDone chan struct{} // closed when the receive loop exits
	closed atomic.Bool

	// deadErr is set when the receive loop exits, recording why. Distinct from
	// closed on purpose — see markDead.
	deadMu  sync.Mutex
	deadErr error
}

// NewClient builds a Client around an already-Open transport. The caller
// owns transport lifetime up to Initialize; after Initialize succeeds the
// Client owns it and Close will tear it down.
func NewClient(t Transport, info Implementation) *Client {
	return &Client{
		transport: t,
		info:      info,
		pending:   make(map[string]chan *Message),
		progress:  make(map[string]chan ProgressParams),
		rxDone:    make(chan struct{}),
		sendSem:   make(chan struct{}, 1),
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
	// The receive loop is what matches a response to this request. If it has
	// already exited there is nobody left to do that, so waiting out the
	// deadline below would just be a slow way to fail.
	if err := c.dead(); err != nil {
		return fmt.Errorf("mcp: connection is no longer readable: %w", err)
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
		// Given up on mid-send — e.g. a streamed response cut off by an
		// interrupt or timeout — the server may well have the request and be
		// working on it. Failures known to precede the write are notSentError;
		// for the rest a cancellation of an id the server never saw is
		// harmless, as the spec has receivers ignore unknown ids.
		var ns notSentError
		if ctx.Err() != nil && !errors.As(err, &ns) {
			c.cancelRequest(ctx, method, idBytes)
		}
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
		c.cancelRequest(ctx, method, idBytes)
		return ctx.Err()
	}
}

// cancelNotifyTimeout bounds sending the notifications/cancelled for an
// abandoned request. A var (not const) so tests can shrink it.
var cancelNotifyTimeout = 5 * time.Second

// cancelRequest tells the server to stop a request this client has given up
// on — the user interrupted, or a timeout fired. The transport spec is
// explicit that a dropped connection is not a cancellation, so without this
// the server runs an abandoned tool call to completion, which with progress
// reports keeping calls alive can be many minutes of work nobody will read.
//
// ctx is the request's own, already done; its cause becomes the reason. The
// notification goes out in the background on a fresh deadline: the caller is
// returning its error now and shouldn't wait on the server to do so. It is a
// best-effort send (see withBestEffort) — a user who just interrupted an
// authorization prompt must not get another one for this. Any response that
// still arrives finds no pending entry and is dropped, as the spec asks.
func (c *Client) cancelRequest(ctx context.Context, method string, id json.RawMessage) {
	if method == MethodInitialize {
		return // the spec forbids cancelling initialize
	}
	params := CancelledParams{RequestID: id, Reason: context.Cause(ctx).Error()}
	go func() {
		// Detached, with nothing above it to recover a panic.
		defer func() { _ = panics.Error(recover(), "mcp cancel notification", "method", method) }()
		nctx, cancel := context.WithTimeout(withBestEffort(context.Background()), cancelNotifyTimeout)
		defer cancel()
		_ = c.Notify(nctx, MethodCancelled, params)
	}()
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

// streamStarter is implemented by a transport whose response can outlive the
// write: Streamable HTTP answering with an SSE stream. StartSend returns once
// the request is written and the response headers are handled; finish, when
// non-nil, reads the rest of the response. Splitting the two is what lets
// the send slot be released while a long tool call streams.
type streamStarter interface {
	StartSend(ctx context.Context, msg *Message) (finish func() error, err error)
}

// expiryForgetter is implemented by a transport whose finish can report a
// session expiry. ForgetExpired is called with that error while the send slot
// is held: it drops the dead session if it is still the current one, and
// reports whether a recovery handshake is needed — false when another sender
// has already replaced the session.
type expiryForgetter interface {
	ForgetExpired(err error) (needHandshake bool)
}

// sendRecoveringSession is the single write path to the transport. The send
// slot is held while msg is written and the response headers are read, so if
// the server reports our session gone the recovery handshake and replay run
// with no other sender able to slip a request onto the dead session in
// between.
//
// The rest of a streamed response is read after the slot is released.
// Holding it for the stream would serialise every other request to the
// server — a list_changed refresh, a concurrent sub-agent's call — behind a
// tool call that may run for many minutes while it reports progress. A
// session that expires mid-stream (the resume request answers 404) is only
// forgotten once the slot is taken back, and recovered right there: clearing
// it any earlier would let other senders go out with no session at all,
// which a server rejects outright instead of with the 404 that triggers
// recovery.
//
// Each phase recovers at most once; a second expiry in a row is a server we
// can't keep a session with, and returning the error beats looping.
func (c *Client) sendRecoveringSession(ctx context.Context, msg *Message) error {
	finish, err := c.startSend(ctx, msg, nil)
	if err != nil || finish == nil {
		return err
	}
	err = finish()
	if !errors.Is(err, ErrSessionExpired) {
		return err
	}
	finish, err = c.startSend(ctx, msg, err)
	if err != nil || finish == nil {
		return err
	}
	return finish()
}

// notSentError marks a send given up while still waiting for the slot: the
// server never saw the request, so there is nothing to cancel.
type notSentError struct{ err error }

func (e notSentError) Error() string { return e.err.Error() }
func (e notSentError) Unwrap() error { return e.err }

// startSend runs the slot-held phase of a send: write msg and handle the
// response headers, re-handshaking and replaying once if they report the
// session expired.
//
// expired, when non-nil, is the expiry msg's own stream died with. The dead
// session is forgotten and replaced first — unless another sender already
// replaced it, in which case msg is simply replayed on the new one.
//
// The handshake deliberately does NOT go back through Call/Notify: those
// acquire the same slot, which isn't reentrant, so reusing them here would
// deadlock. It talks to the transport directly instead — safe precisely because
// we already hold the slot.
func (c *Client) startSend(ctx context.Context, msg *Message, expired error) (func() error, error) {
	// Another sender may be mid-recovery, so a waiting caller must be able to
	// give up on its own deadline rather than only when the holder finishes.
	select {
	case c.sendSem <- struct{}{}:
	case <-ctx.Done():
		return nil, notSentError{ctx.Err()}
	case <-c.rxDone:
		return nil, notSentError{fmt.Errorf("mcp: connection is no longer readable while waiting to send")}
	}
	defer func() { <-c.sendSem }()

	if expired != nil {
		need := true
		if f, ok := c.transport.(expiryForgetter); ok {
			need = f.ForgetExpired(expired)
		}
		if need {
			if rerr := c.rehandshakeLocked(ctx); rerr != nil {
				return nil, fmt.Errorf("%w (recovery handshake failed: %v)", expired, rerr)
			}
		}
		return c.writeLocked(ctx, msg)
	}
	finish, err := c.writeLocked(ctx, msg)
	if !errors.Is(err, ErrSessionExpired) {
		return finish, err
	}
	if rerr := c.rehandshakeLocked(ctx); rerr != nil {
		// Report the original expiry as the cause — that's what the caller
		// needs to understand — with the recovery failure as context.
		return nil, fmt.Errorf("%w (recovery handshake failed: %v)", err, rerr)
	}
	return c.writeLocked(ctx, msg)
}

// writeLocked sends msg, splitting off the read of a streamed response when
// the transport supports it. Caller holds the send slot.
func (c *Client) writeLocked(ctx context.Context, msg *Message) (func() error, error) {
	if s, ok := c.transport.(streamStarter); ok {
		return s.StartSend(ctx, msg)
	}
	return nil, c.transport.Send(ctx, msg)
}

// rehandshakeLocked re-runs initialize + initialized on a transport whose
// session the server has forgotten. Caller must hold the send slot; this sends on
// the transport without taking it. Waiting for the initialize response is
// fine under the lock: the receive loop is a separate goroutine and delivers
// it through the pending map, which the send slot doesn't guard.
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
// Progress reports go to the call that asked for them; other notifications
// are handed to the registered handler, if any; server-initiated
// requests are still ignored (nothing here implements a server-callable
// surface, and answering with an error would be noise).
func (c *Client) receiveLoop() {
	defer close(c.rxDone)
	ctx := context.Background()
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			// Nothing will demux responses after this, so record why and let
			// Call fail fast instead of waiting out a full timeout per attempt.
			c.markDead(err)
			c.abortPending(err)
			return
		}
		if msg.IsResponse() {
			c.deliverResponse(msg)
			continue
		}
		if msg.IsNotification() && msg.Method == MethodProgress {
			c.deliverProgress(msg)
			continue
		}
		if msg.IsNotification() {
			c.notifyMu.Lock()
			fn := c.onNotification
			c.notifyMu.Unlock()
			if fn != nil {
				// The handler is caller-supplied and runs detached, with
				// nothing above it to recover — a server that fires a
				// notification mid-tool-call could otherwise take the whole
				// process down.
				go func() {
					defer func() { _ = panics.Error(recover(), "mcp notification handler", "method", msg.Method) }()
					fn(msg.Method)
				}()
			}
		}
		// Server-initiated request: ignored — see the doc comment above.
	}
}

// deliverProgress hands a progress report to the call that asked for it.
// Reports for unknown tokens — a call that already returned, or a server
// inventing tokens — are dropped, as is any report that would block: the
// receive loop delivers responses too, and progress is only advisory.
func (c *Client) deliverProgress(m *Message) {
	var p ProgressParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return
	}
	c.progressMu.Lock()
	ch, ok := c.progress[progressKey(p.ProgressToken)]
	c.progressMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- p:
	default:
	}
}

// progressKey decodes a token as it came back on the wire. The spec allows an
// integer too, but ours are always "progress-N" strings, so anything that
// isn't a string can't be one of them and maps to no key.
func progressKey(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
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

// markDead records that the receive loop has stopped, so no response will ever
// be demuxed again. Kept separate from closed: Close's CompareAndSwap on that
// flag is what gates tearing the transport down, so reusing it here would make
// a later Close skip transport.Close and leak the connection.
//
// The distinction matters most on stdio, where this is reachable without
// anything being closed: one malformed line desynchronises the JSON decoder, or
// a server closes stdout while staying alive. Before this, the client stayed
// "connected" — writes to stdin still succeeded, so /mcp and the panel kept
// reporting it live while every call sat out its full deadline waiting for a
// reply nobody was left to deliver.
func (c *Client) markDead(err error) {
	if err == nil {
		err = io.EOF
	}
	c.deadMu.Lock()
	if c.deadErr == nil {
		c.deadErr = err
	}
	c.deadMu.Unlock()
}

// dead reports the receive loop's exit error, or nil while it's running.
func (c *Client) dead() error {
	c.deadMu.Lock()
	defer c.deadMu.Unlock()
	return c.deadErr
}

// Live reports whether this client can still carry traffic: not closed, and its
// receive loop still running. Callers that report connection status (the /mcp
// listing, the panel) should prefer it over assuming a registered connection is
// usable.
func (c *Client) Live() bool {
	return !c.closed.Load() && c.dead() == nil
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

// errProgressIdle is the cancellation cause when a CallToolProgress call
// goes a full idle window with neither a response nor a progress report.
var errProgressIdle = errors.New("no response or progress report")

// errProgressCap is the cause when a call that kept reporting progress still
// hit maxProgressCallTimeout.
var errProgressCap = errors.New("still running at the overall limit")

// CallToolProgress is CallTool for a caller that wants the server's progress
// reports: it sends a progressToken and passes each notifications/progress
// for it to onProgress, in arrival order, on the calling goroutine. A nil
// onProgress is plain CallTool.
//
// Reports also keep the call alive. Without a caller deadline, the call fails
// after defaultCallTimeout of silence rather than defaultCallTimeout in total,
// up to maxProgressCallTimeout overall — what lets a long-running tool that
// says it is still working outlive the usual one-minute bound. A caller that
// set its own deadline keeps it.
func (c *Client) CallToolProgress(ctx context.Context, name string, args map[string]any, onProgress func(ProgressParams)) (*CallToolResult, error) {
	if onProgress == nil {
		return c.CallTool(ctx, name, args)
	}
	// Drawn from the request-id counter, so no two in-flight calls share one.
	token := "progress-" + strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan ProgressParams, progressBuffer)
	c.progressMu.Lock()
	c.progress[token] = ch
	c.progressMu.Unlock()
	defer func() {
		c.progressMu.Lock()
		delete(c.progress, token)
		c.progressMu.Unlock()
	}()

	idle := func() {}
	if _, ok := ctx.Deadline(); !ok {
		var cancelMax context.CancelFunc
		ctx, cancelMax = context.WithTimeoutCause(ctx, maxProgressCallTimeout, errProgressCap)
		defer cancelMax()
		var cancel context.CancelCauseFunc
		ctx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
		timer := time.AfterFunc(defaultCallTimeout, func() { cancel(errProgressIdle) })
		defer timer.Stop()
		idle = func() { timer.Reset(defaultCallTimeout) }
	}

	type outcome struct {
		res *CallToolResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		var r CallToolResult
		err := c.Call(ctx, MethodToolsCall, CallToolParams{
			Name: name, Arguments: args, Meta: &RequestMeta{ProgressToken: token},
		}, &r)
		done <- outcome{&r, err}
	}()

	for {
		select {
		case p := <-ch:
			idle()
			onProgress(p)
		case o := <-done:
			// Reports that arrived just ahead of the response are still
			// queued; hand them over before the result so none are lost.
		drain:
			for {
				select {
				case p := <-ch:
					onProgress(p)
				default:
					break drain
				}
			}
			if o.err != nil {
				if errors.Is(context.Cause(ctx), errProgressIdle) {
					return nil, fmt.Errorf("mcp: tools/call %s: %w within %s", name, errProgressIdle, defaultCallTimeout)
				}
				if errors.Is(context.Cause(ctx), errProgressCap) {
					return nil, fmt.Errorf("mcp: tools/call %s: %w of %s", name, errProgressCap, maxProgressCallTimeout)
				}
				return nil, o.err
			}
			return o.res, nil
		}
	}
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
