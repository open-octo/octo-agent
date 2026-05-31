// Package weixin implements the Weixin (微信) iLink adapter.
//
// It uses the iLink protocol (https://ilinkai.weixin.qq.com) for QR login,
// long-poll message receiving, and message sending.
package weixin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Leihb/octo-agent/internal/channel"
	"github.com/Leihb/octo-agent/internal/channel/adapters/weixin/ilink"
)

const platformName = "weixin"

func init() {
	channel.Register(platformName, New)
}

// Config keys expected in channels.yml.
const (
	cfgBaseURL    = "base_url"
	cfgCredPath   = "cred_path"
	cfgTimeoutSec = "timeout_sec"
)

// Adapter implements channel.Adapter for Weixin iLink.
type Adapter struct {
	baseURL  string
	credPath string
	client   *ilink.Client

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(channel.InboundEvent)

	// bot wraps the iLink SDK.
	bot *ilinkBot
}

// ilinkBot wraps the ilink client with credential management.
type ilinkBot struct {
	client        *ilink.Client
	creds         *ilink.Credentials
	contextTokens sync.Map // userID -> contextToken
	cursor        string
}

// New creates a Weixin adapter from platform config.
func New(cfg channel.PlatformConfig) (channel.Adapter, error) {
	baseURL, _ := cfg[cfgBaseURL].(string)
	if baseURL == "" {
		baseURL = ilink.DefaultBaseURL
	}
	credPath, _ := cfg[cfgCredPath].(string)
	if credPath == "" {
		credPath = ilink.DefaultCredPath()
	}
	timeoutSec, _ := cfg[cfgTimeoutSec].(int)
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	client := ilink.NewClient()
	client.HTTP.Timeout = time.Duration(timeoutSec) * time.Second

	return &Adapter{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		credPath: credPath,
		client:   client,
	}, nil
}

// Platform returns "weixin".
func (a *Adapter) Platform() string { return platformName }

// Start begins the long-poll receive loop.
// It loads stored credentials; if none exist, the adapter returns an error
// prompting the user to run `octo channel login` first.
func (a *Adapter) Start(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("weixin adapter already running")
	}
	a.running = true
	a.onMessage = onMessage
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	// Load credentials.
	creds, err := ilink.LoadCredentials(a.credPath)
	if err != nil {
		return fmt.Errorf("weixin: load credentials: %w (run `octo channel login` first)", err)
	}
	if creds == nil {
		return fmt.Errorf("weixin: no credentials found (run `octo channel login` first)")
	}

	a.bot = &ilinkBot{
		client: a.client,
		creds:  creds,
	}

	// Start long-polling for messages.
	retryDelay := time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		updates, err := a.bot.client.GetUpdates(ctx, a.bot.creds.BaseURL, a.bot.creds.Token, a.bot.cursor)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			apiErr, isAPI := err.(*ilink.APIError)
			if isAPI && apiErr.IsSessionExpired() {
				// Session expired — clear credentials and stop.
				_ = ilink.ClearCredentials(a.credPath)
				a.bot.contextTokens = sync.Map{}
				a.bot.cursor = ""
				return fmt.Errorf("weixin: session expired (run `octo channel login` again)")
			}

			time.Sleep(retryDelay)
			retryDelay = min(retryDelay*2, 10*time.Second)
			continue
		}

		if updates.GetUpdatesBuf != "" {
			a.bot.cursor = updates.GetUpdatesBuf
		}
		retryDelay = time.Second

		for _, rawMsg := range updates.Msgs {
			var wire ilink.WireMessage
			if err := json.Unmarshal(rawMsg, &wire); err != nil {
				continue
			}
			a.bot.rememberContext(&wire)
			incoming := parseMessage(&wire)
			if incoming == nil {
				continue
			}
			ev := channel.InboundEvent{
				Type:         "message",
				Platform:     platformName,
				ChatID:       incoming.UserID,
				UserID:       incoming.UserID,
				Text:         incoming.Text,
				MessageID:    fmt.Sprintf("%d", wire.MessageID),
				ChatType:     "direct",
				ContextToken: incoming.ContextToken,
				Raw:          incoming.Raw,
			}
			if a.onMessage != nil {
				a.onMessage(ev)
			}
		}
	}
}

// Stop shuts down the adapter.
func (a *Adapter) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	return nil
}

// SendText sends a text message to a user.
func (a *Adapter) SendText(chatID, text, replyTo string) channel.SendResult {
	if a.bot == nil || a.bot.creds == nil {
		return channel.SendResult{OK: false, Error: "not logged in"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ct, ok := a.bot.contextTokens.Load(chatID)
	if !ok {
		return channel.SendResult{OK: false, Error: "no context_token for user " + chatID}
	}

	chunks := chunkText(text, 4000)
	var lastErr error
	for _, chunk := range chunks {
		msg := ilink.BuildTextMessage(chatID, ct.(string), chunk)
		if err := a.bot.client.SendMessage(ctx, a.bot.creds.BaseURL, a.bot.creds.Token, msg); err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return channel.SendResult{OK: false, Error: lastErr.Error()}
	}
	return channel.SendResult{OK: true}
}

// SendFile sends a file hint (full file upload not yet implemented).
func (a *Adapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	return a.SendText(chatID, fmt.Sprintf("📎 File: %s", name), replyTo)
}

// UpdateMessage is not supported by Weixin iLink.
func (a *Adapter) UpdateMessage(chatID, messageID, text string) bool { return false }

// SupportsMessageUpdates returns false.
func (a *Adapter) SupportsMessageUpdates() bool { return false }

// ValidateConfig checks required fields.
func (a *Adapter) ValidateConfig(cfg channel.PlatformConfig) []string {
	return nil // Weixin iLink uses credentials file, not config fields.
}

// rememberContext stores the context_token for a user.
func (b *ilinkBot) rememberContext(wire *ilink.WireMessage) {
	userID := wire.FromUserID
	if wire.MessageType == ilink.MessageTypeBot {
		userID = wire.ToUserID
	}
	if userID != "" && wire.ContextToken != "" {
		b.contextTokens.Store(userID, wire.ContextToken)
	}
}

// parseMessage converts a WireMessage to an IncomingMessage.
func parseMessage(wire *ilink.WireMessage) *ilink.IncomingMessage {
	if wire.MessageType != ilink.MessageTypeUser {
		return nil
	}

	msg := &ilink.IncomingMessage{
		UserID:       wire.FromUserID,
		Text:         extractText(wire.ItemList),
		Type:         detectType(wire.ItemList),
		Timestamp:    time.UnixMilli(wire.CreateTimeMs),
		Raw:          wire,
		ContextToken: wire.ContextToken,
	}

	for _, item := range wire.ItemList {
		if item.ImageItem != nil {
			msg.Images = append(msg.Images, ilink.ImageContent{
				Media: item.ImageItem.Media, ThumbMedia: item.ImageItem.ThumbMedia,
				AESKey: item.ImageItem.AESKey, URL: item.ImageItem.URL,
				Width: item.ImageItem.ThumbWidth, Height: item.ImageItem.ThumbHeight,
			})
		}
		if item.VoiceItem != nil {
			msg.Voices = append(msg.Voices, ilink.VoiceContent{
				Media: item.VoiceItem.Media, Text: item.VoiceItem.Text,
				DurationMs: item.VoiceItem.Playtime, EncodeType: item.VoiceItem.EncodeType,
			})
		}
		if item.FileItem != nil {
			size, _ := strconv.ParseInt(item.FileItem.Len, 10, 64)
			msg.Files = append(msg.Files, ilink.FileContent{
				Media: item.FileItem.Media, FileName: item.FileItem.FileName,
				MD5: item.FileItem.MD5, Size: size,
			})
		}
		if item.VideoItem != nil {
			msg.Videos = append(msg.Videos, ilink.VideoContent{
				Media: item.VideoItem.Media, ThumbMedia: item.VideoItem.ThumbMedia,
				DurationMs: item.VideoItem.PlayLength,
			})
		}
		if item.RefMsg != nil {
			q := &ilink.QuotedMessage{Title: item.RefMsg.Title}
			if item.RefMsg.MessageItem != nil && item.RefMsg.MessageItem.TextItem != nil {
				q.Text = item.RefMsg.MessageItem.TextItem.Text
			}
			msg.QuotedMessage = q
		}
	}

	return msg
}

func detectType(items []ilink.MessageItem) ilink.ContentType {
	if len(items) == 0 {
		return ilink.ContentText
	}
	switch items[0].Type {
	case ilink.ItemImage:
		return ilink.ContentImage
	case ilink.ItemVoice:
		return ilink.ContentVoice
	case ilink.ItemFile:
		return ilink.ContentFile
	case ilink.ItemVideo:
		return ilink.ContentVideo
	default:
		return ilink.ContentText
	}
}

func extractText(items []ilink.MessageItem) string {
	var parts []string
	for _, item := range items {
		switch item.Type {
		case ilink.ItemText:
			if item.TextItem != nil {
				parts = append(parts, item.TextItem.Text)
			}
		case ilink.ItemImage:
			if item.ImageItem != nil && item.ImageItem.URL != "" {
				parts = append(parts, item.ImageItem.URL)
			} else {
				parts = append(parts, "[image]")
			}
		case ilink.ItemVoice:
			if item.VoiceItem != nil && item.VoiceItem.Text != "" {
				parts = append(parts, item.VoiceItem.Text)
			} else {
				parts = append(parts, "[voice]")
			}
		case ilink.ItemFile:
			if item.FileItem != nil {
				parts = append(parts, fmt.Sprintf("[file: %s]", item.FileItem.FileName))
			} else {
				parts = append(parts, "[file]")
			}
		case ilink.ItemVideo:
			parts = append(parts, "[video]")
		}
	}
	return strings.Join(parts, " ")
}

func chunkText(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		chunks = append(chunks, text[:maxLen])
		text = text[maxLen:]
	}
	return chunks
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
