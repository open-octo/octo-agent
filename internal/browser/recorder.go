package browser

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// RecordedEvent is one captured user action during a demonstration. Each event
// carries enough to replay it: the action kind plus a best-effort selector (and
// the value for inputs). Frame is set when the element lives in a same-origin
// iframe, so replay can pierce it via the " >>> " convention.
type RecordedEvent struct {
	Type     string `json:"type"`     // click | change | upload | navigate
	Selector string `json:"selector"` // best-effort CSS selector within its document
	Frame    string `json:"frame"`    // iframe selector when the target is in a child frame
	Tag      string `json:"tag"`      // target tagName (SELECT/INPUT/…), so replay picks the right action
	Value    string `json:"value"`    // input/select value (change events)
	Text     string `json:"text"`     // element text, for context
	Field    string `json:"field"`    // input's placeholder/name/aria-label/id — names the auto-param
	Secret   bool   `json:"secret"`   // password input: don't persist the value as a param default
	URL      string `json:"url"`      // document URL at capture time
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
}

// NewRecorder creates a recorder bound to a page.
func NewRecorder(page *Page) *Recorder { return &Recorder{page: page, seen: map[string]bool{}} }

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
// unreadable, so the script couldn't tag it).
func (r *Recorder) addEvent(re RecordedEvent, frameSel string) {
	if frameSel != "" {
		re.Frame = frameSel
	}
	r.mu.Lock()
	r.events = append(r.events, re)
	r.mu.Unlock()
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
const captureScript = `(function(){
  if (window.__octoRec) return; window.__octoRec = true;
  function sel(el){
    if(!el || el.nodeType!==1) return '';
    if(el.id) return '#'+CSS.escape(el.id);
    for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'=\"'+CSS.escape(v)+'\"]';}
    var parts=[], node=el, depth=0;
    while(node && node.nodeType===1 && node.tagName!=='BODY' && depth<6){
      // Anchor the path at the nearest ancestor carrying an id: short and stable,
      // versus a free-floating nth-of-type chain from <body> that any layout
      // shift breaks. (el itself has no id here — that returned above.)
      if(node.id){ parts.unshift('#'+CSS.escape(node.id)); return parts.join(' > '); }
      var part=node.tagName.toLowerCase(); var p=node.parentElement;
      if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;}); if(same.length>1){part+=':nth-of-type('+(same.indexOf(node)+1)+')';}}
      parts.unshift(part); node=p; depth++;
    }
    return parts.join(' > ');
  }
  function report(type, e){
    try{var el=e.target; var fr=''; try{ if(window.frameElement) fr=sel(window.frameElement); }catch(_2){}
      var field=((el.placeholder||el.name||(el.getAttribute?el.getAttribute('aria-label'):'')||el.id||'')+'').trim().slice(0,40);
      var secret=(el.type==='password');
      window.__octoRecord(JSON.stringify({type:type, selector:sel(el), frame:fr, tag:el.tagName, text:(el.textContent||'').trim().slice(0,40), field:field, secret:secret, value:(secret?'':(el.value!==undefined?(''+el.value).slice(0,200):'')), url:location.href}));}catch(_){}
  }
  document.addEventListener('click', function(e){report('click',e);}, true);
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
	r.mu.Unlock()
	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
}
