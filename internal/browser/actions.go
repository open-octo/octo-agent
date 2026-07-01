package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// point is a viewport coordinate.
type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// frameDelim separates a same-origin iframe selector from the element selector
// inside it, e.g. "iframe#app >>> #download". One level; cross-origin OOPIFs
// are not handled (their DOM lives in another process).
const frameDelim = " >>> "

func splitFrame(selector string) (frame, elem string) {
	if i := strings.Index(selector, frameDelim); i >= 0 {
		return selector[:i], selector[i+len(frameDelim):]
	}
	return "", selector
}

// elemRefJS builds a JS expression evaluating to the target element, piercing a
// same-origin iframe's contentDocument when a frame selector is given. The
// element selector may be plain CSS or the supported Playwright subset
// (resolveSelectorJS).
func elemRefJS(frame, elem string) string {
	if frame == "" {
		return resolveSelectorJS("document", elem)
	}
	return fmt.Sprintf("(()=>{const f=document.querySelector(%s);return f&&f.contentDocument?(%s):null;})()",
		jsString(frame), resolveSelectorJS("f.contentDocument", elem))
}

// resolveClickTarget picks the selector to act on for a click whose recorded
// element had stable visible text (anchor). Positional selectors drift, and
// worse, after a layout change can silently match the WRONG node — so the
// recorded text is the reliable signal. It polls up to timeout for, in order of
// preference: the original positional element when its text still contains the
// anchor (the unchanged-page fast path, preserving the exact original target),
// or any element carrying the anchor text (drift recovery). If neither appears
// it returns the positional selector unchanged, so a genuine miss still fails
// and reaches the healer. anchor must be non-empty.
func (p *Page) resolveClickTarget(ctx context.Context, frame, elemSel, anchor string, timeout time.Duration) string {
	posSel := elemSel
	if frame != "" {
		posSel = frame + frameDelim + elemSel
	}
	textElem := lastTagOf(elemSel) + `:has-text("` + escapeTextArg(anchor) + `")`
	textSel := textElem
	if frame != "" {
		textSel = frame + frameDelim + textElem
	}
	check := fmt.Sprintf(`(()=>{let pos=null,txt=null;try{pos=%s;}catch(e){}try{txt=%s;}catch(e){}var a=%s;if(pos&&(pos.textContent||"").trim().includes(a))return "pos";if(txt)return "text";return "";})()`,
		elemRefJS(frame, elemSel), elemRefJS(frame, textElem), jsString(anchor))

	deadline := time.Now().Add(timeout)
	for {
		var choice string
		if err := p.Eval(ctx, check, &choice); err == nil {
			switch choice {
			case "pos":
				return posSel
			case "text":
				return textSel
			}
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return posSel
		}
		select {
		case <-ctx.Done():
			return posSel
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// DismissOverlay makes one deterministic attempt to clear a blocking overlay —
// a cookie/consent banner or a modal whose backdrop intercepts clicks, the most
// common non-selector reason a replay step suddenly fails. It clicks the first
// visible control whose trimmed text or aria-label matches a small allowlist of
// dismiss/accept affordances (never anything else, so it can't perform an
// unintended destructive action), and reports whether it clicked something. No
// LLM involved; safe to call speculatively before falling back to the healer.
func (p *Page) DismissOverlay(ctx context.Context) (bool, error) {
	const expr = `(function(){
	  var ok=/^(accept|accept all|accept all cookies|agree|i agree|allow all|got it|ok|okay|close|dismiss|no thanks|continue)$/i;
	  var lab=/(close|dismiss|accept|agree)/i;
	  var els=document.querySelectorAll('button,[role="button"],a,[aria-label]');
	  for(var i=0;i<els.length;i++){var el=els[i];
	    if(!(el.offsetParent!==null||el.getClientRects().length)) continue; // visible only
	    var t=(el.textContent||'').trim();
	    var al=(el.getAttribute&&el.getAttribute('aria-label')||'').trim();
	    if(ok.test(t) || (al && lab.test(al) && al.length<40)){ try{ el.click(); return true; }catch(e){} }
	  }
	  return false;
	})()`
	var clicked bool
	if err := p.Eval(ctx, expr, &clicked); err != nil {
		return false, err
	}
	return clicked, nil
}

// resolveFieldTarget picks the selector to act on for a type/select/upload step
// whose recorded field carried an accessible-name hint (placeholder/name/
// aria-label/id or its <label> text). Form fields have no visible textContent, so
// the click text-anchor can't steer them; this is the field analogue. It polls up
// to timeout for, in order: the original positional element (unchanged-page fast
// path, exact original target), or any input/select/textarea whose accessible name
// equals the hint (drift recovery), returning a freshly generated selector for it.
// If neither appears it returns the positional selector unchanged, so a genuine
// miss still fails and reaches the healer. hint must be non-empty.
func (p *Page) resolveFieldTarget(ctx context.Context, frame, elemSel, hint string, timeout time.Duration) string {
	posSel := elemSel
	if frame != "" {
		posSel = frame + frameDelim + elemSel
	}
	doc := "document"
	if frame != "" {
		doc = fmt.Sprintf("(document.querySelector(%s)||{}).contentDocument", jsString(frame))
	}
	check := fmt.Sprintf(`(()=>{
	  var pos=null; try{pos=%s;}catch(e){}
	  if(pos) return {mode:"pos"};
	  var h=%s; var d=%s; if(!d) return {mode:"none"};
	  function sel(el){
	    if(el.id) return '#'+CSS.escape(el.id);
	    for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'="'+CSS.escape(v)+'"]';}
	    var parts=[],node=el,depth=0;
	    while(node&&node.nodeType===1&&node.tagName!=='BODY'&&depth<6){if(node.id){parts.unshift('#'+CSS.escape(node.id));return parts.join(' > ');}var part=node.tagName.toLowerCase();var p=node.parentElement;if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;});if(same.length>1)part+=':nth-of-type('+(same.indexOf(node)+1)+')';}parts.unshift(part);node=p;depth++;}
	    return parts.join(' > ');
	  }
	  var els=d.querySelectorAll('input,select,textarea,[contenteditable="true"],[contenteditable=""]');
	  for(var i=0;i<els.length;i++){var el=els[i];
	    var lbl=''; try{ if(el.labels&&el.labels.length) lbl=(el.labels[0].textContent||'').trim(); }catch(e){}
	    if(el.placeholder===h||el.name===h||el.id===h||(el.getAttribute&&el.getAttribute('aria-label')===h)||lbl===h){ return {mode:"match", sel:sel(el)}; }
	  }
	  return {mode:"none"};
	})()`, elemRefJS(frame, elemSel), jsString(hint), doc)

	deadline := time.Now().Add(timeout)
	for {
		var res struct {
			Mode string `json:"mode"`
			Sel  string `json:"sel"`
		}
		if err := p.Eval(ctx, check, &res); err == nil {
			switch {
			case res.Mode == "pos":
				return posSel
			case res.Mode == "match" && res.Sel != "":
				if frame != "" {
					return frame + frameDelim + res.Sel
				}
				return res.Sel
			}
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return posSel
		}
		select {
		case <-ctx.Done():
			return posSel
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// elementCenter scrolls an element into view and returns its viewport-center
// point (adding the iframe's offset for framed targets), or an error if the
// selector matches nothing.
func (p *Page) elementCenter(ctx context.Context, selector string) (point, error) {
	frame, elem := splitFrame(selector)
	offset := ""
	if frame != "" {
		offset = fmt.Sprintf(`{const f=document.querySelector(%s); if(f){const fr=f.getBoundingClientRect(); ox=fr.x; oy=fr.y;}}`, jsString(frame))
	}
	expr := fmt.Sprintf(`(() => {
		let el;
		try { el = %s; } catch (e) { return { badSelector: true }; }
		if (!el) return null;
		el.scrollIntoView({ block: 'center', inline: 'center' });
		const r = el.getBoundingClientRect();
		let ox = 0, oy = 0;
		%s
		return { x: ox + r.x + r.width / 2, y: oy + r.y + r.height / 2 };
	})()`, elemRefJS(frame, elem), offset)
	var res *struct {
		X, Y        float64
		BadSelector bool `json:"badSelector"`
	}
	if err := p.Eval(ctx, expr, &res); err != nil {
		return point{}, err
	}
	if res == nil {
		return point{}, fmt.Errorf("selector %q matched nothing", selector)
	}
	if res.BadSelector {
		return point{}, fmt.Errorf("invalid selector %q — use plain CSS or a supported Playwright form (:has-text(\"…\"), text=…, :visible, xpath=…); run the observe action to list valid selectors", selector)
	}
	return point{X: res.X, Y: res.Y}, nil
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

// Hover moves the pointer over an element with a real (trusted) mouse move.
// Synthetic JS events can't trigger CSS :hover reveals — only a genuine pointer
// move does, which is what this dispatches.
func (p *Page) Hover(ctx context.Context, selector string) error {
	pt, err := p.elementCenter(ctx, selector)
	if err != nil {
		return err
	}
	_, err = p.cli.call(ctx, p.sessionID, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved",
		"x":    pt.X,
		"y":    pt.Y,
	})
	return err
}

// SelectOption picks an <option> of a native <select> by its value, visible
// text, or label, then fires input+change so framework bindings react. Native
// select popups are browser-drawn (not DOM), so this is the reliable path.
func (p *Page) SelectOption(ctx context.Context, selector, value string) error {
	frame, elem := splitFrame(selector)
	expr := fmt.Sprintf(`(() => {
		const el = %s;
		if (!el || el.tagName !== 'SELECT') return 'no-select';
		let idx = -1;
		for (let i = 0; i < el.options.length; i++) {
			const o = el.options[i];
			if (o.value === %s || o.text === %s || o.label === %s) { idx = i; break; }
		}
		if (idx < 0) return 'no-option';
		el.selectedIndex = idx;
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
		return 'ok';
	})()`, elemRefJS(frame, elem), jsString(value), jsString(value), jsString(value))
	var status string
	if err := p.Eval(ctx, expr, &status); err != nil {
		return err
	}
	switch status {
	case "ok":
		return nil
	case "no-select":
		return fmt.Errorf("select: %q is not a <select> element", selector)
	case "no-option":
		return fmt.Errorf("select: no option matching %q in %s", value, selector)
	default:
		return fmt.Errorf("select: unexpected status %q", status)
	}
}

// TypeText focuses the element matched by selector and types text into it.
func (p *Page) TypeText(ctx context.Context, selector, text string) error {
	frame, elem := splitFrame(selector)
	focus := fmt.Sprintf(`(() => { const el = %s; if (!el) return false; el.focus(); return true; })()`, elemRefJS(frame, elem))
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

// fieldNonEmpty reports whether a form field currently holds any value/text —
// the post-type sanity check, since programmatic input can be silently swallowed
// by a disabled, re-rendering, or framework-controlled input. It checks
// non-empty (not an exact match) so masked/formatted inputs that transform the
// typed value aren't mistaken for a failure.
func (p *Page) fieldNonEmpty(ctx context.Context, selector string) bool {
	frame, elem := splitFrame(selector)
	expr := fmt.Sprintf(`(()=>{const el=%s; if(!el) return false; const v=('value' in el)?el.value:el.textContent; return !!(v&&(''+v).length);})()`, elemRefJS(frame, elem))
	var ok bool
	_ = p.Eval(ctx, expr, &ok)
	return ok
}

// clearField empties an input/textarea/contenteditable and fires input so
// framework bindings reset, before a re-type retry.
func (p *Page) clearField(ctx context.Context, selector string) {
	frame, elem := splitFrame(selector)
	expr := fmt.Sprintf(`(()=>{const el=%s; if(!el) return; if('value' in el){el.value='';}else{el.textContent='';} el.dispatchEvent(new Event('input',{bubbles:true})); el.focus();})()`, elemRefJS(frame, elem))
	_ = p.Eval(ctx, expr, nil)
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

// Upload sets the files on a file <input> matched by selector, without the OS
// file-picker dialog (CDP DOM.setFileInputFiles). Paths should be absolute.
// Resolving the input by JS object (rather than a DOM nodeId) lets it pierce a
// same-origin iframe via the same frame convention as the other actions.
func (p *Page) Upload(ctx context.Context, selector string, files []string) error {
	frame, elem := splitFrame(selector)
	res, err := p.cli.call(ctx, p.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    elemRefJS(frame, elem),
		"returnByValue": false,
	})
	if err != nil {
		return err
	}
	var r struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return err
	}
	if r.Result.ObjectID == "" {
		return fmt.Errorf("upload: selector %q matched no file input", selector)
	}
	_, err = p.cli.call(ctx, p.sessionID, "DOM.setFileInputFiles", map[string]any{
		"objectId": r.Result.ObjectID,
		"files":    files,
	})
	return err
}

// UploadViaChooser sets files by clicking the control that opens a file chooser
// (a button or a file input) and intercepting the chooser — handling the common
// case where there is no persistent <input type=file> to target directly. The
// real Klook SD upload works this way.
func (p *Page) UploadViaChooser(ctx context.Context, selector string, files []string) error {
	if _, err := p.cli.call(ctx, p.sessionID, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}); err != nil {
		return err
	}
	defer p.cli.call(ctx, p.sessionID, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": false})

	events, unsub := p.cli.subscribe("Page.fileChooserOpened", p.sessionID)
	defer unsub()

	if err := p.Click(ctx, selector); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ev := <-events:
		var fc struct {
			BackendNodeID int `json:"backendNodeId"`
		}
		if err := json.Unmarshal(ev.Params, &fc); err != nil {
			return err
		}
		if fc.BackendNodeID == 0 {
			return fmt.Errorf("upload: file chooser had no backendNodeId")
		}
		_, err := p.cli.call(ctx, p.sessionID, "DOM.setFileInputFiles", map[string]any{
			"backendNodeId": fc.BackendNodeID,
			"files":         files,
		})
		return err
	case <-time.After(15 * time.Second):
		return fmt.Errorf("upload: file chooser did not open after clicking %q", selector)
	}
}

// Back navigates one entry back in history.
func (p *Page) Back(ctx context.Context) error {
	return p.Eval(ctx, "window.history.back()", nil)
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

// Cookie is one browser cookie, including HttpOnly ones JS can't read via
// document.cookie — which is the point: a logged-in session token usually is
// HttpOnly, so reuse/extraction needs CDP, not Eval.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite,omitempty"`
}

// Cookies returns the cookies the current page can see (its URL + frames),
// including HttpOnly. Network.enable is idempotent and a no-op once on.
func (p *Page) Cookies(ctx context.Context) ([]Cookie, error) {
	_, _ = p.cli.call(ctx, p.sessionID, "Network.enable", nil)
	res, err := p.cli.call(ctx, p.sessionID, "Network.getCookies", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Cookies []Cookie `json:"cookies"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	return r.Cookies, nil
}
