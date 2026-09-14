package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestInteractiveDigestVisibility: the digest must include position:fixed
// controls (offsetParent is null for them in Chrome, but they're visible and
// clickable) while still excluding display:none / visibility:hidden ones. The
// digest is the healer's candidate list, so a wrong filter both hides healable
// targets and could offer unclickable ones.
func TestInteractiveDigestVisibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>digest</title>
<button id="normal">Normal</button>
<button id="fixed" style="position:fixed;top:0;left:0">Fixed</button>
<button id="gone" style="display:none">Gone</button>
<button id="invis" style="visibility:hidden">Invis</button>`)
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	// Wait for the document to parse before digesting: NewPage can resolve ahead
	// of the renderer on slow runners (Windows CI returned an empty digest).
	if err := page.WaitFor(ctx, "#fixed", testWaitTimeout); err != nil {
		t.Fatalf("wait for fixture: %v", err)
	}
	digest, err := InteractiveDigest(ctx, page, "", 0)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	has := func(id string) bool {
		for _, d := range digest {
			if d.Selector == "#"+id {
				return true
			}
		}
		return false
	}
	if !has("normal") || !has("fixed") {
		t.Fatalf("digest should include normal + fixed buttons: %+v", digest)
	}
	if has("gone") || has("invis") {
		t.Fatalf("digest must exclude display:none / visibility:hidden: %+v", digest)
	}
}

// TestInteractiveDigestIncludesClickHandledElements: elements that are
// interactive by convention rather than by tag — an inline onclick, a
// deliberately focusable tabindex — belong in the healer's candidate list; a
// tabindex="-1" (programmatic focus only) and a plain div do not.
func TestInteractiveDigestIncludesClickHandledElements(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>digest</title>
<div id="oc" onclick="void 0">Click me</div>
<span id="ti" tabindex="0">Focusable</span>
<span id="neg" tabindex="-1">Programmatic</span>
<div id="plain">Plain</div>`)
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#plain", testWaitTimeout); err != nil {
		t.Fatalf("wait for fixture: %v", err)
	}
	digest, err := InteractiveDigest(ctx, page, "", 0)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	has := func(id string) bool {
		for _, d := range digest {
			if d.Selector == "#"+id {
				return true
			}
		}
		return false
	}
	if !has("oc") || !has("ti") {
		t.Fatalf("digest should include onclick + tabindex elements: %+v", digest)
	}
	if has("neg") || has("plain") {
		t.Fatalf("digest must exclude tabindex=-1 and plain elements: %+v", digest)
	}
}

// TestLabelDigest: the label-matched list names the innermost visible element
// carrying the text — not the wrapper whose textContent merely contains it, not
// a hidden copy, not script source — and an empty label yields nothing.
func TestLabelDigest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>digest</title>
<div id="outer"><span id="inner">笔记管理</span></div>
<span id="hidden" style="display:none">笔记管理</span>
<span id="other">其他</span>
<script>var s="笔记管理";</script>`)
	}))
	defer srv.Close()
	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#other", testWaitTimeout); err != nil {
		t.Fatalf("wait for fixture: %v", err)
	}
	digest, err := LabelDigest(ctx, page, "", " 笔记管理 ", 0)
	if err != nil {
		t.Fatalf("label digest: %v", err)
	}
	if len(digest) != 1 || digest[0].Selector != "#inner" || digest[0].Text != "笔记管理" {
		t.Fatalf("want exactly the innermost visible carrier (#inner), got %+v", digest)
	}
	if got, err := LabelDigest(ctx, page, "", "  ", 0); err != nil || len(got) != 0 {
		t.Fatalf("empty label must yield nothing, got %+v (err %v)", got, err)
	}
}
