package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// point is a viewport coordinate.
type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// frameDelim separates an iframe selector from the element selector inside it,
// e.g. "iframe#app >>> #download". One level. Same-origin frames resolve via
// contentDocument; cross-origin OOPIFs (DOM in another process) are routed to the
// frame's own CDP session by oopifPage — transparently, so callers use the same
// "frame >>> element" convention regardless of origin.
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
	// For a cross-origin iframe, resolve inside the frame's own session (where the
	// element lives at document root), then re-prefix the frame so the subsequent
	// click re-routes there via the method-level redirect.
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return frame + frameDelim + cp.resolveClickTarget(ctx, "", elemSel, anchor, timeout)
		}
	}
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

// anchorCandidate is the DOM facts collected for one candidate element during
// anchored resolution — everything scoreAnchorCandidate needs to judge how well
// the element matches the recorded fingerprint. Via records how the candidate
// was found ("primary" / "alt" selector, or a text/role "scan"), the tie-break
// order when scores draw.
type anchorCandidate struct {
	Sel      string `json:"sel"`      // freshly built selector for this element on the current page
	Text     string `json:"text"`     // trimmed visible text
	Role     string `json:"role"`     // role attribute
	Tag      string `json:"tag"`      // lowercase tag name
	Neighbor bool   `json:"neighbor"` // recorded neighbor text found next to this element
	Via      string `json:"via"`      // primary | alt | scan
}

// scoreAnchorCandidate scores how well a candidate element matches the recorded
// fingerprint: exact visible-text match 4 (contains 2), neighbor text found 2,
// role match 2, tag match 1. Pure function — the DOM facts arrive pre-extracted.
func scoreAnchorCandidate(c anchorCandidate, label string, a *Anchors) int {
	score := 0
	if l := strings.TrimSpace(label); l != "" {
		t := strings.TrimSpace(c.Text)
		if t == l {
			score += 4
		} else if strings.Contains(t, l) {
			score += 2
		}
	}
	if a.NeighborText != "" && c.Neighbor {
		score += 2
	}
	if a.Role != "" && c.Role == a.Role {
		score += 2
	}
	if a.Tag != "" && strings.EqualFold(c.Tag, a.Tag) {
		score++
	}
	return score
}

// anchorMaxScore is the highest score this fingerprint can award — the sum of
// the weights for the signals it actually carries. The acceptance threshold is
// relative to it, so a sparse fingerprint isn't held to a rich one's bar.
func anchorMaxScore(label string, a *Anchors) int {
	max := 0
	if strings.TrimSpace(label) != "" {
		max += 4
	}
	if a.NeighborText != "" {
		max += 2
	}
	if a.Role != "" {
		max += 2
	}
	if a.Tag != "" {
		max++
	}
	return max
}

// pickAnchorCandidate selects the candidate that matches the fingerprint, or
// reports that none does. Acceptance requires the best score to EXCEED half the
// fingerprint's achievable maximum — a candidate that only matches the weak
// signals (tag alone) never wins on position. Score ties break toward the
// candidate found via the primary selector, then via an alternate selector;
// a tie between scan-found elements is ambiguity and fails, because clicking
// either would be a guess.
func pickAnchorCandidate(cands []anchorCandidate, label string, a *Anchors) (string, bool) {
	max := anchorMaxScore(label, a)
	if max == 0 {
		return "", false
	}
	best := -1
	var bestC []anchorCandidate
	for _, c := range cands {
		s := scoreAnchorCandidate(c, label, a)
		switch {
		case s > best:
			best, bestC = s, []anchorCandidate{c}
		case s == best:
			bestC = append(bestC, c)
		}
	}
	if best*2 <= max {
		return "", false
	}
	if len(bestC) == 1 {
		return bestC[0].Sel, true
	}
	for _, via := range []string{"primary", "alt"} {
		var hit []anchorCandidate
		for _, c := range bestC {
			if c.Via == via {
				hit = append(hit, c)
			}
		}
		if len(hit) == 1 {
			return hit[0].Sel, true
		}
		if len(hit) > 1 {
			return "", false
		}
	}
	return "", false
}

// anchorSelBuilderJS builds a fresh selector for an element on the CURRENT page
// (id → attribute → id-anchored positional path). Replay-side twin of the
// capture script's sel(): the capture script isn't installed during replay, so
// the resolver carries its own copy.
const anchorSelBuilderJS = `function __selOf(el){
  if(!el||el.nodeType!==1) return '';
  if(el.id) return '#'+CSS.escape(el.id);
  for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'="'+CSS.escape(v)+'"]';}
  var parts=[],node=el,depth=0;
  while(node&&node.nodeType===1&&node.tagName!=='BODY'&&depth<6){
    if(node.id){parts.unshift('#'+CSS.escape(node.id));return parts.join(' > ');}
    var part=node.tagName.toLowerCase();var p=node.parentElement;
    if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;});if(same.length>1)part+=':nth-of-type('+(same.indexOf(node)+1)+')';}
    parts.unshift(part);node=p;depth++;
  }
  return parts.join(' > ');
}`

// resolveAnchoredTarget re-identifies a step's target element by scoring
// candidates against the recorded fingerprint (step.Anchors + step.Label).
// Candidates come from the primary selector, the alternate selectors, and
// text/role scans of the live document. Unlike resolveClickTarget, a fingerprint
// that matches nothing is an explicit error — replay must NOT fall back to
// blindly acting on the positional selector, because a drifted positional
// selector can match the WRONG element without failing (the silent-wrong-click
// this resolver exists to prevent). The error reaches the healer, which gets the
// fingerprint as constraint material.
func (p *Page) resolveAnchoredTarget(ctx context.Context, frame, primary, label string, a *Anchors, timeout time.Duration) (string, error) {
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			sel, err := cp.resolveAnchoredTarget(ctx, "", primary, label, a, timeout)
			if err != nil {
				return "", err
			}
			return frame + frameDelim + sel, nil
		}
	}
	doc := "document"
	if frame != "" {
		doc = fmt.Sprintf("(document.querySelector(%s)||{}).contentDocument", jsString(frame))
	}
	// Fingerprint values come from a recording file the user can edit, so treat
	// them as untrusted when splicing into the generated script: marshal each to
	// a JSON string and additionally escape single quotes as \u0027 (JSON-legal),
	// so no value can terminate a single-quoted span in the surrounding JS.
	quote := func(s string) string { return strings.ReplaceAll(jsString(s), "'", `\u0027`) }
	altParts := make([]string, len(a.Selectors))
	for i, s := range a.Selectors {
		altParts[i] = quote(s)
	}
	altsJS := "[" + strings.Join(altParts, ",") + "]"
	collect := fmt.Sprintf(`(()=>{
	  %s
	  var d=%s; if(!d) return [];
	  var nb=%s, tag=%s, role=%s, label=%s;
	  function nbHit(el){
	    if(!nb) return false;
	    try{
	      var lb=el.closest&&el.closest('label');
	      if(lb&&(lb.textContent||'').indexOf(nb)>=0) return true;
	      var node=el;
	      for(var up=0;node&&node!==d.body&&up<3;up++){
	        var sib=node.previousElementSibling,k=0;
	        while(sib&&k<2){
	          if(!/^(SCRIPT|STYLE|TEMPLATE)$/.test(sib.tagName)&&((sib.textContent||'').trim()).indexOf(nb)>=0) return true;
	          sib=sib.previousElementSibling;k++;
	        }
	        node=node.parentElement;
	      }
	    }catch(_){}
	    return false;
	  }
	  var seen=[],out=[];
	  function add(el,via){
	    if(!el||el.nodeType!==1||seen.indexOf(el)>=0) return;
	    seen.push(el);
	    out.push({sel:__selOf(el),text:(el.textContent||'').trim().slice(0,80),role:(el.getAttribute&&el.getAttribute('role'))||'',tag:el.tagName.toLowerCase(),neighbor:nbHit(el),via:via});
	  }
	  try{ add(d.querySelector(%s),'primary'); }catch(_){}
	  var alts=(%s)||[];
	  for(var i=0;i<alts.length;i++){ try{ add(d.querySelector(alts[i]),'alt'); }catch(_){} }
	  if(label){
	    try{
	      var els=d.querySelectorAll(tag||'*'),n=0;
	      for(var j=0;j<els.length&&n<8;j++){ var t=(els[j].textContent||'').trim(); if(t===label||t.indexOf(label)>=0){ add(els[j],'scan'); n++; } }
	    }catch(_){}
	  }
	  if(role){
	    try{
	      var rs=d.querySelectorAll((tag||'')+'[role="'+CSS.escape(role)+'"]');
	      for(var k2=0;k2<rs.length&&k2<8;k2++){ add(rs[k2],'scan'); }
	    }catch(_){}
	  }
	  return out;
	})()`, anchorSelBuilderJS, doc, quote(a.NeighborText), quote(strings.ToLower(a.Tag)), quote(a.Role), quote(strings.TrimSpace(label)), quote(primary), altsJS)

	deadline := time.Now().Add(timeout)
	for {
		var cands []anchorCandidate
		if err := p.Eval(ctx, collect, &cands); err == nil {
			if sel, ok := pickAnchorCandidate(cands, label, a); ok && sel != "" {
				if frame != "" {
					return frame + frameDelim + sel, nil
				}
				return sel, nil
			}
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return "", fmt.Errorf("no element matches the recorded fingerprint (label=%q, role=%q, tag=%q, neighbor=%q) — the page changed or the target is ambiguous", label, a.Role, a.Tag, a.NeighborText)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
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
	  // Consent/dismiss affordances only. Deliberately NOT ok/okay/continue/submit/
	  // next — those are commonly a flow's PRIMARY action, and clicking one on a
	  // transient (non-overlay) failure would advance/submit the page unintended.
	  var ok=/^(accept|accept all|accept all cookies|accept cookies|agree|i agree|allow all|got it|close|dismiss)$/i;
	  var lab=/(close|dismiss)/i;
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
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return frame + frameDelim + cp.resolveFieldTarget(ctx, "", elemSel, hint, timeout)
		}
	}
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
	if frame, elem := splitFrame(selector); frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return cp.Click(ctx, elem)
		}
	}
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
	if frame, elem := splitFrame(selector); frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return cp.Hover(ctx, elem)
		}
	}
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
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return cp.SelectOption(ctx, elem, value)
		}
	}
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
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return cp.TypeText(ctx, elem, text)
		}
	}
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
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return cp.fieldNonEmpty(ctx, elem)
		}
	}
	expr := fmt.Sprintf(`(()=>{const el=%s; if(!el) return false; const v=('value' in el)?el.value:el.textContent; return !!(v&&(''+v).length);})()`, elemRefJS(frame, elem))
	var ok bool
	_ = p.Eval(ctx, expr, &ok)
	return ok
}

// clearField empties an input/textarea/contenteditable and fires input so
// framework bindings reset, before a re-type retry.
func (p *Page) clearField(ctx context.Context, selector string) {
	frame, elem := splitFrame(selector)
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			cp.clearField(ctx, elem)
			return
		}
	}
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
		key = keyName
		code = "Key" + strings.ToUpper(keyName)
		vk = int(strings.ToUpper(keyName)[0])
	} else {
		return fmt.Errorf("key: unsupported key %q", keyName)
	}

	// Text carried on the keyDown so the press also runs its default action —
	// without it Chrome fires the DOM events but skips the action (Enter would
	// neither submit a form nor newline a textarea; a printable char would not
	// insert). Only with no modifiers held: a combo like ctrl+a must not type a.
	text := ""
	if mods == 0 {
		switch {
		case key == "Enter":
			text = "\r"
		case len(keyName) == 1:
			text = keyName
		case keyName == "space":
			text = " "
		}
	}

	for _, typ := range []string{"keyDown", "keyUp"} {
		params := map[string]any{
			"type":                  typ,
			"modifiers":             mods,
			"key":                   key,
			"code":                  code,
			"windowsVirtualKeyCode": vk,
		}
		if text != "" && typ == "keyDown" {
			params["text"] = text
		}
		if _, err := p.cli.call(ctx, p.sessionID, "Input.dispatchKeyEvent", params); err != nil {
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

// normalizeUploadFiles validates the paths handed to DOM.setFileInputFiles
// BEFORE they reach Chrome: a leading "~/" is expanded (the browser has no
// shell semantics), the result must be absolute, and the file must exist.
// This is a hard prerequisite, not a nicety — Chrome treats a path the browser
// process can't grant (relative, unexpanded "~", nonexistent) as a compromised
// renderer and KILLS the page (RESULT_CODE_KILLED_BAD_MESSAGE), which then
// leaves every subsequent CDP call on that session hanging.
func normalizeUploadFiles(files []string) ([]string, error) {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f == "~" || strings.HasPrefix(f, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("upload: expand %q: %w", f, err)
			}
			f = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(f, "~"), "/"))
		}
		if !filepath.IsAbs(f) {
			return nil, fmt.Errorf("upload: path %q must be absolute (the browser does not resolve relative paths)", f)
		}
		if _, err := os.Stat(f); err != nil {
			return nil, fmt.Errorf("upload: file not found: %s", f)
		}
		out = append(out, f)
	}
	return out, nil
}

// Upload sets the files on a file <input> matched by selector, without the OS
// file-picker dialog (CDP DOM.setFileInputFiles). Paths should be absolute.
// Resolving the input by JS object (rather than a DOM nodeId) lets it pierce a
// same-origin iframe via the same frame convention as the other actions.
func (p *Page) Upload(ctx context.Context, selector string, files []string) error {
	files, err := normalizeUploadFiles(files)
	if err != nil {
		return err
	}
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
	files, err := normalizeUploadFiles(files)
	if err != nil {
		return err
	}
	if frame, elem := splitFrame(selector); frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return cp.UploadViaChooser(ctx, elem, files)
		}
	}
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
