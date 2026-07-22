package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRecorderAnchorsSelectorAtNearestID: an element with no id of its own gets a
// selector anchored at its nearest id-bearing ancestor, not a free nth-of-type
// chain from <body> — shorter and far more stable across layout changes.
func TestRecorderAnchorsSelectorAtNearestID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>rec</title>
<div><div id="panel"><div><span><a>Open</a></span></div></div></div>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#panel a", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := page.Click(ctx, "#panel a"); err != nil {
		t.Fatalf("click: %v", err)
	}
	var evs []RecordedEvent
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if evs = rec.Events(); len(evs) >= 1 {
			break
		}
	}
	rec.Stop()
	if len(evs) == 0 {
		t.Fatal("no events captured")
	}
	if !strings.HasPrefix(evs[0].Selector, "#panel") {
		t.Fatalf("selector should anchor at the nearest id (#panel), got %q", evs[0].Selector)
	}
}

// TestRecorderCapturesActions records a demonstration: a click and a select are
// driven via trusted input (standing in for a human), and the recorder must
// capture both with usable selectors and values.
func TestRecorderCapturesActions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>rec</title>
<button id="b">Go</button>
<select id="s"><option value="a">A</option><option value="b">B</option></select>
<script>window.clicks=0;document.getElementById('b').addEventListener('click',function(){window.clicks++});</script>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#b", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}

	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("recorder start: %v", err)
	}

	if err := page.Click(ctx, "#b"); err != nil {
		t.Fatalf("click: %v", err)
	}
	if err := page.SelectOption(ctx, "#s", "b"); err != nil {
		t.Fatalf("select: %v", err)
	}

	// Events arrive asynchronously over the binding channel.
	var evs []RecordedEvent
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if evs = rec.Events(); len(evs) >= 2 {
			break
		}
	}
	rec.Stop()

	var sawClick, sawChange bool
	for _, e := range evs {
		if e.Type == "click" && e.Selector == "#b" {
			sawClick = true
		}
		if e.Type == "change" && e.Selector == "#s" && e.Value == "b" {
			sawChange = true
		}
	}
	if !sawClick {
		t.Errorf("did not capture click on #b; got %+v", evs)
	}
	if !sawChange {
		t.Errorf("did not capture change on #s=b; got %+v", evs)
	}

	// Replay the recording on a fresh page and confirm it reproduces the effects.
	fresh, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("fresh page: %v", err)
	}
	if err := fresh.WaitFor(ctx, "#b", testWaitTimeout); err != nil {
		t.Fatalf("wait fresh: %v", err)
	}
	if err := Replay(ctx, fresh, evs); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var clicks int
	var selVal string
	if err := fresh.Eval(ctx, "window.clicks", &clicks); err != nil {
		t.Fatalf("eval clicks: %v", err)
	}
	if err := fresh.Eval(ctx, "document.querySelector('#s').value", &selVal); err != nil {
		t.Fatalf("eval select: %v", err)
	}
	if clicks < 1 {
		t.Errorf("replay did not click the button (clicks=%d)", clicks)
	}
	if selVal != "b" {
		t.Errorf("replay did not set the select (value=%q)", selVal)
	}
}

// TestRecorderCapturesNewTab: a demonstration that opens a new tab (target=_blank)
// and keeps going there is captured end to end — previously everything after the
// tab switch was silently lost (the attach watcher only instrumented iframes).
func TestRecorderCapturesNewTab(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/b", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>b</title><button id="bb">B</button>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>a</title><a id="open" href="/b" target="_blank">Open</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#open", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	np, err := b.ClickFollow(ctx, page, "#open")
	if err != nil {
		t.Fatalf("open new tab: %v", err)
	}
	if np == page {
		t.Fatal("click did not open a new tab")
	}
	if err := np.WaitFor(ctx, "#bb", testWaitTimeout); err != nil {
		t.Fatalf("wait #bb: %v", err)
	}
	// The recorder instruments the new tab off the attach event — give it a beat
	// before driving the click it must capture.
	time.Sleep(500 * time.Millisecond)
	if err := np.Click(ctx, "#bb"); err != nil {
		t.Fatalf("click #bb: %v", err)
	}
	found := false
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "click" && e.Selector == "#bb" {
				found = true
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("click in the new tab was not captured: %+v", rec.Events())
	}
}

// TestRecorderCapturesNoopenerNewTab: a demonstration continuing in a tab opened
// WITHOUT an opener relationship — window.open(..., "noopener"), the shape of
// e.g. Zhihu's 写文章 button — must still be captured. Session-scoped auto-attach
// never fires for unrelated targets, so this only works through the browser-level
// freeze-on-create auto-attach; both the new tab's first navigation and the click
// inside it must land in the recording.
func TestRecorderCapturesNoopenerNewTab(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/b", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>b</title><button id="bb">B</button>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>a</title>
<button id="open">Open</button>
<script>document.getElementById('open').addEventListener('click',function(){window.open('/b','_blank','noopener')});</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#open", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	np, err := b.ClickFollow(ctx, page, "#open")
	if err != nil {
		t.Fatalf("open new tab: %v", err)
	}
	if np == page {
		t.Fatal("click did not open a new tab")
	}
	if err := np.WaitFor(ctx, "#bb", testWaitTimeout); err != nil {
		t.Fatalf("wait #bb: %v", err)
	}
	// Give the attach-event instrumentation a beat, as in the opener variant.
	time.Sleep(500 * time.Millisecond)
	if err := np.Click(ctx, "#bb"); err != nil {
		t.Fatalf("click #bb: %v", err)
	}
	var sawNav, sawClick bool
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "navigate" && strings.HasSuffix(e.URL, "/b") {
				sawNav = true
			}
			if e.Type == "click" && e.Selector == "#bb" {
				sawClick = true
			}
		}
		if sawNav && sawClick {
			break
		}
	}
	if !sawClick {
		t.Fatalf("click in the noopener tab was not captured: %+v", rec.Events())
	}
	if !sawNav {
		t.Fatalf("the noopener tab's navigation was not captured (replay would lose the page): %+v", rec.Events())
	}
	// Exactly one capture — a double-attach (browser-level + session-level
	// auto-attach both firing for the same target) would instrument the tab
	// twice and duplicate every event.
	count := 0
	for _, e := range rec.Events() {
		if e.Type == "click" && e.Selector == "#bb" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 captured click on #bb, got %d: %+v", count, rec.Events())
	}
}

// TestRecorderCapturesNewTab_ClickBeforeInstrument: the user clicks in a freshly
// opened tab immediately — before instrumentation would normally finish. The
// freeze-on-create auto-attach (waitForDebuggerOnStart) guarantees the page
// cannot load or be interacted with until the recorder is in place, so the
// click must still be captured. Regression test for the race where early
// new-tab clicks were silently lost.
func TestRecorderCapturesNewTab_ClickBeforeInstrument(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/b", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>b</title><button id="bb">B</button>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>a</title><a id="open" href="/b" target="_blank">Open</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#open", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	np, err := b.ClickFollow(ctx, page, "#open")
	if err != nil {
		t.Fatalf("open new tab: %v", err)
	}
	if np == page {
		t.Fatal("click did not open a new tab")
	}
	// Wait for the button to be visible. The tab is frozen at creation until
	// instrumentation completes, so #bb can only render after the recorder is
	// fully in place.
	if err := np.WaitFor(ctx, "#bb", testWaitTimeout); err != nil {
		t.Fatalf("wait #bb: %v", err)
	}
	// Now click immediately and verify capture — no extra sleep for instrument.
	if err := np.Click(ctx, "#bb"); err != nil {
		t.Fatalf("click #bb: %v", err)
	}
	found := false
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "click" && e.Selector == "#bb" {
				found = true
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("immediate click in the new tab was not captured (race condition): %+v", rec.Events())
	}
	// Verify no duplicate clicks were captured (reload shouldn't re-trigger).
	count := 0
	for _, e := range rec.Events() {
		if e.Type == "click" && e.Selector == "#bb" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 captured click on #bb, got %d: %+v", count, rec.Events())
	}
}

// TestAutoWaitNetwork: a click that triggers a fetch/XHR is followed by an
// auto-inserted "network" wait event — the page is loading data and the next
// step must not race ahead of it.
func TestAutoWaitNetwork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/data", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("loaded"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>net</title>
<button id="load">Load</button>
<div id="result"></div>
<script>
document.getElementById('load').addEventListener('click', function(){
  fetch('/data').then(function(r){return r.text()}).then(function(t){
	document.getElementById('result').textContent=t;
  });
});
</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#load", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	if err := page.Click(ctx, "#load"); err != nil {
		t.Fatalf("click: %v", err)
	}
	// Wait for the auto-inserted network wait event.
	var waitEv *RecordedEvent
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "wait" && e.WaitKind == "network" {
				waitEv = &e
				break
			}
		}
		if waitEv != nil {
			break
		}
	}
	if waitEv == nil {
		t.Fatalf("expected auto-inserted network wait event, got: %+v", rec.Events())
	}
	// The wait must appear AFTER the click in the events slice.
	clickIdx, waitIdx := -1, -1
	for i, e := range rec.Events() {
		if e.Type == "click" && e.Selector == "#load" {
			clickIdx = i
		}
		if e.Type == "wait" && e.WaitKind == "network" {
			waitIdx = i
		}
	}
	if clickIdx == -1 || waitIdx == -1 || waitIdx <= clickIdx {
		t.Fatalf("network wait should come after click (click=%d wait=%d), events=%+v", clickIdx, waitIdx, rec.Events())
	}
}

// TestAutoWaitElement: when a click causes a modal/dialog to appear, an
// auto-inserted "element" wait event for that modal is emitted — the next step
// that interacts with the modal must wait for it to be present.
func TestAutoWaitElement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>modal</title>
<button id="open">Open</button>
<script>
document.getElementById('open').addEventListener('click', function(){
  var d=document.createElement('div');
  d.id='mymodal';
  d.setAttribute('role','dialog');
  d.className='modal';
  d.innerHTML='<button id="ok">OK</button>';
  document.body.appendChild(d);
});
</script>`))
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#open", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	if err := page.Click(ctx, "#open"); err != nil {
		t.Fatalf("click: %v", err)
	}
	// Wait for the auto-inserted element wait event targeting the modal.
	var waitEv *RecordedEvent
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "wait" && e.WaitKind == "element" && e.Selector == "#mymodal" {
				waitEv = &e
				break
			}
		}
		if waitEv != nil {
			break
		}
	}
	if waitEv == nil {
		t.Fatalf("expected auto-inserted element wait event for #mymodal, got: %+v", rec.Events())
	}
}

// TestCompileRecordingWaitEvents: wait events compiled into wait steps — a
// "network" wait becomes a wait step with network:true; an "element" wait
// becomes a wait step with a selector.
func TestCompileRecordingWaitEvents(t *testing.T) {
	events := []RecordedEvent{
		{Type: "click", Selector: "#b", Tag: "BUTTON"},
		{Type: "wait", WaitKind: "network", TimeoutMS: 10000},
		{Type: "click", Selector: "#b2", Tag: "BUTTON"},
		{Type: "wait", WaitKind: "element", Selector: "#modal"},
	}
	rec := CompileRecording("test", "test desc", "", events)
	if len(rec.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d: %+v", len(rec.Steps), rec.Steps)
	}
	if s := rec.Steps[1]; s.Action != "wait" || !s.Network || s.TimeoutMS != 10000 {
		t.Fatalf("network wait step wrong: %+v", s)
	}
	if s := rec.Steps[3]; s.Action != "wait" || s.Selector != "#modal" {
		t.Fatalf("element wait step wrong: %+v", s)
	}
}

// TestSelectorSemanticAnchor: a click on a calendar cell (role="gridcell",
// class="ant-picker-cell") produces a selector that includes those semantic
// anchors instead of a bare :nth-of-type chain — so the recording survives
// layout shifts that a purely positional selector would not.
func TestSelectorSemanticAnchor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>cal</title>
<table class="ant-picker-content">
  <tbody>
	<tr role="row"><td role="gridcell" class="ant-picker-cell"><div class="ant-picker-cell-inner">19</div></td><td role="gridcell" class="ant-picker-cell"><div class="ant-picker-cell-inner">20</div></td></tr>
  </tbody>
</table>`))
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, ".ant-picker-content", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	// Click the cell containing "20" — second cell in the row.
	if err := page.Click(ctx, ".ant-picker-cell:nth-of-type(2) .ant-picker-cell-inner"); err != nil {
		t.Fatalf("click: %v", err)
	}
	var got string
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "click" {
				got = e.Selector
			}
		}
		if got != "" {
			break
		}
	}
	if got == "" {
		t.Fatalf("no click captured: %+v", rec.Events())
	}
	// Must include both the role and class anchors — proves semantic anchoring.
	if !strings.Contains(got, "role") {
		t.Fatalf("expected selector to carry role anchor, got %q", got)
	}
	if !strings.Contains(got, "ant-picker-cell-inner") {
		t.Fatalf("expected selector to carry structural class, got %q", got)
	}
}

// TestRecorderCapturesEnter: Enter in a text input is captured with the field's
// current value (change never fires without a blur); Enter in a textarea is a
// newline, not a submit, and must not be captured.
func TestRecorderCapturesEnter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>enter</title>
<form onsubmit="event.preventDefault()"><input id="q"></form><textarea id="t"></textarea>`))
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#q", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()
	if err := page.TypeText(ctx, "#q", "hello"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := page.Click(ctx, "#q"); err != nil {
		t.Fatalf("focus #q: %v", err)
	}
	if err := page.Key(ctx, "enter"); err != nil {
		t.Fatalf("enter #q: %v", err)
	}
	if err := page.Click(ctx, "#t"); err != nil {
		t.Fatalf("focus #t: %v", err)
	}
	if err := page.Key(ctx, "enter"); err != nil {
		t.Fatalf("enter #t: %v", err)
	}
	var enters []RecordedEvent
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		enters = enters[:0]
		for _, e := range rec.Events() {
			if e.Type == "enter" {
				enters = append(enters, e)
			}
		}
		if len(enters) >= 1 {
			break
		}
	}
	if len(enters) != 1 {
		t.Fatalf("want exactly 1 enter event (the textarea one excluded), got %d: %+v", len(enters), rec.Events())
	}
	if enters[0].Selector != "#q" || enters[0].Value != "hello" {
		t.Fatalf("enter event = %+v, want selector #q with the typed snapshot", enters[0])
	}
}

// TestRecorderCapturesSPANavigation: history.pushState route changes are recorded
// as navigate events (they never fire Page.frameNavigated), while SPA routing
// inside a subframe is ignored — it isn't the page moving.
func TestRecorderCapturesSPANavigation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/frame", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>f</title><button id="fb" onclick="history.pushState({},'','/frame2')">F</button>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>spa</title>
<button id="go" onclick="history.pushState({},'','/route2')">Go</button><iframe src="/frame"></iframe>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#go", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()
	if err := page.Click(ctx, "#go"); err != nil {
		t.Fatalf("click #go: %v", err)
	}
	hasNav := func(sub string) bool {
		for _, e := range rec.Events() {
			if e.Type == "navigate" && strings.Contains(e.URL, sub) {
				return true
			}
		}
		return false
	}
	found := false
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if found = hasNav("/route2"); found {
			break
		}
	}
	if !found {
		t.Fatalf("pushState navigation was not captured: %+v", rec.Events())
	}
	// SPA routing inside the iframe must NOT produce a navigate event. Wait for
	// the iframe's button first — the top document's #go can be found (and the
	// pushState captured) while a slow runner is still loading the iframe.
	if err := page.WaitFor(ctx, "iframe >>> #fb", testWaitTimeout); err != nil {
		t.Fatalf("wait iframe button: %v", err)
	}
	if err := page.Eval(ctx, `document.querySelector('iframe').contentDocument.getElementById('fb').click()`, nil); err != nil {
		t.Fatalf("drive iframe pushState: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if hasNav("/frame2") {
		t.Fatalf("subframe SPA routing should be ignored: %+v", rec.Events())
	}
}

// TestAutoDownloadDetection: a click that triggers a browser download is
// upgraded to a "download" event with the suggested filename — so the compiled
// recording emits a download step that replay uses to capture the file.
func TestAutoDownloadDetection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/file.xlsx", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="report.xlsx"`)
		w.Write([]byte("xlsx-bytes"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>dl</title><a id="dl" href="/file.xlsx" download>Export</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#dl", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	if err := page.Click(ctx, "#dl"); err != nil {
		t.Fatalf("click: %v", err)
	}
	// Wait for the click to be upgraded to a download event. The ceiling
	// matches testWaitTimeout: downloadWillBegin delivery on a starved runner
	// can lag the click by many seconds (5s of polling flaked on windows-latest).
	found := false
	deadline := time.Now().Add(testWaitTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "download" && e.DownloadName == "report.xlsx" {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("expected click to be upgraded to download event, got: %+v", rec.Events())
	}
}

// TestRecorderStopCleansUpDownloadDir: the temp directory watchDownloads
// creates to receive recording-time downloads must not outlive the recorder —
// Stop() should remove it so repeated record/stop cycles don't leak temp dirs
// on disk.
func TestRecorderStopCleansUpDownloadDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>dl-dir</title><button id="b">B</button>`))
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#b", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	var dlDir string
	for i := 0; i < 50; i++ {
		rec.mu.Lock()
		dlDir = rec.dlDir
		rec.mu.Unlock()
		if dlDir != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dlDir == "" {
		t.Fatal("watchDownloads never set a download dir")
	}
	if _, err := os.Stat(dlDir); err != nil {
		t.Fatalf("download dir should exist while recording: %v", err)
	}

	rec.Stop()

	if _, err := os.Stat(dlDir); !os.IsNotExist(err) {
		t.Fatalf("download dir should be removed after Stop, stat err: %v", err)
	}
}

// TestCompileRecordingDownloadEvent: a download event compiles to a download step
// with an auto-declared file[] output binding.
func TestCompileRecordingDownloadEvent(t *testing.T) {
	events := []RecordedEvent{
		{Type: "click", Selector: "#b", Tag: "BUTTON"},
		{Type: "download", Selector: "#dl", Tag: "A", DownloadName: "report.xlsx"},
	}
	rec := CompileRecording("test", "test desc", "", events)
	if len(rec.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d: %+v", len(rec.Steps), rec.Steps)
	}
	dl := rec.Steps[1]
	if dl.Action != "download" {
		t.Fatalf("expected download step, got %+v", dl)
	}
	if dl.Selector != "#dl" {
		t.Fatalf("download selector wrong: %+v", dl)
	}
	if dl.Bind == "" {
		t.Fatalf("download step should bind to an output")
	}
	// Output must be declared as file[].
	var found bool
	for _, o := range rec.Outputs {
		if o.Name == dl.Bind {
			found = true
			if o.Type != "file[]" {
				t.Fatalf("output %q should be file[], got %q", o.Name, o.Type)
			}
		}
	}
	if !found {
		t.Fatalf("bind %q not declared in outputs %+v", dl.Bind, rec.Outputs)
	}
}

// TestRecorderCapturesAnchorFacts: a captured click carries the element's
// fingerprint — alternate selectors (built with a different strategy than the
// primary), the role attribute, and the nearest label-like neighbor text.
func TestRecorderCapturesAnchorFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>anchors</title>
<div><span>操作</span><button role="tab" class="btn-export">导出</button></div>`))
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, ".btn-export", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rec.Stop()

	if err := page.Click(ctx, ".btn-export"); err != nil {
		t.Fatalf("click: %v", err)
	}
	var ev *RecordedEvent
	for i := 0; i < 50 && ev == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, e := range rec.Events() {
			if e.Type == "click" {
				ev = &e
				break
			}
		}
	}
	if ev == nil {
		t.Fatalf("no click captured: %+v", rec.Events())
	}
	if ev.Role != "tab" {
		t.Fatalf("role not captured: %+v", ev)
	}
	if ev.NeighborText != "操作" {
		t.Fatalf("neighbor text not captured: %+v", ev)
	}
	if len(ev.AltSelectors) == 0 {
		t.Fatalf("alternate selectors not captured: %+v", ev)
	}
	for _, alt := range ev.AltSelectors {
		if alt == ev.Selector {
			t.Fatalf("alternate duplicates the primary selector: %+v", ev)
		}
	}
}
