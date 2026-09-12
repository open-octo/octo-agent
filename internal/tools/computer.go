package tools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig on the JPEG re-encode NewImageBlock produces
	_ "image/png"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/computer"
	"github.com/open-octo/octo-agent/internal/config"
)

// computerPlatform reports whether this OS has a computer-use substrate
// (macOS and Windows; see internal/computer).
func computerPlatform() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// computerEnabled gates advertising of the tool: the experimental
// tools.computer.enabled switch (default off). Follows browserEnabled's
// pattern — hidden from the model's tool list when off, still dispatchable.
// The switch is also ignored on platforms without a substrate: the Settings
// API already refuses the write there, and a hand-edited yaml must not
// advertise a tool whose every call fails with ErrUnsupported. (A darwin
// build without CGO still advertises it — its ErrUnsupported names the
// missing CGO, which is the actionable message in that case.)
func computerEnabled() bool {
	if !computerPlatform() {
		return false
	}
	cfg, _ := config.LoadCached()
	return strings.EqualFold(strings.TrimSpace(cfg.Tools.Computer.Enabled), "on")
}

// ComputerTool is agentic desktop computer-use: the model sees the screen via
// screenshot and acts on it with synthesized mouse/keyboard input, or reads
// and drives the app's accessibility tree. This is the live
// screenshot→decide→act loop (not record/replay). Substrates exist for macOS
// (CGO) and Windows (pure Win32 / UI Automation) — see internal/computer;
// elsewhere every action returns computer.ErrUnsupported.
type ComputerTool struct{}

var computerActions = []string{
	"screenshot", "left_click", "right_click", "double_click", "mouse_move",
	"type", "key", "scroll", "ax_tree", "ax_press", "ax_set",
}

func (ComputerTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "computer",
		Description: "See and operate the desktop (macOS or Windows) directly, two channels: " +
			"(1) accessibility (PREFERRED when the app exposes elements): ax_tree reads an app's UI " +
			"as a semantic list (role + label + frame, each prefixed with an e<N> id) — macOS " +
			"Accessibility or Windows UI Automation — and ax_press/ax_set act on an element by that id " +
			"(reliable — works even when the label is empty or repeated, e.g. ten sliders all labelled " +
			"\"0\") or by role+label matching; these work on BACKGROUNDED apps with no cursor move and no " +
			"focus change, and are far more precise than pixel guessing. (2) pixels: screenshot the main " +
			"display, then click/move/type/key/scroll by coordinate — needed for custom-drawn UIs (games, " +
			"CAD) with no accessibility tree; this channel shares the user's cursor and focus, so pass " +
			"\"app\" on left_click/right_click/double_click/type/key to bring that app to the foreground " +
			"first — otherwise Octo's own window can hold focus and the input silently lands nowhere. " +
			"Use for tasks in native apps with no CLI/API (the browser tool covers anything web). Pixel " +
			"workflow: screenshot FIRST, act, screenshot again to verify. On macOS this requires the " +
			"Accessibility (input) and Screen Recording (capture) permissions granted to the app running " +
			"octo — an action failing with a permission error means the grant is missing, not that the " +
			"action was wrong. On Windows there is no permission dialog, but apps running as " +
			"administrator ignore input from a non-elevated octo.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": computerActions,
					"description": "ax_tree: dump the target app's accessibility tree, each line prefixed \"e<N>\" " +
						"(start here for AX-rich apps). " +
						"ax_press: press the element given by id (preferred) or whose label matches (button/menu " +
						"item/square...). " +
						"ax_set: set a text field's string or a slider's number, by id or label. " +
						"screenshot: capture the screen. left_click/right_click/double_click/mouse_move at (x, y); " +
						"pass \"app\" to focus that app first. " +
						"type: type text at the current focus; pass \"app\" to focus it first. " +
						"key: press a key or combo like \"enter\", \"cmd+c\"; pass \"app\" to focus it first. " +
						"scroll: scroll at the cursor by (dx, dy) lines, positive dy = down.",
				},
				"app":       map[string]any{"type": "string", "description": "Target app name. Required for ax_* actions; optional for left_click/right_click/double_click/type/key to bring that app to the foreground before acting (its own window may otherwise steal focus and swallow the input silently — reported result includes the frontmost app afterward so a miss is visible). macOS: the process name as shown in its menu bar, e.g. \"国际象棋\", \"Safari\". Windows: the executable name without .exe (e.g. \"notepad\") or a substring of the window title. The app must have at least one on-screen (non-minimized) window."},
				"id":        map[string]any{"type": "string", "description": "Element id for ax_press/ax_set, copied verbatim from ax_tree's \"e<N>\" prefix (e.g. \"e12\" or \"12\"). Preferred over role+label — the only way to address an element whose label is empty or shared with others. Only valid against a tree dumped with the same max_depth; re-dump ax_tree if the app's UI may have changed since."},
				"role":      map[string]any{"type": "string", "description": "Accessibility role filter for ax_press/ax_set when matching by label (ignored when id is given) — pass what ax_tree shows, e.g. \"AXButton\", \"AXMenuItem\", \"AXSlider\" on macOS or \"Button\", \"MenuItem\", \"Edit\" on Windows (the AX prefix is optional on both). Optional but strongly recommended to disambiguate."},
				"label":     map[string]any{"type": "string", "description": "Element label to match for ax_press/ax_set when id is not given: exact label wins, otherwise case-insensitive substring of title/description/value. Copy labels verbatim from ax_tree output."},
				"value":     map[string]any{"type": "string", "description": "Value to set (ax_set): a number for sliders/steppers, a string for text fields."},
				"max_depth": map[string]any{"type": "number", "description": "Tree depth limit for ax_tree (default 12; smaller = shorter output). Also pass the same value to ax_press/ax_set when addressing by id if a non-default depth was used for the dump — otherwise indices can shift."},
				"x":         map[string]any{"type": "number", "description": "X coordinate in screenshot pixels (click/move)."},
				"y":         map[string]any{"type": "number", "description": "Y coordinate in screenshot pixels (click/move)."},
				"text":      map[string]any{"type": "string", "description": "Text to type (type action)."},
				"key":       map[string]any{"type": "string", "description": "Key or combo, e.g. \"enter\", \"tab\", \"escape\", \"backspace\", \"delete\", \"cmd+c\", \"ctrl+shift+s\" (key action). cmd is the Command key on macOS and the Windows key on Windows — use ctrl for Windows shortcuts."},
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
		if err := activateAppArg(input); err != nil {
			return agent.ToolResult{}, err
		}
		return computerClick("left", 1, input)
	case "right_click":
		if err := activateAppArg(input); err != nil {
			return agent.ToolResult{}, err
		}
		return computerClick("right", 1, input)
	case "double_click":
		if err := activateAppArg(input); err != nil {
			return agent.ToolResult{}, err
		}
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
		if err := activateAppArg(input); err != nil {
			return agent.ToolResult{}, err
		}
		text := stringArg(input, "text")
		if err := computer.TypeText(text); err != nil {
			return agent.ToolResult{}, err
		}
		msg := fmt.Sprintf("typed %d characters", len([]rune(text)))
		if front := computer.FrontmostAppName(); front != "" {
			msg += fmt.Sprintf(" — frontmost app is now %q (make sure that's the intended target)", front)
		}
		return agent.ToolResult{Text: msg}, nil
	case "key":
		if err := requireTrusted(); err != nil {
			return agent.ToolResult{}, err
		}
		if err := activateAppArg(input); err != nil {
			return agent.ToolResult{}, err
		}
		if err := computer.Press(stringArg(input, "key")); err != nil {
			return agent.ToolResult{}, err
		}
		msg := "pressed " + stringArg(input, "key")
		if front := computer.FrontmostAppName(); front != "" {
			msg += fmt.Sprintf(" — frontmost app is now %q (make sure that's the intended target)", front)
		}
		return agent.ToolResult{Text: msg}, nil
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
		if id, has, err := parseElementID(input); err != nil {
			return agent.ToolResult{}, err
		} else if has {
			maxDepth := int(numArg(input, "max_depth"))
			if err := computer.AXPressByID(pid, maxDepth, id); err != nil {
				return agent.ToolResult{}, err
			}
			return agent.ToolResult{Text: fmt.Sprintf("pressed element e%d — re-dump ax_tree (or screenshot) to verify the result", id)}, nil
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
		value := stringArg(input, "value")
		if id, has, err := parseElementID(input); err != nil {
			return agent.ToolResult{}, err
		} else if has {
			maxDepth := int(numArg(input, "max_depth"))
			if err := computer.AXSetValueByID(pid, maxDepth, id, value); err != nil {
				return agent.ToolResult{}, err
			}
			return agent.ToolResult{Text: fmt.Sprintf("set element e%d to %s — re-dump ax_tree to verify", id, value)}, nil
		}
		label := stringArg(input, "label")
		if err := computer.AXSetValue(pid, stringArg(input, "role"), label, value); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "set element matching " + label + " to " + value + " — re-dump ax_tree to verify"}, nil
	default:
		return agent.ToolResult{}, fmt.Errorf("computer: unknown action %q (valid: %v)", action, computerActions)
	}
}

// activateAppArg brings the optional "app" argument's window to the
// foreground before a pixel-channel click/type/key — Octo's own window can
// otherwise hold focus and silently swallow the input (see
// computer.ActivateApp). A no-op when "app" is omitted, so existing calls
// without it behave exactly as before.
func activateAppArg(input map[string]any) error {
	app := stringArg(input, "app")
	if app == "" {
		return nil
	}
	if err := requireTrusted(); err != nil {
		return err
	}
	w, err := computer.FindWindow(app)
	if err != nil {
		return err
	}
	if err := computer.ActivateApp(w.PID); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond) // let the window manager finish raising it before input follows
	return nil
}

// parseElementID reads the optional "id" argument for ax_press/ax_set,
// tolerating the "e12" string ax_tree prints, a bare "12", or a JSON number.
func parseElementID(input map[string]any) (id int, has bool, err error) {
	raw, ok := input["id"]
	if !ok || raw == nil {
		return 0, false, nil
	}
	switch v := raw.(type) {
	case float64:
		return int(v), true, nil
	case int:
		return v, true, nil
	case string:
		s := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "e"), "E")
		n, perr := strconv.Atoi(s)
		if perr != nil {
			return 0, true, fmt.Errorf("computer: bad element id %q (want e.g. \"e12\" or 12)", v)
		}
		return n, true, nil
	default:
		return 0, true, fmt.Errorf("computer: bad element id %v", raw)
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

// axStructuralRoles are pure layout/grouping roles that carry no meaning of
// their own when unlabeled — spacer groups, panes, split views. Interactive
// controls (buttons, fields, sliders, menu items, static text...) are always
// shown even unlabeled: their e<N> id is often the ONLY way to address them
// (ten sliders all labelled "0", an empty-label text area), which is the
// whole point of numbering every element.
var axStructuralRoles = map[string]bool{
	"AXGroup": true, "AXUnknown": true, "AXSplitGroup": true,
	"AXScrollArea": true, "AXLayoutArea": true, "AXLayoutItem": true,
	"AXGenericElement": true,
	"Group":            true, "Pane": true, "Custom": true,
}

// computerAXTree renders the app's accessibility tree as an indented digest.
// Each line is prefixed with its e<N> id — N is the element's position in
// AXTree's returned slice, stable for ax_press/ax_set(id=...) as long as the
// same pid + max_depth is used and the app's UI hasn't changed since.
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
	shown := 0
	for i, e := range els {
		label := e.Label()
		if label == "" && !computer.SameRole(e.Role, "AXWindow") && axStructuralRoles[e.Role] {
			continue // anonymous layout groups are noise to the model
		}
		for d := 0; d < e.Depth; d++ {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "e%d %s %q [%.0f,%.0f %.0fx%.0f]\n", i, e.Role, label, e.X, e.Y, e.W, e.H)
		shown++
	}
	fmt.Fprintf(&b, "(%d of %d elements shown; pass id=e<N> — preferred — or role+label verbatim to ax_press/ax_set)", shown, len(els))
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
