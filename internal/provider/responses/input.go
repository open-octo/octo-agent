package responses

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// toInput converts agent history into Responses input items. model is the
// model this request targets: reasoning items are replayed only when the same
// model produced them, since their encrypted content is opaque to any other.
func toInput(model string, in []agent.Message) []any {
	out := make([]any, 0, len(in))
	for _, m := range in {
		switch {
		case m.Role == agent.RoleSystem:
			continue
		case m.Role == agent.RoleAssistant && len(m.Blocks) > 0:
			out = appendAssistantBlocks(out, model, m.Blocks)
		case m.Role == agent.RoleUser && len(m.Blocks) > 0:
			out = appendUserBlocks(out, m.Blocks)
		default:
			out = append(out, inputMessage{Role: string(m.Role), Content: m.Content})
		}
	}
	return out
}

func appendAssistantBlocks(out []any, model string, blocks []agent.ContentBlock) []any {
	for i, b := range blocks {
		switch b.Type {
		case "thinking":
			if replayableReasoning(model, blocks, i) {
				out = append(out, reasoningItem{
					Type:             "reasoning",
					ID:               b.ID,
					Summary:          []summaryPart{},
					EncryptedContent: b.Signature,
				})
			}
		case "text":
			if b.Text != "" {
				out = append(out, inputMessage{Role: "assistant", Content: b.Text, Phase: b.Phase})
			}
		case "tool_use":
			out = append(out, functionCallItem{
				Type:      "function_call",
				CallID:    b.ID,
				Name:      b.Name,
				Arguments: marshalInput(b.Input),
			})
		}
	}
	return out
}

// replayableReasoning reports whether blocks[i] is a reasoning item this
// request can send back: it came from the Responses API (has an id — an
// Anthropic thinking block has none), it carries encrypted content, the same
// model produced it, and the output it led to is still in the message after
// it. Anything else is dropped; the model just loses that turn's reasoning.
func replayableReasoning(model string, blocks []agent.ContentBlock, i int) bool {
	b := blocks[i]
	if b.ID == "" || b.Signature == "" || b.Model != model {
		return false
	}
	for _, next := range blocks[i+1:] {
		if next.Type == "tool_use" || (next.Type == "text" && next.Text != "") {
			return true
		}
	}
	return false
}

// appendUserBlocks emits each tool_result as a function_call_output item, then
// any text and images as one trailing user message — function_call_output is
// sent as a plain string, so images a tool produced ride on that user message
// the same way the Chat Completions adapter handles them.
func appendUserBlocks(out []any, blocks []agent.ContentBlock) []any {
	var text strings.Builder
	var images []inputPart
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			out = append(out, functionCallOutputItem{
				Type:   "function_call_output",
				CallID: b.ToolUseID,
				Output: b.Result,
			})
		case "text":
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(b.Text)
		case "image":
			if b.Image != nil {
				images = append(images, inputPart{
					Type:     "input_image",
					ImageURL: fmt.Sprintf("data:%s;base64,%s", b.Image.MIMEType, base64.StdEncoding.EncodeToString(b.Image.Data)),
					Detail:   "auto",
				})
			}
		}
	}
	switch {
	case len(images) > 0:
		parts := make([]inputPart, 0, 1+len(images))
		if text.Len() > 0 {
			parts = append(parts, inputPart{Type: "input_text", Text: text.String()})
		}
		parts = append(parts, images...)
		out = append(out, inputMessage{Role: "user", Content: parts})
	case text.Len() > 0:
		out = append(out, inputMessage{Role: "user", Content: text.String()})
	}
	return out
}

// marshalInput encodes tool input as a JSON string; nil or empty becomes "{}".
func marshalInput(input map[string]any) string {
	if len(input) == 0 {
		return "{}"
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(b)
}
