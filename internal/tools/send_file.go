package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// ChannelFileSender delivers a local file to the IM chat the current turn is
// bound to. It is implemented by the server (wrapping the platform adapter's
// SendFile) and stamped into the turn ctx via WithChannelSender — the same
// ctx-scoping pattern as Asker, since only IM turns have a chat to push to.
type ChannelFileSender interface {
	// SendFile uploads path to the bound chat under the display name name
	// (empty name falls back to the file's basename). The adapter picks the
	// wire type (image / video / file) from the extension.
	SendFile(path, name string) error
}

// ctxKeyChannelSender carries the turn-scoped ChannelFileSender.
type ctxKeyChannelSender struct{}

// WithChannelSender stamps the file sender for the duration of an IM turn.
func WithChannelSender(ctx context.Context, s ChannelFileSender) context.Context {
	return context.WithValue(ctx, ctxKeyChannelSender{}, s)
}

// channelSenderFrom resolves the file sender for this turn, or nil when the
// turn is not bound to an IM chat (CLI / TUI / Web).
func channelSenderFrom(ctx context.Context) ChannelFileSender {
	if s, ok := ctx.Value(ctxKeyChannelSender{}).(ChannelFileSender); ok && s != nil {
		return s
	}
	return nil
}

// SendFileTool delivers a local file to a user through an IM channel (WeChat,
// Telegram, Discord, …). A chat reply carries only text, so a generated image,
// chart, document, or any other artifact must be pushed as a file.
//
// Two modes, mirroring send_message:
//   - On an IM turn it defaults to the CURRENT chat via the turn-scoped
//     ChannelFileSender (runChannelTurns injects it with WithChannelSender).
//   - With an explicit platform + chat_id it targets ANY reachable chat via the
//     process-global ChannelMessenger — so a web/sub-agent turn can push a file
//     to the user's WeChat even though it has no chat of its own.
//
// Advertised in DefaultToolsFor whenever a messenger is registered (the server),
// so it is available on both web and IM turns; hidden in CLI/TUI.
type SendFileTool struct{}

// SendFileToolDef exposes the tool definition for callers that assemble a tool
// list manually.
func SendFileToolDef() agent.ToolDefinition { return SendFileTool{}.Definition() }

func (SendFileTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "send_file",
		Description: "Send a local file to a user over an IM channel (image, video, document, or any " +
			"file). Use this whenever the user should receive an actual file — a generated image or " +
			"chart, a screenshot, a PDF or other document, a data export. A normal reply only carries " +
			"text, so a file MUST go through this tool. The wire type (image / video / file) is chosen " +
			"automatically from the extension, so name images .png/.jpg and videos .mp4. The file must " +
			"already exist on disk; create it first (write_file, a script, a download), then send it.\n\n" +
			"Recipient: on an IM chat, omit platform/chat_id and it goes to the current chat. To send " +
			"to a DIFFERENT chat (e.g. from the web UI to the user's WeChat), pass platform + chat_id; " +
			"if you don't know the chat_id, pass just platform (or nothing) to list reachable chats.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path of the file to send.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Optional display filename shown to the user. Defaults to the file's basename.",
				},
				"platform": map[string]any{
					"type":        "string",
					"description": "Target IM platform (e.g. \"weixin\", \"telegram\"). Omit to send to the current IM chat; pass it to target another chat.",
				},
				"chat_id": map[string]any{
					"type":        "string",
					"description": "Target chat/user ID. Omit to send to the current IM chat, or (with platform) to list reachable chats.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (SendFileTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	path := strings.TrimSpace(stringArg(input, "path"))
	if path == "" {
		return agent.ToolResult{}, fmt.Errorf("send_file: path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("send_file: %w", err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("send_file: %w", err)
	}
	if fi.IsDir() {
		return agent.ToolResult{}, fmt.Errorf("send_file: %q is a directory", abs)
	}

	name := strings.TrimSpace(stringArg(input, "name"))
	if name == "" {
		name = filepath.Base(abs)
	}

	platform := strings.TrimSpace(stringArg(input, "platform"))
	chatID := strings.TrimSpace(stringArg(input, "chat_id"))

	// Explicit cross-chat target → go through the messenger.
	if platform != "" || chatID != "" {
		m := activeMessenger
		if m == nil {
			return agent.ToolResult{}, fmt.Errorf("send_file: sending to another chat is only available on the server")
		}
		if chatID == "" {
			cands := filterRecipients(m.KnownChats(), platform)
			if len(cands) == 1 {
				platform, chatID = cands[0].Platform, cands[0].ChatID
			} else {
				return listRecipients(cands, platform)
			}
		}
		if platform == "" {
			return agent.ToolResult{}, fmt.Errorf("send_file: platform is required when chat_id is given")
		}
		if err := m.SendFile(platform, chatID, abs, name); err != nil {
			return agent.ToolResult{}, fmt.Errorf("send_file: %w", err)
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Sent %s (%d bytes) to %s chat %s.", name, fi.Size(), platform, chatID),
		}, nil
	}

	// No explicit target: default to the current IM chat if this turn has one.
	if sender := channelSenderFrom(ctx); sender != nil {
		if err := sender.SendFile(abs, name); err != nil {
			return agent.ToolResult{}, fmt.Errorf("send_file: %w", err)
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Sent %s (%d bytes) to the chat.", name, fi.Size()),
		}, nil
	}

	// Web/other server turn with no target: guide the model to pick a chat.
	if m := activeMessenger; m != nil {
		return listRecipients(m.KnownChats(), "")
	}
	return agent.ToolResult{}, fmt.Errorf("send_file: no chat to send to — specify platform + chat_id, or use this from an IM chat")
}
