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
	if err := page.WaitFor(ctx, "#panel a", 5*time.Second); err != nil {
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
	if err := page.WaitFor(ctx, "#b", 5*time.Second); err != nil {
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
	if err := fresh.WaitFor(ctx, "#b", 5*time.Second); err != nil {
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
