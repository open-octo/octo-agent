package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// KnownRecipient is one IM chat the send_message tool can push to. The server
// derives the list from live channel sessions plus the persisted /bind table;
// the tool surfaces it so the model can resolve a request like "message my
// WeChat" to a concrete platform + chat_id even from a web turn with no chat
// of its own.
type KnownRecipient struct {
	Platform string `json:"platform"`
	ChatID   string `json:"chat_id"`
	UserID   string `json:"user_id,omitempty"`
	Label    string `json:"label,omitempty"`
}

// ChannelMessenger backs the send_message tool. It is implemented by the
// server (the only process that runs channel adapters) and registered via
// SetMessenger, mirroring SetRestarter: process-global, set once at server
// start, nil everywhere else so the tool stays hidden in CLI/TUI.
type ChannelMessenger interface {
	// SendMessage delivers text to chatID on platform, preferring a live
	// adapter and falling back to a one-shot send from config.
	SendMessage(platform, chatID, text string) error
	// KnownChats lists the recipients the bot can currently address.
	KnownChats() []KnownRecipient
}

var activeMessenger ChannelMessenger

// SetMessenger registers the messenger the send_message tool delegates to.
// Pass nil to disable (the tool then doesn't appear in DefaultToolsFor).
func SetMessenger(m ChannelMessenger) { activeMessenger = m }

func messengerEnabled() bool { return activeMessenger != nil }

// SendMessageTool lets the model send a plain-text message to an IM chat
// (WeChat, Telegram, Discord, …) from any turn — including a web session with
// no chat of its own. This is the proactive-push counterpart to a normal chat
// reply, which only reaches whoever the current turn is already talking to.
type SendMessageTool struct{}

func (SendMessageTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "send_message",
		Description: "Send a text message to a user or group over an IM channel (WeChat, Telegram, " +
			"Discord, Feishu, DingTalk, WeCom). Use this to proactively reach a chat that is NOT " +
			"the current conversation — e.g. the user is on the web UI and asks you to message " +
			"their WeChat. A normal reply only reaches the current chat, so a message to any other " +
			"chat MUST go through this tool.\n\n" +
			"Identify the recipient with platform + chat_id. If you don't know the chat_id, call " +
			"the tool with just the platform (or no arguments) and it returns the list of chats the " +
			"bot can reach, so you can pick one and call again. The recipient must have messaged " +
			"the bot at least once (that is how the bot learns the chat_id).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"platform": map[string]any{
					"type":        "string",
					"description": "IM platform, e.g. \"weixin\" (WeChat), \"telegram\", \"discord\", \"feishu\", \"dingtalk\", \"wecom\". Omit to list reachable chats across all platforms.",
				},
				"chat_id": map[string]any{
					"type":        "string",
					"description": "Target chat/user ID on the platform. Omit to list the reachable chats instead of sending.",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Message body to send. Required when actually sending.",
				},
			},
			"required": []string{},
		},
	}
}

func (SendMessageTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	m := activeMessenger
	if m == nil {
		return agent.ToolResult{}, fmt.Errorf("send_message: not available in this mode (server only)")
	}

	platform := strings.TrimSpace(stringArg(input, "platform"))
	chatID := strings.TrimSpace(stringArg(input, "chat_id"))
	text := strings.TrimSpace(stringArg(input, "text"))

	// No target yet, or an explicit discovery call (no text): list reachable
	// chats so the model can pick one.
	if chatID == "" || text == "" {
		chats := filterRecipients(m.KnownChats(), platform)
		// Exactly one candidate + text present → unambiguous, just send.
		if chatID == "" && text != "" && len(chats) == 1 {
			return doSend(m, chats[0].Platform, chats[0].ChatID, text)
		}
		return listRecipients(chats, platform, text)
	}

	if platform == "" {
		return agent.ToolResult{}, fmt.Errorf("send_message: platform is required when chat_id is given")
	}
	return doSend(m, platform, chatID, text)
}

func doSend(m ChannelMessenger, platform, chatID, text string) (agent.ToolResult, error) {
	if err := m.SendMessage(platform, chatID, text); err != nil {
		return agent.ToolResult{}, fmt.Errorf("send_message: %w", err)
	}
	return agent.ToolResult{
		Text: fmt.Sprintf("Message sent to %s chat %s.", platform, chatID),
	}, nil
}

func filterRecipients(all []KnownRecipient, platform string) []KnownRecipient {
	if platform == "" {
		return all
	}
	out := make([]KnownRecipient, 0, len(all))
	for _, r := range all {
		if r.Platform == platform {
			out = append(out, r)
		}
	}
	return out
}

// listRecipients returns a model-readable roster of reachable chats. It is a
// normal (non-error) result: the model reads it and calls send_message again
// with a concrete chat_id.
func listRecipients(chats []KnownRecipient, platform, text string) (agent.ToolResult, error) {
	if len(chats) == 0 {
		scope := "any platform"
		if platform != "" {
			scope = platform
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("No reachable chats on %s. The recipient must message the bot at "+
				"least once before it can push to them; ask the user to do so, or provide a chat_id.", scope),
		}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Reachable chats (%d) — call send_message again with the chosen platform and chat_id:\n", len(chats))
	for _, r := range chats {
		b.WriteString("  - platform=")
		b.WriteString(r.Platform)
		b.WriteString(" chat_id=")
		b.WriteString(r.ChatID)
		if r.Label != "" {
			b.WriteString(" (")
			b.WriteString(r.Label)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if text != "" {
		b.WriteString("\nThen resend with text set to your message.")
	}
	return agent.ToolResult{Text: strings.TrimRight(b.String(), "\n")}, nil
}
