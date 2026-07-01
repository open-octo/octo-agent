package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// oopifServer serves a parent page embedding a cross-origin iframe. The iframe's
// src uses a different host string (localhost vs 127.0.0.1) than the parent so
// site isolation puts it in its own process — a real OOPIF, unreachable via the
// parent's contentDocument. innerBody is the iframe document's <body> contents.
func oopifServer(t *testing.T, innerBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/inner", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>inner</title>%s`, innerBody)
	})
	var srv *httptest.Server
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		inner := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/inner"
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>outer</title>
<div style="height:40px">header</div><iframe id="f" src=%q style="width:400px;height:300px;border:0"></iframe>`, inner)
	})
	srv = httptest.NewServer(mux)
	return srv
}

func newOOPIFBrowser(t *testing.T, ctx context.Context) *Browser {
	t.Helper()
	b, err := Launch(ctx, LaunchOptions{Headless: true, ExtraArgs: []string{"--site-per-process"}})
	if err != nil {
		t.Skipf("chrome unavailable: %v", err)
	}
	return b
}

// TestOOPIFReplay: replay clicks a button inside a cross-origin iframe. The step's
// frame selector routes into the OOPIF's own session; the click lands there, and a
// verification WaitFor through the same frame path confirms it.
func TestOOPIFReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := oopifServer(t, `<button id="go" onclick="var d=document.createElement('div');d.id='done';document.body.appendChild(d)">Go</button>`)
	defer srv.Close()

	b := newOOPIFBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	skill := &Skill{Name: "x", Steps: []Step{{Action: "click", Frame: "#f", Selector: "#go"}}}
	if _, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 15 * time.Second, Browser: b}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	// The click must have run inside the OOPIF: #done appears there. WaitFor also
	// routes through the frame, so a pass proves both directions work.
	if err := page.WaitFor(ctx, "#f >>> #done", 10*time.Second); err != nil {
		t.Fatalf("click did not land inside the cross-origin iframe: %v", err)
	}
}

// TestOOPIFTypeReplay: type into a text input inside a cross-origin iframe, then
// read it back through the frame path.
func TestOOPIFTypeReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := oopifServer(t, `<input id="q" name="q">`)
	defer srv.Close()

	b := newOOPIFBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	skill := &Skill{Name: "x", Steps: []Step{{Action: "type", Frame: "#f", Selector: "#q", Value: "hello"}}}
	if _, _, _, err := ReplaySkill(ctx, page, skill, nil, ReplayOptions{StepTimeout: 15 * time.Second}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	cp, ok := page.oopifPage(ctx, "#f")
	if !ok {
		t.Fatal("could not resolve the OOPIF page for verification")
	}
	var got string
	if err := cp.Eval(ctx, `document.querySelector('#q').value`, &got); err != nil {
		t.Fatalf("eval in oopif: %v", err)
	}
	if got != "hello" {
		t.Fatalf("type did not land in the cross-origin input: value=%q", got)
	}
}

// TestOOPIFRecord: a user gesture inside a cross-origin iframe is captured and
// tagged with the parent iframe selector, so the recording replays back into it.
func TestOOPIFRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := oopifServer(t, `<button id="go">Go</button>`)
	defer srv.Close()

	b := newOOPIFBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	// Ensure the OOPIF is attached/registered before recording starts.
	if err := page.WaitFor(ctx, "#f >>> #go", 10*time.Second); err != nil {
		t.Fatalf("oopif not ready: %v", err)
	}

	rec := NewRecorder(page)
	if err := rec.Start(ctx); err != nil {
		t.Fatalf("recorder start: %v", err)
	}
	// Stand in for the user: a trusted click inside the cross-origin iframe.
	if err := page.Click(ctx, "#f >>> #go"); err != nil {
		t.Fatalf("click: %v", err)
	}

	var evs []RecordedEvent
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		if evs = rec.Events(); len(evs) >= 1 {
			break
		}
	}
	rec.Stop()

	var found bool
	for _, e := range evs {
		if e.Type == "click" && e.Selector == "#go" && e.Frame == "#f" {
			found = true
		}
	}
	if !found {
		t.Fatalf("did not capture the cross-origin iframe click tagged with frame #f; got %+v", evs)
	}
}
