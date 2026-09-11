package tools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig on the JPEG re-encode NewImageBlock produces
	_ "image/png"
	"sync"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/computer"
)

// ComputerTool is agentic desktop computer-use: the model sees the screen via
// screenshot and acts on it with synthesized mouse/keyboard input. This is the
// live screenshot→decide→act loop (not record/replay) — the substrate is
// macOS-only behind CGO (see internal/computer); elsewhere every action
// returns computer.ErrUnsupported.
type ComputerTool struct{}

var computerActions = []string{
	"screenshot", "left_click", "right_click", "double_click", "mouse_move",
	"type", "key", "scroll",
}

func (ComputerTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "computer",
		Description: "See and operate the macOS desktop directly: screenshot the main display, then " +
			"click, move, type, press keys, and scroll by pixel coordinate. Use for tasks in native apps " +
			"with no CLI/API (the browser tool covers anything web). Workflow: screenshot FIRST to see " +
			"the current state, act, then screenshot again to verify — never chain actions blind. " +
			"Coordinates are in the screenshot's own pixel space (logical points, origin top-left of the " +
			"main display); the screenshot result states the width×height. Requires macOS Accessibility " +
			"(input) and Screen Recording (capture) permissions granted to the app running octo — an " +
			"action failing with a permission error means the grant is missing, not that the action was wrong.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": computerActions,
					"description": "screenshot: capture the screen (always start here and re-verify after acting). " +
						"left_click/right_click/double_click: click at (x, y). mouse_move: hover at (x, y). " +
						"type: type text at the current focus. key: press a key or combo like \"enter\", \"cmd+c\", \"ctrl+shift+tab\". " +
						"scroll: scroll at the cursor by (dx, dy) lines, positive dy = down.",
				},
				"x":    map[string]any{"type": "number", "description": "X coordinate in screenshot pixels (click/move)."},
				"y":    map[string]any{"type": "number", "description": "Y coordinate in screenshot pixels (click/move)."},
				"text": map[string]any{"type": "string", "description": "Text to type (type action)."},
				"key":  map[string]any{"type": "string", "description": "Key or combo, e.g. \"enter\", \"tab\", \"escape\", \"cmd+c\", \"cmd+shift+s\" (key action)."},
				"dx":   map[string]any{"type": "number", "description": "Horizontal scroll lines, positive = right (scroll)."},
				"dy":   map[string]any{"type": "number", "description": "Vertical scroll lines, positive = down (scroll)."},
			},
			"required": []string{"action"},
		},
	}
}

func (ComputerTool) Execute(ctx context.Context, name string, input map[string]any) (agent.ToolResult, error) {
	action := stringArg(input, "action")
	switch action {
	case "screenshot":
		return computerScreenshot(ctx)
	case "left_click":
		return computerClick("left", 1, input)
	case "right_click":
		return computerClick("right", 1, input)
	case "double_click":
		return computerClick("left", 2, input)
	case "mouse_move":
		if err := requireTrusted(); err != nil {
			return agent.ToolResult{}, err
		}
		x, y := mapCoord(numArg(input, "x"), numArg(input, "y"))
		if err := computer.MoveTo(x, y); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("cursor moved to (%.0f, %.0f)", x, y)}, nil
	case "type":
		if err := requireTrusted(); err != nil {
			return agent.ToolResult{}, err
		}
		text := stringArg(input, "text")
		if err := computer.TypeText(text); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("typed %d characters", len([]rune(text)))}, nil
	case "key":
		if err := requireTrusted(); err != nil {
			return agent.ToolResult{}, err
		}
		if err := computer.Press(stringArg(input, "key")); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "pressed " + stringArg(input, "key")}, nil
	case "scroll":
		if err := requireTrusted(); err != nil {
			return agent.ToolResult{}, err
		}
		if err := computer.Scroll(numArg(input, "dx"), numArg(input, "dy")); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("scrolled (%.0f, %.0f)", numArg(input, "dx"), numArg(input, "dy"))}, nil
	default:
		return agent.ToolResult{}, fmt.Errorf("computer: unknown action %q (valid: %v)", action, computerActions)
	}
}

// shotScale converts model coordinates — pixels of the last screenshot as
// actually sent to the provider, which compressImageData may have downscaled
// from the raw capture — into the logical-point space CGEvent posts in.
// Spike simplification: process-global, assumes one display and one
// conversation driving the screen at a time.
var shotScale = struct {
	sync.Mutex
	x, y float64
}{x: 1, y: 1}

func computerScreenshot(ctx context.Context) (agent.ToolResult, error) {
	if !computer.ScreenCaptureAllowed() {
		computer.RequestScreenCapture() // pop the system grant dialog
		return agent.ToolResult{}, fmt.Errorf("computer: Screen Recording permission not granted — a system dialog may have appeared; grant it to the app running octo in System Settings → Privacy & Security → Screen Recording, then retry (the app may need a restart for the grant to take effect)")
	}
	png, err := computer.Screenshot()
	if err != nil {
		return agent.ToolResult{}, err
	}
	path := saveScreenshot(png)

	// The model's coordinate space is the image IT sees. NewImageBlock
	// normalizes (downscale + JPEG) for the provider, so measure the block's
	// final bytes, not the raw capture, and remember the mapping to points.
	sent := png
	var blk agent.ContentBlock
	var haveBlock bool
	if ImagesAllowed(ctx) {
		blk, haveBlock = agent.NewImageBlock("image/png", png)
		if haveBlock && blk.Image != nil {
			sent = blk.Image.Data
		}
	}
	aw, ah := 0, 0
	if cfg, _, derr := image.DecodeConfig(bytes.NewReader(sent)); derr == nil {
		aw, ah = cfg.Width, cfg.Height
	}
	if pw, ph, serr := computer.ScreenSize(); serr == nil && aw > 0 && ah > 0 {
		shotScale.Lock()
		shotScale.x, shotScale.y = pw/float64(aw), ph/float64(ah)
		shotScale.Unlock()
	}

	coords := fmt.Sprintf("the attached image is %dx%d px — express all click/move coordinates in these image pixels, origin top-left", aw, ah)
	if !ImagesAllowed(ctx) {
		return agent.ToolResult{Text: "screenshot saved to " + path + " (the current model can't view images and no vision_helper is configured — computer-use needs a vision-capable model)"}, nil
	}
	res := agent.ToolResult{Text: "screenshot saved to " + path + "; " + coords}
	if haveBlock {
		res.Blocks = []agent.ContentBlock{blk}
	}
	return res, nil
}

// mapCoord converts a model-supplied image pixel into logical points.
func mapCoord(x, y float64) (float64, float64) {
	shotScale.Lock()
	defer shotScale.Unlock()
	return x * shotScale.x, y * shotScale.y
}

func computerClick(button string, clicks int, input map[string]any) (agent.ToolResult, error) {
	if err := requireTrusted(); err != nil {
		return agent.ToolResult{}, err
	}
	x, y := mapCoord(numArg(input, "x"), numArg(input, "y"))
	if err := computer.Click(button, x, y, clicks); err != nil {
		return agent.ToolResult{}, err
	}
	verb := button + " clicked"
	if clicks == 2 {
		verb = "double clicked"
	}
	return agent.ToolResult{Text: fmt.Sprintf("%s (%.0f, %.0f) — screenshot to verify the result", verb, x, y)}, nil
}

// requireTrusted gates every input-synthesis action on the macOS Accessibility
// grant, with an actionable error instead of silently dropping events.
func requireTrusted() error {
	if !computer.Trusted() {
		computer.RequestAccessibility() // pop the system grant dialog
		return fmt.Errorf("computer: Accessibility permission not granted — input would be dropped silently. A system dialog may have appeared; grant it to the app running octo in System Settings → Privacy & Security → Accessibility, then retry")
	}
	return nil
}

// numArg reads a numeric argument, tolerating the JSON decode producing
// float64 (or int in hand-rolled callers).
func numArg(input map[string]any, key string) float64 {
	switch v := input[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}
