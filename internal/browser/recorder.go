package browser

import (
	"context"
	"encoding/json"
	"sync"
)

// RecordedEvent is one captured user action during a demonstration. Each event
// carries enough to replay it: the action kind plus a best-effort selector (and
// the value for inputs). Frame is set when the element lives in a same-origin
// iframe, so replay can pierce it via the " >>> " convention.
type RecordedEvent struct {
	Type     string `json:"type"`     // click | change
	Selector string `json:"selector"` // best-effort CSS selector within its document
	Frame    string `json:"frame"`    // iframe selector when the target is in a child frame
	Tag      string `json:"tag"`      // target tagName (SELECT/INPUT/…), so replay picks the right action
	Value    string `json:"value"`    // input/select value (change events)
	Text     string `json:"text"`     // element text, for context
	URL      string `json:"url"`      // document URL at capture time
}

// Recorder captures a user's actions on a page by injecting a DOM listener that
// reports through a CDP binding. It records the user's real demonstration — it
// does not drive the page itself.
type Recorder struct {
	page *Page

	mu     sync.Mutex
	events []RecordedEvent
	unsub  func()
}

// NewRecorder creates a recorder bound to a page.
func NewRecorder(page *Page) *Recorder { return &Recorder{page: page} }

// captureScript installs capture-phase click/change listeners that report each
// action (with a stable-ish selector) through the __octoRecord binding.
const captureScript = `(function(){
  if (window.__octoRec) return; window.__octoRec = true;
  function sel(el){
    if(!el || el.nodeType!==1) return '';
    if(el.id) return '#'+CSS.escape(el.id);
    for(var i=0;i<4;i++){var a=['data-testid','data-test','name','aria-label'][i];var v=el.getAttribute&&el.getAttribute(a);if(v)return el.tagName.toLowerCase()+'['+a+'=\"'+CSS.escape(v)+'\"]';}
    var parts=[], node=el, depth=0;
    while(node && node.nodeType===1 && node.tagName!=='BODY' && depth<5){
      var part=node.tagName.toLowerCase(); var p=node.parentElement;
      if(p){var same=[].slice.call(p.children).filter(function(c){return c.tagName===node.tagName;}); if(same.length>1){part+=':nth-of-type('+(same.indexOf(node)+1)+')';}}
      parts.unshift(part); node=p; depth++;
    }
    return parts.join(' > ');
  }
  function report(type, e){
    try{var el=e.target; var fr=''; try{ if(window.frameElement) fr=sel(window.frameElement); }catch(_2){}
      window.__octoRecord(JSON.stringify({type:type, selector:sel(el), frame:fr, tag:el.tagName, text:(el.textContent||'').trim().slice(0,40), value:(el.value!==undefined?(''+el.value).slice(0,200):''), url:location.href}));}catch(_){}
  }
  document.addEventListener('click', function(e){report('click',e);}, true);
  document.addEventListener('change', function(e){var t=e.target; report((t&&t.type==='file')?'upload':'change', e);}, true);
})();`

// Start begins capturing. The binding and listeners are installed on the live
// document and on every future document (so post-navigation pages and
// same-origin child frames are covered too).
func (r *Recorder) Start(ctx context.Context) error {
	p := r.page
	if _, err := p.cli.call(ctx, p.sessionID, "Runtime.addBinding", map[string]any{"name": "__octoRecord"}); err != nil {
		return err
	}
	if _, err := p.cli.call(ctx, p.sessionID, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": captureScript}); err != nil {
		return err
	}
	// Install into the already-loaded document too.
	if err := p.Eval(ctx, captureScript, nil); err != nil {
		return err
	}

	events, unsub := p.cli.subscribe("Runtime.bindingCalled", p.sessionID)
	r.mu.Lock()
	r.unsub = unsub
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
			r.mu.Lock()
			r.events = append(r.events, re)
			r.mu.Unlock()
		}
	}()
	return nil
}

// Events returns the captured actions so far.
func (r *Recorder) Events() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

// Replay re-executes recorded events directly (no skill file, no params,
// no healer) — a convenience over the skill path for raw record→replay.
func Replay(ctx context.Context, page *Page, events []RecordedEvent) error {
	skill := CompileSkill("", "", "", events)
	_, err := ReplaySkill(ctx, page, &skill, nil, ReplayOptions{})
	return err
}

// Stop ends capture (the subscription is dropped; the page-side listeners are
// harmless and go away on navigation/close).
func (r *Recorder) Stop() {
	r.mu.Lock()
	unsub := r.unsub
	r.unsub = nil
	r.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}
