// Package feishu implements the Feishu (飞书) adapter.
//
// Feishu uses a WebSocket connection for receiving bot messages and a REST
// API for sending.
//
// Flow:
//  1. OAuth → tenant_access_token
//  2. POST /open-apis/ws/connection → WebSocket URL
//  3. Connect WebSocket, listen for im.message.receive_v1 events
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
	"mime/multipart"
	"net/http"
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
	platformName   = "feishu"
	defaultDomain  = "https://open.feishu.cn"
	reconnectDelay = 5 * time.Second
	readDeadline   = 120 * time.Second
	pingInterval   = 90 * time.Second
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

// Package-level compiled regexps (avoids recompiling on every message send).
var (
	feishuStripImgRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	feishuTableRe    = regexp.MustCompile(`\|.+\|`)
)

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

	wsURL, err := a.getWSEndpoint()
	if err != nil {
		return fmt.Errorf("ws endpoint: %w", err)
	}

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	log.Printf("[feishu] connected to WebSocket")

	// FEISHU-005: proactive ping goroutine.
	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					log.Printf("[feishu] ping error: %v", err)
					conn.Close()
					return
				}
			}
		}
	}()

	return a.readEvents(ctx, conn, onMessage)
}

// Stop gracefully shuts down.
func (a *Adapter) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	return nil
}

// SendText sends a text message.
func (a *Adapter) SendText(chatID, text, replyTo string) channel.SendResult {
	msgID, err := a.sendMessage(chatID, text, replyTo)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	return channel.SendResult{OK: true, MessageID: msgID}
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

func (a *Adapter) getWSEndpoint() (string, error) {
	req, _ := http.NewRequest("POST", a.domain+"/open-apis/ws/connection", nil)
	req.Header.Set("Authorization", "Bearer "+a.getToken())
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var r struct {
		Code int `json:"code"`
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.Code != 0 {
		return "", fmt.Errorf("ws endpoint: code %d", r.Code)
	}
	return r.Data.URL, nil
}

func (a *Adapter) readEvents(ctx context.Context, conn *websocket.Conn, onMessage func(channel.InboundEvent)) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// FEISHU-004: set read deadline before every ReadMessage call.
		conn.SetReadDeadline(time.Now().Add(readDeadline))

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		// Reset deadline after a successful read.
		conn.SetReadDeadline(time.Time{})

		var envelope struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case "ping":
			pong, _ := json.Marshal(map[string]string{"type": "pong"})
			conn.WriteMessage(websocket.TextMessage, pong)
		case "message":
			a.handleEvent(envelope.Data, onMessage)
		}
	}
}

// ─── Bot info ──────────────────────────────────────────────────────────────

func (a *Adapter) getBotOpenID() string {
	req, _ := http.NewRequest("GET", a.domain+"/open-apis/bot/v3/info", nil)
	req.Header.Set("Authorization", "Bearer "+a.getToken())
	resp, err := a.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var r struct {
		Code int `json:"code"`
		Data struct {
			Bot struct {
				OpenID string `json:"open_id"`
			} `json:"bot"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return ""
	}
	return r.Data.Bot.OpenID
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

func (a *Adapter) handleEvent(raw json.RawMessage, onMessage func(channel.InboundEvent)) {
	var ev fsEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		log.Printf("[feishu] unmarshal event: %v", err)
		return
	}

	if ev.Header.EventType != "im.message.receive_v1" {
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
	}

	// FEISHU-006: drop only if both text and files are empty.
	if text == "" && len(files) == 0 {
		return
	}

	// FEISHU-011: enrich with Feishu doc content.
	if text != "" {
		text = a.enrichDocContent(text)
	}

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
	tmpFile, err := os.CreateTemp("", "feishu-file-*")
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
// FEISHU-011: silently skips on error.
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
	// Strip image markdown before rendering.
	cleaned := feishuStripImgRe.ReplaceAllString(text, "")

	hasCodeOrTable := strings.Contains(cleaned, "```") ||
		feishuTableRe.MatchString(cleaned)

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
