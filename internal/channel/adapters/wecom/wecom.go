// Package wecom implements the WeCom (Enterprise WeChat) intelligent-robot
// adapter.
//
// Protocol: plain JSON frames over a WebSocket long connection to
// wss://openws.work.weixin.qq.com.
//
// Frame format: { cmd, headers: { req_id }, body }
// Commands:
//
//	aibot_subscribe      — auth (client → server)
//	ping                 — heartbeat (client → server)
//	aibot_msg_callback   — inbound message (server → client)
//	aibot_send_msg       — send a message (client → server)
//
// An auth rejection (non-zero errcode acking the subscribe frame) is fatal —
// the credentials are wrong and reconnecting cannot help.
package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/open-octo/octo-agent/internal/channel"
)

const (
	platformName = "wecom"
	defaultWSURL = "wss://openws.work.weixin.qq.com"

	heartbeatInterval = 30 * time.Second
	reconnectDelay    = 5 * time.Second
	// readTimeout catches silent connection drops (NAT timeout, firewall
	// idle-kill) that never deliver a FIN/RST. The server pings every ~30s,
	// so 75s gives two missed pings before we give up.
	readTimeout = 75 * time.Second
	// ackTimeout bounds send_frame_and_wait round-trips.
	ackTimeout = 30 * time.Second

	// maxMessageBytes: WeCom rejects a markdown message whose content exceeds
	// 4096 bytes with errcode 40058 ("markdown.content exceed max length
	// 4096") — a hard, documented, byte-counted (not character-counted) cap.
	// SendText previously sent content as a single unbounded payload (#1116).
	maxMessageBytes = 4096

	// unsupportedMediaNotice is sent in place of silence (#1123) when a
	// message is a msgtype we don't decode (voice, video, …). Transcription/
	// decoding can come later; going silent is the bug being fixed here.
	unsupportedMediaNotice = "This message type isn't supported yet — please send text."
)

func init() {
	channel.Register(platformName, New)
}

// Config keys.
const (
	cfgBotID        = "bot_id"
	cfgSecret       = "secret"
	cfgAllowedUsers = "allowed_users"
	// Group-robot webhook for proactive pushes (either the full URL or just
	// the key from the webhook URL the WeCom admin shows).
	cfgWebhookURL = "webhook_url"
	cfgWebhookKey = "webhook_key"
	// cfgWSURL is a test seam, not user-facing config.
	cfgWSURL = "ws_url"
	// cfgBotNickname is the bot's display name as it appears in a group
	// chat's roster — #1122: WeCom's aibot_msg_callback carries no
	// structured "was the bot mentioned" field for group messages (unlike
	// Telegram/Discord/Feishu entity metadata or DingTalk's atUsers); per
	// WeCom's own docs example ("@RobotA hello robot"), the only signal is
	// the literal "@<nickname>" text in the message. Without this configured
	// there is nothing to match against, so group gating stays off (matches
	// pre-#1122 behavior) rather than silently blocking every group message.
	cfgBotNickname = "bot_nickname"
)

// defaultWebhookBase is the group-robot webhook endpoint webhook_key expands to.
const defaultWebhookBase = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send"

// Adapter implements channel.Adapter for WeCom intelligent robots.
type Adapter struct {
	botID        string
	secret       string
	wsURL        string
	webhookURL   string
	allowedUsers map[string]bool
	// botNickname is the bot's group-chat display name, used to detect a
	// literal "@<nickname>" mention in group message text (see cfgBotNickname
	// for why). Empty means group gating stays off.
	botNickname string

	conn   *websocket.Conn
	connMu sync.Mutex // guards conn pointer and writes

	pendingAcks map[string]chan *wsFrame
	ackMu       sync.Mutex

	http   *http.Client
	cancel context.CancelFunc
}

// New creates a WeCom adapter from platform config. The aibot credentials
// (bot_id + secret) drive the interactive WebSocket channel; a group-robot
// webhook (webhook_url / webhook_key) drives proactive pushes when the
// WebSocket isn't connected — e.g. channel.SendOnce from octo serve, which
// runs no inbound adapters. Either is sufficient on its own.
func New(cfg channel.PlatformConfig) (channel.Adapter, error) {
	botID, _ := cfg[cfgBotID].(string)
	secret, _ := cfg[cfgSecret].(string)

	webhookURL, _ := cfg[cfgWebhookURL].(string)
	if webhookURL == "" {
		if key, _ := cfg[cfgWebhookKey].(string); key != "" {
			webhookURL = defaultWebhookBase + "?key=" + key
		}
	}

	if (botID == "" || secret == "") && webhookURL == "" {
		return nil, fmt.Errorf("wecom: bot_id+secret (aibot) or webhook_key/webhook_url (group robot) is required")
	}

	wsURL, _ := cfg[cfgWSURL].(string)
	if wsURL == "" {
		wsURL = defaultWSURL
	}

	allowed := make(map[string]bool)
	if raw, ok := cfg[cfgAllowedUsers].(string); ok {
		for _, u := range strings.Split(raw, ",") {
			if u = strings.TrimSpace(u); u != "" {
				allowed[u] = true
			}
		}
	}

	botNickname, _ := cfg[cfgBotNickname].(string)

	return &Adapter{
		botID:        botID,
		secret:       secret,
		wsURL:        wsURL,
		webhookURL:   webhookURL,
		allowedUsers: allowed,
		botNickname:  botNickname,
		pendingAcks:  make(map[string]chan *wsFrame),
		http:         &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Platform returns "wecom".
func (a *Adapter) Platform() string { return platformName }

// Start connects to the WeCom WebSocket and blocks until ctx is cancelled.
// Transient connection errors reconnect; an auth rejection aborts.
func (a *Adapter) Start(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	if a.botID == "" || a.secret == "" {
		return fmt.Errorf("wecom: bot_id and secret are required for the interactive channel (webhook-only config can still push notifications)")
	}
	ctx, a.cancel = context.WithCancel(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		err := a.connectAndListen(ctx, onMessage)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if _, ok := err.(*authError); ok {
				return fmt.Errorf("wecom: %w", err)
			}
			log.Printf("[wecom] connection lost: %v (reconnecting in %s)", err, reconnectDelay)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

// Stop gracefully shuts down.
func (a *Adapter) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	return nil
}

// SendText sends a markdown message via aibot_send_msg when the WebSocket is
// connected. Without a connection — channel.SendOnce from octo serve, which
// runs no inbound adapters — it falls back to the group-robot webhook, which
// delivers to the webhook's bound group (chatID cannot select a chat there).
//
// Content over maxMessageBytes is split into consecutive messages (#1116):
// WeCom rejects an oversized markdown payload outright (errcode 40058)
// rather than truncating it, so an unsplit long reply was dropped entirely.
//
// WeCom's "markdown" msgtype (the only one this adapter sends) supports only
// a documented subset — headings, bold, links, inline code spans, quotes,
// and 3 built-in font colors (#1119). Notably unsupported: tables, lists,
// and multi-line fenced code blocks (only single-line inline code works) —
// a fenced ```block``` arrives with its fence markers rendered as literal
// text. Pipe tables are flattened to readable lines before sending rather
// than left as raw "| a | b |" rows with no table rendering to give them
// structure; code fences are sent through unchanged since there's no lossless
// plain-text substitute for them worth the added complexity here.
func (a *Adapter) SendText(chatID, text, replyTo string) channel.SendResult {
	text = channel.FlattenPipeTables(text)
	chunks := channel.SplitForSend(text, maxMessageBytes)
	if len(chunks) == 0 {
		return channel.SendResult{OK: true}
	}

	a.connMu.Lock()
	connected := a.conn != nil
	a.connMu.Unlock()

	var res channel.SendResult
	for _, chunk := range chunks {
		if !connected {
			res = a.sendWebhook(chunk)
		} else {
			_, err := a.sendFrameAndWait("aibot_send_msg", map[string]any{
				"chatid":   chatID,
				"msgtype":  "markdown",
				"markdown": map[string]string{"content": chunk},
			})
			if err != nil {
				res = channel.SendResult{OK: false, Error: err.Error()}
			} else {
				res = channel.SendResult{OK: true}
			}
		}
		if !res.OK {
			return res
		}
	}
	return res
}

// sendWebhook posts a markdown message to the configured group-robot webhook.
func (a *Adapter) sendWebhook(text string) channel.SendResult {
	if a.webhookURL == "" {
		return channel.SendResult{OK: false, Error: "not connected and no webhook_key/webhook_url configured for proactive pushes"}
	}
	body, _ := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": text},
	})
	resp, err := a.http.Post(a.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("bad webhook response: %v", err)}
	}
	if r.ErrCode != 0 {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("webhook error %d: %s", r.ErrCode, r.ErrMsg)}
	}
	return channel.SendResult{OK: true}
}

const (
	uploadChunkSize = 512 * 1024 // 512 KB per chunk
	uploadMaxChunks = 100
)

// SendFile uploads a local file via the WeCom three-step chunked-upload
// protocol and sends it as an attachment.
//
// Protocol:
//  1. aibot_upload_media_init  — announce filename/size/chunks/md5 → upload_id
//  2. aibot_upload_media_chunk × N — send base64-encoded 512 KB chunks
//  3. aibot_upload_media_finish     — commit → media_id
//  4. aibot_send_msg with the resulting media_id
func (a *Adapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	a.connMu.Lock()
	connected := a.conn != nil
	a.connMu.Unlock()
	if !connected {
		msg := "wecom: SendFile requires an active WebSocket connection (webhook-only config supports text but not file uploads)"
		if a.webhookURL != "" {
			msg += "; ask the user to message the bot so a WebSocket session is created"
		}
		return channel.SendResult{OK: false, Error: msg}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: read file: %v", err)}
	}
	filename := name
	if filename == "" {
		filename = filepath.Base(path)
	}

	mediaType := detectMediaType(path)
	totalSize := len(data)
	totalChunks := (totalSize + uploadChunkSize - 1) / uploadChunkSize
	if totalChunks == 0 {
		totalChunks = 1
	}
	if totalChunks > uploadMaxChunks {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: file too large (%d chunks, max %d)", totalChunks, uploadMaxChunks)}
	}

	sum := md5.Sum(data)
	md5hex := hex.EncodeToString(sum[:])

	log.Printf("[wecom] SendFile chat=%s filename=%s size=%d chunks=%d md5=%s", chatID, filename, totalSize, totalChunks, md5hex)

	// Step 1: init
	initFrame, err := a.sendFrameAndWait("aibot_upload_media_init", map[string]any{
		"type":         mediaType,
		"filename":     filename,
		"total_size":   totalSize,
		"total_chunks": totalChunks,
		"md5":          md5hex,
	})
	if err != nil {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: upload_media_init: %v", err)}
	}
	var initBody struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(initFrame.Body, &initBody); err != nil || initBody.UploadID == "" {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: upload_media_init: missing upload_id in %s", initFrame.Body)}
	}
	uploadID := initBody.UploadID
	log.Printf("[wecom] upload_id=%s", uploadID)

	// Step 2: chunks
	for i := 0; i < totalChunks; i++ {
		start := i * uploadChunkSize
		end := start + uploadChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]
		b64 := base64.StdEncoding.EncodeToString(chunk)
		log.Printf("[wecom] uploading chunk %d/%d", i+1, totalChunks)
		_, err := a.sendFrameAndWait("aibot_upload_media_chunk", map[string]any{
			"upload_id":   uploadID,
			"chunk_index": i,
			"base64_data": b64,
		})
		if err != nil {
			return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: upload_media_chunk %d: %v", i, err)}
		}
	}

	// Step 3: finish
	log.Printf("[wecom] upload_media_finish upload_id=%s", uploadID)
	finishFrame, err := a.sendFrameAndWait("aibot_upload_media_finish", map[string]any{
		"upload_id": uploadID,
	})
	if err != nil {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: upload_media_finish: %v", err)}
	}
	var finishBody struct {
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(finishFrame.Body, &finishBody); err != nil || finishBody.MediaID == "" {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: upload_media_finish: missing media_id in %s", finishFrame.Body)}
	}
	mediaID := finishBody.MediaID
	log.Printf("[wecom] SendFile media_id=%s", mediaID)

	// Step 4: send message with media_id
	_, err = a.sendFrameAndWait("aibot_send_msg", map[string]any{
		"chatid":  chatID,
		"msgtype": mediaType,
		mediaType: map[string]string{"media_id": mediaID},
	})
	if err != nil {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("wecom: send_msg (file): %v", err)}
	}
	return channel.SendResult{OK: true}
}

// detectMediaType returns the WeCom media type string from a file extension.
func detectMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "image"
	case ".mp4", ".avi", ".mov", ".mkv":
		return "video"
	case ".mp3", ".wav", ".amr", ".m4a":
		return "voice"
	default:
		return "file"
	}
}

// UpdateMessage — WeCom does not support editing sent messages.
func (a *Adapter) UpdateMessage(chatID, messageID, text string) bool { return false }

// SupportsMessageUpdates returns false.
func (a *Adapter) SupportsMessageUpdates() bool { return false }

// SendTyping — the aibot protocol has no typing indicator.
func (a *Adapter) SendTyping(chatID, contextToken string) error { return nil }

// StopTyping — no-op for WeCom.
func (a *Adapter) StopTyping(chatID, contextToken string) error { return nil }

// Flush — WeCom has no outgoing buffer.
func (a *Adapter) Flush(chatID string) {}

func (a *Adapter) SupportsButtons() bool { return false }

func (a *Adapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	// Fall back to plain text — WeCom doesn't support interactive buttons.
	return a.SendText(chatID, text, replyTo)
}

// ValidateConfig checks required fields.
func (a *Adapter) ValidateConfig(cfg channel.PlatformConfig) []string {
	botID, _ := cfg[cfgBotID].(string)
	secret, _ := cfg[cfgSecret].(string)
	whURL, _ := cfg[cfgWebhookURL].(string)
	whKey, _ := cfg[cfgWebhookKey].(string)

	hasAibot := botID != "" || secret != ""
	hasWebhook := whURL != "" || whKey != ""
	if !hasAibot && !hasWebhook {
		return []string{"bot_id+secret (aibot) or webhook_key/webhook_url (group robot) is required"}
	}

	var errs []string
	if hasAibot {
		if botID == "" {
			errs = append(errs, "bot_id is required")
		}
		if secret == "" {
			errs = append(errs, "secret is required")
		}
	}
	return errs
}

// ─── WebSocket protocol ─────────────────────────────────────────────────────

// authError marks a credential rejection — reconnecting cannot help.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

type wsFrame struct {
	Cmd     string `json:"cmd,omitempty"`
	Headers struct {
		ReqID string `json:"req_id"`
	} `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	Errcode *int            `json:"errcode,omitempty"`
	Errmsg  string          `json:"errmsg,omitempty"`
}

// frameErrcode digs errcode out of the frame top level or body.
func (f *wsFrame) frameErrcode() (int, string) {
	if f.Errcode != nil {
		return *f.Errcode, f.Errmsg
	}
	var body struct {
		Errcode *int   `json:"errcode"`
		Errmsg  string `json:"errmsg"`
	}
	if len(f.Body) > 0 && json.Unmarshal(f.Body, &body) == nil && body.Errcode != nil {
		return *body.Errcode, body.Errmsg
	}
	return 0, ""
}

func (a *Adapter) connectAndListen(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	log.Printf("[wecom] connecting to %s", a.wsURL)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, a.wsURL, nil)
	if err != nil {
		return err
	}

	a.connMu.Lock()
	a.conn = conn
	a.connMu.Unlock()
	defer func() {
		a.connMu.Lock()
		a.conn = nil
		a.connMu.Unlock()
		conn.Close()
	}()

	// Close the socket when ctx is cancelled so the blocking read returns.
	connCtx, cancelConn := context.WithCancel(ctx)
	defer cancelConn()
	go func() {
		<-connCtx.Done()
		conn.Close()
	}()

	log.Printf("[wecom] connected, authenticating (bot_id=%s)", a.botID)
	subReqID := reqID("subscribe")
	if err := a.writeFrame("aibot_subscribe", subReqID, map[string]string{
		"bot_id": a.botID, "secret": a.secret,
	}); err != nil {
		return err
	}

	heartbeatStop := make(chan struct{})
	defer close(heartbeatStop)
	go a.heartbeatLoop(conn, heartbeatStop)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		var frame wsFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			log.Printf("[wecom] failed to parse frame: %v", err)
			continue
		}

		// Dispatch acks to waiting senders first.
		if id := frame.Headers.ReqID; id != "" {
			a.ackMu.Lock()
			ch, ok := a.pendingAcks[id]
			a.ackMu.Unlock()
			if ok {
				select {
				case ch <- &frame:
				default:
				}
				continue
			}
		}

		switch frame.Cmd {
		case "aibot_msg_callback":
			go a.handleInbound(frame.Body, onMessage)
		case "aibot_event_callback":
			// Ignored.
		case "":
			// Ack / error frame with no waiting sender.
			code, msg := frame.frameErrcode()
			if code != 0 {
				log.Printf("[wecom] error response: errcode=%d errmsg=%s req_id=%s", code, msg, frame.Headers.ReqID)
				if strings.HasPrefix(frame.Headers.ReqID, "subscribe_") {
					return &authError{fmt.Sprintf("authentication failed (errcode=%d): %s", code, msg)}
				}
			}
		default:
			log.Printf("[wecom] unknown cmd=%s", frame.Cmd)
		}
	}
}

func (a *Adapter) heartbeatLoop(conn *websocket.Conn, stop chan struct{}) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if err := a.writeFrame("ping", reqID("ping"), nil); err != nil {
			log.Printf("[wecom] ping failed: %v, forcing reconnect", err)
			// Close the socket so the blocked read returns and the caller
			// reconnects.
			conn.Close()
			return
		}
	}
}

func (a *Adapter) writeFrame(cmd, id string, body any) error {
	frame := map[string]any{
		"cmd":     cmd,
		"headers": map[string]string{"req_id": id},
	}
	if body != nil {
		frame["body"] = body
	}
	b, _ := json.Marshal(frame)

	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.conn == nil {
		return fmt.Errorf("not connected")
	}
	return a.conn.WriteMessage(websocket.TextMessage, b)
}

// sendFrameAndWait sends a frame and blocks until an ack with the same req_id
// arrives, the timeout fires, or the connection drops.
func (a *Adapter) sendFrameAndWait(cmd string, body any) (*wsFrame, error) {
	id := reqID(strings.TrimPrefix(cmd, "aibot_"))
	ch := make(chan *wsFrame, 1)

	a.ackMu.Lock()
	a.pendingAcks[id] = ch
	a.ackMu.Unlock()
	defer func() {
		a.ackMu.Lock()
		delete(a.pendingAcks, id)
		a.ackMu.Unlock()
	}()

	if err := a.writeFrame(cmd, id, body); err != nil {
		return nil, err
	}

	select {
	case frame := <-ch:
		if code, msg := frame.frameErrcode(); code != 0 {
			if msg == "" {
				msg = "unknown"
			}
			return nil, fmt.Errorf("wecom API error %d: %s (cmd=%s)", code, msg, cmd)
		}
		return frame, nil
	case <-time.After(ackTimeout):
		return nil, fmt.Errorf("timeout waiting for ack (req_id=%s, cmd=%s)", id, cmd)
	}
}

func reqID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// ─── Inbound ────────────────────────────────────────────────────────────────

type wcMedia struct {
	URL    string `json:"url"`
	AESKey string `json:"aeskey"`
}

type wcFile struct {
	URL    string `json:"url"`
	AESKey string `json:"aeskey"`
	Name   string `json:"name"`
}

type wcMessage struct {
	MsgType  string `json:"msgtype"`
	ChatID   string `json:"chatid"`
	ChatType string `json:"chattype"`
	MsgID    string `json:"msgid"`
	From     struct {
		UserID string `json:"userid"`
	} `json:"from"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
	Image *wcMedia `json:"image,omitempty"`
	File  *wcFile  `json:"file,omitempty"`
}

// decryptWCMedia decrypts AES-256-CBC data with the per-resource aeskey.
// Key = base64_decode(aeskey), IV = first 16 bytes of key, PKCS7 unpad.
// On any error, returns the original data (matching Ruby fallback behavior).
func decryptWCMedia(data []byte, aeskeyB64 string) ([]byte, error) {
	// Pad to multiple of 4
	if rem := len(aeskeyB64) % 4; rem != 0 {
		aeskeyB64 += strings.Repeat("=", 4-rem)
	}
	key, err := base64.StdEncoding.DecodeString(aeskeyB64)
	if err != nil {
		return data, nil
	}
	if len(key) < 16 {
		return data, nil
	}
	iv := key[:16]
	block, err := aes.NewCipher(key)
	if err != nil {
		return data, nil
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return data, nil
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	// PKCS7 unpad
	if len(out) == 0 {
		return data, nil
	}
	padLen := int(out[len(out)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(out) {
		return data, nil
	}
	return out[:len(out)-padLen], nil
}

// wcMediaHostSuffixes are the Tencent/WeCom CDN host suffixes that serve
// media for inbound messages. The media URL arrives in the (user-originated)
// callback frame, so it is validated against this allowlist before any fetch
// to prevent the bot from being coerced into requesting arbitrary internal
// URLs (SSRF).
var wcMediaHostSuffixes = []string{
	".qq.com",
	".qpic.cn",
	".weixinbridge.com",
}

// wcMediaURLRe is a strict allowlist pattern for inbound WeCom media URLs.
// It anchors the host to a Tencent/WeCom CDN domain and only permits https.
// The regexp check doubles as a CodeQL request-forgery sanitizer (regexp
// matches are recognised as barrier guards by the go/request-forgery query).
var wcMediaURLRe = regexp.MustCompile(`^https://(?:[a-zA-Z0-9.-]+\.)?(?:qq\.com|qpic\.cn|weixinbridge\.com)(?:[/?#].*)?$`)

// wcMediaHostAllowed reports whether host is a WeCom media host suffix.
func wcMediaHostAllowed(host string) bool {
	for _, suf := range wcMediaHostSuffixes {
		if host == suf[1:] || strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

// allowedWCMediaURL reports whether raw is an https URL on a WeCom media host.
func allowedWCMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	return wcMediaHostAllowed(u.Hostname())
}

// sanitizeWCMediaURL validates a WeCom media URL and returns a reconstructed
// URL whose scheme and host are tightly controlled. The regexp acts as the
// primary barrier guard recognised by CodeQL; the allowlist and url.Parse
// checks provide defence in depth.
func sanitizeWCMediaURL(raw string) (string, error) {
	if !wcMediaURLRe.MatchString(raw) {
		return "", fmt.Errorf("wecom: media URL does not match allowlisted pattern")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("wecom: invalid media URL: %w", err)
	}
	if u.Scheme != "https" || u.User != nil {
		return "", fmt.Errorf("wecom: media URL must be https without credentials")
	}
	host := u.Hostname()
	if !wcMediaHostAllowed(host) {
		return "", fmt.Errorf("wecom: media URL host %q is not allowlisted", host)
	}
	safe := &url.URL{
		Scheme:   "https",
		Host:     host,
		Path:     u.Path,
		RawQuery: u.RawQuery,
	}
	return safe.String(), nil
}

// downloadWCMedia fetches a WeCom media URL and optionally decrypts it.
func downloadWCMedia(mediaURL, aeskey string) ([]byte, error) {
	safeURL, err := sanitizeWCMediaURL(mediaURL)
	if err != nil {
		return nil, fmt.Errorf("wecom: refusing to fetch media: %w", err)
	}

	// Block open redirects: any 302 target must also pass the host allowlist.
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("wecom: too many redirects")
			}
			if !allowedWCMediaURL(req.URL.String()) {
				return fmt.Errorf("wecom: redirect to non-allowlisted URL %q", req.URL.String())
			}
			return nil
		},
	}
	resp, err := client.Get(safeURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20)) // 50 MB cap
	if err != nil {
		return nil, err
	}
	if aeskey != "" {
		body, _ = decryptWCMedia(body, aeskey)
	}
	return body, nil
}

// detectMIME returns a MIME type from the first bytes of data.
func detectMIME(data []byte) string {
	if len(data) < 4 {
		return "application/octet-stream"
	}
	switch {
	case bytes.HasPrefix(data, []byte("\xFF\xD8\xFF")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("GIF8")):
		return "image/gif"
	case bytes.HasPrefix(data, []byte("RIFF")) && len(data) >= 12 && string(data[8:12]) == "WEBP":
		return "image/webp"
	case bytes.HasPrefix(data, []byte("BM")):
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

func (a *Adapter) handleInbound(body json.RawMessage, onMessage func(channel.InboundEvent)) {
	var msg wcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		log.Printf("[wecom] unmarshal callback: %v", err)
		return
	}

	chatID := msg.ChatID
	if chatID == "" {
		chatID = msg.From.UserID
	}
	if chatID == "" {
		return
	}

	if len(a.allowedUsers) > 0 && !a.allowedUsers[msg.From.UserID] {
		return
	}

	// #1122: group chats had no @-mention gate at all — every message from an
	// allowlisted user was processed regardless of chat type. WeCom's
	// aibot_msg_callback carries no structured "was the bot mentioned" field
	// (see cfgBotNickname), so this only engages when bot_nickname is
	// configured; without it, behavior is unchanged from before this fix.
	if msg.ChatType == "group" && a.botNickname != "" {
		mention := "@" + a.botNickname
		// Case-insensitive, matching Telegram's own text-based mention
		// comparison (strings.EqualFold on @username) — a user typing
		// "@Octo" instead of "@octo" shouldn't silently fail to reach the bot.
		idx := strings.Index(strings.ToLower(msg.Text.Content), strings.ToLower(mention))
		if idx < 0 {
			return
		}
		// Strip the literal mention from the text that reaches the agent,
		// same as Telegram/Feishu/DingTalk already do for their own mention
		// syntax — it's UI-level addressing, not part of the query. Slicing
		// by byte offset (not strings.Replace) preserves the original
		// casing of whatever text remains.
		msg.Text.Content = strings.TrimSpace(msg.Text.Content[:idx] + msg.Text.Content[idx+len(mention):])
	}

	switch msg.MsgType {
	case "text", "image", "file":
		// handled below
	default:
		// #1123: voice/video/other unsupported msgtypes used to bare-return
		// here with not even a log line — the user had no idea their message
		// was received at all.
		log.Printf("[wecom] unsupported msgtype=%s from %s, acknowledging", msg.MsgType, msg.From.UserID)
		a.SendText(chatID, unsupportedMediaNotice, "")
		return
	}

	var text string
	var files []channel.FileAttachment

	switch msg.MsgType {
	case "text":
		text = strings.TrimSpace(msg.Text.Content)

	case "image":
		if msg.Image == nil || msg.Image.URL == "" {
			return
		}
		data, err := downloadWCMedia(msg.Image.URL, msg.Image.AESKey)
		if err != nil {
			log.Printf("[wecom] download image failed: %v", err)
			return
		}
		mime := detectMIME(data)
		dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		files = append(files, channel.FileAttachment{
			Type:     "image",
			Name:     "image.jpg",
			MimeType: mime,
			DataURL:  dataURL,
		})

	case "file":
		if msg.File == nil || msg.File.URL == "" {
			return
		}
		data, err := downloadWCMedia(msg.File.URL, msg.File.AESKey)
		if err != nil {
			log.Printf("[wecom] download file failed: %v", err)
			return
		}
		filename := msg.File.Name
		if filename == "" {
			filename = "attachment"
		}
		dir := filepath.Join(os.TempDir(), "octo-wecom")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Printf("[wecom] mkdir temp dir: %v", err)
			return
		}
		tmp, err := os.CreateTemp(dir, "wecom-*-"+filepath.Base(filename))
		if err != nil {
			log.Printf("[wecom] create temp file: %v", err)
			return
		}
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			log.Printf("[wecom] write temp file: %v", err)
			return
		}
		tmp.Close()
		files = append(files, channel.FileAttachment{
			Type: "file",
			Name: filename,
			Path: tmp.Name(),
		})
	}

	if text == "" && len(files) == 0 {
		return
	}

	chatType := "direct"
	if msg.ChatType == "group" {
		chatType = "group"
	}

	// Debug-level: reception is routine traffic, silent at the default level so
	// it never fills serve.log. Message text was never logged here.
	slog.Debug("channel message received", "platform", platformName, "chat", chatID, "user", msg.From.UserID, "chat_type", chatType, "msgtype", msg.MsgType)
	onMessage(channel.InboundEvent{
		Type:      "message",
		Platform:  platformName,
		ChatID:    chatID,
		UserID:    msg.From.UserID,
		Text:      text,
		Files:     files,
		MessageID: msg.MsgID,
		ChatType:  chatType,
		Raw:       msg,
	})
}
