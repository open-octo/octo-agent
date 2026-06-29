package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// point is a viewport coordinate.
type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// elementCenter scrolls an element into view and returns its viewport-center
// point, or an error if the selector matches nothing.
func (p *Page) elementCenter(ctx context.Context, selector string) (point, error) {
	expr := fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return null;
		el.scrollIntoView({ block: 'center', inline: 'center' });
		const r = el.getBoundingClientRect();
		return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
	})()`, jsString(selector))
	var pt *point
	if err := p.Eval(ctx, expr, &pt); err != nil {
		return point{}, err
	}
	if pt == nil {
		return point{}, fmt.Errorf("click: selector %q matched nothing", selector)
	}
	return *pt, nil
}

// Click performs a trusted browser-level click at the element's center.
// Trusted input (vs a JS .click()) counts as a real user gesture, which some
// flows — file dialogs, download buttons — require, and is less detectable.
func (p *Page) Click(ctx context.Context, selector string) error {
	pt, err := p.elementCenter(ctx, selector)
	if err != nil {
		return err
	}
	for _, typ := range []string{"mousePressed", "mouseReleased"} {
		_, err := p.cli.call(ctx, p.sessionID, "Input.dispatchMouseEvent", map[string]any{
			"type":       typ,
			"x":          pt.X,
			"y":          pt.Y,
			"button":     "left",
			"clickCount": 1,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// TypeText focuses the element matched by selector and types text into it.
func (p *Page) TypeText(ctx context.Context, selector, text string) error {
	focus := fmt.Sprintf(`(() => { const el = document.querySelector(%s); if (!el) return false; el.focus(); return true; })()`, jsString(selector))
	var ok bool
	if err := p.Eval(ctx, focus, &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("type: selector %q matched nothing", selector)
	}
	_, err := p.cli.call(ctx, p.sessionID, "Input.insertText", map[string]any{"text": text})
	return err
}

// modifierBits maps modifier names to the CDP modifier bitmask.
var modifierBits = map[string]int{"alt": 1, "ctrl": 2, "control": 2, "meta": 4, "cmd": 4, "command": 4, "shift": 8}

// namedKeys maps a few common keys to their CDP descriptors.
var namedKeys = map[string]struct {
	key, code string
	vk        int
}{
	"enter":     {"Enter", "Enter", 13},
	"return":    {"Enter", "Enter", 13},
	"escape":    {"Escape", "Escape", 27},
	"esc":       {"Escape", "Escape", 27},
	"tab":       {"Tab", "Tab", 9},
	"backspace": {"Backspace", "Backspace", 8},
	"delete":    {"Delete", "Delete", 46},
	"space":     {" ", "Space", 32},
	"arrowdown": {"ArrowDown", "ArrowDown", 40},
	"arrowup":   {"ArrowUp", "ArrowUp", 38},
}

// Key presses a single key or modifier+key combo, e.g. "enter", "escape",
// "ctrl+a", "cmd+shift+s".
func (p *Page) Key(ctx context.Context, combo string) error {
	parts := strings.Split(strings.ToLower(combo), "+")
	mods := 0
	keyName := parts[len(parts)-1]
	for _, m := range parts[:len(parts)-1] {
		bit, ok := modifierBits[strings.TrimSpace(m)]
		if !ok {
			return fmt.Errorf("key: unknown modifier %q", m)
		}
		mods |= bit
	}

	var key, code string
	var vk int
	if nk, ok := namedKeys[strings.TrimSpace(keyName)]; ok {
		key, code, vk = nk.key, nk.code, nk.vk
	} else if len(keyName) == 1 {
		c := keyName[0]
		key = keyName
		code = "Key" + strings.ToUpper(keyName)
		vk = int(strings.ToUpper(keyName)[0])
		_ = c
	} else {
		return fmt.Errorf("key: unsupported key %q", keyName)
	}

	for _, typ := range []string{"keyDown", "keyUp"} {
		_, err := p.cli.call(ctx, p.sessionID, "Input.dispatchKeyEvent", map[string]any{
			"type":                  typ,
			"modifiers":             mods,
			"key":                   key,
			"code":                  code,
			"windowsVirtualKeyCode": vk,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Scroll dispatches a wheel event. If selector is empty it scrolls at the
// viewport origin.
func (p *Page) Scroll(ctx context.Context, selector string, dx, dy float64) error {
	pt := point{X: 10, Y: 10}
	if selector != "" {
		var err error
		if pt, err = p.elementCenter(ctx, selector); err != nil {
			return err
		}
	}
	_, err := p.cli.call(ctx, p.sessionID, "Input.dispatchMouseEvent", map[string]any{
		"type":   "mouseWheel",
		"x":      pt.X,
		"y":      pt.Y,
		"deltaX": dx,
		"deltaY": dy,
	})
	return err
}

// Screenshot captures the current viewport as PNG bytes.
func (p *Page) Screenshot(ctx context.Context) ([]byte, error) {
	res, err := p.cli.call(ctx, p.sessionID, "Page.captureScreenshot", map[string]any{"format": "png"})
	if err != nil {
		return nil, err
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}

// AXTree returns the full accessibility tree as raw CDP node JSON — the Tier-2
// semantic layer used for target resolution and verification.
func (p *Page) AXTree(ctx context.Context) (json.RawMessage, error) {
	// Enable is idempotent; ignore its error so a re-enable is harmless.
	_, _ = p.cli.call(ctx, p.sessionID, "Accessibility.enable", nil)
	res, err := p.cli.call(ctx, p.sessionID, "Accessibility.getFullAXTree", nil)
	if err != nil {
		return nil, err
	}
	return res, nil
}
