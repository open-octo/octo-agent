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
