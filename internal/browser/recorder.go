package browser

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// RecordedEvent is one captured user action during a demonstration. Each event
// carries enough to replay it: the action kind plus a best-effort selector (and
// the value for inputs). Frame is set when the element lives in a same-origin
// iframe, so replay can pierce it via the " >>> " convention.
//
// Type == "wait" is special: it has no target element, only a condition to wait
// for. WaitKind is "network" (wait for fetch/XHR to settle) or "element" (wait
// for Selector to appear). TimeoutMS caps how long to wait.
//
// AltSelectors, Role and NeighborText are redundant anchors (the element's
// fingerprint): alternate selectors built with different strategies, the role
// attribute, and the nearest stable label-like text. Together with Text and Tag
// they let replay re-identify the element by scoring when the primary Selector
// drifts — see resolveAnchoredTarget.
type RecordedEvent struct {
	Type         string   `json:"type"`                    // click | change | upload | navigate | enter | wait | download
	Selector     string   `json:"selector"`                // best-effort CSS selector within its document
	Frame        string   `json:"frame"`                   // iframe selector when the target is in a child frame
	Tag          string   `json:"tag"`                     // target tagName (SELECT/INPUT/…), so replay picks the right action
	Value        string   `json:"value"`                   // input/select value (change events)
	Text         string   `json:"text"`                    // element text, for context
	Field        string   `json:"field"`                   // input's placeholder/name/aria-label/id — names the auto-param
	Secret       bool     `json:"secret"`                  // password input: don't persist the value as a param default
	URL          string   `json:"url"`                     // document URL at capture time
	WaitKind     string   `json:"wait_kind,omitempty"`     // wait: "network" | "element"
	TimeoutMS    int      `json:"timeout_ms,omitempty"`    // wait: cap in ms
	DownloadName string   `json:"download_name,omitempty"` // download: suggested filename from CDP
	AltSelectors []string `json:"alt_selectors,omitempty"` // alternate selectors (different strategies than Selector)
	Role         string   `json:"role,omitempty"`          // element's role attribute
	NeighborText string   `json:"neighbor_text,omitempty"` // nearest stable label-like text (label / preceding sibling)
}

// Recorder captures a user's actions on a page by injecting a DOM listener that
// reports through a CDP binding. It records the user's real demonstration — it
// does not drive the page itself.
type Recorder struct {
	page *Page

	mu     sync.Mutex
	events []RecordedEvent
	unsubs []func()
	seen   map[string]bool // sessions already instrumented (avoid double-install)

	// lastClickIdx / lastClickAt track the most recent click so a later
	// Browser.downloadWillBegin can upgrade it to a download event — the file
	// arrives after the click that triggered it. -1 means no recent click.
	lastClickIdx int
	lastClickAt  time.Time

	// dlDir is the temp directory downloads land in during recording (set by
	// watchDownloads). Removed in Stop() — recording-time downloads are only
	// used to detect the event, not kept.
	dlDir string
}

// NewRecorder creates a recorder bound to a page.
func NewRecorder(page *Page) *Recorder {
	return &Recorder{page: page, seen: map[string]bool{}, lastClickIdx: -1}
}

// claimSession atomically records a session as instrumented, returning false if
// it already was — the guarded compare-and-set that lets Start and the
// attach-watcher goroutine race safely.
func (r *Recorder) claimSession(session string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[session] {
		return false
	}
	r.seen[session] = true
	return true
}

// releaseSession undoes a claim so a session whose instrumentation failed can be
// retried on a later attach event.
func (r *Recorder) releaseSession(session string) {
	r.mu.Lock()
	delete(r.seen, session)
	r.mu.Unlock()
}

// addEvent appends a captured event, forcing frame to frameSel when the event
// came from a cross-origin iframe session (whose own window.frameElement is
// unreadable, so the script couldn't tag it). Click events are tracked so a later
// Browser.downloadWillBegin can upgrade the click to a download event.
func (r *Recorder) addEvent(re RecordedEvent, frameSel string) {
	if frameSel != "" {
		re.Frame = frameSel
	}
	r.mu.Lock()
	if re.Type == "click" {
		r.lastClickIdx = len(r.events)
		r.lastClickAt = time.Now()
	}
	r.events = append(r.events, re)
	r.mu.Unlock()
}

// upgradeLastClickToDownload upgrades the most recent click event to a "download"
// event with the given filename — called when Browser.downloadWillBegin fires
// within a short window after the click that triggered it. No-op if the click is
// too old or already consumed.
func (r *Recorder) upgradeLastClickToDownload(filename string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.lastClickIdx
	if idx < 0 || idx >= len(r.events) {
		return
	}
	if time.Since(r.lastClickAt) > 5*time.Second {
		return
	}
	ev := r.events[idx]
	if ev.Type != "click" {
		return
	}
	ev.Type = "download"
	ev.DownloadName = filename
	r.events[idx] = ev
	r.lastClickIdx = -1 // consumed
}

// watchDownloads subscribes to Browser.downloadWillBegin globally (the browser-
// wide session, since downloads are not page-scoped). When a download begins
// shortly after a recorded click, that click is upgraded to a "download" event
// with the captured filename — CompileRecording then emits a download step that
// replay uses to capture the file.
func (r *Recorder) watchDownloads(ctx context.Context) {
	// Enable the Browser domain (idempotent) and configure download behavior so
	// Chrome emits downloadWillBegin. eventsEnabled is required — without it the
	// event never fires. Downloads during recording land in a temp dir; only the
	// event metadata is kept, and Stop() removes the directory (and whatever
	// landed in it) once recording ends.
	_, _ = r.page.cli.call(ctx, "", "Browser.enable", nil)
	dlDir, err := os.MkdirTemp("", "octo-rec-dl-*")
	if err != nil {
		dlDir = ""
	}
	r.mu.Lock()
	r.dlDir = dlDir
	r.mu.Unlock()
	_, _ = r.page.cli.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{
		"behavior":      "allow",
		"downloadPath":  dlDir,
		"eventsEnabled": true,
	})
	dlEvents, unsub := r.page.cli.subscribe("Browser.downloadWillBegin", "")
	r.mu.Lock()
	r.unsubs = append(r.unsubs, unsub)
	r.mu.Unlock()
	go func() {
		for ev := range dlEvents {
			var w struct {
				SuggestedFilename string `json:"suggestedFilename"`
			}
			if json.Unmarshal(ev.Params, &w) != nil {
				continue
			}
			r.upgradeLastClickToDownload(w.SuggestedFilename)
		}
	}()
}

// instrumentSession installs the capture binding + listeners on one CDP session
// and streams its click/change events into the recording, tagged with frameSel.
// Used for the top document (frameSel "") and each cross-origin iframe session.
func (r *Recorder) instrumentSession(ctx context.Context, session, frameSel string) error {
	// Claim the session under the lock: instrumentSession runs concurrently from
	// Start (main) and the attach-watcher goroutine, so an unguarded seen map is a
	// data race (concurrent map writes → panic). Claiming also dedups. Release on
	// failure so a session that failed to install can be retried later.
	if !r.claimSession(session) {
		return nil
	}
	release := func(err error) error {
		if err != nil {
			r.releaseSession(session)
		}
		return err
	}
	if _, err := r.page.cli.call(ctx, session, "Runtime.addBinding", map[string]any{"name": "__octoRecord"}); err != nil {
		return release(err)
	}
	if _, err := r.page.cli.call(ctx, session, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": captureScript}); err != nil {
		return release(err)
	}
	// Install into the already-loaded document too.
	if _, err := r.page.cli.call(ctx, session, "Runtime.evaluate", map[string]any{"expression": captureScript}); err != nil {
		return release(err)
	}
	events, unsub := r.page.cli.subscribe("Runtime.bindingCalled", session)
	r.mu.Lock()
	r.unsubs = append(r.unsubs, unsub)
	r.mu.Unlock()
	go func() {
		for ev := range events {
			var b struct {
				Name    string `json:"name"`
				Payload string `json:"payload"`
			}
			if json.Unmarshal(ev.Params, &b) != nil || b.Name != "__octoRecord" {
				continue
			}
			var re RecordedEvent
			if json.Unmarshal([]byte(b.Payload), &re) != nil {
				continue
			}
			r.addEvent(re, frameSel)
		}
	}()
	return nil
}

// instrumentOOPIF resolves a cross-origin iframe session's parent selector and
// instruments it, so gestures the user performs inside the frame are captured and
// tagged with the frame replay needs to route back into.
func (r *Recorder) instrumentOOPIF(ctx context.Context, session string) {
	// Enable the domains ourselves rather than depend on the browser watcher's
	// async registerOOPIF having run first — otherwise getFrameTree/addBinding
	// below could race it and fail, leaving the frame uninstrumented. Idempotent.
	for _, d := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if _, err := r.page.cli.call(ctx, session, d, nil); err != nil {
			return
		}
	}
	res, err := r.page.cli.call(ctx, session, "Page.getFrameTree", nil)
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
	frameSel := ""
	if r.page.browser != nil {
		frameSel = r.page.browser.frameSelectorFor(ctx, r.page.sessionID, ft.FrameTree.Frame.ID)
	}
	_ = r.instrumentSession(ctx, session, frameSel)
}

// captureScript installs capture-phase click/change/keydown listeners that report
// each action (with a stable-ish selector) through the __octoRecord binding.
//
// It also auto-inserts wait events: after each click, it checks whether the click
// triggered network activity (SPA data loading) and emits a "network" wait, and a
// MutationObserver detects significant new DOM elements (modals, popups,
// calendars, overlays) and emits an "element" wait for the first such element.
// These waits make the compiled recording replayable without the next step
// racing ahead of a page that hasn't settled.
const captureScript = `(function(){
  if (window.__octoRec) return; window.__octoRec = true;
  /* ---- network monitor: track in-flight fetch/XHR (reused by WaitForNetworkIdle) ---- */
  if (!window.__octoNet){
    var s=window.__octoNet={n:0, idleSince:Date.now()};
    function inc(){ s.n++; s.idleSince=0; }
    function dec(){ s.n=Math.max(0,s.n-1); if(s.n===0) s.idleSince=Date.now(); }
    try{ var of=window.fetch; if(of){ window.fetch=function(){ inc(); return of.apply(this,arguments).then(function(r){dec();return r;},function(e){dec();throw e;}); }; } }catch(_){}
    try{ var send=XMLHttpRequest.prototype.send; XMLHttpRequest.prototype.send=function(){ inc(); try{ this.addEventListener('loadend',function(){dec();},{once:true}); }catch(_){ dec(); } return send.apply(this,arguments); }; }catch(_){}
  }
  function attrSel(el){
    if(!el || el.nodeType!==1) return '';
    if(el.id) return '#'+CSS.escape(el.id);
    for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'=\"'+CSS.escape(v)+'\"]';}
    return '';
  }
  // pathSel walks up from el building a selector path, one part per node via
  // nodeFn — anchored at the nearest id-bearing ancestor when one exists.
  function pathSel(el, nodeFn){
    var parts=[], node=el, depth=0;
    while(node && node.nodeType===1 && node.tagName!=='BODY' && depth<6){
      if(node.id){ parts.unshift('#'+CSS.escape(node.id)); return parts.join(' > '); }
      parts.unshift(nodeFn(node)); node=node.parentElement; depth++;
    }
    return parts.join(' > ');
  }
  function sel(el){
    if(!el || el.nodeType!==1) return '';
    var a=attrSel(el); if(a) return a;
    return pathSel(el, pathNode);
  }
  // altSels returns alternate selectors built with strategies DIFFERENT from
  // the primary sel() — the semantic path when the primary was attribute-based,
  // plus the plain positional path. Replay tries them as extra candidates and
  // scores each against the fingerprint, so a page whose classes all changed
  // (CSS-in-JS hash rollover) can still be re-anchored by position + text.
  function altSels(el){
    if(!el || el.nodeType!==1) return [];
    var primary=sel(el), out=[];
    var cands=[pathSel(el, pathNode), pathSel(el, posNode)];
    for(var i=0;i<cands.length;i++){ var c=cands[i]; if(c && c!==primary && out.indexOf(c)<0) out.push(c); }
    return out.slice(0,2);
  }
  // posNode is the purely positional path part: tag:nth-of-type(N), no classes
  // or roles — survives a class-hash rollover that kills the semantic path.
  function posNode(node){
    var tag=node.tagName.toLowerCase();
    var p=node.parentElement;
    if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;}); if(same.length>1){return tag+':nth-of-type('+(same.indexOf(node)+1)+')';}}
    return tag;
  }
  // neighborText finds the nearest stable label-like text for el: an enclosing
  // <label>, else a preceding sibling's short text, walking up to 3 ancestors.
  // It anchors the element to what the USER sees next to it — which survives
  // wholesale class/structure churn that breaks every selector strategy.
  function neighborText(el){
    try{
      var lb=el.closest && el.closest('label');
      if(lb){ var t=(lb.textContent||'').trim(); if(t && t.length<=30) return t; }
      var node=el;
      // Stop at <body>: walking past it reaches <head>, whose <title> text is
      // not something the user sees NEXT TO the element — a garbage anchor.
      for(var up=0; node && node!==document.body && up<3; up++){
        var sib=node.previousElementSibling, k=0;
        while(sib && k<2){
          if(!/^(SCRIPT|STYLE|TEMPLATE)$/.test(sib.tagName)){
            var t2=(sib.textContent||'').trim(); if(t2 && t2.length<=30) return t2;
          }
          sib=sib.previousElementSibling; k++;
        }
        node=node.parentElement;
      }
    }catch(_){}
    return '';
  }
  // pathNode builds a selector for one node in the path: tagName plus a semantic
  // anchor when available (role, structural class) — so a table cell reads
  // td.ant-picker-cell[role="gridcell"]:nth-of-type(2) instead of plain
  // td:nth-of-type(2). The semantic anchor survives layout shifts that a pure
  // positional index would not. Falls back to :nth-of-type only when nothing
  // else distinguishes the node among same-tag siblings.
  function pathNode(node){
    var tag=node.tagName.toLowerCase();
    var role=node.getAttribute && node.getAttribute('role');
    if(role) return tag+'[role=\"'+CSS.escape(role)+'\"]'+nthSuffix(node);
    var cls=structuralClass(node);
    if(cls) return tag+'.'+CSS.escape(cls)+nthSuffix(node);
    var p=node.parentElement;
    if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;}); if(same.length>1){return tag+':nth-of-type('+(same.indexOf(node)+1)+')';}}
    return tag;
  }
  function nthSuffix(node){
    var p=node.parentElement;
    if(!p) return '';
    var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;});
    return same.length>1?':nth-of-type('+(same.indexOf(node)+1)+')':'';
  }
  // structuralClass picks the most likely-semantic class from an element's class
  // list — prefers longer, component-style names (BEM blocks, library classes)
  // over short generics like "active"/"disabled". Returns "" when nothing looks
  // structural, so the caller can fall back to something else.
  function structuralClass(node){
    if(!node.className || typeof node.className!=='string') return '';
    var cls=node.className.split(/\s+/).filter(function(c){return c && c.length>3});
    if(cls.length===0) return '';
    // Prefer longer names — usually the component block (ant-picker-cell-inner
    // over active). Break ties by preferring names that look like BEM
    // (double-dash) or library-prefixed (ant-, el-, mui-).
    cls.sort(function(a,b){
      var scoreA=(a.indexOf('--')>=0?10:0)+(/(ant|el|mui|odin)-/.test(a)?5:0)+a.length;
      var scoreB=(b.indexOf('--')>=0?10:0)+(/(ant|el|mui|odin)-/.test(b)?5:0)+b.length;
      return scoreB-scoreA;
    });
    return cls[0];
  }
  /* ---- wait-event reporting (debounced) ---- */
  var _lastWaitAt=0;
  var WAIT_COOLDOWN=200;
  function reportWait(kind, selector, timeout){
    var now=Date.now();
    if(now-_lastWaitAt<WAIT_COOLDOWN) return;
    _lastWaitAt=now;
    try{ window.__octoRecord(JSON.stringify({type:'wait', wait_kind:kind, selector:selector||'', timeout_ms:timeout||10000, url:location.href})); }catch(_){}
  }
  function report(type, e){
    try{var el=e.target; var fr=''; try{ if(window.frameElement) fr=sel(window.frameElement); }catch(_2){}
      var field=((el.placeholder||el.name||(el.getAttribute?el.getAttribute('aria-label'):'')||el.id||'')+'').trim().slice(0,40);
      var secret=(el.type==='password');
      var role=''; try{ role=el.getAttribute('role')||''; }catch(_3){}
      window.__octoRecord(JSON.stringify({type:type, selector:sel(el), frame:fr, tag:el.tagName, text:(el.textContent||'').trim().slice(0,40), field:field, secret:secret, value:(secret?'':(el.value!==undefined?(''+el.value).slice(0,200):'')), url:location.href, alt_selectors:altSels(el), role:role, neighbor_text:neighborText(el)}));}catch(_){}
  }
  /* ---- click: report + detect network activity after a short delay ---- */
  document.addEventListener('click', function(e){
    report('click', e);
    setTimeout(function(){ try{ var s=window.__octoNet; if(s && s.n>0) reportWait('network','',10000); }catch(_){} }, 150);
  }, true);
  document.addEventListener('change', function(e){var t=e.target; report((t&&t.type==='file')?'upload':'change', e);}, true);
  // Enter in a text input = the submit gesture (change never fires without a
  // blur, so the typed value only reaches us as this event's snapshot). Not
  // captured: textarea (Enter is a newline, already in the change value),
  // select/buttons/links (Enter fires a click, caught above), non-text inputs
  // (checkbox/radio have no typed value), and IME composition (Enter confirms
  // the candidate — it is not a submit, CJK users hit it constantly).
  var TEXTY={text:1,search:1,email:1,url:1,tel:1,number:1,password:1};
  document.addEventListener('keydown', function(e){
    if(e.key!=='Enter'&&e.keyCode!==13) return;
    if(e.isComposing||e.keyCode===229) return;
    var t=e.target;
    if(!t||t.nodeType!==1||t.tagName!=='INPUT') return;
    if(!TEXTY[t.type||'text']) return;
    report('enter', e);
  }, true);
  /* ---- MutationObserver: detect modals/popups/calendars/overlays ---- */
  var _waitTimer=null, _observer=null;
  function isSignificant(el){
    if(!el || el.nodeType!==1) return false;
    var role=el.getAttribute('role');
    if(role==='dialog'||role==='alertdialog') return true;
    if(el.getAttribute('aria-modal')==='true') return true;
    var cls=(el.className&&typeof el.className==='string')?el.className.toLowerCase():'';
    if(/(^|\s)(modal|dialog|popup|popover|drawer|overlay|calendar|picker|dropdown|lightbox|tooltip|ant-modal|ant-picker|ant-dropdown|ant-drawer|ant-popover|ant-tooltip)($|\s)/.test(cls)) return true;
    try{ var st=window.getComputedStyle(el); if((st.position==='fixed'||st.position==='absolute')&&el.offsetWidth>window.innerWidth*0.3&&el.offsetHeight>window.innerHeight*0.3) return true; }catch(_){}
    return false;
  }
  function ensureObserver(){
    if(_observer) return;
    _observer=new MutationObserver(function(muts){
      var found=null;
      for(var i=0;i<muts.length&&!found;i++){
        var m=muts[i];
        if(m.type==='childList'&&m.addedNodes){
          for(var j=0;j<m.addedNodes.length;j++){ if(isSignificant(m.addedNodes[j])){ found=m.addedNodes[j]; break; } }
        }
      }
      if(!found) return;
      clearTimeout(_waitTimer);
      _waitTimer=setTimeout(function(){
        // Only report if the element is still in the DOM (not immediately removed).
        try{ if(document.body && document.body.contains(found)) reportWait('element', sel(found), 10000); }catch(_){}
      }, 300);
    });
    _observer.observe(document.documentElement, {childList:true, subtree:true});
  }
  if(document.readyState==='loading'){ document.addEventListener('DOMContentLoaded', ensureObserver); } else { ensureObserver(); }
})();`

// Start begins capturing. The binding and listeners are installed on the live
// document and on every future document (so post-navigation pages and
// same-origin child frames are covered too).
func (r *Recorder) Start(ctx context.Context) error {
	p := r.page
	// Instrument the top document.
	if err := r.instrumentSession(ctx, p.sessionID, ""); err != nil {
		return err
	}
	// Watch for downloads triggered by clicks in any page: the click records as
	// a plain click, then Browser.downloadWillBegin retroactively upgrades it.
	r.watchDownloads(ctx)
	// Instrument cross-origin iframes: those already present, and any that attach
	// during the demonstration (so gestures inside a payment/OAuth/captcha frame
	// are captured, tagged with the parent selector replay routes back into).
	if p.browser != nil {
		p.browser.oopifMu.Lock()
		sessions := make([]string, 0, len(p.browser.oopifs))
		for _, s := range p.browser.oopifs {
			sessions = append(sessions, s)
		}
		p.browser.oopifMu.Unlock()
		for _, s := range sessions {
			r.instrumentOOPIF(ctx, s)
		}
		attached, attachUnsub := p.cli.subscribe("Target.attachedToTarget", "")
		r.mu.Lock()
		r.unsubs = append(r.unsubs, attachUnsub)
		r.mu.Unlock()
		go func() {
			for ev := range attached {
				var a struct {
					SessionID  string `json:"sessionId"`
					TargetInfo struct {
						Type string `json:"type"`
					} `json:"targetInfo"`
				}
				if json.Unmarshal(ev.Params, &a) != nil || a.SessionID == "" {
					continue
				}
				ictx, icancel := context.WithTimeout(context.Background(), 10*time.Second)
				switch a.TargetInfo.Type {
				case "iframe":
					// The browser watcher enables the child's domains; give it a
					// moment, then instrument for recording.
					r.instrumentOOPIF(ictx, a.SessionID)
				case "page":
					// A tab the demonstration itself opened (target=_blank /
					// window.open): instrument it like the top document so the rest
					// of the demo is captured — replay follows the click that opened
					// the tab, so the steps stay consistent. Tabs already open when
					// recording started are deliberately NOT instrumented: switching
					// to one leaves no replayable step, so its events would compile
					// into a skill that replays on the wrong page.
					r.instrumentPageSession(ictx, a.SessionID)
				}
				icancel()
			}
		}()
	}

	// Also capture top-level navigations (address-bar, link, SPA history) so a
	// multi-page demonstration replays from the right pages — otherwise the only
	// navigate step is the synthesized start URL, and the recording loses every
	// page the user moved to.
	r.watchNavigations(ctx, p.sessionID)
	return nil
}

// instrumentPageSession instruments a newly attached tab (a page target) the
// same way the top document is instrumented, plus navigation capture — the demo
// may navigate the new tab (or drive an SPA in it) before touching anything.
//
// Race fix: a tab the demonstration itself opened (target=_blank / window.open)
// starts loading immediately, and the user can click in it before our
// instrumentation finishes — any events before the captureScript is injected
// are lost. We close that window by pausing the page with Page.stopLoading the
// moment we attach, running the full instrumentation, then refreshing the
// document via Page.navigate to the current URL so the recorder is in place
// before anything can be clicked. The user never sees an interactive-but-
// uninstrumented page.
func (r *Recorder) instrumentPageSession(ctx context.Context, session string) {
	// Pause loading immediately so the page can't finish rendering (and can't be
	// clicked) before the recorder is installed. Best-effort: a page that already
	// finished loading is unaffected, and one that never loads still instruments.
	_, _ = r.page.cli.call(ctx, session, "Page.stopLoading", nil)
	// Snapshot the current URL before reload — stopLoading may leave the page in
	// a loading state whose URL is the in-flight request, not the final one.
	curURL := ""
	if res, err := r.page.cli.call(ctx, session, "Runtime.evaluate", map[string]any{"expression": "location.href"}); err == nil {
		var u string
		_ = json.Unmarshal(res, &u)
		curURL = u
	}
	// Enable the domains ourselves rather than depend on the browser watcher's
	// async registration having run first — same reasoning as instrumentOOPIF.
	for _, d := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if _, err := r.page.cli.call(ctx, session, d, nil); err != nil {
			return
		}
	}
	if err := r.instrumentSession(ctx, session, ""); err != nil {
		return
	}
	r.watchNavigations(ctx, session)
	// Recorder is in place — refresh the document so the injected captureScript
	// (via addScriptToEvaluateOnNewDocument) fires on the fresh page and the
	// user sees a fully-loaded page. Page.navigate to the current URL; if the
	// URL is non-navigable (data: / blob: / chrome-error:) skip the reload.
	if curURL != "" && !strings.HasPrefix(curURL, "data:") && !strings.HasPrefix(curURL, "blob:") && !strings.HasPrefix(curURL, "chrome-error:") {
		_, _ = r.page.cli.call(ctx, session, "Page.navigate", map[string]any{"url": curURL})
	}
}

// watchNavigations records top-level navigations on one page session: both
// cross-document (Page.frameNavigated) and same-document SPA route changes
// (Page.navigatedWithinDocument — pushState/replaceState never fire
// frameNavigated). Subframe navigations are ignored: the cross-document event
// carries the frame's parentId, and the same-document one is filtered against
// the session's top frame (resolved once here). Consecutive repeats collapse.
func (r *Recorder) watchNavigations(ctx context.Context, session string) {
	topFrameID := ""
	if res, err := r.page.cli.call(ctx, session, "Page.getFrameTree", nil); err == nil {
		var ft struct {
			FrameTree struct {
				Frame struct {
					ID string `json:"id"`
				} `json:"frame"`
			} `json:"frameTree"`
		}
		if json.Unmarshal(res, &ft) == nil {
			topFrameID = ft.FrameTree.Frame.ID
		}
	}

	navEvents, navUnsub := r.page.cli.subscribe("Page.frameNavigated", session)
	spaEvents, spaUnsub := r.page.cli.subscribe("Page.navigatedWithinDocument", session)
	r.mu.Lock()
	r.unsubs = append(r.unsubs, navUnsub, spaUnsub)
	r.mu.Unlock()

	recordNav := func(u string) {
		if u == "" || u == "about:blank" {
			return
		}
		r.mu.Lock()
		n := len(r.events)
		if n > 0 && r.events[n-1].Type == "navigate" && r.events[n-1].URL == u {
			r.mu.Unlock()
			return // collapse the initial-load echo / repeat
		}
		r.events = append(r.events, RecordedEvent{Type: "navigate", URL: u})
		r.mu.Unlock()
	}

	go func() {
		for ev := range navEvents {
			var b struct {
				Frame struct {
					URL      string `json:"url"`
					ParentID string `json:"parentId"`
				} `json:"frame"`
			}
			if json.Unmarshal(ev.Params, &b) != nil || b.Frame.ParentID != "" {
				continue // ignore subframe navigations
			}
			recordNav(b.Frame.URL)
		}
	}()
	go func() {
		for ev := range spaEvents {
			var b struct {
				FrameID string `json:"frameId"`
				URL     string `json:"url"`
			}
			if json.Unmarshal(ev.Params, &b) != nil {
				continue
			}
			if topFrameID != "" && b.FrameID != topFrameID {
				continue // SPA routing inside a subframe — not a page move
			}
			recordNav(b.URL)
		}
	}()
}

// Events returns the captured actions so far.
func (r *Recorder) Events() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

// Replay re-executes recorded events directly (no recording file, no params,
// no healer) — a convenience over the recording path for raw record→replay.
func Replay(ctx context.Context, page *Page, events []RecordedEvent) error {
	recording := CompileRecording("", "", "", events)
	_, _, _, err := ReplayRecording(ctx, page, &recording, nil, ReplayOptions{})
	return err
}

// Stop ends capture (all subscriptions are dropped; the page-side listeners are
// harmless and go away on navigation/close).
func (r *Recorder) Stop() {
	r.mu.Lock()
	unsubs := r.unsubs
	r.unsubs = nil
	dlDir := r.dlDir
	r.dlDir = ""
	r.mu.Unlock()
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
	if dlDir != "" {
		_ = os.RemoveAll(dlDir)
	}
}
