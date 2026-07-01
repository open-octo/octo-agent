package browser

import (
	"context"
	"encoding/json"
	"time"
)

// Cross-origin (out-of-process) iframe support.
//
// A same-origin iframe's DOM is reachable from its parent via
// element.contentDocument, which is how the " >>> " frame convention resolves
// (see elemRefJS). A cross-origin iframe (an OOPIF) lives in a separate renderer
// process; contentDocument is null and the DOM is only reachable through the
// iframe's own CDP session. The target watcher records those sessions by frameId,
// and oopifPage resolves a frame selector to a Page bound to that session — after
// which every existing Page operation works unchanged, because within the OOPIF's
// session its document IS the frame (no contentDocument hop, and even coordinate
// clicks dispatch correctly against the frame-local viewport).

// watchTargets consumes Target.attachedToTarget / detachedFromTarget for the whole
// connection and maintains the frameId→session map. It runs for the life of the
// connection and exits when it closes.
func (b *Browser) watchTargets() {
	attached, _ := b.cli.subscribe("Target.attachedToTarget", "")
	detached, _ := b.cli.subscribe("Target.detachedFromTarget", "")
	go func() {
		for {
			select {
			case <-b.cli.done():
				return
			case ev, ok := <-attached:
				if !ok {
					return
				}
				var p struct {
					SessionID  string `json:"sessionId"`
					TargetInfo struct {
						Type string `json:"type"`
					} `json:"targetInfo"`
				}
				if json.Unmarshal(ev.Params, &p) == nil && p.TargetInfo.Type == "iframe" && p.SessionID != "" {
					go b.registerOOPIF(p.SessionID)
				}
			case ev, ok := <-detached:
				if !ok {
					return
				}
				var p struct {
					SessionID string `json:"sessionId"`
				}
				if json.Unmarshal(ev.Params, &p) == nil {
					b.forgetOOPIF(p.SessionID)
				}
			}
		}
	}()
}

// registerOOPIF prepares a freshly attached cross-origin iframe session (enables
// the domains its Page operations need, cascades auto-attach for nested OOPIFs)
// and records its frameId → session so oopifPage can find it.
func (b *Browser) registerOOPIF(session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, d := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if _, err := b.cli.call(ctx, session, d, nil); err != nil {
			return
		}
	}
	// Cascade so a cross-origin iframe nested inside this one also attaches.
	_, _ = b.cli.call(ctx, session, "Target.setAutoAttach", map[string]any{
		"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true,
	})
	res, err := b.cli.call(ctx, session, "Page.getFrameTree", nil)
	if err != nil {
		return
	}
	var ft struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if json.Unmarshal(res, &ft) != nil || ft.FrameTree.Frame.ID == "" {
		return
	}
	b.oopifMu.Lock()
	b.oopifs[ft.FrameTree.Frame.ID] = session
	b.oopifMu.Unlock()
}

// forgetOOPIF drops any frame mapping for a detached session.
func (b *Browser) forgetOOPIF(session string) {
	b.oopifMu.Lock()
	for fid, s := range b.oopifs {
		if s == session {
			delete(b.oopifs, fid)
		}
	}
	b.oopifMu.Unlock()
}

// sessionForFrame returns the CDP session registered for a frameId, polling
// briefly since attach can lag the parent's DOM being queryable.
func (b *Browser) sessionForFrame(ctx context.Context, frameID string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		b.oopifMu.Lock()
		s, ok := b.oopifs[frameID]
		b.oopifMu.Unlock()
		if ok {
			return s, true
		}
		if !time.Now().Before(deadline) {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// oopifPage resolves a frame selector to a Page bound to the cross-origin iframe's
// own session, plus true. It returns (nil, false) when there is no frame, when the
// frame is same-origin (contentDocument works — callers keep the existing path),
// or when no OOPIF session can be found. The returned Page addresses the frame's
// document as its root, so callers act on the bare element selector (no frame).
func (p *Page) oopifPage(ctx context.Context, frame string) (*Page, bool) {
	if frame == "" || p.browser == nil {
		return nil, false
	}
	frameID, sameOrigin, ok := p.frameContentID(ctx, frame)
	if !ok || sameOrigin || frameID == "" {
		return nil, false
	}
	session, ok := p.browser.sessionForFrame(ctx, frameID, 3*time.Second)
	if !ok {
		return nil, false
	}
	return &Page{cli: p.cli, sessionID: session, targetID: "", browser: p.browser}, true
}

// frameSelectorFor finds, in pageSession's document, the <iframe> element whose
// content frame is frameID and returns a generated CSS selector for it — the
// reverse of oopifPage, used when recording to tag a cross-origin iframe's events
// with the parent selector replay needs to route back into it. Returns "" if no
// matching iframe is found (e.g. a nested OOPIF not present at the top level).
func (b *Browser) frameSelectorFor(ctx context.Context, pageSession, frameID string) string {
	doc, err := b.cli.call(ctx, pageSession, "DOM.getDocument", map[string]any{"depth": 0})
	if err != nil {
		return ""
	}
	var d struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if json.Unmarshal(doc, &d) != nil {
		return ""
	}
	qs, err := b.cli.call(ctx, pageSession, "DOM.querySelectorAll", map[string]any{"nodeId": d.Root.NodeID, "selector": "iframe,frame"})
	if err != nil {
		return ""
	}
	var q struct {
		NodeIDs []int `json:"nodeIds"`
	}
	if json.Unmarshal(qs, &q) != nil {
		return ""
	}
	const selFn = `function(){
	  function sel(el){
	    if(!el||el.nodeType!==1) return '';
	    if(el.id) return '#'+CSS.escape(el.id);
	    for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'="'+CSS.escape(v)+'"]';}
	    var parts=[],node=el,depth=0;
	    while(node&&node.nodeType===1&&node.tagName!=='BODY'&&depth<6){if(node.id){parts.unshift('#'+CSS.escape(node.id));return parts.join(' > ');}var part=node.tagName.toLowerCase();var p=node.parentElement;if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;});if(same.length>1)part+=':nth-of-type('+(same.indexOf(node)+1)+')';}parts.unshift(part);node=p;depth++;}
	    return parts.join(' > ');
	  }
	  return sel(this);
	}`
	for _, nid := range q.NodeIDs {
		dn, err := b.cli.call(ctx, pageSession, "DOM.describeNode", map[string]any{"nodeId": nid})
		if err != nil {
			continue
		}
		var node struct {
			Node struct {
				FrameID string `json:"frameId"`
			} `json:"node"`
		}
		if json.Unmarshal(dn, &node) != nil || node.Node.FrameID != frameID {
			continue
		}
		rn, err := b.cli.call(ctx, pageSession, "DOM.resolveNode", map[string]any{"nodeId": nid})
		if err != nil {
			return ""
		}
		var obj struct {
			Object struct {
				ObjectID string `json:"objectId"`
			} `json:"object"`
		}
		if json.Unmarshal(rn, &obj) != nil || obj.Object.ObjectID == "" {
			return ""
		}
		res, err := b.cli.call(ctx, pageSession, "Runtime.callFunctionOn", map[string]any{
			"objectId": obj.Object.ObjectID, "functionDeclaration": selFn, "returnByValue": true,
		})
		if err != nil {
			return ""
		}
		var r struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(res, &r) == nil {
			return r.Result.Value
		}
		return ""
	}
	return ""
}

// frameContentID inspects an iframe element in this page's document: it reports
// whether the element's contentDocument is same-origin-accessible (in which case
// no OOPIF routing is needed) and, when cross-origin, the content frameId used to
// find the OOPIF's session.
func (p *Page) frameContentID(ctx context.Context, frame string) (frameID string, sameOrigin bool, ok bool) {
	var chk struct {
		Found bool `json:"found"`
		Same  bool `json:"same"`
	}
	expr := `(function(){var f=document.querySelector(` + jsString(frame) + `); if(!f) return {found:false}; var same=false; try{same=(f.contentDocument!=null);}catch(e){same=false;} return {found:true, same:same};})()`
	if err := p.Eval(ctx, expr, &chk); err != nil || !chk.Found {
		return "", false, false
	}
	if chk.Same {
		return "", true, true
	}
	// Cross-origin: get the element's object handle, then its content frameId.
	res, err := p.cli.call(ctx, p.sessionID, "Runtime.evaluate", map[string]any{
		"expression": "document.querySelector(" + jsString(frame) + ")", "returnByValue": false,
	})
	if err != nil {
		return "", false, false
	}
	var re struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if json.Unmarshal(res, &re) != nil || re.Result.ObjectID == "" {
		return "", false, false
	}
	dn, err := p.cli.call(ctx, p.sessionID, "DOM.describeNode", map[string]any{"objectId": re.Result.ObjectID})
	if err != nil {
		return "", false, false
	}
	var node struct {
		Node struct {
			FrameID string `json:"frameId"`
		} `json:"node"`
	}
	if json.Unmarshal(dn, &node) != nil || node.Node.FrameID == "" {
		return "", false, false
	}
	return node.Node.FrameID, false, true
}
