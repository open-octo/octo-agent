package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Clear must empty a field that already has content and leave it ready for a
// fresh TypeText — the whole point is that type inserts at the caret and cannot
// replace what is there.
func TestClear_EmptiesFieldBeforeRetype(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><body>
			<input id="q" value="stale">
			<textarea id="notes">old notes</textarea>
			<div id="rich" contenteditable="true">rich text</div>
		</body></html>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()

	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if err := page.WaitFor(ctx, "#q", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}

	for _, sel := range []string{"#q", "#notes", "#rich"} {
		if err := page.Clear(ctx, sel); err != nil {
			t.Fatalf("clear %s: %v", sel, err)
		}
		if page.fieldNonEmpty(ctx, sel) {
			t.Errorf("%s still has content after Clear", sel)
		}
	}

	// Clearing an already-empty field is a no-op, not an error.
	if err := page.Clear(ctx, "#q"); err != nil {
		t.Fatalf("clear empty field: %v", err)
	}

	// The field is usable afterwards: a fresh type lands alone, not appended.
	if err := page.TypeText(ctx, "#q", "fresh"); err != nil {
		t.Fatalf("type after clear: %v", err)
	}
	var got string
	if err := page.Eval(ctx, `document.querySelector('#q').value`, &got); err != nil {
		t.Fatalf("read value: %v", err)
	}
	if got != "fresh" {
		t.Errorf("value after clear+type = %q, want %q", got, "fresh")
	}

	if err := page.Clear(ctx, "#nope"); err == nil {
		t.Error("clear on a missing selector should error")
	}
}
