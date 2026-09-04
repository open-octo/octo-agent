package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ContentBlock is a single element of a multi-part message. It unifies the
// roles a block can play in an LLM conversation:
//
//   - "text"        — plain assistant or user text
//   - "tool_use"    — the model requesting a tool call (assistant turn)
//   - "tool_result" — the result of a tool call (user turn)
//   - "thinking"    — a reasoning model's extended-thinking trace (assistant turn)
//   - "image"       — an image for multimodal model consumption (user turn)
//
// The zero value is not valid; use the New*Block helpers instead.
type ContentBlock struct {
	// Type distinguishes the block variant: "text", "tool_use", "tool_result",
	// "thinking", "image".
	Type string `json:"type"`

	// Text is the text payload (type=="text").
	Text string `json:"text,omitempty"`

	// ID is the unique call identifier supplied by the model (type=="tool_use").
	// ToolUseID on a tool_result block must match the ID of the corresponding
	// tool_use block.
	ID string `json:"id,omitempty"`

	// Name is the tool name the model wants to invoke (type=="tool_use").
	Name string `json:"name,omitempty"`

	// Input is the parsed argument map the model passes to the tool
	// (type=="tool_use"). Keys and value types are defined by the tool's
	// JSON Schema Parameters.
	Input map[string]any `json:"input,omitempty"`

	// InputError is set on a tool_use block whose arguments arrived as
	// malformed JSON — an unescaped newline or quote inside a string, or a
	// call truncated at max_tokens. Input is then an empty map (never nil, so
	// the block still round-trips to the provider as `"input": {}`), and the
	// agent loop answers the call with this message instead of running the
	// tool: the model must learn its JSON was broken, not go hunting for a
	// "missing" parameter.
	InputError string `json:"input_error,omitempty"`

	// ToolUseID links this result back to its originating tool_use block
	// (type=="tool_result"). Must equal the ID field of the paired block.
	ToolUseID string `json:"tool_use_id,omitempty"`

	// Result is the textual output of the tool execution (type=="tool_result").
	Result string `json:"result,omitempty"`

	// IsError signals that the tool execution failed (type=="tool_result").
	// The LLM can inspect Result for the error message and recover gracefully.
	IsError bool `json:"is_error,omitempty"`

	// UI is an optional structured rendering of the result for UI consumers
	// (type=="tool_result"). Persisted with the session so history replay can
	// render rich result cards. Provider adapters build their wire payloads
	// field-by-field and never serialise this — it is invisible to the model.
	UI any `json:"ui,omitempty"`

	// Thinking is the reasoning trace text (type=="thinking"). Anthropic-protocol
	// reasoning models (Claude, Kimi k2.6) return it as a first-class content
	// block that must be preserved and replayed on subsequent requests when tool
	// use is in play, or the API rejects the follow-up.
	Thinking string `json:"thinking,omitempty"`

	// Signature authenticates a thinking block (type=="thinking"). It must be
	// sent back verbatim alongside the thinking text on the next request.
	Signature string `json:"signature,omitempty"`

	// Reasoning carries an OpenAI-protocol thinking model's reasoning trace that
	// must be echoed back on the next request (type=="tool_use"). deepseek-v4
	// returns reasoning_content alongside a tool call and rejects the follow-up
	// unless it's resent; the OpenAI adapter stashes it here so it round-trips
	// through history. Providers that don't need it ignore the field.
	Reasoning string `json:"reasoning,omitempty"`

	// Image carries vision-model image data (type=="image"). Used when a tool
	// result includes an image that should be rendered by a multimodal model
	// (Claude, GPT-4o, Kimi k2.6, etc.). The provider adapter serialises it to
	// the vendor-specific wire format (Anthropic base64 source, OpenAI data URL).
	Image *ImageData `json:"-"`

	// ImagePath points at a persisted on-disk copy of Image (type=="image").
	// Image bytes are never serialised into the session transcript; a block
	// saved with a path is rehydrated from it by LoadSession so a resumed
	// conversation can re-send the image to the provider.
	ImagePath string `json:"image_path,omitempty"`

	// ImageDescription is the vision helper's rendering of this image as text
	// (type=="image"), for primary models that can't accept image input. It is
	// filled lazily by the pre-send transform (see describeImages) and persists
	// with the session; non-empty means "already described", and the helper is
	// never called for this block again — the block is its own cache.
	//
	// Only a successful description is stored. The failure fallback text goes
	// into the outgoing snapshot alone, so this field never holds an apology.
	ImageDescription string `json:"image_description,omitempty"`

	// ImageDescFailures counts consecutive description failures for this block
	// (type=="image"). At visionHelperMaxFailures the block stops calling the
	// helper for the rest of the session, so a dead endpoint costs one timeout
	// per image rather than one per turn. LoadSession resets it to zero, which
	// is what gives a resumed session a fresh budget once the endpoint is
	// fixed; it is serialised only so an in-process Save/Load round-trip
	// doesn't lose the count mid-session.
	ImageDescFailures int `json:"image_desc_failures,omitempty"`
}

// ImageData holds raw image bytes and their MIME type for multimodal uploads.
type ImageData struct {
	MIMEType string // e.g. "image/jpeg", "image/png"
	Data     []byte // raw file bytes
}

// NewTextBlock creates a ContentBlock with Type=="text".
func NewTextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// NewToolUseBlock creates a ContentBlock with Type=="tool_use".
// id must be unique within the conversation turn (supplied by the LLM).
func NewToolUseBlock(id, name string, input map[string]any) ContentBlock {
	return ContentBlock{
		Type:  "tool_use",
		ID:    id,
		Name:  name,
		Input: input,
	}
}

// NewToolUseBlockFromJSON builds a tool_use block from the raw argument JSON a
// provider streamed. Every adapter used to `_ = json.Unmarshal` here and hand
// the tool a nil map, so a model that emitted broken JSON was told "path is
// required" and retried the identical broken call. Now the parse failure is
// kept on the block (InputError) with the offending spot quoted, and Input is
// an empty — not nil — map so the block is still valid on the wire.
func NewToolUseBlockFromJSON(id, name, raw string) ContentBlock {
	b := NewToolUseBlock(id, name, map[string]any{})
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return b
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		b.InputError = describeInputError(raw, err)
		return b
	}
	if input != nil { // "null" parses cleanly into a nil map; keep the empty one
		b.Input = input
	}
	return b
}

// describeInputError turns a json error into something the model can act on:
// the decoder's message, the bytes around the failure, and a truncation hint
// when the text simply stops before the closing brace.
func describeInputError(raw string, err error) string {
	msg := err.Error()
	var se *json.SyntaxError
	if errors.As(err, &se) {
		off := int(se.Offset)
		lo, hi := max(0, off-40), min(len(raw), off+20)
		msg = fmt.Sprintf("%s near byte %d: …%s…", se.Error(), off, strings.ToValidUTF8(raw[lo:hi], "?"))
	}
	if !strings.HasSuffix(raw, "}") {
		msg += " (the arguments end without a closing brace — the call may have been truncated by the output token limit)"
	}
	return msg
}

// NewThinkingBlock creates a ContentBlock with Type=="thinking". The signature
// authenticates the trace and must be preserved for the round-trip.
func NewThinkingBlock(thinking, signature string) ContentBlock {
	return ContentBlock{
		Type:      "thinking",
		Thinking:  thinking,
		Signature: signature,
	}
}

// NewToolResultBlock creates a ContentBlock with Type=="tool_result".
// toolUseID must match the ID of the corresponding tool_use block.
// isError should be true when the tool execution failed; result carries the
// error message in that case.
func NewToolResultBlock(toolUseID, result string, isError bool) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Result:    result,
		IsError:   isError,
	}
}

// NewImageBlock creates a ContentBlock with Type=="image" for multimodal
// model consumption. The provider adapter is responsible for converting this
// to the vendor-specific wire format. The bytes are normalized on the way in
// (see compressImageData): oversized captures are downscaled and re-encoded
// so every attach path — clipboard, composer, IM, tool results — stays under
// provider size limits without each caller re-implementing it.
//
// ok is false when the bytes aren't an image format the providers accept, in
// which case there is no block to send and the caller should describe the
// content in text instead. Sending one anyway costs the whole turn: the media
// type travels verbatim into Anthropic's `source.media_type` and OpenAI's data
// URL, and an unsupported value fails the entire request, not just the image.
//
// The format is decided by sniffing the bytes, not by the caller's mimeType.
// Callers get that string from somewhere untrustworthy — a file extension, or
// an MCP server that names whatever it likes (including nothing) — and a wrong
// label is indistinguishable from a wrong image until the provider rejects it.
func NewImageBlock(mimeType string, data []byte) (ContentBlock, bool) {
	sniffed := sniffImageType(data)
	if sniffed == "" {
		return ContentBlock{}, false
	}
	// Trust the bytes over the label. compressImageData may re-encode to JPEG,
	// which is itself supported, so the result stays valid either way.
	outType, outData := compressImageData(sniffed, data)
	if !modelImageTypes[outType] {
		return ContentBlock{}, false
	}
	return ContentBlock{
		Type:  "image",
		Image: &ImageData{MIMEType: outType, Data: outData},
	}, true
}

// modelImageTypes are the formats every provider adapter can put on the wire.
// Anthropic documents exactly these four; OpenAI accepts a superset, so the
// intersection is what's safe to send without branching per vendor.
var modelImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// sniffImageType identifies an image from its magic bytes, returning "" for
// anything that isn't one of the provider-supported formats. Hand-rolled
// rather than using http.DetectContentType because only these four matter and
// the decision needs to be exact — DetectContentType also reports formats
// (bmp, tiff) the providers reject, which would just move the problem.
//
// Deliberately not attempting conversion for the rejects: the standard library
// decodes png, jpeg and gif only, which are already supported, so there is no
// bmp/tiff/heic/svg case that could be re-encoded without a new dependency.
func sniffImageType(data []byte) string {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff":
		return "image/jpeg"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	// RIFF container: bytes 8..12 name the payload, "WEBP" for an image.
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}
