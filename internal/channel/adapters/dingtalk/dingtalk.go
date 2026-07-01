// Package dingtalk implements the DingTalk (钉钉) Stream Mode adapter.
//
// DingTalk Stream Mode provides a persistent WebSocket connection for
// receiving bot messages and a webhook/OAPI for sending replies.
//
// Flow:
//  1. OAuth client_credentials → access_token
//  2. Open gateway connection → ticket
//  3. WebSocket to wss://wss.dingtalk.com/connect
//  4. Register for /v1.0/im/bot/messages/get topic
//  5. Receive frames → extract message → deliver to ChannelManager
//  6. Send replies via sessionWebhook (from inbound event) or OAPI
package dingtalk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
	platformName    = "dingtalk"
	apiBase         = "https://api.dingtalk.com"
	oapiBase        = "https://oapi.dingtalk.com"
	gatewayHost     = "wss://wss.dingtalk.com"
	msgTopic        = "/v1.0/im/bot/messages/get"
	reconnectDelay  = 5 * time.Second
	webhookSafetyMs = 5 * 60 * 1000 // 5 min in ms
)

func init() {
	channel.Register(platformName, New)
}

// Config keys.
const (
	cfgClientID     = "client_id"
	cfgClientSecret = "client_secret"
	cfgRobotCode    = "robot_code"
	cfgAllowedUsers = "allowed_users"
)

// atMentionRe strips leading @mention from inbound text.
var atMentionRe = regexp.MustCompile(`^@[^\s]+\s*`)

// Adapter implements channel.Adapter for DingTalk Stream Mode.
type Adapter struct {
	clientID     string
	clientSecret string
	robotCode    string // defaults to clientID — they coincide for enterprise-internal robots
	apiBase      string
	oapiBase     string
	allowedUsers map[string]bool

	http        *http.Client
	accessToken string
	tokenExpiry time.Time
	tokenMu     sync.Mutex

	// OAPI token (legacy /media/upload endpoint).
	oapiToken       string
	oapiTokenExpiry time.Time
	oapiTokenMu     sync.Mutex

	webhooks  map[string]webhookEntry
	webhookMu sync.Mutex

	// routes caches routing info needed for OAPI sends (send_file).
	routes  map[string]routeEntry
	routeMu sync.Mutex

	running bool
	cancel  context.CancelFunc
}

type webhookEntry struct {
	URL         string
	ExpiresAtMs int64
}

type routeEntry struct {
	RobotCode string
	ConvID    string
	UserID    string
	ConvType  string
}

// New creates a DingTalk adapter from platform config.
func New(cfg channel.PlatformConfig) (channel.Adapter, error) {
	cid, _ := cfg[cfgClientID].(string)
	secret, _ := cfg[cfgClientSecret].(string)
	if cid == "" || secret == "" {
		return nil, fmt.Errorf("dingtalk: client_id and client_secret are required")
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

	robotCode, _ := cfg[cfgRobotCode].(string)
	if robotCode == "" {
		robotCode = cid
	}

	return &Adapter{
		clientID:     cid,
		clientSecret: secret,
		robotCode:    robotCode,
		apiBase:      apiBase,
		oapiBase:     oapiBase,
		allowedUsers: allowed,
		http:         &http.Client{Timeout: 30 * time.Second},
		webhooks:     make(map[string]webhookEntry),
		routes:       make(map[string]routeEntry),
	}, nil
}

// Platform returns "dingtalk".
func (a *Adapter) Platform() string { return platformName }

// Start connects to DingTalk Stream Mode and blocks until ctx is cancelled.
// DT-001: wraps the whole connect sequence in a reconnect loop.
func (a *Adapter) Start(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	defer func() { a.running = false }()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		err := a.connectOnce(ctx, onMessage)
		if err == nil || ctx.Err() != nil {
			return nil
		}
		log.Printf("[dingtalk] connection error: %v; reconnecting in %s", err, reconnectDelay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

// connectOnce runs one full connect-listen cycle.
func (a *Adapter) connectOnce(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	// DT-009: refresh token synchronously under mutex.
	if err := a.refreshToken(); err != nil {
		return fmt.Errorf("token: %w", err)
	}

	ticket, wsURL, err := a.openGateway()
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	_ = ticket // wsURL already contains the ticket

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	log.Printf("[dingtalk] stream connected, listening")
	return a.readFrames(ctx, conn, onMessage)
}

// Stop gracefully shuts down.
func (a *Adapter) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	return nil
}

// SendText sends a Markdown message via sessionWebhook or proactive OAPI.
func (a *Adapter) SendText(chatID, text, replyTo string) channel.SendResult {
	if url := a.resolveWebhook(chatID); url != "" {
		return a.sendWebhook(url, text)
	}
	return a.sendProactive(chatID, text)
}

// SendFile uploads a file and sends it as a media message via OAPI.
// DT-007: 3-step OAPI approach: get OAPI token → upload media → send media message.
func (a *Adapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	if _, err := os.Stat(path); err != nil {
		return channel.SendResult{OK: false, Error: "file not found: " + path}
	}

	route := a.resolveRoute(chatID)
	if route == nil {
		return channel.SendResult{OK: false, Error: "no routing info for chat " + chatID}
	}

	kind := "file"
	if isImagePath(path) {
		kind = "image"
	}

	mediaID, err := a.uploadMedia(path, kind)
	if err != nil {
		return channel.SendResult{OK: false, Error: "upload failed: " + err.Error()}
	}

	fileName := name
	if fileName == "" {
		fileName = filepath.Base(path)
	}

	return a.sendMedia(route, mediaID, kind, fileName)
}

// UpdateMessage — DingTalk does not support editing sent messages.
func (a *Adapter) UpdateMessage(chatID, messageID, text string) bool { return false }

// SupportsMessageUpdates returns false.
func (a *Adapter) SupportsMessageUpdates() bool { return false }

// SendTyping sends a typing indicator. DingTalk Stream Mode does not support this.
func (a *Adapter) SendTyping(chatID, contextToken string) error { return nil }

// StopTyping — no-op for DingTalk.
func (a *Adapter) StopTyping(chatID, contextToken string) error { return nil }

// Flush — DingTalk has no outgoing buffer.
func (a *Adapter) Flush(chatID string) {}

// ValidateConfig checks required fields.
func (a *Adapter) ValidateConfig(cfg channel.PlatformConfig) []string {
	var errs []string
	if cid, _ := cfg[cfgClientID].(string); cid == "" {
		errs = append(errs, "client_id is required")
	}
	if sec, _ := cfg[cfgClientSecret].(string); sec == "" {
		errs = append(errs, "client_secret is required")
	}
	return errs
}

// ─── OAuth ─────────────────────────────────────────────────────────────────

// DT-009: refreshToken is always called synchronously under its own lock path.
// getToken calls it directly when expired, never in a goroutine.
func (a *Adapter) refreshToken() error {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	// Re-check under lock in case another caller already refreshed.
	if a.accessToken != "" && time.Now().Before(a.tokenExpiry) {
		return nil
	}

	body := map[string]string{"appKey": a.clientID, "appSecret": a.clientSecret}
	b, _ := json.Marshal(body)
	resp, err := a.http.Post(a.apiBase+"/v1.0/oauth2/accessToken", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var r struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if r.AccessToken == "" {
		return fmt.Errorf("empty access token in response")
	}

	a.accessToken = r.AccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(r.ExpireIn-60) * time.Second)
	log.Printf("[dingtalk] token obtained, expires in %ds", r.ExpireIn)
	return nil
}

func (a *Adapter) getToken() string {
	a.tokenMu.Lock()
	tok := a.accessToken
	expired := time.Now().After(a.tokenExpiry)
	a.tokenMu.Unlock()

	if tok == "" || expired {
		// DT-009: synchronous refresh, no goroutine.
		if err := a.refreshToken(); err != nil {
			log.Printf("[dingtalk] token refresh failed: %v", err)
			return tok
		}
		a.tokenMu.Lock()
		tok = a.accessToken
		a.tokenMu.Unlock()
	}
	return tok
}

// getOAPIToken returns a cached OAPI token (legacy /media/upload endpoint).
// DT-007: OAPI token is independent from the v1.0 access token.
func (a *Adapter) getOAPIToken() (string, error) {
	a.oapiTokenMu.Lock()
	defer a.oapiTokenMu.Unlock()

	if a.oapiToken != "" && time.Now().Before(a.oapiTokenExpiry) {
		return a.oapiToken, nil
	}

	url := fmt.Sprintf("%s/gettoken?appkey=%s&appsecret=%s", a.oapiBase, a.clientID, a.clientSecret)
	resp, err := a.http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var r struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.ErrCode != 0 || r.AccessToken == "" {
		return "", fmt.Errorf("OAPI token error %d: %s", r.ErrCode, r.ErrMsg)
	}

	a.oapiToken = r.AccessToken
	a.oapiTokenExpiry = time.Now().Add(time.Duration(r.ExpiresIn-60) * time.Second)
	return a.oapiToken, nil
}

// ─── Gateway ───────────────────────────────────────────────────────────────

// DT-004: openGateway includes subscriptions, ua, and localIp fields.
// Returns (ticket, wsURL, error).
func (a *Adapter) openGateway() (string, string, error) {
	body := map[string]any{
		"clientId":     a.clientID,
		"clientSecret": a.clientSecret,
		"subscriptions": []map[string]string{
			{"type": "CALLBACK", "topic": msgTopic},
		},
		"ua":      "octo-agent",
		"localIp": "127.0.0.1",
	}
	b, _ := json.Marshal(body)
	resp, err := a.http.Post(a.apiBase+"/v1.0/gateway/connections/open", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var r struct {
		Ticket   string `json:"ticket"`
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", err
	}
	if r.Ticket == "" {
		return "", "", fmt.Errorf("empty ticket in gateway response")
	}

	wsURL := r.Endpoint
	if wsURL == "" {
		wsURL = fmt.Sprintf("%s/connect?appKey=%s&ticket=%s", gatewayHost, a.clientID, r.Ticket)
	} else {
		wsURL = fmt.Sprintf("%s?ticket=%s", r.Endpoint, r.Ticket)
	}
	return r.Ticket, wsURL, nil
}

// ─── Stream frames ─────────────────────────────────────────────────────────

type streamFrame struct {
	SpecVersion string            `json:"specVersion"`
	Type        string            `json:"type"`
	Headers     map[string]string `json:"headers"`
	Data        json.RawMessage   `json:"data"`
}

func (a *Adapter) readFrames(ctx context.Context, conn *websocket.Conn, onMessage func(channel.InboundEvent)) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		var frame streamFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			continue
		}

		// DT-002: handle SYSTEM frames (ping/disconnect).
		if frame.Type == "SYSTEM" {
			if err := a.handleSystemFrame(conn, frame); err != nil {
				return err
			}
			continue
		}

		// DT-003: send ACK immediately for EVENT/CALLBACK frames.
		if frame.Type == "EVENT" || frame.Type == "CALLBACK" {
			a.sendACK(conn, frame)
		}

		if frame.Headers["topic"] != msgTopic {
			continue
		}
		a.handleInbound(frame.Data, onMessage)
	}
}

// DT-002: handleSystemFrame processes SYSTEM frames.
// ping → respond with pong; disconnect → return error to trigger reconnect.
func (a *Adapter) handleSystemFrame(conn *websocket.Conn, frame streamFrame) error {
	topic := frame.Headers["topic"]
	switch topic {
	case "ping":
		// Mirror the frame back with topic changed to "pong".
		pongHeaders := make(map[string]string, len(frame.Headers))
		for k, v := range frame.Headers {
			pongHeaders[k] = v
		}
		pongHeaders["topic"] = "pong"
		pong := map[string]any{
			"specVersion": frame.SpecVersion,
			"type":        "SYSTEM",
			"headers":     pongHeaders,
			"data":        frame.Data,
		}
		b, _ := json.Marshal(pong)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			return fmt.Errorf("pong write: %w", err)
		}
		log.Printf("[dingtalk] pong sent")
	case "disconnect":
		log.Printf("[dingtalk] server requested disconnect, reconnecting")
		return fmt.Errorf("server disconnect")
	}
	return nil
}

// DT-003: sendACK sends an ACK frame for EVENT/CALLBACK frames.
func (a *Adapter) sendACK(conn *websocket.Conn, frame streamFrame) {
	ack := map[string]any{
		"code": 200,
		"headers": map[string]string{
			"messageId": frame.Headers["messageId"],
			"topic":     "ack",
		},
		"message": "OK",
		"data":    "",
	}
	b, _ := json.Marshal(ack)
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		log.Printf("[dingtalk] ACK send failed: %v", err)
	}
}

// ─── Inbound event handling ────────────────────────────────────────────────

type dtEvent struct {
	SenderStaffID             string `json:"senderStaffId"`
	SenderID                  string `json:"senderId"`
	ConversationID            string `json:"conversationId"`
	SessionWebhook            string `json:"sessionWebhook"`
	SessionWebhookExpiredTime int64  `json:"sessionWebhookExpiredTime"`
	ConversationType          string `json:"conversationType"`
	RobotCode                 string `json:"robotCode"`
	MsgID                     string `json:"msgId"`
	MsgType                   string `json:"msgtype"`
	ChatbotUserID             string `json:"chatbotUserId"`
	Text                      struct {
		Content string `json:"content"`
	} `json:"text"`
	// Content holds picture/file download codes and richText.
	Content struct {
		DownloadCode string          `json:"downloadCode"`
		FileName     string          `json:"fileName"`
		RichText     json.RawMessage `json:"richText"`
	} `json:"content"`
	AtUsers []struct {
		DingtalkID string `json:"dingtalkId"`
	} `json:"atUsers"`
}

func (a *Adapter) handleInbound(raw json.RawMessage, onMessage func(channel.InboundEvent)) {
	var ev dtEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		log.Printf("[dingtalk] unmarshal event: %v", err)
		return
	}

	senderID := ev.SenderStaffID
	if senderID == "" {
		senderID = ev.SenderID
	}
	if senderID == "" {
		return
	}

	chatID := ev.ConversationID
	if chatID == "" {
		chatID = senderID
	}

	// Cache webhook.
	if ev.SessionWebhook != "" {
		a.webhookMu.Lock()
		a.webhooks[chatID] = webhookEntry{URL: ev.SessionWebhook, ExpiresAtMs: ev.SessionWebhookExpiredTime}
		a.webhookMu.Unlock()
	}

	// Cache route for outbound file sends.
	robotCode := ev.RobotCode
	if robotCode == "" {
		robotCode = a.robotCode
	}
	a.routeMu.Lock()
	a.routes[chatID] = routeEntry{
		RobotCode: robotCode,
		ConvID:    ev.ConversationID,
		UserID:    senderID,
		ConvType:  ev.ConversationType,
	}
	a.routeMu.Unlock()

	// Group chat: only @-mention.
	if ev.ConversationType == "2" {
		mentioned := false
		for _, u := range ev.AtUsers {
			if u.DingtalkID == ev.ChatbotUserID {
				mentioned = true
				break
			}
		}
		content := ev.Text.Content
		if !mentioned && !strings.Contains(content, "@") {
			return
		}
	}

	if len(a.allowedUsers) > 0 && !a.allowedUsers[senderID] {
		return
	}

	text, files := a.extractPayload(ev)
	// DT-005: guard changed to check both text and files.
	if strings.TrimSpace(text) == "" && len(files) == 0 {
		return
	}

	ct := "direct"
	if ev.ConversationType == "2" {
		ct = "group"
	}

	onMessage(channel.InboundEvent{
		Type:      "message",
		Platform:  platformName,
		ChatID:    chatID,
		UserID:    senderID,
		Text:      text,
		Files:     files,
		MessageID: ev.MsgID,
		ChatType:  ct,
		Raw:       ev,
	})
}

// extractPayload parses text and file attachments from an inbound event.
// DT-005, DT-006: handles picture, file, and richText msgtypes.
func (a *Adapter) extractPayload(ev dtEvent) (string, []channel.FileAttachment) {
	var text string
	var files []channel.FileAttachment

	robotCode := ev.RobotCode
	if robotCode == "" {
		robotCode = a.robotCode
	}

	switch ev.MsgType {
	case "text":
		// DT-011: strip @mention with regexp.
		c := atMentionRe.ReplaceAllString(ev.Text.Content, "")
		text = strings.TrimSpace(c)

	case "picture":
		// DT-005: download picture and attach as image.
		code := ev.Content.DownloadCode
		if code != "" {
			data, mimeType, err := a.downloadDTFile(code, robotCode)
			if err == nil && len(data) > 0 {
				dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
				files = append(files, channel.FileAttachment{
					Type:    "image",
					DataURL: dataURL,
				})
			} else if err != nil {
				log.Printf("[dingtalk] picture download failed: %v", err)
			}
		}

	case "file":
		// DT-005: download file and save to temp path.
		code := ev.Content.DownloadCode
		fileName := ev.Content.FileName
		if code != "" {
			data, _, err := a.downloadDTFile(code, robotCode)
			if err == nil && len(data) > 0 {
				tmpFile, err := os.CreateTemp("", "dingtalk-*-"+fileName)
				if err == nil {
					_, writeErr := tmpFile.Write(data)
					tmpFile.Close()
					if writeErr == nil {
						files = append(files, channel.FileAttachment{
							Type: "file",
							Name: fileName,
							Path: tmpFile.Name(),
						})
					} else {
						os.Remove(tmpFile.Name())
					}
				}
			} else if err != nil {
				log.Printf("[dingtalk] file download failed: %v", err)
			}
		}

	case "richText":
		// DT-006: parse richText array, collect text and picture parts.
		var parts []struct {
			Text         string `json:"text"`
			DownloadCode string `json:"downloadCode"`
			Type         string `json:"type"`
		}
		if err := json.Unmarshal(ev.Content.RichText, &parts); err == nil {
			for _, p := range parts {
				if p.Text != "" {
					text += p.Text
				} else if p.DownloadCode != "" && p.Type == "picture" {
					data, mimeType, err := a.downloadDTFile(p.DownloadCode, robotCode)
					if err == nil && len(data) > 0 {
						dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
						files = append(files, channel.FileAttachment{
							Type:    "image",
							DataURL: dataURL,
						})
					}
				}
			}
		}

	default:
		log.Printf("[dingtalk] unsupported msgtype=%s, ignoring", ev.MsgType)
	}

	return text, files
}

// downloadDTFile fetches a file by downloadCode from the DingTalk robot API.
// DT-005: POST /v1.0/robot/messageFiles/download → downloadUrl → fetch bytes.
func (a *Adapter) downloadDTFile(downloadCode, robotCode string) ([]byte, string, error) {
	// Step 1: exchange downloadCode for a temporary URL.
	body := map[string]string{
		"downloadCode": downloadCode,
		"robotCode":    robotCode,
	}
	b, _ := json.Marshal(body)
	tok := a.getToken()
	req, err := http.NewRequest("POST", a.apiBase+"/v1.0/robot/messageFiles/download", bytes.NewReader(b))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", tok)

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	var r struct {
		DownloadURL string `json:"downloadUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, "", err
	}
	if r.DownloadURL == "" {
		return nil, "", fmt.Errorf("empty downloadUrl in response")
	}

	// Step 2: fetch the actual file bytes.
	fileResp, err := a.http.Get(r.DownloadURL)
	if err != nil {
		return nil, "", err
	}
	defer fileResp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(fileResp.Body, 50<<20)) // 50 MB cap
	if err != nil {
		return nil, "", err
	}

	mimeType := fileResp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	// Trim parameters from mime type for data URL.
	if idx := strings.Index(mimeType, ";"); idx > 0 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	return data, mimeType, nil
}

// ─── Send ──────────────────────────────────────────────────────────────────

func (a *Adapter) resolveWebhook(chatID string) string {
	a.webhookMu.Lock()
	defer a.webhookMu.Unlock()
	e, ok := a.webhooks[chatID]
	if !ok {
		return ""
	}
	if e.ExpiresAtMs > 0 && time.Now().UnixMilli()+webhookSafetyMs >= e.ExpiresAtMs {
		delete(a.webhooks, chatID)
		return ""
	}
	return e.URL
}

func (a *Adapter) resolveRoute(chatID string) *routeEntry {
	a.routeMu.Lock()
	defer a.routeMu.Unlock()
	if r, ok := a.routes[chatID]; ok {
		cp := r
		return &cp
	}
	return nil
}

// sendProactive pushes a message through the robot send APIs.
func (a *Adapter) sendProactive(chatID, text string) channel.SendResult {
	tok := a.getToken()
	if tok == "" {
		return channel.SendResult{OK: false, Error: "no access token"}
	}

	msgParam, _ := json.Marshal(map[string]string{"title": "Octo", "text": text})
	body := map[string]any{
		"robotCode": a.robotCode,
		"msgKey":    "sampleMarkdown",
		"msgParam":  string(msgParam),
	}
	path := "/v1.0/robot/oToMessages/batchSend"
	if strings.HasPrefix(chatID, "cid") {
		path = "/v1.0/robot/groupMessages/send"
		body["openConversationId"] = chatID
	} else {
		body["userIds"] = []string{chatID}
	}

	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", a.apiBase+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", tok)

	resp, err := a.http.Do(req)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("robot api %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}
	return channel.SendResult{OK: true}
}

// sendWebhook delivers text via the per-message sessionWebhook.
// DT-010: reads response body and checks errcode.
func (a *Adapter) sendWebhook(url, text string) channel.SendResult {
	body := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": "Octo",
			"text":  text,
		},
	}
	b, _ := json.Marshal(body)
	resp, err := a.http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &r); err == nil && r.ErrCode != 0 {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("webhook errcode %d: %s", r.ErrCode, r.ErrMsg)}
	}
	return channel.SendResult{OK: true}
}

// ─── File upload (DT-007) ──────────────────────────────────────────────────

// uploadMedia uploads a local file to DingTalk via the OAPI legacy endpoint.
// Returns the media_id string.
func (a *Adapter) uploadMedia(path, kind string) (string, error) {
	tok, err := a.getOAPIToken()
	if err != nil {
		return "", fmt.Errorf("OAPI token: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// type field
	if err := mw.WriteField("type", kind); err != nil {
		return "", err
	}

	// media field
	fileName := filepath.Base(path)
	mimeType := mimeForFile(fileName)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="media"; filename="%s"`, fileName))
	h.Set("Content-Type", mimeType)
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	mw.Close()

	uploadURL := fmt.Sprintf("%s/media/upload?access_token=%s", a.oapiBase, tok)
	req, err := http.NewRequest("POST", uploadURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "", fmt.Errorf("upload response parse: %w", err)
	}
	if r.ErrCode != 0 || r.MediaID == "" {
		return "", fmt.Errorf("upload failed errcode=%d errmsg=%s", r.ErrCode, r.ErrMsg)
	}
	log.Printf("[dingtalk] upload_media ok kind=%s media_id_len=%d", kind, len(r.MediaID))
	return r.MediaID, nil
}

// sendMedia sends a previously uploaded media file via the robot OAPI.
func (a *Adapter) sendMedia(route *routeEntry, mediaID, kind, fileName string) channel.SendResult {
	tok := a.getToken()
	if tok == "" {
		return channel.SendResult{OK: false, Error: "no access token"}
	}

	var msgKey string
	var msgParam map[string]string
	if kind == "image" {
		msgKey = "sampleImageMsg"
		msgParam = map[string]string{"photoURL": mediaID}
	} else {
		msgKey = "sampleFile"
		ext := strings.TrimPrefix(filepath.Ext(fileName), ".")
		msgParam = map[string]string{
			"mediaId":  mediaID,
			"fileName": fileName,
			"fileType": ext,
		}
	}

	msgParamJSON, _ := json.Marshal(msgParam)

	var path string
	var body map[string]any
	if route.ConvType == "2" {
		path = "/v1.0/robot/groupMessages/send"
		body = map[string]any{
			"msgKey":             msgKey,
			"msgParam":           string(msgParamJSON),
			"openConversationId": route.ConvID,
			"robotCode":          route.RobotCode,
		}
	} else {
		path = "/v1.0/robot/oToMessages/batchSend"
		body = map[string]any{
			"msgKey":    msgKey,
			"msgParam":  string(msgParamJSON),
			"userIds":   []string{route.UserID},
			"robotCode": route.RobotCode,
		}
	}

	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", a.apiBase+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", tok)

	resp, err := a.http.Do(req)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("send_media %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}
	log.Printf("[dingtalk] send_media ok kind=%s msgKey=%s", kind, msgKey)
	return channel.SendResult{OK: true}
}

// ─── Helpers ───────────────────────────────────────────────────────────────

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

func mimeForFile(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}
