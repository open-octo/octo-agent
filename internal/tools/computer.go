package tools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig on the JPEG re-encode NewImageBlock produces
	_ "image/png"
	"runtime"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/computer"
	"github.com/open-octo/octo-agent/internal/config"
)

// computerEnabled gates advertising of the tool: the experimental
// tools.computer.enabled switch (default off). Follows browserEnabled's
// pattern — hidden from the model's tool list when off, still dispatchable.
// The substrate is macOS-only, so the switch is also ignored off-darwin: the
// Settings API already refuses the write there, and a hand-edited yaml must
// not advertise a tool whose every call fails with ErrUnsupported. (A darwin
// build without CGO still advertises it — its ErrUnsupported names the
// missing CGO, which is the actionable message in that case.)
func computerEnabled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	cfg, _ := config.LoadCached()
	return strings.EqualFold(strings.TrimSpace(cfg.Tools.Computer.Enabled), "on")
}

// ComputerTool is agentic desktop computer-use: the model sees the screen via
// screenshot and acts on it with synthesized mouse/keyboard input. This is the
// live screenshot→decide→act loop (not record/replay) — the substrate is
// macOS-only behind CGO (see internal/computer); elsewhere every action
// returns computer.ErrUnsupported.
type ComputerTool struct{}

var computerActions = []string{
	"screenshot", "left_click", "right_click", "double_click", "mouse_move",
	"type", "key", "scroll", "ax_tree", "ax_press", "ax_set",
}

func (ComputerTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "computer",
		Description: "See and operate the macOS desktop directly, two channels: " +
			"(1) accessibility (PREFERRED when the app exposes elements): ax_tree reads an app's UI " +
			"as a semantic list (role + label + frame), ax_press/ax_set act by label — these work on " +
			"BACKGROUNDED apps with no cursor move and no focus change, and are far more precise than " +
			"pixel guessing. (2) pixels: screenshot the main display, then click/move/type/key/scroll by " +
			"coordinate — needed for custom-drawn UIs (games, CAD) with no accessibility tree; this " +
			"channel shares the user's cursor and focus. Use for tasks in native apps with no CLI/API " +
			"(the browser tool covers anything web). Pixel workflow: screenshot FIRST, act, screenshot " +
			"again to verify. Requires macOS Accessibility (input) and Screen Recording (capture) " +
			"permissions granted to the app running octo — an action failing with a permission error " +
			"means the grant is missing, not that the action was wrong.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": computerActions,
					"description": "ax_tree: dump the target app's accessibility tree (start here for AX-rich apps). " +
						"ax_press: press the element whose label matches (button/menu item/square...). " +
						"ax_set: set a text field's string or a slider's number. " +
						"screenshot: capture the screen. left_click/right_click/double_click/mouse_move at (x, y). " +
						"type: type text at the current focus. key: press a key or combo like \"enter\", \"cmd+c\". " +
						"scroll: scroll at the cursor by (dx, dy) lines, positive dy = down.",
				},
				"app":       map[string]any{"type": "string", "description": "Target app name for ax_* actions — the process name as shown in its menu bar, e.g. \"国际象棋\", \"Safari\". The app must have at least one on-screen (non-minimized) window."},
				"role":      map[string]any{"type": "string", "description": "Accessibility role filter for ax_press/ax_set, e.g. \"AXButton\", \"AXMenuItem\", \"AXSlider\", \"AXTextField\". Optional but strongly recommended — pass what ax_tree shows to disambiguate."},
				"label":     map[string]any{"type": "string", "description": "Element label to match for ax_press/ax_set: exact label wins, otherwise case-insensitive substring of title/description/value. Copy labels verbatim from ax_tree output."},
				"value":     map[string]any{"type": "string", "description": "Value to set (ax_set): a number for sliders/steppers, a string for text fields."},
				"max_depth": map[string]any{"type": "number", "description": "Tree depth limit for ax_tree (default 12; smaller = shorter output)."},
				"x":         map[string]any{"type": "number", "description": "X coordinate in screenshot pixels (click/move)."},
				"y":         map[string]any{"type": "number", "description": "Y coordinate in screenshot pixels (click/move)."},
				"text":      map[string]any{"type": "string", "description": "Text to type (type action)."},
				"key":       map[string]any{"type": "string", "description": "Key or combo, e.g. \"enter\", \"tab\", \"escape\", \"cmd+c\", \"cmd+shift+s\" (key action)."},
				"dx":        map[string]any{"type": "number", "description": "Horizontal scroll lines, positive = right (scroll)."},
				"dy":        map[string]any{"type": "number", "description": "Vertical scroll lines, positive = down (scroll)."},
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
	case "ax_tree":
		return computerAXTree(input)
	case "ax_press":
		pid, err := computerAppPid(input)
		if err != nil {
			return agent.ToolResult{}, err
		}
		label := stringArg(input, "label")
		if err := computer.AXPress(pid, stringArg(input, "role"), label); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "pressed element matching " + label + " — re-dump ax_tree (or screenshot) to verify the result"}, nil
	case "ax_set":
		pid, err := computerAppPid(input)
		if err != nil {
			return agent.ToolResult{}, err
		}
		label := stringArg(input, "label")
		if err := computer.AXSetValue(pid, stringArg(input, "role"), label, stringArg(input, "value")); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "set element matching " + label + " to " + stringArg(input, "value") + " — re-dump ax_tree to verify"}, nil
	default:
		return agent.ToolResult{}, fmt.Errorf("computer: unknown action %q (valid: %v)", action, computerActions)
	}
}

// computerAppPid resolves the ax_* actions' app argument to a pid. Going
// through the window list means the app needs one on-screen window — a
// known limitation, called out in the error.
func computerAppPid(input map[string]any) (int, error) {
	if err := requireTrusted(); err != nil {
		return 0, err
	}
	app := stringArg(input, "app")
	if app == "" {
		return 0, fmt.Errorf(`computer: ax_* actions need the "app" parameter (target app's process name)`)
	}
	w, err := computer.FindWindow(app)
	if err != nil {
		return 0, err
	}
	return w.PID, nil
}

// computerAXTree renders the app's accessibility tree as an indented digest.
func computerAXTree(input map[string]any) (agent.ToolResult, error) {
	pid, err := computerAppPid(input)
	if err != nil {
		return agent.ToolResult{}, err
	}
	depth := int(numArg(input, "max_depth"))
	els, err := computer.AXTree(pid, depth)
	if err != nil {
		return agent.ToolResult{}, err
	}
	var b strings.Builder
	for _, e := range els {
		label := e.Label()
		if label == "" && e.Role != "AXWindow" {
			continue // anonymous spacer groups are noise to the model
		}
		for i := 0; i < e.Depth; i++ {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%s %q [%.0f,%.0f %.0fx%.0f]\n", e.Role, label, e.X, e.Y, e.W, e.H)
	}
	fmt.Fprintf(&b, "(%d elements; pass role+label verbatim to ax_press/ax_set)", len(els))
	return agent.ToolResult{Text: b.String()}, nil
}

// shotScale converts model coordinates — pixels of the last screenshot as
// actually sent to the provider, which compressImageData may have downscaled
// from the raw capture — into the logical-point space CGEvent posts in.
// Process-global by design: assumes one display and one conversation
// driving the screen at a time.
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
