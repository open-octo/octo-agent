package tunnel

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// bridge demultiplexes one device's shim frames onto the local server. It
// replays http-req frames as loopback HTTP calls and opens a loopback /ws per
// ws-open stream; responses and server-pushed messages are framed again,
// encrypted with the device's Noise session, and sent back through the relay.
//
// This is the host counterpart of the phone's local shim (mobile/src/shim.ts):
// the phone multiplexes the webview's /api + /ws into shim frames, and the
// bridge fans them back out onto the real loopback server.
type bridge struct {
	t        *Tunnel
	ctx      context.Context
	deviceID string
	sess     *session

	// sendMu serializes Noise encryption with the relay write. A device's
	// CipherState nonce increments per message, so the order frames are
	// encrypted must equal the order they hit the wire — many goroutines
	// (one per in-flight HTTP call, one per ws stream) send concurrently.
	sendMu sync.Mutex

	mu      sync.Mutex
	streams map[string]*wsStream
	closed  bool
}

type wsStream struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func newBridge(ctx context.Context, t *Tunnel, deviceID string, sess *session) *bridge {
	return &bridge{t: t, ctx: ctx, deviceID: deviceID, sess: sess, streams: map[string]*wsStream{}}
}

// handle dispatches one inbound shim frame. HTTP calls run on their own
// goroutine so a slow request doesn't stall the device's frame stream.
func (b *bridge) handle(f shimFrame) {
	switch f.Kind {
	case shimHTTPReq:
		go b.doHTTP(f)
	case shimWSOpen:
		b.openWS(f)
	case shimWSMessage:
		b.wsWrite(f.ID, f.Data)
	case shimWSClose:
		b.closeStream(f.ID)
	default:
		// http-resp / ws-msg only travel host→phone; nothing to do inbound.
	}
}

// send frames, encrypts, and writes to the relay under sendMu.
func (b *bridge) send(f shimFrame) {
	data, err := f.encode()
	if err != nil {
		b.t.logf("[tunnel] device=%s encode: %v", b.deviceID, err)
		return
	}
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	ct, err := b.sess.encrypt(data)
	if err != nil {
		b.t.logf("[tunnel] device=%s encrypt: %v", b.deviceID, err)
		return
	}
	if err := b.t.writeRelay(frame{Type: frameData, Device: b.deviceID, Payload: ct}); err != nil {
		b.t.logf("[tunnel] device=%s relay write: %v", b.deviceID, err)
	}
}

func (b *bridge) doHTTP(f shimFrame) {
	var body io.Reader
	if f.Body != nil {
		body = strings.NewReader(*f.Body)
	}
	req, err := http.NewRequestWithContext(b.ctx, f.Method, b.t.httpBase+f.Path, body)
	if err != nil {
		b.send(httpError(f.ID, err))
		return
	}
	for k, v := range f.Headers {
		if skipRequestHeader(k) {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := b.t.httpClient.Do(req)
	if err != nil {
		b.send(httpError(f.ID, err))
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	b.send(shimFrame{Kind: shimHTTPResp, ID: f.ID, Status: resp.StatusCode, Headers: headers, Body: strPtr(string(respBody))})
}

func httpError(id string, err error) shimFrame {
	return shimFrame{Kind: shimHTTPResp, ID: id, Status: http.StatusBadGateway, Headers: map[string]string{}, Body: strPtr(err.Error())}
}

// skipRequestHeader drops headers the loopback HTTP client must set itself; the
// phone's copy of them would be wrong or rejected.
func skipRequestHeader(k string) bool {
	switch strings.ToLower(k) {
	case "host", "connection", "content-length", "accept-encoding", "transfer-encoding":
		return true
	}
	return false
}

func (b *bridge) openWS(f shimFrame) {
	conn, _, err := websocket.DefaultDialer.DialContext(b.ctx, b.t.wsBase+f.Path, nil)
	if err != nil {
		b.send(shimFrame{Kind: shimWSError, ID: f.ID, Message: err.Error()})
		return
	}
	st := &wsStream{conn: conn}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		conn.Close()
		return
	}
	b.streams[f.ID] = st
	b.mu.Unlock()
	go b.wsPump(f.ID, st)
}

// wsPump forwards server-pushed messages on one loopback /ws to the phone, and
// reports the close when the loopback side ends.
func (b *bridge) wsPump(id string, st *wsStream) {
	for {
		_, msg, err := st.conn.ReadMessage()
		if err != nil {
			b.send(shimFrame{Kind: shimWSClose, ID: id})
			b.closeStream(id)
			return
		}
		b.send(shimFrame{Kind: shimWSMessage, ID: id, Data: string(msg)})
	}
}

func (b *bridge) wsWrite(id, data string) {
	b.mu.Lock()
	st := b.streams[id]
	b.mu.Unlock()
	if st == nil {
		return
	}
	st.writeMu.Lock()
	defer st.writeMu.Unlock()
	_ = st.conn.WriteMessage(websocket.TextMessage, []byte(data))
}

// closeStream closes and forgets a ws stream. Idempotent: a stream can be closed
// by the phone (ws-close) and by its own pump (loopback ended) in either order.
func (b *bridge) closeStream(id string) {
	b.mu.Lock()
	st := b.streams[id]
	delete(b.streams, id)
	b.mu.Unlock()
	if st != nil {
		st.conn.Close()
	}
}

// close tears down every ws stream; called when the device or relay connection
// goes away.
func (b *bridge) close() {
	b.mu.Lock()
	b.closed = true
	streams := b.streams
	b.streams = map[string]*wsStream{}
	b.mu.Unlock()
	for _, st := range streams {
		st.conn.Close()
	}
}
