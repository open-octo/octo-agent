// Package telegram implements the Telegram Bot API adapter.
//
// Transport: HTTPS long-poll via getUpdates — no public domain required.
// Auth: a single bot token obtained from @BotFather.
// Group rule: the bot only reacts when @-mentioned or replied to (matches
// the Feishu and DingTalk adapters).
//
// base_url is configurable to allow self-hosted Bot API servers
// (https://github.com/tdlib/telegram-bot-api), which is the practical
// escape hatch on networks where api.telegram.org is blocked.
package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
)

const (
	platformName   = "telegram"
	defaultBaseURL = "https://api.telegram.org"

	// longPollTimeout is how long the server holds getUpdates open.
	longPollTimeout = 25 * time.Second
	// pollHTTPTimeout must comfortably exceed the long-poll window so we
	// don't tear down healthy connections mid-poll.
	pollHTTPTimeout = longPollTimeout + 10*time.Second

	// maxMessageBytes leaves a margin under Telegram's 4096-char cap. Named
	// "Bytes" (not "Chars", its pre-#1116 name) because channel.SplitForSend
	// counts bytes, not runes — for CJK text this is more conservative than
	// the exact character cap requires, never less.
	maxMessageBytes = 4000

	// telegramHardCap is Telegram's actual message length limit. HTML
	// rendering (see markdown.go) adds tag overhead on top of the
	// maxMessageBytes-capped plain-text chunk, so a heavily-formatted chunk
	// can occasionally cross 4096 even though the source didn't. renderForSend
	// checks against this before deciding whether to send the rendered HTML.
	telegramHardCap = 4096

	// unsupportedMediaNotice is sent in place of silence (#1123) when a
	// message contains only a kind we don't process (voice, video, sticker,
	// location, …). Transcription/decoding can come later; going silent is
	// the bug being fixed here.
	unsupportedMediaNotice = "This message type isn't supported yet — please send text."
)

func init() {
	channel.Register(platformName, New)
}

// Config keys.
const (
	cfgBotToken     = "bot_token"
	cfgBaseURL      = "base_url"
	cfgParseMode    = "parse_mode"
	cfgAllowedUsers = "allowed_users"
)

// Adapter implements channel.Adapter for the Telegram Bot API.
type Adapter struct {
	token        string
	baseURL      string
	parseMode    string
	allowedUsers map[string]bool

	http     *http.Client
	pollHTTP *http.Client

	// Cached bot identity, used for the group @-mention check.
	botID       int64
	botUsername string
	mentionRe   *regexp.Regexp

	running bool
	cancel  context.CancelFunc
}

// New creates a Telegram adapter from platform config.
func New(cfg channel.PlatformConfig) (channel.Adapter, error) {
	token, _ := cfg[cfgBotToken].(string)
	if token == "" {
		return nil, fmt.Errorf("telegram: bot_token is required")
	}

	baseURL, _ := cfg[cfgBaseURL].(string)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	// Default HTML (#1119): legacy Markdown mode is Telegram's strictest and
	// most fragile parser — any unbalanced/unescaped reserved character fails
	// the whole chunk. HTML only requires escaping &, <, > in literal text,
	// and our renderer (markdown.go) produces well-formed tags from the real
	// parsed Markdown AST, so a single stray '*' in the model's output can no
	// longer take down the entire message's formatting. An explicit empty
	// parse_mode disables formatting.
	parseMode := "HTML"
	if v, ok := cfg[cfgParseMode].(string); ok {
		parseMode = v
	}

	allowed := make(map[string]bool)
	if raw, ok := cfg[cfgAllowedUsers].(string); ok {
		for _, u := range strings.Split(raw, ",") {
			if u = strings.TrimSpace(u); u != "" {
				allowed[u] = true
			}
		}
	} else if list, ok2 := cfg[cfgAllowedUsers].([]interface{}); ok2 {
		for _, v := range list {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				allowed[s] = true
			}
		}
	}

	return &Adapter{
		token:        token,
		baseURL:      baseURL,
		parseMode:    parseMode,
		allowedUsers: allowed,
		http:         &http.Client{Timeout: 30 * time.Second},
		pollHTTP:     &http.Client{Timeout: pollHTTPTimeout},
	}, nil
}

// Platform returns "telegram".
func (a *Adapter) Platform() string { return platformName }

// Start long-polls getUpdates and blocks until ctx is cancelled.
func (a *Adapter) Start(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	defer func() { a.running = false }()

	// Bot identity is used to gate group mentions; failure is non-fatal —
	// the poll loop will retry fetchBotIdentity each cycle until it succeeds.
	if err := a.fetchBotIdentity(ctx); err != nil {
		log.Printf("[telegram] getMe failed (will retry): %v", err)
	} else {
		log.Printf("[telegram] bot identity: @%s (id=%d)", a.botUsername, a.botID)
	}
	log.Printf("[telegram] starting long-poll (base_url=%s)", a.baseURL)

	var offset int64
	consecutiveErrors := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// If bot identity is still unknown, retry getMe before each poll cycle.
		if a.botID == 0 {
			if err := a.fetchBotIdentity(ctx); err == nil {
				log.Printf("[telegram] bot identity: @%s (id=%d)", a.botUsername, a.botID)
			}
		}

		updates, err := a.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Long-poll timeouts are normal (no updates during the window) — not errors.
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
				continue
			}
			consecutiveErrors++
			delay := 5 * time.Second
			if consecutiveErrors > 3 {
				delay = 30 * time.Second
			}
			log.Printf("[telegram] poll error: %v (retrying in %s)", err, delay)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
			continue
		}
		consecutiveErrors = 0

		for _, upd := range updates {
			offset = upd.UpdateID + 1
			a.processUpdate(upd, onMessage)
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

// SendText sends a message, splitting content over Telegram's length cap into
// consecutive messages (#1116: shared with every other adapter via
// channel.SplitForSend, which is also code-fence-aware — see chunking.go).
// Returns the message ID of the last chunk.
func (a *Adapter) SendText(chatID, text, replyTo string) channel.SendResult {
	chunks := channel.SplitForSend(text, maxMessageBytes)
	if len(chunks) == 0 {
		return channel.SendResult{OK: true}
	}

	var lastID string
	for i, chunk := range chunks {
		body, mode := a.renderForSend(chunk)
		params := map[string]any{
			"chat_id":                  chatID,
			"text":                     body,
			"disable_web_page_preview": true,
		}
		if mode != "" {
			params["parse_mode"] = mode
		}
		if replyTo != "" && i == 0 {
			params["reply_to_message_id"] = replyTo
		}

		msg, err := a.callMessage("sendMessage", params)
		if err != nil {
			// Parse failures fall back to the original plain chunk — the usual
			// cause is a construct our renderer/the configured parse_mode
			// doesn't handle the way Telegram expects.
			if mode != "" && isParseError(err) {
				log.Printf("[telegram] parse_mode failed, retrying as plain text: %v", err)
				params["text"] = chunk
				delete(params, "parse_mode")
				msg, err = a.callMessage("sendMessage", params)
			}
			if err != nil {
				return channel.SendResult{OK: false, Error: err.Error()}
			}
		}
		lastID = fmt.Sprintf("%d", msg.MessageID)
	}
	return channel.SendResult{OK: true, MessageID: lastID}
}

// renderForSend converts chunk for the wire according to a.parseMode. HTML
// mode runs it through the goldmark-based renderer in markdown.go (#1119);
// other modes (legacy Markdown, MarkdownV2, or "" for plain text) are sent
// as-is, unchanged from before this fix. If the rendered HTML would cross
// Telegram's real 4096-char hard cap — formatting tags add overhead on top of
// the plain-text byte budget chunks are split on — fall back to the plain
// chunk rather than risk an oversized request.
func (a *Adapter) renderForSend(chunk string) (body, mode string) {
	if a.parseMode != "HTML" {
		return chunk, a.parseMode
	}
	rendered := renderTelegramHTML(chunk)
	if len(rendered) > telegramHardCap {
		log.Printf("[telegram] rendered HTML (%d bytes) exceeds hard cap, sending plain", len(rendered))
		return chunk, ""
	}
	return rendered, "HTML"
}

// SendFile sends a local file to the chat. Images (png/jpg/jpeg/gif/webp) are
// sent via sendPhoto; all other files are sent via sendDocument.
func (a *Adapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	if _, err := os.Stat(path); err != nil {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("file not found: %s", path)}
	}

	ext := strings.ToLower(filepath.Ext(path))
	isImage := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp"

	var msg *tgMessage
	var err error
	if isImage {
		msg, err = a.sendMultipart("sendPhoto", chatID, "photo", path, "", replyTo)
	} else {
		msg, err = a.sendMultipart("sendDocument", chatID, "document", path, name, replyTo)
	}
	if err != nil {
		log.Printf("[telegram] send_file failed for %s: %v", path, err)
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	return channel.SendResult{OK: true, MessageID: fmt.Sprintf("%d", msg.MessageID)}
}

// sendMultipart builds and sends a multipart/form-data request for file upload.
func (a *Adapter) sendMultipart(method, chatID, fileField, filePath, fileName, replyTo string) (*tgMessage, error) {
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	// Write chat_id field.
	if fw, err := w.CreateFormField("chat_id"); err == nil {
		fw.Write([]byte(chatID))
	}

	// Write reply_to_message_id if provided.
	if replyTo != "" {
		if fw, err := w.CreateFormField("reply_to_message_id"); err == nil {
			fw.Write([]byte(replyTo))
		}
	}

	// Write the file field.
	if fileName == "" {
		fileName = filepath.Base(filePath)
	}
	mime := mimeForExt(filepath.Ext(filePath))
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fileField, fileName))
	h.Set("Content-Type", mime)
	if fw, err := w.CreatePart(h); err == nil {
		fw.Write(fileBytes)
	}

	w.Close()

	url := fmt.Sprintf("%s/bot%s/%s", a.baseURL, a.token, method)
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(rawBody, &r); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	if !r.OK {
		return nil, &apiError{Code: r.ErrorCode, Description: r.Description}
	}

	var msg tgMessage
	if err := json.Unmarshal(r.Result, &msg); err != nil {
		return nil, fmt.Errorf("invalid message in response: %w", err)
	}
	return &msg, nil
}

// mimeForExt returns a MIME type for common file extensions.
func mimeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// UpdateMessage edits a previously sent message in place.
func (a *Adapter) UpdateMessage(chatID, messageID, text string) bool {
	body, mode := a.renderForSend(text)
	params := map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     body,
		"disable_web_page_preview": true,
	}
	if mode != "" {
		params["parse_mode"] = mode
	}
	_, err := a.callMessage("editMessageText", params)
	if err != nil && mode != "" && isParseError(err) {
		params["text"] = text
		delete(params, "parse_mode")
		_, err = a.callMessage("editMessageText", params)
	}
	if err != nil {
		log.Printf("[telegram] update_message failed: %v", err)
		return false
	}
	return true
}

// SupportsMessageUpdates returns true.
func (a *Adapter) SupportsMessageUpdates() bool { return true }

// SendTyping sends a "typing" chat action (auto-expires after ~5s client-side).
func (a *Adapter) SendTyping(chatID, contextToken string) error {
	_, err := a.call(nil, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, a.http)
	return err
}

// StopTyping — Telegram's typing action expires automatically; nothing to cancel.
func (a *Adapter) StopTyping(chatID, contextToken string) error { return nil }

// Flush — Telegram has no outgoing buffer.
func (a *Adapter) Flush(chatID string) {}

func (a *Adapter) SupportsButtons() bool { return true }

// SendButtons sends a text message with inline keyboard buttons. Up to 3
// buttons per row; subsequent chunks (if the text exceeds maxMessageBytes)
// are sent without buttons since only the first chunk carries the prompt.
func (a *Adapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	// Build inline keyboard rows: up to 3 buttons per row.
	var rows [][]map[string]any
	for i := 0; i < len(buttons); i += 3 {
		end := i + 3
		if end > len(buttons) {
			end = len(buttons)
		}
		row := make([]map[string]any, 0, end-i)
		for _, b := range buttons[i:end] {
			row = append(row, map[string]any{
				"text":          b.Label,
				"callback_data": b.ID,
			})
		}
		rows = append(rows, row)
	}

	chunks := channel.SplitForSend(text, maxMessageBytes)
	if len(chunks) == 0 {
		return channel.SendResult{OK: true, MessageID: ""}
	}

	var lastID string
	for i, chunk := range chunks {
		body, mode := a.renderForSend(chunk)
		params := map[string]any{
			"chat_id":                  chatID,
			"text":                     body,
			"disable_web_page_preview": true,
		}
		if mode != "" {
			params["parse_mode"] = mode
		}
		if replyTo != "" && i == 0 {
			params["reply_to_message_id"] = replyTo
		}
		if i == 0 {
			params["reply_markup"] = map[string]any{"inline_keyboard": rows}
		}

		msg, err := a.callMessage("sendMessage", params)
		if err != nil {
			if mode != "" && isParseError(err) {
				log.Printf("[telegram] parse_mode failed, retrying as plain text: %v", err)
				delete(params, "parse_mode")
				params["text"] = chunk
				msg, err = a.callMessage("sendMessage", params)
			}
			if err != nil {
				return channel.SendResult{OK: false, Error: err.Error()}
			}
		}
		lastID = fmt.Sprintf("%d", msg.MessageID)
	}
	return channel.SendResult{OK: true, MessageID: lastID}
}

// ValidateConfig checks required fields.
func (a *Adapter) ValidateConfig(cfg channel.PlatformConfig) []string {
	var errs []string
	if tok, _ := cfg[cfgBotToken].(string); tok == "" {
		errs = append(errs, "bot_token is required")
	}
	return errs
}

// ─── Bot API ────────────────────────────────────────────────────────────────

type apiError struct {
	Code        int
	Description string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("telegram API error %d: %s", e.Code, e.Description)
}

func isParseError(err error) bool {
	ae, ok := err.(*apiError)
	if !ok {
		return false
	}
	d := strings.ToLower(ae.Description)
	return strings.Contains(d, "can't parse entities") || strings.Contains(d, "markdown")
}

// #1118: a 429 used to just fail the call outright — with SendText's
// per-chunk loop, that meant "chunk 1 arrives, chunk 3 silently missing"
// partial delivery on any burst. Telegram reports how long to back off in
// parameters.retry_after, so honor it with a small bounded retry instead of
// dropping the chunk.
const (
	maxRateLimitRetries = 3
	maxRateLimitWait    = 30 * time.Second
)

// waitForRetry sleeps for the API's requested backoff (capped, and floored at
// one second when the server didn't say), honoring ctx cancellation. Returns
// false if ctx was cancelled first.
func waitForRetry(ctx context.Context, seconds int) bool {
	d := time.Duration(seconds) * time.Second
	if d <= 0 {
		d = time.Second
	}
	if d > maxRateLimitWait {
		d = maxRateLimitWait
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// call POSTs JSON to /bot<TOKEN>/<method> and returns the raw `result`.
// A nil ctx means context.Background(); the long-poll path passes the Start
// context so Stop cancels an in-flight getUpdates promptly.
func (a *Adapter) call(ctx context.Context, method string, params map[string]any, client *http.Client) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	b, _ := json.Marshal(params)
	url := fmt.Sprintf("%s/bot%s/%s", a.baseURL, a.token, method)

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		var r struct {
			OK          bool            `json:"ok"`
			Result      json.RawMessage `json:"result"`
			ErrorCode   int             `json:"error_code"`
			Description string          `json:"description"`
			Parameters  *struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("invalid JSON response: %w", err)
		}
		if r.OK {
			return r.Result, nil
		}
		if r.ErrorCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			wait := 0
			if r.Parameters != nil {
				wait = r.Parameters.RetryAfter
			}
			if !waitForRetry(ctx, wait) {
				return nil, ctx.Err()
			}
			continue
		}
		return nil, &apiError{Code: r.ErrorCode, Description: r.Description}
	}
}

type tgPhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int    `json:"file_size"`
}

type tgDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

type tgEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type tgMessage struct {
	MessageID int64 `json:"message_id"`
	Date      int64 `json:"date"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From struct {
		ID    int64 `json:"id"`
		IsBot bool  `json:"is_bot"`
	} `json:"from"`
	Text           string        `json:"text"`
	Caption        string        `json:"caption"`
	Photo          []tgPhotoSize `json:"photo"`
	Document       *tgDocument   `json:"document"`
	Entities       []tgEntity    `json:"entities"`
	ReplyToMessage *struct {
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
	} `json:"reply_to_message"`

	// #1123: presence-only markers for message kinds we don't process (no
	// transcription/decoding path) — used solely to tell "the user sent
	// something we can't handle" apart from "the user sent nothing", so we
	// can acknowledge instead of going silent. json.RawMessage is nil when
	// the field is absent and non-nil (even "{}") when Telegram includes it.
	Voice     json.RawMessage `json:"voice,omitempty"`
	Audio     json.RawMessage `json:"audio,omitempty"`
	Video     json.RawMessage `json:"video,omitempty"`
	VideoNote json.RawMessage `json:"video_note,omitempty"`
	Sticker   json.RawMessage `json:"sticker,omitempty"`
	Location  json.RawMessage `json:"location,omitempty"`
}

// hasUnsupportedMedia reports whether the message carries a kind we
// recognize but don't process (see the presence markers above).
func (m *tgMessage) hasUnsupportedMedia() bool {
	return m.Voice != nil || m.Audio != nil || m.Video != nil ||
		m.VideoNote != nil || m.Sticker != nil || m.Location != nil
}

func (a *Adapter) callMessage(method string, params map[string]any) (*tgMessage, error) {
	raw, err := a.call(nil, method, params, a.http)
	if err != nil {
		return nil, err
	}
	var msg tgMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("invalid message in response: %w", err)
	}
	return &msg, nil
}

func (a *Adapter) fetchBotIdentity(ctx context.Context) error {
	raw, err := a.call(ctx, "getMe", map[string]any{}, a.http)
	if err != nil {
		return err
	}
	var me struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &me); err != nil {
		return err
	}
	a.botID = me.ID
	a.botUsername = me.Username
	if me.Username != "" {
		a.mentionRe = regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(me.Username) + `\b`)
	}
	return nil
}

type tgCallbackQuery struct {
	ID   string `json:"id"`
	From struct {
		ID int64 `json:"id"`
	} `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *tgMessage       `json:"message"`
	CallbackQuery *tgCallbackQuery `json:"callback_query"`
}

func (a *Adapter) getUpdates(ctx context.Context, offset int64) ([]tgUpdate, error) {
	params := map[string]any{
		"timeout":         int(longPollTimeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}
	if offset > 0 {
		params["offset"] = offset
	}
	raw, err := a.call(ctx, "getUpdates", params, a.pollHTTP)
	if err != nil {
		return nil, err
	}
	var updates []tgUpdate
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, fmt.Errorf("invalid updates in response: %w", err)
	}
	return updates, nil
}

// ─── Inbound ────────────────────────────────────────────────────────────────

func (a *Adapter) processUpdate(upd tgUpdate, onMessage func(channel.InboundEvent)) {
	// Handle callback queries (inline keyboard button presses) first.
	if cb := upd.CallbackQuery; cb != nil {
		a.processCallbackQuery(cb, onMessage)
		return
	}

	msg := upd.Message
	if msg == nil || msg.Chat.ID == 0 || msg.From.ID == 0 {
		return
	}

	isGroup := msg.Chat.Type == "group" || msg.Chat.Type == "supergroup"
	text := msg.Text

	if isGroup {
		if !a.groupMention(msg, text) {
			return
		}
		text = a.stripBotMention(text)
	}

	userID := fmt.Sprintf("%d", msg.From.ID)
	if len(a.allowedUsers) > 0 && !a.allowedUsers[userID] {
		return
	}

	if text == "" {
		text = msg.Caption
	}
	text = strings.TrimSpace(text)

	// Collect file attachments.
	var files []channel.FileAttachment

	if len(msg.Photo) > 0 {
		// Pick the largest (last) photo size variant.
		largest := msg.Photo[len(msg.Photo)-1]
		rawBytes, filePath, err := a.downloadTGFile(largest.FileID)
		if err != nil {
			log.Printf("[telegram] image download failed: %v", err)
		} else {
			mime := detectImageMIME(rawBytes)
			dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(rawBytes)
			name := filepath.Base(filePath)
			if name == "" || name == "." {
				name = "image.jpg"
			}
			files = append(files, channel.FileAttachment{
				Type:     "image",
				Name:     name,
				MimeType: mime,
				DataURL:  dataURL,
			})
		}
	}

	if msg.Document != nil {
		rawBytes, _, err := a.downloadTGFile(msg.Document.FileID)
		if err != nil {
			log.Printf("[telegram] document download failed: %v", err)
		} else {
			name := msg.Document.FileName
			if name == "" {
				name = "attachment"
			}
			f, err := os.CreateTemp("", "tg-doc-*-"+name)
			if err == nil {
				f.Write(rawBytes)
				f.Close()
				files = append(files, channel.FileAttachment{
					Type: "file",
					Name: name,
					Path: f.Name(),
				})
			}
		}
	}

	if text == "" && len(files) == 0 {
		// #1123: voice/audio/video/video_note/sticker/location arrive with no
		// text and nothing we download, so this used to be a bare drop — the
		// user sees the bot simply ignore them, indistinguishable from
		// broken. Acknowledge instead of going silent; a legitimately empty
		// message (no such marker) still drops with no reply, as before.
		if msg.hasUnsupportedMedia() {
			a.SendText(fmt.Sprintf("%d", msg.Chat.ID), unsupportedMediaNotice, fmt.Sprintf("%d", msg.MessageID))
		}
		return
	}

	chatType := "direct"
	if isGroup {
		chatType = "group"
	}

	// Content-free reception line: message text is never logged (only metadata),
	// so this stays a safe per-message signal in serve.log at the default level.
	slog.Info("channel message received", "platform", platformName, "chat", msg.Chat.ID, "user", userID, "chat_type", chatType, "bytes", len(text))
	onMessage(channel.InboundEvent{
		Type:      "message",
		Platform:  platformName,
		ChatID:    fmt.Sprintf("%d", msg.Chat.ID),
		UserID:    userID,
		Text:      text,
		Files:     files,
		MessageID: fmt.Sprintf("%d", msg.MessageID),
		ChatType:  chatType,
		Raw:       msg,
	})
}

// processCallbackQuery handles an inline keyboard button press. It answers
// the callback query to dismiss the loading indicator, then routes the button
// press as an InboundEvent with ButtonID set.
func (a *Adapter) processCallbackQuery(cb *tgCallbackQuery, onMessage func(channel.InboundEvent)) {
	// Answer the callback query so Telegram dismisses the loading UI.
	a.call(nil, "answerCallbackQuery", map[string]any{
		"callback_query_id": cb.ID,
		// No text — we don't need to show a toast notification.
	}, a.http)

	if cb.Message == nil || cb.Data == "" {
		return
	}

	userID := fmt.Sprintf("%d", cb.From.ID)
	if len(a.allowedUsers) > 0 && !a.allowedUsers[userID] {
		return
	}

	log.Printf("[telegram] callback from user %s: %s", userID, cb.Data)
	onMessage(channel.InboundEvent{
		Type:      "message",
		Platform:  platformName,
		ChatID:    fmt.Sprintf("%d", cb.Message.Chat.ID),
		UserID:    userID,
		MessageID: fmt.Sprintf("%d", cb.Message.MessageID),
		ButtonID:  cb.Data,
		ChatType:  "direct", // callback queries from groups still work per-user
		Raw:       cb,
	})
}

// downloadTGFile resolves file_id to a file_path via getFile, then downloads
// the raw bytes. Returns (bytes, filePath, error).
func (a *Adapter) downloadTGFile(fileID string) ([]byte, string, error) {
	raw, err := a.call(context.Background(), "getFile", map[string]any{"file_id": fileID}, a.http)
	if err != nil {
		return nil, "", fmt.Errorf("getFile: %w", err)
	}
	var result struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, "", fmt.Errorf("parse getFile response: %w", err)
	}
	if result.FilePath == "" {
		return nil, "", fmt.Errorf("getFile returned no file_path")
	}
	downloadURL := fmt.Sprintf("%s/file/bot%s/%s", a.baseURL, a.token, result.FilePath)
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("file download HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20)) // 50 MB cap
	if err != nil {
		return nil, "", err
	}
	return data, result.FilePath, nil
}

// detectImageMIME sniffs the first bytes to identify common image types.
func detectImageMIME(b []byte) string {
	if len(b) < 4 {
		return "image/jpeg"
	}
	if b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47 {
		return "image/png"
	}
	if b[0] == 0x47 && b[1] == 0x49 && b[2] == 0x46 {
		return "image/gif"
	}
	if len(b) >= 8 && b[0] == 0x52 && b[1] == 0x49 && b[2] == 0x46 && b[3] == 0x46 {
		return "image/webp"
	}
	return "image/jpeg"
}

// groupMention reports whether the bot should react to a group message:
// either the text @-mentions the bot's username via a mention entity, or the
// message replies to one of the bot's own messages. Falls back to regex when
// no entities are present. Fails closed when bot identity is unknown.
func (a *Adapter) groupMention(msg *tgMessage, text string) bool {
	if a.botID == 0 {
		return false
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From.ID == a.botID {
		return true
	}
	// Use entity offsets when available for precise mention matching.
	runes := []rune(text)
	for _, e := range msg.Entities {
		if e.Type != "mention" {
			continue
		}
		if e.Offset < 0 || e.Offset+e.Length > len(runes) {
			continue
		}
		mention := string(runes[e.Offset : e.Offset+e.Length])
		if strings.EqualFold(mention, "@"+a.botUsername) {
			return true
		}
	}
	// Fallback to regex for clients that omit entity metadata.
	return a.mentionRe != nil && a.mentionRe.MatchString(text)
}

func (a *Adapter) stripBotMention(text string) string {
	if a.mentionRe == nil {
		return text
	}
	return strings.TrimSpace(a.mentionRe.ReplaceAllString(text, ""))
}

// ─── Helpers ────────────────────────────────────────────────────────────────
//
// The length-cap splitter used to live here (splitMessage); it's now
// channel.SplitForSend, shared by every adapter (#1116) — see
// internal/channel/chunking.go and its tests for the splitting behavior
// itself.
