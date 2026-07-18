package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRecorderAnchorsSelectorAtNearestID: an element with no id of its own gets a
// selector anchored at its nearest id-bearing ancestor, not a free nth-of-type
// chain from <body> — shorter and far more stable across layout changes.
func TestRecorderAnchorsSelectorAtNearestID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

// TestRecorderCapturesEnter: Enter in a text input is captured with the field's
// current value (change never fires without a blur); Enter in a textarea is a
// newline, not a submit, and must not be captured.
func TestRecorderCapturesEnter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	// SPA routing inside the iframe must NOT produce a navigate event.
	if err := page.Eval(ctx, `document.querySelector('iframe').contentDocument.getElementById('fb').click()`, nil); err != nil {
		t.Fatalf("drive iframe pushState: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if hasNav("/frame2") {
		t.Fatalf("subframe SPA routing should be ignored: %+v", rec.Events())
	}
}
