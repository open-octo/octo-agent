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

// SendFileTool delivers a local file to the user through the active IM channel
// (WeChat, Telegram, Discord, …). It is the only way the model can put a file
// in front of an IM user: a chat reply carries text, so a generated image,
// chart, document, or any other artifact must be pushed as a file.
//
// The tool lives in allTools so DefaultRegistry can dispatch it, but it is
// withheld from the advertised tool list everywhere except IM turns — see
// DefaultToolsFor, which always skips it, and runChannelTurns, which appends
// its definition and injects the sender. On a non-IM turn channelSenderFrom
// returns nil and the tool reports a clean, model-readable error.
type SendFileTool struct{}

// SendFileToolDef is the definition runChannelTurns appends to the IM tool
// list. Exported so the server can advertise the tool without re-deriving it.
func SendFileToolDef() agent.ToolDefinition { return SendFileTool{}.Definition() }

func (SendFileTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "send_file",
		Description: "Send a local file to the user through the current chat (image, video, " +
			"document, or any file). Use this whenever the user should receive an actual file — " +
			"a generated image or chart, a screenshot, a PDF or other document, a data export. " +
			"A normal reply only carries text, so a file the user asked for MUST go through this " +
			"tool. The wire type (image / video / file) is chosen automatically from the " +
			"extension, so name images .png/.jpg and videos .mp4. The file must already exist on " +
			"disk; create it first (write_file, a script, a download), then send it.",
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
			},
			"required": []string{"path"},
		},
	}
}

func (SendFileTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	sender := channelSenderFrom(ctx)
	if sender == nil {
		return agent.ToolResult{}, fmt.Errorf("send_file: no chat to send to — this tool only works while chatting over an IM channel")
	}

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

	if err := sender.SendFile(abs, name); err != nil {
		return agent.ToolResult{}, fmt.Errorf("send_file: %w", err)
	}
	return agent.ToolResult{
		Text: fmt.Sprintf("Sent %s (%d bytes) to the chat.", name, fi.Size()),
	}, nil
}
