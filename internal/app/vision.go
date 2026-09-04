package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

// visionDescribeTimeout bounds one description call. Aligned with the MCP
// client's per-call deadline: long enough for a large screenshot on a slow
// endpoint, short enough that a black-holed endpoint doesn't hang the turn.
const visionDescribeTimeout = 60 * time.Second

// visionDescribeMaxTokens caps the helper's reply. A full-page screenshot
// transcription runs long, and a cut-off description is worse than a terse
// one, so this is generous relative to the ~200-800 tokens a typical image
// produces.
const visionDescribeMaxTokens = 4096

// visionDescriber implements agent.ImageDescriber against a configured
// vision_helper endpoint.
//
// The sender is built once: every description in the session hits the same
// endpoint with the same cache key. Whether descriptions are wanted at all is
// re-decided per turn from the agent's current model, so /model switches are
// followed without re-wiring anything.
type visionDescriber struct {
	agent  *agent.Agent
	cfg    config.Config
	sender agent.Sender
	model  string
	// buildErr, when non-nil, records why the helper's sender could not be
	// built (missing key, unknown provider). Describe fails with it instead of
	// calling anything, so the model gets a fallback line naming the fix.
	buildErr error
}

// NewVisionDescriber builds the image describer for an agent, or returns nil
// when the feature is off: no vision_helper configured, the reference doesn't
// resolve, or the model it names can't see.
//
// Nil must track exactly the condition the tool gates check
// (ResolveVisionHelper): the gates start handing image blocks to a text-only
// model on the promise that this describer will translate them. A helper that
// resolves but can't be called — no API key, sender construction failure — is
// therefore returned as a describer whose Describe errors with the reason;
// that flows into the per-image fallback text (and the retry budget stops the
// repeats), instead of raw image blocks 400-ing every turn at the provider.
func NewVisionDescriber(a *agent.Agent, cfg config.Config) agent.ImageDescriber {
	entry, ok := cfg.ResolveVisionHelper()
	if !ok {
		return nil
	}
	d := &visionDescriber{agent: a, cfg: cfg, model: entry.Model}
	apiKey := os.Getenv(VendorAPIKeyEnvVar(entry.Provider))
	if apiKey == "" {
		apiKey = entry.APIKey
	}
	// Same rule as buildClient: only a named vendor needs a key. A keyless
	// Custom endpoint (a local Ollama vision model) builds its sender below.
	if apiKey == "" && !VendorKeyOptional(entry.Provider) {
		d.buildErr = fmt.Errorf("vision helper %q has no API key — set %s or the endpoint's api_key", cfg.VisionHelper, VendorAPIKeyEnvVar(entry.Provider))
		return d
	}
	sender, err := NewSender(SenderOptions{
		Provider: entry.Provider,
		APIKey:   apiKey,
		BaseURL:  entry.BaseURL,
		Protocol: entry.Protocol,
		Headers:  entry.Headers,
		CacheKey: "vision-helper:" + entry.Model,
	})
	if err != nil {
		d.buildErr = fmt.Errorf("vision helper %q: %w", cfg.VisionHelper, err)
		return d
	}
	d.sender = sender
	return d
}

// Active reports whether the primary model needs images translated. Re-read
// every turn: a /model switch to a vision-capable model must stop the
// translation immediately, and a switch back must resume it.
func (d *visionDescriber) Active() bool {
	return !d.cfg.ModelVision(d.agent.GetModel())
}

// Describe sends one image to the helper and returns its rendering as text.
func (d *visionDescriber) Describe(ctx context.Context, img agent.ImageData) (string, error) {
	if d.buildErr != nil {
		return "", d.buildErr
	}
	ctx, cancel := context.WithTimeout(ctx, visionDescribeTimeout)
	defer cancel()

	block, ok := agent.NewImageBlock(img.MIMEType, img.Data)
	if !ok {
		return "", fmt.Errorf("unsupported image format %q", img.MIMEType)
	}
	msgs := []agent.Message{{Role: agent.RoleUser, Blocks: []agent.ContentBlock{
		agent.NewTextBlock("Describe this image."),
		block,
	}}}

	reply, err := d.sender.SendMessages(ctx, d.model, visionDescribePrompt(d.cfg.Language), msgs, visionDescribeMaxTokens)
	if err != nil {
		return "", err
	}
	desc := renderVisionDescription(reply.Content)
	if desc == "" {
		return "", fmt.Errorf("vision helper %q returned an empty description", d.model)
	}
	return desc, nil
}

// visionDescribePrompt is the helper's system prompt. Verbatim transcription
// is the point: a summary loses the error message or the button label that the
// primary model was asked about. The prose fields follow the UI language so
// the description reads naturally alongside the rest of the conversation.
func visionDescribePrompt(language string) string {
	lang := "English"
	if strings.EqualFold(strings.TrimSpace(language), "zh") {
		lang = "Chinese"
	}
	return `You are octo's vision helper. Another model cannot see images, so you are its eyes.

Transcribe the image into a single JSON object, completely and faithfully. Never summarise away text, and never invent content that is not visible.

{
  "type": "screenshot | photo | chart | document | other",
  "text_content": "every piece of text in the image, transcribed verbatim, preserving reading order; empty string if there is none",
  "elements": [
    {
      "label": "the element's text or a short description",
      "position": "top-left | top-center | top-right | middle-left | center | middle-right | bottom-left | bottom-center | bottom-right",
      "kind": "button | input | dialog | table | icon | image | link | text"
    }
  ],
  "summary": "one sentence describing the image as a whole"
}

text_content is always transcribed in the image's own language. Write "summary" and each element "label" in ` + lang + `.

Output the JSON object and nothing else.`
}

// visionDescription mirrors the schema the prompt requests.
type visionDescription struct {
	Type        string `json:"type"`
	TextContent string `json:"text_content"`
	Elements    []struct {
		Label    string `json:"label"`
		Position string `json:"position"`
		Kind     string `json:"kind"`
	} `json:"elements"`
	Summary string `json:"summary"`
}

// renderVisionDescription turns the helper's reply into the text the primary
// model reads. A reply that isn't valid JSON is used as-is rather than thrown
// away: a model that answered in plain prose still described the image, and
// discarding that would spend the retry budget on a working endpoint.
func renderVisionDescription(raw string) string {
	body := extractJSONObject(raw)
	var d visionDescription
	if body == "" || json.Unmarshal([]byte(body), &d) != nil {
		return strings.TrimSpace(raw)
	}

	var b strings.Builder
	if d.Summary != "" {
		b.WriteString(d.Summary)
	}
	if d.Type != "" {
		fmt.Fprintf(&b, "\n(image type: %s)", d.Type)
	}
	if d.TextContent != "" {
		b.WriteString("\n\nText in the image:\n")
		b.WriteString(d.TextContent)
	}
	if len(d.Elements) > 0 {
		b.WriteString("\n\nElements:")
		for _, e := range d.Elements {
			b.WriteString("\n- ")
			b.WriteString(e.Label)
			switch {
			case e.Kind != "" && e.Position != "":
				fmt.Fprintf(&b, " (%s, %s)", e.Kind, e.Position)
			case e.Kind != "":
				fmt.Fprintf(&b, " (%s)", e.Kind)
			case e.Position != "":
				fmt.Fprintf(&b, " (%s)", e.Position)
			}
		}
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		// Well-formed JSON with every field empty says nothing useful; fall
		// back to the raw reply rather than caching an empty description.
		return strings.TrimSpace(raw)
	}
	return out
}

// extractJSONObject pulls a JSON object out of a reply that may be fenced or
// wrapped in prose. Returns "" when there's no object-shaped span.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return s[i : j+1]
}
