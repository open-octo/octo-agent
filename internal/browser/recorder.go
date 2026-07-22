package browser

import (
	"context"
	"encoding/json"
	"os"
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
	NewTab       bool     `json:"new_tab,omitempty"`       // navigate: the first load of a tab the page itself opened (has an opener)
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

	// pendingDLName / pendingDLAt park a downloadWillBegin that arrived before
	// the click that triggered it: the download event travels straight from the
	// browser process while the click rides the renderer→binding→CDP path, so
	// on a loaded machine the download can win the race. The next click within
	// the attribution window claims it.
	pendingDLName string
	pendingDLAt   time.Time

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
		if r.pendingDLName != "" && time.Since(r.pendingDLAt) <= downloadAttributionWindow {
			// A downloadWillBegin already arrived for this click (it beat the
			// click's binding delivery) — record the click as the download.
			re.Type = "download"
			re.DownloadName = r.pendingDLName
			r.pendingDLName = ""
		} else {
			r.lastClickIdx = len(r.events)
			r.lastClickAt = time.Now()
		}
	}
	r.events = append(r.events, re)
	r.mu.Unlock()
}

// downloadAttributionWindow is how far apart a click and its
// Browser.downloadWillBegin may arrive and still be treated as one action.
// A var, not a const, so the package tests can widen it for starved CI
// runners where event delivery lags by many seconds.
var downloadAttributionWindow = 5 * time.Second

// upgradeLastClickToDownload upgrades the most recent click event to a "download"
// event with the given filename — called when Browser.downloadWillBegin fires
// within a short window after the click that triggered it. When no eligible
// click has been recorded yet (the download event won the race against the
// click's binding delivery), the download is parked for addEvent to claim.
func (r *Recorder) upgradeLastClickToDownload(filename string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.lastClickIdx
	if idx >= 0 && idx < len(r.events) && r.events[idx].Type == "click" &&
		time.Since(r.lastClickAt) <= downloadAttributionWindow {
		ev := r.events[idx]
		ev.Type = "download"
		ev.DownloadName = filename
		r.events[idx] = ev
		r.lastClickIdx = -1 // consumed
		return
	}
	r.pendingDLName = filename
	r.pendingDLAt = time.Now()
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
	r.watchBindingEvents(session, frameSel)
	return nil
}

// watchBindingEvents subscribes to the capture binding's calls on one session
// and appends each delivered event. Purely client-side — safe to install on a
// still-frozen target.
func (r *Recorder) watchBindingEvents(session, frameSel string) {
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
    var s=window.__octoNet={n:0, gen:0, idleSince:Date.now()};
    function inc(){ s.n++; s.gen++; s.idleSince=0; }
    function dec(){ s.n=Math.max(0,s.n-1); if(s.n===0) s.idleSince=Date.now(); }
    try{ var of=window.fetch; if(of){ window.fetch=function(){ inc(); return of.apply(this,arguments).then(function(r){dec();return r;},function(e){dec();throw e;}); }; } }catch(_){}
    try{ var send=XMLHttpRequest.prototype.send; XMLHttpRequest.prototype.send=function(){ inc(); try{ this.addEventListener('loadend',function(){dec();},{once:true}); }catch(_){ dec(); } return send.apply(this,arguments); }; }catch(_){}
  }
  // volatileId flags auto-generated ids that change per mount/session —
  // react-aria/radix/headlessui counters, "Popover12"-style numbered
  // components, ":r3:"-style useId output, long numeric runs. Anchoring a
  // recording on one produces a selector that is dead by the next session
  // (observed: Zhihu's #Popover6-content / #react-aria-2-tabpanel-…). A
  // volatile id is only DEMOTED — the attribute/path strategies and the
  // fingerprint alternates still identify the element.
  function volatileId(id){
    return /^(react-aria|radix|headlessui|downshift|floating-ui|aria)[-:]/i.test(id)
      || /^:r[0-9a-z]+:$/i.test(id)
      || /^[A-Za-z]+\d+([-_][A-Za-z0-9]+)*$/.test(id)
      || /\d{4,}/.test(id);
  }
  function attrSel(el){
    if(!el || el.nodeType!==1) return '';
    if(el.id && !volatileId(el.id)) return '#'+CSS.escape(el.id);
    for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'=\"'+CSS.escape(v)+'\"]';}
    return '';
  }
  // pathSel walks up from el building a selector path, one part per node via
  // nodeFn — anchored at the nearest STABLE-id-bearing ancestor when one exists.
  function pathSel(el, nodeFn){
    var parts=[], node=el, depth=0;
    while(node && node.nodeType===1 && node.tagName!=='BODY' && depth<6){
      if(node.id && !volatileId(node.id)){ parts.unshift('#'+CSS.escape(node.id)); return parts.join(' > '); }
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
  // interactiveAncestor retargets an event from its raw target (an svg path, a
  // text span, a bare wrapper div) to the nearest interactive ancestor — the
  // element carrying the semantics (tag, aria-label, role, text) that a stable
  // selector, a scoring fingerprint and the self-heal prompt all need.
  // Recording the raw target produced steps like "click div:nth-of-type(4)"
  // with no label/role/hint: fragile to replay and impossible to heal.
  var INTERACTIVE_ROLES={button:1,link:1,menuitem:1,menuitemcheckbox:1,menuitemradio:1,tab:1,option:1,checkbox:1,radio:1,'switch':1,combobox:1,treeitem:1};
  function interactiveAncestor(el){
    var node=el, depth=0;
    while(node && node.nodeType===1 && node!==document.body && depth<6){
      var tag=node.tagName;
      if(tag==='BUTTON'||tag==='A'||tag==='INPUT'||tag==='SELECT'||tag==='TEXTAREA'||tag==='SUMMARY'||tag==='OPTION') return node;
      var role=node.getAttribute && node.getAttribute('role');
      if(role && INTERACTIVE_ROLES[role]) return node;
      node=node.parentElement; depth++;
    }
    return el;
  }
  function report(type, e){
    try{var el=(type==='click')?interactiveAncestor(e.target):e.target; var fr=''; try{ if(window.frameElement) fr=sel(window.frameElement); }catch(_2){}
      var field=((el.placeholder||el.name||(el.getAttribute?el.getAttribute('aria-label'):'')||el.id||'')+'').trim().slice(0,40);
      var secret=(el.type==='password');
      var role=''; try{ role=el.getAttribute('role')||''; }catch(_3){}
      window.__octoRecord(JSON.stringify({type:type, selector:sel(el), frame:fr, tag:el.tagName, text:(el.textContent||'').trim().slice(0,40), field:field, secret:secret, value:(secret?'':(el.value!==undefined?(''+el.value).slice(0,200):'')), url:location.href, alt_selectors:altSels(el), role:role, neighbor_text:neighborText(el)}));}catch(_){}
  }
  /* ---- click: report + detect network activity after a short delay ---- */
  // Compare the activity GENERATION, not just in-flight count: on a loaded
  // machine the 150ms timer can fire after a fast request already completed,
  // and "n>0 right now" would miss it — "any request started since the click"
  // does not.
  document.addEventListener('click', function(e){
    report('click', e);
    var g0=window.__octoNet?window.__octoNet.gen:0;
    setTimeout(function(){ try{ var s=window.__octoNet; if(s && (s.n>0 || s.gen!==g0)) reportWait('network','',10000); }catch(_){} }, 150);
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
		// Freeze-on-create for targets attached during the demonstration: with
		// waitForDebuggerOnStart a new tab is PAUSED at creation — nothing loads,
		// renders, or accepts a click — until we finish instrumenting it and
		// resume it below. This is the CDP-native fix for the race where the user
		// clicks in a fresh tab before the captureScript is in place; Stop()
		// restores the non-freezing mode.
		_, _ = p.cli.call(ctx, p.sessionID, "Target.setAutoAttach", map[string]any{
			"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
		})
		// The session-scoped call above only fires for targets Chrome considers
		// related to the recorded page — child frames, workers, popups that keep
		// an opener. A tab opened WITHOUT an opener relationship (target="_blank"
		// is implicitly rel=noopener since Chrome 88; sites also open editors via
		// window.open(..., "noopener")) is an unrelated top-level target: no
		// attachedToTarget ever fires, the tab is never instrumented, and the rest
		// of the demonstration is silently absent from the recording. Request
		// freeze-on-create at the BROWSER level too — that scope fires for every
		// created target regardless of opener. The handler below instruments page
		// targets and resumes everything it sees, so the extra events are safe.
		_, _ = p.cli.call(ctx, "", "Target.setAutoAttach", map[string]any{
			"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
		})
		attached, attachUnsub := p.cli.subscribe("Target.attachedToTarget", "")
		r.mu.Lock()
		r.unsubs = append(r.unsubs, attachUnsub)
		r.mu.Unlock()
		go func() {
			for ev := range attached {
				var a struct {
					SessionID  string `json:"sessionId"`
					TargetInfo struct {
						TargetID string `json:"targetId"`
						Type     string `json:"type"`
						OpenerID string `json:"openerId"`
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
					r.instrumentPageSession(ictx, a.SessionID, a.TargetInfo.TargetID, a.TargetInfo.OpenerID != "")
				}
				// Resume the target regardless of type or instrumentation outcome —
				// waitForDebuggerOnStart keeps it frozen until this call, and a
				// frozen tab is strictly worse than an uninstrumented one.
				_, _ = p.cli.call(ictx, a.SessionID, "Runtime.runIfWaitingForDebugger", nil)
				icancel()
			}
		}()
	}

	// Also capture top-level navigations (address-bar, link, SPA history) so a
	// multi-page demonstration replays from the right pages — otherwise the only
	// navigate step is the synthesized start URL, and the recording loses every
	// page the user moved to.
	r.watchNavigations(ctx, p.sessionID, false)
	return nil
}

// instrumentPageSession instruments a newly attached tab (a page target) the
// same way the top document is instrumented, plus navigation capture — the demo
// may navigate the new tab (or drive an SPA in it) before touching anything.
//
// Race note: recording auto-attaches with waitForDebuggerOnStart, so this runs
// while the tab is still FROZEN at creation — nothing can load, render, or be
// clicked until the attach watcher resumes it with Runtime.runIfWaitingForDebugger
// after this returns. The captureScript is therefore in place before the page's
// very first document commits: the click-before-instrument race cannot occur.
// (An earlier design tried Page.stopLoading + re-navigate instead; it either
// cancelled the tab's first navigation irrecoverably — the destination isn't
// recorded anywhere yet — or swapped the document out from under the user's
// first click. The freeze is the mechanism CDP provides for exactly this.)
func (r *Recorder) instrumentPageSession(ctx context.Context, session, targetID string, hasOpener bool) {
	// Claim by TARGET, not by session: one tab can surface as several sessions
	// of the same client (auto-attach at creation plus a later explicit
	// Target.attachToTarget), and a page-side binding call notifies every
	// session that registered the binding — instrumenting a second session
	// would record every action twice. The "target:" prefix keeps these claims
	// disjoint from instrumentSession's session-keyed ones.
	if !r.claimSession("target:" + targetID) {
		return
	}
	// A frozen tab answers NO renderer-bound command until it is resumed — a
	// tab opened without an opener relationship gets a fresh renderer that
	// isn't serviced while paused — so a call-and-wait sequence here would
	// deadlock against the resume that only happens after we return. Instead,
	// queue the whole setup on the wire (callAsync writes immediately; wire
	// order guarantees it all applies before any page code runs), install the
	// client-side subscriptions, queue the resume, and only then collect the
	// responses. Opener-related popups whose shared renderer answers while
	// frozen take the exact same path — the awaits just complete sooner.
	cli := r.page.cli
	var waits []func(context.Context) (json.RawMessage, error)
	// Enable the domains ourselves rather than depend on the browser watcher's
	// async registration having run first — same reasoning as instrumentOOPIF.
	for _, d := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		waits = append(waits, cli.callAsync(session, d, nil))
	}
	waits = append(waits, cli.callAsync(session, "Runtime.addBinding", map[string]any{"name": "__octoRecord"}))
	waits = append(waits, cli.callAsync(session, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": captureScript}))
	r.watchBindingEvents(session, "")
	// hasOpener distinguishes a tab the PAGE spawned (its first navigation is
	// tagged NewTab so CompileRecording can collapse the click detour that
	// opened it) from a tab the USER opened by hand (Cmd+T + typed URL — the
	// preceding clicks are unrelated and must survive).
	r.watchNavigations(ctx, session, hasOpener)
	waits = append(waits, cli.callAsync(session, "Runtime.runIfWaitingForDebugger", nil))
	for _, wait := range waits {
		_, _ = wait(ctx) // best-effort, matching the attach handler's contract
	}
	// Install into an already-loaded document too (the script self-dedups). On
	// a tab attached frozen at creation this evaluates in the transient initial
	// document — harmless; the real document is covered by the on-new-document
	// registration above.
	_, _ = cli.call(ctx, session, "Runtime.evaluate", map[string]any{"expression": captureScript})
}

// watchNavigations records top-level navigations on one page session: both
// cross-document (Page.frameNavigated) and same-document SPA route changes
// (Page.navigatedWithinDocument — pushState/replaceState never fire
// frameNavigated). Subframe navigations are ignored: the cross-document event
// carries the frame's parentId, and the same-document one is filtered against
// the session's top frame (resolved asynchronously — a still-frozen tab only
// answers Page.getFrameTree after its resume, and blocking here would deadlock
// instrumentPageSession's queued-setup sequence; until it resolves the filter
// admits all same-document navs, the same fallback as a failed lookup).
// Consecutive repeats collapse.
func (r *Recorder) watchNavigations(ctx context.Context, session string, markFirstNav bool) {
	var topMu sync.Mutex
	topFrameID := ""
	waitFrameTree := r.page.cli.callAsync(session, "Page.getFrameTree", nil)
	go func() {
		res, err := waitFrameTree(ctx)
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
		if json.Unmarshal(res, &ft) == nil {
			topMu.Lock()
			topFrameID = ft.FrameTree.Frame.ID
			topMu.Unlock()
		}
	}()

	navEvents, navUnsub := r.page.cli.subscribe("Page.frameNavigated", session)
	spaEvents, spaUnsub := r.page.cli.subscribe("Page.navigatedWithinDocument", session)
	r.mu.Lock()
	r.unsubs = append(r.unsubs, navUnsub, spaUnsub)
	r.mu.Unlock()

	firstNav := markFirstNav
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
		ev := RecordedEvent{Type: "navigate", URL: u}
		if firstNav {
			ev.NewTab = true
			firstNav = false
		}
		r.events = append(r.events, ev)
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
			topMu.Lock()
			top := topFrameID
			topMu.Unlock()
			if top != "" && b.FrameID != top {
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
	// Restore non-freezing auto-attach (Start switched it to freeze-on-create so
	// new tabs pause until instrumented). Best-effort: the page may be gone.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.page.cli.call(ctx, r.page.sessionID, "Target.setAutoAttach", map[string]any{
		"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true,
	})
	// The browser-level freeze-on-create has no pre-recording baseline to
	// restore — nothing else on this connection enables browser-scoped
	// auto-attach — so turn it off entirely.
	_, _ = r.page.cli.call(ctx, "", "Target.setAutoAttach", map[string]any{
		"autoAttach": false, "waitForDebuggerOnStart": false, "flatten": true,
	})
}
