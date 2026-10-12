// Package feishu implements the Feishu (飞书) adapter.
//
// Feishu uses a WebSocket connection for receiving bot messages and a REST
// API for sending.
//
// Flow:
//  1. OAuth → tenant_access_token
//  2. POST /callback/ws/endpoint (AppID + AppSecret) → WebSocket URL
//  3. Connect WebSocket, exchange protobuf frames (see frame.go), listen for
//     im.message.receive_v1 events
//  4. Send replies via POST /open-apis/im/v1/messages
package feishu

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/uploads"
)

const (
	platformName   = "feishu"
	defaultDomain  = "https://open.feishu.cn"
	reconnectDelay = 5 * time.Second

	// defaultPingInterval applies only if the endpoint response carries no
	// ClientConfig.PingInterval.
	defaultPingInterval = 90 * time.Second
	pongGracePeriod     = 5 * time.Second
	// partsTTL bounds how long a split event waits for its remaining parts;
	// maxParts rejects absurd part counts from a malformed frame.
	partsTTL = 5 * time.Second
	maxParts = 1024

	// maxMessageBytes: Feishu's documented per-message content limits are far
	// larger than any normal agentic reply (custom-bot webhook guidance is
	// "JSON under ~30KB"), so this exists purely as a conservative backstop
	// against silently failing on a truly pathological reply (#1116), not a
	// limit anyone should expect to hit in practice.
	maxMessageBytes = 20000

	// unsupportedMediaNotice is sent in place of silence (#1123) when a
	// message is a kind we don't decode (voice, post/rich-text video, …).
	// Transcription/decoding can come later; going silent is the bug being
	// fixed here.
	unsupportedMediaNotice = "This message type isn't supported yet — please send text."
)

func init() {
	channel.Register(platformName, New)
}

// Config keys.
const (
	cfgAppID        = "app_id"
	cfgAppSecret    = "app_secret"
	cfgDomain       = "domain"
	cfgAllowedUsers = "allowed_users"
)

// feishuImageRe matches Markdown image syntax so it can be converted to a
// clickable link (#1119) — Feishu's markdown renderer (both the "post" md
// tag and the card "markdown" element) has no inline-image tag, so
// "![alt](url)" used to be stripped entirely, losing the URL along with it.
var feishuImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]*)\)`)

// Adapter implements channel.Adapter for Feishu.
type Adapter struct {
	appID        string
	appSecret    string
	domain       string
	allowedUsers map[string]bool

	http        *http.Client
	accessToken string
	tokenExpiry time.Time
	tokenMu     sync.Mutex

	botOpenID string
	running   bool
	cancel    context.CancelFunc
}

// New creates a Feishu adapter from platform config.
func New(cfg channel.PlatformConfig) (channel.Adapter, error) {
	appID, _ := cfg[cfgAppID].(string)
	secret, _ := cfg[cfgAppSecret].(string)
	if appID == "" || secret == "" {
		return nil, fmt.Errorf("feishu: app_id and app_secret are required")
	}

	domain, _ := cfg[cfgDomain].(string)
	if domain == "" {
		domain = defaultDomain
	}

	allowed := make(map[string]bool)
	if raw, ok := cfg[cfgAllowedUsers].(string); ok {
		for _, u := range strings.Split(raw, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				allowed[u] = true
			}
		}
	}

	return &Adapter{
		appID:        appID,
		appSecret:    secret,
		domain:       domain,
		allowedUsers: allowed,
		http:         &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Platform returns "feishu".
func (a *Adapter) Platform() string { return platformName }

// Start connects to Feishu WebSocket and listens for messages.
// It reconnects on transient errors; only ctx.Done() stops it.
func (a *Adapter) Start(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	defer func() { a.running = false }()

	// Fetch bot identity once upfront; if it fails we will retry lazily in handleEvent.
	a.botOpenID = a.getBotOpenID()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		err := a.connectOnce(ctx, onMessage)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("[feishu] connection lost: %v (reconnecting in %s)", err, reconnectDelay)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(reconnectDelay):
			}
		}
	}
}

// connectOnce fetches a WS endpoint, dials, and reads until error or ctx done.
func (a *Adapter) connectOnce(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	if err := a.refreshToken(); err != nil {
		return fmt.Errorf("token: %w", err)
	}

	ep, err := a.getWSEndpoint(ctx)
	if err != nil {
		return fmt.Errorf("ws endpoint: %w", err)
	}

	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, ep.URL, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", handshakeError(resp, err))
	}
	defer conn.Close()

	ws := &wsConn{conn: conn}
	ws.setPingInterval(ep.ClientConfig.PingInterval)
	// Every ping frame carries the service id, which the endpoint only hands
	// out inside the URL's query string.
	if u, err := url.Parse(ep.URL); err == nil {
		if id, err := strconv.ParseInt(u.Query().Get("service_id"), 10, 32); err == nil {
			ws.serviceID = int32(id)
		} else {
			// Pings may then go unanswered and the connection time out;
			// leave a trace of why.
			log.Printf("[feishu] endpoint URL has no usable service_id; pinging with 0")
		}
	}

	log.Printf("[feishu] connected to WebSocket")

	// FEISHU-005: proactive ping goroutine. The server answers each ping
	// with a pong that may carry an updated ClientConfig. It also closes the
	// socket on cancellation: otherwise readEvents stays blocked until the
	// read deadline, and a stopped adapter keeps acking events the server
	// will then never redeliver.
	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go func() {
		for {
			if err := ws.ping(); err != nil {
				log.Printf("[feishu] ping error: %v", err)
				conn.Close()
				return
			}
			select {
			case <-pingCtx.Done():
				conn.Close()
				return
			case <-time.After(ws.pingInterval()):
			}
		}
	}()

	return a.readEvents(ctx, ws, onMessage)
}

// handshakeError surfaces the gateway's Handshake-* headers (auth failure,
// connection limit, …) when the WebSocket upgrade is refused.
func handshakeError(resp *http.Response, err error) error {
	if resp == nil {
		return err
	}
	status := resp.Header.Get("Handshake-Status")
	msg := resp.Header.Get("Handshake-Msg")
	if status == "" && msg == "" {
		return fmt.Errorf("%w (HTTP %d)", err, resp.StatusCode)
	}
	return fmt.Errorf("%w (HTTP %d, handshake status %s: %s)", err, resp.StatusCode, status, msg)
}

// wsConn wraps one long connection. Both the ping goroutine and the read
// loop's acks write to it, and gorilla allows only one concurrent writer.
type wsConn struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	serviceID int32
	pingNanos atomic.Int64
}

func (w *wsConn) setPingInterval(seconds int) {
	if seconds > 0 {
		w.pingNanos.Store(int64(time.Duration(seconds) * time.Second))
	}
}

func (w *wsConn) pingInterval() time.Duration {
	if d := time.Duration(w.pingNanos.Load()); d > 0 {
		return d
	}
	return defaultPingInterval
}

// readTimeout matches the SDK: two missed pongs plus a grace period.
func (w *wsConn) readTimeout() time.Duration {
	return 2*w.pingInterval() + pongGracePeriod
}

func (w *wsConn) writeFrame(f *frame) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	w.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return w.conn.WriteMessage(websocket.BinaryMessage, f.marshal())
}

func (w *wsConn) pingFrame() *frame {
	return &frame{
		Method:  frameMethodControl,
		Service: w.serviceID,
		Headers: []frameHeader{{Key: hdrType, Value: "ping"}},
	}
}

func (w *wsConn) ping() error { return w.writeFrame(w.pingFrame()) }

// Stop gracefully shuts down.
func (a *Adapter) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	return nil
}

// SendText sends a text (or, for code/tables, interactive card) message,
// splitting content that exceeds maxMessageBytes into consecutive messages
// (#1116). Returns the message ID of the last chunk. replyTo is only
// attached to the first chunk — attaching it to every chunk would render as
// several bubbles all "replying to" the same original message instead of a
// natural sequential thread.
func (a *Adapter) SendText(chatID, text, replyTo string) channel.SendResult {
	chunks := channel.SplitForSend(text, maxMessageBytes)
	if len(chunks) == 0 {
		return channel.SendResult{OK: true}
	}

	var lastID string
	for i, chunk := range chunks {
		rt := ""
		if i == 0 {
			rt = replyTo
		}
		msgID, err := a.sendMessage(chatID, chunk, rt)
		if err != nil {
			return channel.SendResult{OK: false, Error: err.Error()}
		}
		lastID = msgID
	}
	return channel.SendResult{OK: true, MessageID: lastID}
}

// SendFile uploads a local file to Feishu and sends it to the chat.
// Images go through the image-upload endpoint and are sent as "image"
// messages; everything else is uploaded as a generic file and sent as a
// "file" message.
func (a *Adapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	if _, err := os.Stat(path); err != nil {
		return channel.SendResult{OK: false, Error: "file not found: " + path}
	}

	var msgType, content string
	var err error
	if isImagePath(path) {
		var imageKey string
		if imageKey, err = a.uploadImage(path); err == nil {
			b, _ := json.Marshal(map[string]string{"image_key": imageKey})
			msgType, content = "image", string(b)
		}
	} else {
		var fileKey string
		if fileKey, err = a.uploadFile(path, name); err == nil {
			b, _ := json.Marshal(map[string]string{"file_key": fileKey})
			msgType, content = "file", string(b)
		}
	}
	if err != nil {
		return channel.SendResult{OK: false, Error: "upload failed: " + err.Error()}
	}

	msgID, err := a.sendRaw(chatID, msgType, content, replyTo)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	return channel.SendResult{OK: true, MessageID: msgID}
}

// UpdateMessage edits an existing message.
func (a *Adapter) UpdateMessage(chatID, messageID, text string) bool {
	return a.updateMessage(messageID, text)
}

// SupportsMessageUpdates returns true.
func (a *Adapter) SupportsMessageUpdates() bool { return true }

// SendTyping — Feishu does not have a typing indicator API.
func (a *Adapter) SendTyping(chatID, contextToken string) error { return nil }

// StopTyping — no-op for Feishu.
func (a *Adapter) StopTyping(chatID, contextToken string) error { return nil }

// Flush — Feishu has no outgoing buffer.
func (a *Adapter) Flush(chatID string) {}

func (a *Adapter) SupportsButtons() bool { return true }

// SendButtons sends an interactive card with button elements. Uses the
// interactive card schema (same as SendText for code/table content) but
// appends button elements to the card body.
func (a *Adapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	// Build button elements.
	var btnElements []any
	for _, b := range buttons {
		btnElements = append(btnElements, map[string]any{
			"tag":  "button",
			"text": map[string]any{"tag": "plain_text", "content": b.Label},
			"type": "primary",
			"value": map[string]string{
				"button_id": b.ID,
			},
		})
	}

	// Build the card with text content + button group.
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "content": text},
				map[string]any{
					"tag":     "action",
					"actions": btnElements,
				},
			},
		},
	}
	content, _ := json.Marshal(card)

	msgID, err := a.sendRaw(chatID, "interactive", string(content), replyTo)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	return channel.SendResult{OK: true, MessageID: msgID}
}

// ValidateConfig checks required fields.
func (a *Adapter) ValidateConfig(cfg channel.PlatformConfig) []string {
	var errs []string
	if id, _ := cfg[cfgAppID].(string); id == "" {
		errs = append(errs, "app_id is required")
	}
	if sec, _ := cfg[cfgAppSecret].(string); sec == "" {
		errs = append(errs, "app_secret is required")
	}
	return errs
}

// ─── OAuth ─────────────────────────────────────────────────────────────────

func (a *Adapter) refreshToken() error {
	body := map[string]string{"app_id": a.appID, "app_secret": a.appSecret}
	b, _ := json.Marshal(body)

	resp, err := a.http.Post(
		a.domain+"/open-apis/auth/v3/tenant_access_token/internal",
		"application/json",
		bytes.NewReader(b),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var r struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if r.Code != 0 {
		return fmt.Errorf("oauth error %d: %s", r.Code, r.Msg)
	}

	a.tokenMu.Lock()
	a.accessToken = r.TenantAccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(r.Expire-60) * time.Second)
	a.tokenMu.Unlock()

	log.Printf("[feishu] token obtained, expires in %ds", r.Expire)
	return nil
}

// getToken returns a valid access token, refreshing synchronously if expired.
// FEISHU-012: refresh is synchronous under the lock, no background goroutine.
func (a *Adapter) getToken() string {
	a.tokenMu.Lock()
	tok := a.accessToken
	expired := time.Now().After(a.tokenExpiry)
	a.tokenMu.Unlock()

	if tok == "" || expired {
		_ = a.refreshToken()
		a.tokenMu.Lock()
		tok = a.accessToken
		a.tokenMu.Unlock()
	}
	return tok
}

// ─── WebSocket ─────────────────────────────────────────────────────────────

// wsClientConfig is the server-issued connection tuning. Only PingInterval
// is honored; reconnects keep the fixed reconnectDelay.
type wsClientConfig struct {
	PingInterval int `json:"PingInterval"` // seconds
}

type wsEndpoint struct {
	URL          string         `json:"URL"`
	ClientConfig wsClientConfig `json:"ClientConfig"`
}

// getWSEndpoint asks for a long-connection URL. Unlike the OpenAPI routes,
// this one authenticates with the app credentials in the body rather than a
// bearer token.
func (a *Adapter) getWSEndpoint(ctx context.Context) (wsEndpoint, error) {
	b, _ := json.Marshal(map[string]string{"AppID": a.appID, "AppSecret": a.appSecret})
	req, err := http.NewRequestWithContext(ctx, "POST", a.domain+"/callback/ws/endpoint", bytes.NewReader(b))
	if err != nil {
		return wsEndpoint{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return wsEndpoint{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return wsEndpoint{}, err
	}
	var r struct {
		Code int        `json:"code"`
		Msg  string     `json:"msg"`
		Data wsEndpoint `json:"data"`
	}
	// Check the status before decoding: a plain-text body like
	// "404 page not found" would otherwise surface as a baffling
	// "cannot unmarshal number" error (#2611).
	if resp.StatusCode != http.StatusOK {
		if json.Unmarshal(body, &r) == nil && r.Msg != "" {
			return wsEndpoint{}, fmt.Errorf("HTTP %d: code %d: %s", resp.StatusCode, r.Code, r.Msg)
		}
		// Truncated: this lands in a log line repeated every reconnect.
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return wsEndpoint{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return wsEndpoint{}, err
	}
	if r.Code != 0 {
		return wsEndpoint{}, fmt.Errorf("code %d: %s", r.Code, r.Msg)
	}
	if r.Data.URL == "" {
		return wsEndpoint{}, fmt.Errorf("response has no URL")
	}
	return r.Data, nil
}

func (a *Adapter) readEvents(ctx context.Context, ws *wsConn, onMessage func(channel.InboundEvent)) error {
	pending := make(map[string]*pendingParts)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// FEISHU-004: set read deadline before every ReadMessage call.
		ws.conn.SetReadDeadline(time.Now().Add(ws.readTimeout()))

		msgType, raw, err := ws.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if msgType != websocket.BinaryMessage {
			continue
		}

		f, err := unmarshalFrame(raw)
		if err != nil {
			log.Printf("[feishu] decode frame: %v", err)
			continue
		}

		switch f.Method {
		case frameMethodControl:
			if f.header(hdrType) == "pong" && len(f.Payload) > 0 {
				var cfg wsClientConfig
				if json.Unmarshal(f.Payload, &cfg) == nil {
					ws.setPingInterval(cfg.PingInterval)
				}
			}
		case frameMethodData:
			// The SDK only handles (and acks) "event" frames; "card" frames
			// are the legacy card-callback channel.
			if f.header(hdrType) != "event" {
				continue
			}
			payload := collectParts(pending, f)
			if payload == nil {
				continue
			}
			// A stopped adapter must not ack: the event would be lost
			// instead of redelivered to the live connection.
			if ctx.Err() != nil {
				return nil
			}
			// Ack before dispatching: handleEvent can block on attachment
			// downloads, and a late ack invites a redelivery.
			if err := ws.writeFrame(ackFrame(f)); err != nil {
				return fmt.Errorf("ack: %w", err)
			}
			a.handleEvent(payload, onMessage)
		}
	}
}

// pendingParts holds the chunks of an event the server split across frames.
type pendingParts struct {
	parts   [][]byte
	started time.Time
}

// collectParts returns the complete payload for f, or nil while parts of a
// split event are still missing.
func collectParts(pending map[string]*pendingParts, f *frame) []byte {
	sum, _ := strconv.Atoi(f.header(hdrSum))
	if sum <= 1 {
		return f.Payload
	}
	seq, err := strconv.Atoi(f.header(hdrSeq))
	if err != nil || seq < 0 || seq >= sum || sum > maxParts {
		return nil
	}

	now := time.Now()
	for id, p := range pending {
		if now.Sub(p.started) > partsTTL {
			delete(pending, id)
		}
	}

	id := f.header(hdrMessageID)
	p := pending[id]
	if p == nil || len(p.parts) != sum {
		p = &pendingParts{parts: make([][]byte, sum), started: now}
		pending[id] = p
	}
	p.parts[seq] = f.Payload
	var full []byte
	for _, part := range p.parts {
		if len(part) == 0 {
			return nil
		}
		full = append(full, part...)
	}
	delete(pending, id)
	return full
}

// ackFrame echoes f back with a {"code":200} payload, which is how the SDK
// acknowledges a handled event.
func ackFrame(f *frame) *frame {
	ack := *f
	ack.Headers = append(append([]frameHeader(nil), f.Headers...), frameHeader{Key: hdrBizRt, Value: "0"})
	ack.Payload = []byte(`{"code":200}`)
	return &ack
}

// ─── Bot info ──────────────────────────────────────────────────────────────

type botInfo struct {
	OpenID string `json:"open_id"`
}

// getBotOpenID resolves the bot's own open_id via GET /open-apis/bot/v3/info.
// Unlike most Feishu endpoints, this legacy v3 API returns `bot` at the top
// level of the response rather than under `data`. The official larksuite SDKs
// read the top level and fall back to `data.bot`, so we accept both shapes.
// Every failure is logged: an empty result silently disables all group chats
// because handleEvent fails closed without it (#2346).
func (a *Adapter) getBotOpenID() string {
	req, _ := http.NewRequest("GET", a.domain+"/open-apis/bot/v3/info", nil)
	req.Header.Set("Authorization", "Bearer "+a.getToken())
	resp, err := a.http.Do(req)
	if err != nil {
		log.Printf("[feishu] bot info: %v", err)
		return ""
	}
	defer resp.Body.Close()

	var r struct {
		Code int     `json:"code"`
		Msg  string  `json:"msg"`
		Bot  botInfo `json:"bot"`
		Data struct {
			Bot botInfo `json:"bot"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		// Include the status so an HTML error page from a proxy is
		// distinguishable from a malformed Feishu reply.
		log.Printf("[feishu] bot info: decode response (HTTP %d): %v", resp.StatusCode, err)
		return ""
	}
	if r.Code != 0 {
		log.Printf("[feishu] bot info: code %d: %s", r.Code, r.Msg)
		return ""
	}
	id := r.Bot.OpenID
	if id == "" {
		id = r.Data.Bot.OpenID
	}
	if id == "" {
		log.Printf("[feishu] bot info: response carried no open_id")
		return ""
	}
	log.Printf("[feishu] bot open_id: %s", id)
	return id
}

// ─── Message handling ──────────────────────────────────────────────────────

type fsEvent struct {
	Schema string `json:"schema"`
	Header struct {
		EventType string `json:"event_type"`
	} `json:"header"`
	Event struct {
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
			// FEISHU-007: mentions list from the message object.
			Mentions []struct {
				ID struct {
					OpenID string `json:"open_id"`
				} `json:"id"`
			} `json:"mentions"`
		} `json:"message"`
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
		} `json:"sender"`
	} `json:"event"`
}

// FEISHU-006: extended content structs for image and file messages.
type msgContent struct {
	Text     string `json:"text"`
	ImageKey string `json:"image_key"`
	FileKey  string `json:"file_key"`
	FileName string `json:"file_name"`
}

// feishuDocURLRe matches Feishu document URLs (docx, docs, wiki).
var feishuDocURLRe = regexp.MustCompile(`https://[^/]+\.feishu\.cn/(docx|docs|wiki)/([A-Za-z0-9]+)`)

// fsEventHeader is the common header parsed from every Feishu event to
// determine its type before dispatching to the specific handler.
type fsEventHeader struct {
	Header struct {
		EventType string `json:"event_type"`
	} `json:"header"`
}

func (a *Adapter) handleEvent(raw json.RawMessage, onMessage func(channel.InboundEvent)) {
	var hdr fsEventHeader
	if err := json.Unmarshal(raw, &hdr); err != nil {
		log.Printf("[feishu] unmarshal event header: %v", err)
		return
	}

	switch hdr.Header.EventType {
	case "im.message.receive_v1":
		a.handleMessageEvent(raw, onMessage)
	case "card.action.trigger":
		a.handleCardAction(raw, onMessage)
	}
}

func (a *Adapter) handleMessageEvent(raw json.RawMessage, onMessage func(channel.InboundEvent)) {
	var ev fsEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		log.Printf("[feishu] unmarshal message event: %v", err)
		return
	}

	msg := ev.Event.Message
	chatType := "direct"
	if msg.ChatType == "group" {
		chatType = "group"
	}

	senderID := ev.Event.Sender.SenderID.OpenID
	if senderID == "" {
		return
	}

	// Group chat: @-mention check.
	// FEISHU-013: lazy botOpenID fetch on first message if it was unavailable at Start.
	if chatType == "group" {
		if a.botOpenID == "" {
			a.botOpenID = a.getBotOpenID()
		}
		if a.botOpenID == "" {
			// Still unavailable — fail closed to avoid spamming the group.
			log.Printf("[feishu] bot_open_id unavailable; dropping group message to avoid spam")
			return
		}
		// FEISHU-007: check mentions slice instead of raw JSON string search.
		mentioned := false
		for _, m := range ev.Event.Message.Mentions {
			if m.ID.OpenID == a.botOpenID {
				mentioned = true
				break
			}
		}
		if !mentioned {
			return
		}
	}

	if len(a.allowedUsers) > 0 && !a.allowedUsers[senderID] {
		return
	}

	var content msgContent
	json.Unmarshal([]byte(msg.Content), &content)

	var text string
	var files []channel.FileAttachment
	unsupported := false

	switch msg.MessageType {
	case "text":
		text = strings.TrimSpace(content.Text)
		text = stripAtMentions(text)
	case "image":
		// FEISHU-006: download image and attach.
		if content.ImageKey != "" {
			if fa, err := a.downloadResource(msg.MessageID, content.ImageKey, "image", ""); err != nil {
				log.Printf("[feishu] image download failed: %v", err)
			} else {
				files = append(files, fa)
			}
		}
	case "file":
		// FEISHU-006: download file and attach.
		if content.FileKey != "" {
			if fa, err := a.downloadResource(msg.MessageID, content.FileKey, "file", content.FileName); err != nil {
				log.Printf("[feishu] file download failed: %v", err)
			} else {
				files = append(files, fa)
			}
		}
	default:
		// #1123: voice, post (rich text/video), and other kinds we don't
		// decode land here — used to fall straight through to the drop below
		// with no signal to the user at all.
		unsupported = true
	}

	// FEISHU-006: drop only if both text and files are empty.
	if text == "" && len(files) == 0 {
		if unsupported {
			a.SendText(msg.ChatID, unsupportedMediaNotice, "")
		}
		return
	}

	// FEISHU-011: enrich with Feishu doc content.
	if text != "" {
		text = a.enrichDocContent(text)
	}

	// Content-free reception line: message text is never logged (only metadata),
	// so this stays a safe per-message signal in serve.log at the default level.
	slog.Info("channel message received", "platform", platformName, "chat", msg.ChatID, "user", senderID, "chat_type", chatType, "bytes", len(text))
	onMessage(channel.InboundEvent{
		Type:      "message",
		Platform:  platformName,
		ChatID:    msg.ChatID,
		UserID:    senderID,
		Text:      text,
		Files:     files,
		MessageID: msg.MessageID,
		ChatType:  chatType,
		Raw:       ev,
	})
}

// handleCardAction processes a Feishu card button press event. The event
// payload carries the button's value (which includes button_id) and the
// operator's identity. Routes the press as an InboundEvent with ButtonID set.
func (a *Adapter) handleCardAction(raw json.RawMessage, onMessage func(channel.InboundEvent)) {
	var ev struct {
		Event struct {
			Action struct {
				Value map[string]string `json:"value"`
				Tag   string            `json:"tag"`
			} `json:"action"`
			Operator struct {
				OpenID string `json:"open_id"`
			} `json:"operator"`
			Message struct {
				MessageID string `json:"message_id"`
				ChatID    string `json:"chat_id"`
			} `json:"message"`
		} `json:"event"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		log.Printf("[feishu] unmarshal card action event: %v", err)
		return
	}

	if ev.Event.Action.Tag != "button" {
		return
	}

	senderID := ev.Event.Operator.OpenID
	if senderID == "" {
		return
	}
	if len(a.allowedUsers) > 0 && !a.allowedUsers[senderID] {
		return
	}

	chatID := ev.Event.Message.ChatID
	if chatID == "" {
		return
	}

	buttonID := ev.Event.Action.Value["button_id"]
	if buttonID == "" {
		return
	}

	log.Printf("[feishu] card action from user %s: %s", senderID, buttonID)
	onMessage(channel.InboundEvent{
		Type:      "message",
		Platform:  platformName,
		ChatID:    chatID,
		UserID:    senderID,
		MessageID: ev.Event.Message.MessageID,
		ButtonID:  buttonID,
		ChatType:  "direct",
		Raw:       ev,
	})
}

// downloadResource fetches a message attachment from Feishu.
// For images it returns a DataURL; for files it saves to a temp file.
func (a *Adapter) downloadResource(messageID, key, resourceType, fileName string) (channel.FileAttachment, error) {
	url := fmt.Sprintf("%s/open-apis/im/v1/messages/%s/resources/%s?type=%s",
		a.domain, messageID, key, resourceType)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+a.getToken())

	resp, err := a.http.Do(req)
	if err != nil {
		return channel.FileAttachment{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return channel.FileAttachment{}, fmt.Errorf("download %s: HTTP %d", resourceType, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20)) // 50 MB cap
	if err != nil {
		return channel.FileAttachment{}, err
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	// Trim parameters like "; charset=…".
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}

	if resourceType == "image" {
		mime := ct
		if !strings.HasPrefix(mime, "image/") {
			mime = "image/jpeg"
		}
		dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		return channel.FileAttachment{
			Type:     "image",
			MimeType: mime,
			DataURL:  dataURL,
		}, nil
	}

	// File: save to temp.
	dir, _ := uploads.ChannelTempDir("feishu") // "" falls back to the OS temp root on error
	tmpFile, err := os.CreateTemp(dir, "feishu-file-*")
	if err != nil {
		return channel.FileAttachment{}, err
	}
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return channel.FileAttachment{}, err
	}
	tmpFile.Close()

	name := fileName
	if name == "" {
		name = key
	}
	return channel.FileAttachment{
		Type:     "file",
		Name:     name,
		Path:     tmpFile.Name(),
		MimeType: ct,
	}, nil
}

// enrichDocContent fetches inline Feishu doc content and appends it to the text.
// #1123: a fetch failure used to only log server-side — the user pasted a
// doc link, got an answer that silently ignored it, and no hint why. Now
// appends a short note instead so the agent (and, through it, the user)
// knows the doc couldn't be read.
func (a *Adapter) enrichDocContent(text string) string {
	matches := feishuDocURLRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text
	}

	var sections []string
	for _, m := range matches {
		docToken := m[2]
		content, err := a.fetchDocRawContent(docToken)
		if err != nil {
			log.Printf("[feishu] doc fetch failed for %s: %v", docToken, err)
			sections = append(sections, fmt.Sprintf("[Couldn't read the linked doc %s]", m[0]))
			continue
		}
		if content != "" {
			sections = append(sections, fmt.Sprintf("[Doc content from %s]\n%s", m[0], content))
		}
	}

	if len(sections) == 0 {
		return text
	}
	return text + "\n\n" + strings.Join(sections, "\n\n")
}

// fetchDocRawContent calls the Feishu docx raw_content API.
func (a *Adapter) fetchDocRawContent(docToken string) (string, error) {
	url := fmt.Sprintf("%s/open-apis/docx/v1/documents/%s/raw_content", a.domain, docToken)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+a.getToken())

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var r struct {
		Code int `json:"code"`
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.Code != 0 {
		return "", fmt.Errorf("doc api code %d", r.Code)
	}
	return strings.TrimSpace(r.Data.Content), nil
}

func stripAtMentions(text string) string {
	// Remove Feishu @-mention format: @name or <at>...</at>
	for {
		start := strings.Index(text, "<at ")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "</at>")
		if end < 0 {
			break
		}
		text = text[:start] + text[start+end+5:]
	}
	text = strings.TrimSpace(text)
	// Remove leading @name patterns.
	for strings.HasPrefix(text, "@") {
		idx := strings.Index(text, " ")
		if idx < 0 {
			break
		}
		text = strings.TrimSpace(text[idx+1:])
	}
	return text
}

// ─── Send ──────────────────────────────────────────────────────────────────

// buildMsgPayload selects msg_type and content based on text content.
// FEISHU-010: uses "post" (md) for plain text, "interactive" (card) for code/tables.
func buildMsgPayload(text string) (msgType, content string) {
	// Feishu's markdown has no inline-image tag, so a "![alt](url)" reference
	// is rewritten as a clickable "[alt](url)" link instead of being dropped
	// (#1119) — links render fine in both the "post" and card paths below.
	cleaned := feishuImageRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := feishuImageRe.FindStringSubmatch(m)
		alt, url := parts[1], parts[2]
		if url == "" {
			return alt
		}
		if alt == "" {
			alt = "image"
		}
		return "[" + alt + "](" + url + ")"
	})

	// #1119: a naive "line has 2+ pipes" check false-positived on ordinary
	// prose that happens to contain "|", flipping the message into card
	// rendering for text that isn't a table at all. LooksLikeMarkdownTable
	// requires an actual header-separator row.
	hasCodeOrTable := strings.Contains(cleaned, "```") ||
		channel.LooksLikeMarkdownTable(cleaned)

	if hasCodeOrTable {
		// Use interactive card (schema 2.0) with markdown element.
		card := map[string]any{
			"schema": "2.0",
			"config": map[string]any{"wide_screen_mode": true},
			"body": map[string]any{
				"elements": []any{
					map[string]any{"tag": "markdown", "content": cleaned},
				},
			},
		}
		b, _ := json.Marshal(card)
		return "interactive", string(b)
	}

	// Plain post with md tag.
	post := map[string]any{
		"zh_cn": map[string]any{
			"content": []any{
				[]any{map[string]any{"tag": "md", "text": cleaned}},
			},
		},
	}
	b, _ := json.Marshal(post)
	return "post", string(b)
}

// ─── Upload ────────────────────────────────────────────────────────────────

// uploadImage uploads an image and returns its image_key.
func (a *Adapter) uploadImage(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("image_type", "message"); err != nil {
		return "", err
	}
	part, err := mw.CreateFormFile("image", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	mw.Close()

	req, _ := http.NewRequest("POST", a.domain+"/open-apis/im/v1/images", &buf)
	req.Header.Set("Authorization", "Bearer "+a.getToken())
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ImageKey string `json:"image_key"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.Code != 0 {
		return "", fmt.Errorf("upload image %d: %s", r.Code, r.Msg)
	}
	return r.Data.ImageKey, nil
}

// uploadFile uploads a generic file and returns its file_key.
func (a *Adapter) uploadFile(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fileName := name
	if fileName == "" {
		fileName = filepath.Base(path)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("file_type", feishuFileType(fileName)); err != nil {
		return "", err
	}
	if err := mw.WriteField("file_name", fileName); err != nil {
		return "", err
	}
	part, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	mw.Close()

	req, _ := http.NewRequest("POST", a.domain+"/open-apis/im/v1/files", &buf)
	req.Header.Set("Authorization", "Bearer "+a.getToken())
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			FileKey string `json:"file_key"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.Code != 0 {
		return "", fmt.Errorf("upload file %d: %s", r.Code, r.Msg)
	}
	return r.Data.FileKey, nil
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// feishuFileType maps a filename to one of Feishu's accepted file_type values.
// Unknown extensions fall back to "stream".
func feishuFileType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4":
		return "mp4"
	case ".opus":
		return "opus"
	case ".pdf":
		return "pdf"
	case ".doc", ".docx":
		return "doc"
	case ".xls", ".xlsx":
		return "xls"
	case ".ppt", ".pptx":
		return "ppt"
	default:
		return "stream"
	}
}

// sendMessage posts a text message to a chat.
// FEISHU-008: replyTo is passed through as reply_to_message_id when non-empty.
func (a *Adapter) sendMessage(chatID, text, replyTo string) (string, error) {
	msgType, content := buildMsgPayload(text)
	return a.sendRaw(chatID, msgType, content, replyTo)
}

// sendRaw posts a pre-built message (any msg_type) to a chat.
func (a *Adapter) sendRaw(chatID, msgType, content, replyTo string) (string, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   msgType,
		"content":    content,
	}
	if replyTo != "" {
		body["reply_to_message_id"] = replyTo
	}
	b, _ := json.Marshal(body)

	url := a.domain + "/open-apis/im/v1/messages?receive_id_type=chat_id"
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+a.getToken())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	respBytes, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(respBytes, &r); err != nil {
		return "", fmt.Errorf("send: %s", string(respBytes))
	}
	if r.Code != 0 {
		return "", fmt.Errorf("send %d: %s", r.Code, r.Msg)
	}
	return r.Data.MessageID, nil
}

// updateMessage patches an existing message.
// FEISHU-009: includes msg_type in the PATCH body.
func (a *Adapter) updateMessage(messageID, text string) bool {
	msgType, content := buildMsgPayload(text)

	body := map[string]any{
		"msg_type": msgType,
		"content":  content,
	}
	b, _ := json.Marshal(body)

	url := a.domain + "/open-apis/im/v1/messages/" + messageID
	req, _ := http.NewRequest("PATCH", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+a.getToken())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var r struct {
		Code int `json:"code"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	return r.Code == 0
}
