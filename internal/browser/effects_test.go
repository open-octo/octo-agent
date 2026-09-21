package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestAttachEffectsAttribution: URLAfter comes from the navigations between a
// gesture and the next one (last stop of a redirect chain, only when it
// differs from the URL the gesture was made on); requests attach to the most
// recent gesture at or before their arrival and only inside the window; a
// request before any gesture, or long after the last, attaches to nothing;
// navigate/wait events get no Effects.
func TestAttachEffectsAttribution(t *testing.T) {
	events := []RecordedEvent{
		{Type: "click", Selector: "#a", URL: "https://x/a", At: 1000},
		{Type: "navigate", URL: "https://x/b", At: 1100},
		{Type: "navigate", URL: "https://x/c", At: 1150, NewTab: true},
		{Type: "click", Selector: "#b", URL: "https://x/c", At: 3000},
		{Type: "wait", WaitKind: "network", At: 3100},
		{Type: "navigate", URL: "https://x/c", At: 3200, SameDoc: true}, // re-announced same URL: not a change
		{Type: "click", Selector: "#c", URL: "https://x/c", At: 5000},
	}
	ms := func(v int64) time.Time { return time.UnixMilli(v) }
	reqs := []recordedRequest{
		{Method: "GET", At: ms(600)},   // 400ms before any gesture: past the lead tolerance, nobody's
		{Method: "POST", At: ms(1200)}, // → #a
		{Method: "GET", At: ms(3400)},  // → #b
		{Method: "GET", At: ms(3500)},  // → #b
		{Method: "", At: ms(3600)},     // → #b, method defaults to GET
		{Method: "GET", At: ms(9000)},  // 4s after #c: outside the window
		{Method: "POST", At: ms(4900)}, // 100ms BEFORE #c: the cross-goroutine stamp race → still #c's
		{Method: "PUT", At: ms(4600)},  // 400ms before #c (past the lead tolerance), 1.6s after #b (past the window): nobody's
	}
	attachEffects(events, reqs)

	a := events[0].Effects
	if a == nil || a.URLAfter != "https://x/c" || !a.NewTab || a.Requests["POST"] != 1 || len(a.Requests) != 1 {
		t.Fatalf("#a effects = %+v", a)
	}
	b := events[3].Effects
	if b == nil || b.URLAfter != "" || b.NewTab || b.Requests["GET"] != 3 || len(b.Requests) != 1 {
		t.Fatalf("#b effects = %+v", b)
	}
	c := events[6].Effects
	if c == nil || c.URLAfter != "" || c.Requests["POST"] != 1 || len(c.Requests) != 1 {
		t.Fatalf("#c effects = %+v (want only the POST stamped just before it)", c)
	}
	for _, i := range []int{1, 2, 4, 5} {
		if events[i].Effects != nil {
			t.Fatalf("event %d (%s) must carry no Effects: %+v", i, events[i].Type, events[i].Effects)
		}
	}
}

// TestMarkLikelyNoopTailOnly: walking back from the end, GET-only / request-
// less clicks are marked and waits are skipped; the first write, URL change,
// typed value, navigation or legacy event (no Effects) stops the walk, so an
// identical no-op click earlier in the recording stays unmarked.
func TestMarkLikelyNoopTailOnly(t *testing.T) {
	fx := func(reqs map[string]int, urlAfter string) *Effects {
		return &Effects{Requests: reqs, URLAfter: urlAfter}
	}
	events := []RecordedEvent{
		{Type: "click", Selector: "#early-noop", Effects: fx(nil, "")}, // no-op, but in the middle
		{Type: "click", Selector: "#write", Effects: fx(map[string]int{"POST": 1}, "")},
		{Type: "click", Selector: "#read", Effects: fx(map[string]int{"GET": 1}, "")},
		{Type: "wait", WaitKind: "network"},
		{Type: "click", Selector: "#nothing", Effects: fx(nil, "")},
		{Type: "enter", Selector: "#q", Effects: fx(nil, "")}, // a bare Enter (nothing typed) that did nothing
		{Type: "wait", WaitKind: "network"},
	}
	markLikelyNoop(events)
	want := map[string]bool{"#read": true, "#nothing": true, "#q": true}
	for _, e := range events {
		if e.LikelyNoop != want[e.Selector] {
			t.Fatalf("%s likely_noop=%v, want %v (%+v)", e.Selector, e.LikelyNoop, want[e.Selector], events)
		}
	}

	stoppers := map[string][]RecordedEvent{
		"url change":   {{Type: "click", Effects: fx(nil, "https://x/next")}},
		"new tab":      {{Type: "click", Effects: &Effects{NewTab: true}}},
		"download":     {{Type: "download", Effects: &Effects{Download: true}}},
		"typed":        {{Type: "change", Effects: fx(nil, "")}},
		"typed+enter":  {{Type: "enter", Value: "octo", Effects: fx(nil, "")}}, // Enter carrying what was typed: a submission
		"secret+enter": {{Type: "enter", Secret: true, Effects: fx(nil, "")}},
		"navigate":     {{Type: "navigate", URL: "https://x/n"}},
		"legacy":       {{Type: "click"}}, // no Effects: an older recorder, nothing to judge by
	}
	for name, tail := range stoppers {
		evs := append([]RecordedEvent{{Type: "click", Selector: "#before", Effects: fx(nil, "")}}, tail...)
		markLikelyNoop(evs)
		for _, e := range evs {
			if e.LikelyNoop {
				t.Fatalf("%s: nothing may be marked when the tail is a %s, got %+v", name, name, evs)
			}
		}
	}
}

func TestEffectsString(t *testing.T) {
	if got := (&Effects{}).String(); got != "none" {
		t.Fatalf("empty effects = %q", got)
	}
	fx := &Effects{URLAfter: "https://x/n", NewTab: true, Download: true, Requests: map[string]int{"POST": 1, "GET": 2}}
	if got := fx.String(); got != "url→https://x/n new_tab download GET×2 POST×1" {
		t.Fatalf("effects string = %q", got)
	}
	var nilFx *Effects
	if nilFx.String() != "" {
		t.Fatal("nil effects must render empty")
	}
}

// TestRecorderCapturesEffects (Chrome): a real recording attributes each
// click's requests by method, sees a navigation as a URL change, and marks
// only the trailing no-op clicks. Click order: a POST, a navigation, a GET, a
// click that touches nothing — so the tail (GET, nothing) is marked and the
// navigation stops the walk.
func TestRecorderCapturesEffects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	const page = `<!doctype html><title>fx</title>
<button id="post" onclick="fetch('/api/w',{method:'POST',body:'x'})">保存</button>
<button id="nav" onclick="location.href='/next'">下一页</button>
<button id="get" onclick="fetch('/api/r')">刷新</button>
<button id="none" onclick="document.getElementById('box').hidden=!document.getElementById('box').hidden">展开</button>
<div id="box" hidden>内容</div>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(page))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	pg, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := pg.WaitFor(ctx, "#none", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// A gesture and the request it causes are stamped by two different
	// consumers, and a loaded runner can leave more than the default window
	// between them — which drops the request instead of attributing it. The
	// window is a var for exactly this; widen it while this test runs so the
	// assertions below are about attribution, not about runner speed.
	origWindow := effectsAttributionWindow
	effectsAttributionWindow = 30 * time.Second
	t.Cleanup(func() { effectsAttributionWindow = origWindow })

	rec := NewRecorder(pg)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	recordedClicks := func() []RecordedEvent {
		var out []RecordedEvent
		for _, e := range rec.Events() {
			if e.Type == "click" {
				out = append(out, e)
			}
		}
		return out
	}
	// click waits until the recorder has both the gesture and whatever it was
	// supposed to cause, instead of sleeping a fixed amount and hoping. A
	// request is attributed to the last gesture stamped at or before it, so a
	// request still on the wire when the NEXT click is stamped is credited to
	// that one — which is what a fixed sleep let a slow runner do (the #get
	// fetch landing after #none, leaving #get with no effects at all).
	// Waiting for the effect to be recorded makes the ordering the test's
	// choice rather than the runner's.
	n := 0
	click := func(sel string, recorded func(RecordedEvent) bool) {
		t.Helper()
		if err := pg.Click(ctx, sel); err != nil {
			t.Fatalf("click %s: %v", sel, err)
		}
		n++
		deadline := time.Now().Add(20 * time.Second)
		for {
			got := recordedClicks()
			if len(got) >= n && recorded(got[n-1]) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("click %s: the recorder never saw its effect; clicks so far = %+v", sel, got)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	recordedAtAll := func(RecordedEvent) bool { return true }

	click("#post", func(e RecordedEvent) bool { return e.Effects != nil && e.Effects.Requests["POST"] >= 1 })
	click("#nav", func(e RecordedEvent) bool {
		return e.Effects != nil && strings.HasSuffix(e.Effects.URLAfter, "/next")
	})
	if err := pg.WaitFor(ctx, "#none", testWaitTimeout); err != nil {
		t.Fatalf("wait after nav: %v", err)
	}
	click("#get", func(e RecordedEvent) bool { return e.Effects != nil && e.Effects.Requests["GET"] >= 1 })
	// Nothing to wait for on the last one — its whole point is that it causes
	// nothing — so it gets the one fixed settle left in the test, long enough
	// for a request it did not make to show up and fail the assertion below.
	click("#none", recordedAtAll)
	time.Sleep(500 * time.Millisecond)

	var clicks []RecordedEvent
	for _, e := range rec.Events() {
		if e.Type == "click" {
			clicks = append(clicks, e)
		}
	}
	if len(clicks) != 4 {
		t.Fatalf("want 4 clicks, got %d: %+v", len(clicks), rec.Events())
	}
	post, nav, get, none := clicks[0], clicks[1], clicks[2], clicks[3]
	if post.Effects == nil || post.Effects.Requests["POST"] < 1 || post.Effects.URLAfter != "" || post.LikelyNoop {
		t.Fatalf("#post effects = %+v likely_noop=%v", post.Effects, post.LikelyNoop)
	}
	if nav.Effects == nil || !strings.HasSuffix(nav.Effects.URLAfter, "/next") || nav.LikelyNoop {
		t.Fatalf("#nav effects = %+v likely_noop=%v", nav.Effects, nav.LikelyNoop)
	}
	if get.Effects == nil || get.Effects.Requests["GET"] < 1 || len(get.Effects.Requests) != 1 || !get.LikelyNoop {
		t.Fatalf("#get effects = %+v likely_noop=%v (want GET-only, marked)", get.Effects, get.LikelyNoop)
	}
	if none.Effects == nil || len(none.Effects.Requests) != 0 || none.Effects.URLAfter != "" || !none.LikelyNoop {
		t.Fatalf("#none effects = %+v likely_noop=%v (want empty, marked)", none.Effects, none.LikelyNoop)
	}
	for _, e := range clicks {
		if e.At == 0 {
			t.Fatalf("every gesture must be timestamped: %+v", e)
		}
	}
}
