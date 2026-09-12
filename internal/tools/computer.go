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

// computerPlatform reports whether this OS has a computer-use substrate at all
// (macOS and Windows; see internal/computer). It is deliberately weaker than
// computer.Supported(): a macOS build without CGO still counts here, because
// the tool keeps advertising there and its ErrUnsupported names the missing
// CGO, which is the actionable message in that case. Only platforms with no
// implementation whatsoever are excluded (the Settings API refuses the write
// there, and a hand-edited yaml must not advertise a tool whose every call
// fails).
func computerPlatform() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// computerEnabled gates advertising of the tool: the experimental
// tools.computer.enabled switch (default off). Follows browserEnabled's
// pattern — hidden from the model's tool list when off, still dispatchable.
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
	"drag", "type", "key", "scroll", "ax_tree", "ax_press", "ax_set",
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
			"CAD) with no accessibility tree, or continuous pointer motion (drag: sliders, crop boxes, " +
			"mask brushes) that a press-release pair at one point can't do; this channel shares the " +
			"user's cursor and focus, so pass \"app\" on left_click/right_click/double_click/drag/type/key " +
			"to bring that app to the foreground first — otherwise Octo's own window can hold focus and " +
			"the input silently lands nowhere. " +
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
						"(start here for AX-rich apps; windows only by default, pass menu_bar=true to browse the " +
						"menu bar instead). " +
						"ax_press: press the element given by id (preferred) or whose label matches (button/menu " +
						"item/square...). " +
						"ax_set: set a text field's string or a slider's number, by id or label. " +
						"screenshot: capture the screen. left_click/right_click/double_click/mouse_move at (x, y); " +
						"pass \"app\" to focus that app first. " +
						"drag: press-and-hold at (x, y), move to (x2, y2) with interpolated intermediate motion, " +
						"then release — for sliders, crop boxes, and mask brushes that a click can't operate; " +
						"pass \"app\" to focus that app first. " +
						"type: type text at the current focus; pass \"app\" to focus it first. " +
						"key: press a key or combo like \"enter\", \"cmd+c\"; pass \"app\" to focus it first. " +
						"scroll: scroll at the cursor by (dx, dy) lines, positive dy = down.",
				},
				"app":       map[string]any{"type": "string", "description": "Target app name. Required for ax_* actions; optional for left_click/right_click/double_click/drag/type/key to bring that app to the foreground before acting (its own window may otherwise steal focus and swallow the input silently — reported result includes the frontmost app afterward so a miss is visible). macOS: the process name as shown in its menu bar, e.g. \"国际象棋\", \"Safari\". Windows: the executable name without .exe (e.g. \"notepad\") or a substring of the window title. The app must have at least one on-screen (non-minimized) window."},
				"id":        map[string]any{"type": "string", "description": "Element id for ax_press/ax_set, copied verbatim from ax_tree's \"e<N>\" prefix (e.g. \"e12\" or \"12\"). Preferred over role+label — the only way to address an element whose label is empty or shared with others. Only valid against a tree dumped with the same max_depth AND menu_bar; re-dump ax_tree if the app's UI may have changed since."},
				"menu_bar":  map[string]any{"type": "boolean", "description": "ax_tree: dump the app's menu bar INSTEAD of its windows (default false = windows only). A chatty app's global menu can be hundreds of AXMenuItem entries that would otherwise dominate the digest, so it's opt-in — pass true to browse menu items (e.g. before clicking \"File > Save As…\"), then dump again with menu_bar=false to go back to the windows. ax_press/ax_set: pass the same menu_bar the dump used when addressing by id."},
				"role":      map[string]any{"type": "string", "description": "Accessibility role filter for ax_press/ax_set when matching by label (ignored when id is given) — pass what ax_tree shows, e.g. \"AXButton\", \"AXMenuItem\", \"AXSlider\" on macOS or \"Button\", \"MenuItem\", \"Edit\" on Windows (the AX prefix is optional on both). Optional but strongly recommended to disambiguate."},
				"label":     map[string]any{"type": "string", "description": "Element label to match for ax_press/ax_set when id is not given: exact label wins, otherwise case-insensitive substring of title/description/value. Copy labels verbatim from ax_tree output."},
				"value":     map[string]any{"type": "string", "description": "Value to set (ax_set): a number for sliders/steppers, a string for text fields."},
				"max_depth": map[string]any{"type": "number", "description": "Tree depth limit for ax_tree (default 12; smaller = shorter output). Also pass the same value to ax_press/ax_set when addressing by id if a non-default depth was used for the dump — otherwise indices can shift."},
				"x":         map[string]any{"type": "number", "description": "X coordinate in screenshot pixels (click/move); drag's start point."},
				"y":         map[string]any{"type": "number", "description": "Y coordinate in screenshot pixels (click/move); drag's start point."},
				"x2":        map[string]any{"type": "number", "description": "X coordinate in screenshot pixels — drag's end point (required for drag, ignored otherwise)."},
				"y2":        map[string]any{"type": "number", "description": "Y coordinate in screenshot pixels — drag's end point (required for drag, ignored otherwise)."},
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
	case "drag":
		if err := requireTrusted(); err != nil {
			return agent.ToolResult{}, err
		}
		if err := activateAppArg(input); err != nil {
			return agent.ToolResult{}, err
		}
		x1, y1 := mapCoord(numArg(input, "x"), numArg(input, "y"))
		x2, y2 := mapCoord(numArg(input, "x2"), numArg(input, "y2"))
		if err := computer.Drag(x1, y1, x2, y2); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("dragged (%.0f, %.0f) to (%.0f, %.0f) — screenshot to verify the result", x1, y1, x2, y2)}, nil
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
			e, err := computer.AXPressByID(pid, maxDepth, boolArg(input, "menu_bar"), id)
			if err != nil {
				return agent.ToolResult{}, err
			}
			return agent.ToolResult{Text: fmt.Sprintf("pressed element e%d%s — re-dump ax_tree (or screenshot) to verify the result", id, axDigestSuffix(e))}, nil
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
			e, err := computer.AXSetValueByID(pid, maxDepth, boolArg(input, "menu_bar"), id, value)
			if err != nil {
				return agent.ToolResult{}, err
			}
			return agent.ToolResult{Text: fmt.Sprintf("set element e%d%s to %s — re-dump ax_tree to verify", id, axDigestSuffix(e), value)}, nil
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
// AXTree's returned slice, stable for ax_press/ax_set(id=..., menu_bar=...)
// as long as the same pid + max_depth + menu_bar is used and the app's UI
// hasn't changed since. menu_bar=false (default) dumps the app's windows;
// menu_bar=true dumps the menu bar INSTEAD (not in addition to) — a chatty
// app's global menu can be hundreds of items that would otherwise dominate
// the digest for no benefit, since the model rarely needs it.
func computerAXTree(input map[string]any) (agent.ToolResult, error) {
	pid, err := computerAppPid(input)
	if err != nil {
		return agent.ToolResult{}, err
	}
	depth := int(numArg(input, "max_depth"))
	menuBar := boolArg(input, "menu_bar")
	els, err := computer.AXTree(pid, depth, menuBar)
	if err != nil {
		return agent.ToolResult{}, err
	}
	return agent.ToolResult{Text: renderAXTree(els)}, nil
}

// renderAXTree formats an already-fetched digest — split out from
// computerAXTree so the one invariant that matters (the printed e<N> is
// always the element's true index into els, never a display-only counter,
// even though some elements are hidden from the printed text by
// axStructuralRoles) is unit-testable without the AX/UIA substrate. Silently
// switching the "e%d" to the shown-so-far count instead of the loop index i
// would make every id-addressed ax_press/ax_set land on the wrong element
// with no error — this is the one line that must never regress.
func renderAXTree(els []computer.AXElement) string {
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
		// AXElement's frame is in the platform's native input space (points on
		// macOS, physical pixels on Windows — see internal/computer's package
		// doc); unmapCoord converts it into the same image-pixel space
		// screenshot/click/move use, so a coordinate copied from here needs no
		// manual scale-factor math before being passed to left_click/mouse_move.
		x, y := unmapCoord(e.X, e.Y)
		w, h := unmapCoord(e.W, e.H)
		fmt.Fprintf(&b, "e%d %s %q [%.0f,%.0f %.0fx%.0f]\n", i, e.Role, label, x, y, w, h)
		shown++
	}
	fmt.Fprintf(&b, "(%d of %d elements shown; pass id=e<N> — preferred — or role+label verbatim to ax_press/ax_set; coordinates are in screenshot image pixels)", shown, len(els))
	if !shotScaleKnown() {
		b.WriteString("\n⚠ no screenshot taken yet — these coordinates assume 1:1 scale and may be off; take a screenshot first for pixel-accurate values")
	}
	return b.String()
}

// axDigestSuffix renders a matched element's role+label for an id-addressed
// ax_press/ax_set success message, e.g. " (AXButton \"Save\")" — "" when both
// are empty (nothing informative to add).
func axDigestSuffix(e computer.AXElement) string {
	label := e.Label()
	if e.Role == "" && label == "" {
		return ""
	}
	return fmt.Sprintf(" (%s %q)", e.Role, label)
}

// shotScale converts model coordinates — pixels of the last screenshot as
// actually sent to the provider, which compressImageData may have downscaled
// from the raw capture — into the platform's native input space (logical
// points on macOS, physical pixels on Windows). Process-global by design:
// assumes one display and one conversation driving the screen at a time.
// known is false until the first screenshot computes a real ratio; ax_tree
// uses it to warn instead of silently assuming 1:1 when asked for pixel
// coordinates before any screenshot has been taken.
var shotScale = struct {
	sync.Mutex
	x, y  float64
	known bool
}{x: 1, y: 1}

func computerScreenshot(ctx context.Context) (agent.ToolResult, error) {
	if err := requireSubstrate(); err != nil {
		return agent.ToolResult{}, err
	}
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
		shotScale.known = true
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

// mapCoord converts a model-supplied image pixel into the platform's native
// input space (logical points on macOS, physical pixels on Windows).
func mapCoord(x, y float64) (float64, float64) {
	shotScale.Lock()
	defer shotScale.Unlock()
	return x * shotScale.x, y * shotScale.y
}

// unmapCoord is mapCoord's inverse: native input space back into the image
// pixels screenshot/click/move speak, so ax_tree can report frame coordinates
// the model can paste straight into a click/move without the manual
// scale-factor arithmetic the tool used to leave to it (screenshot's image
// may be downscaled from the raw Retina/DPI capture — see shotScale).
func unmapCoord(x, y float64) (float64, float64) {
	shotScale.Lock()
	defer shotScale.Unlock()
	sx, sy := shotScale.x, shotScale.y
	if sx == 0 {
		sx = 1
	}
	if sy == 0 {
		sy = 1
	}
	return x / sx, y / sy
}

// shotScaleKnown reports whether a screenshot has computed a real scale
// factor yet — before that, unmapCoord silently assumes 1:1, which is wrong
// on any Retina/DPI-scaled display.
func shotScaleKnown() bool {
	shotScale.Lock()
	defer shotScale.Unlock()
	return shotScale.known
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

// requireSubstrate rejects an action in a build with no native implementation
// before any permission gate can misreport the cause. A stub has no grant to
// fetch, so "Screen Recording / Accessibility not granted" would send the user
// to System Settings for a switch that cannot help — every action of such a
// build would fail there, forever.
func requireSubstrate() error {
	if !computer.Supported() {
		return computer.ErrUnsupported
	}
	return nil
}

// requireTrusted gates every input-synthesis action on the macOS Accessibility
// grant, with an actionable error instead of silently dropping events.
func requireTrusted() error {
	if err := requireSubstrate(); err != nil {
		return err
	}
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
