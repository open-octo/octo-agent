package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// clearFixture exercises every branch of Page.Clear:
//   - plain input / textarea / contenteditable: the select-all + Backspace path
//   - #swallow: a keydown handler cancels Backspace, so the key path leaves the
//     value intact and Clear must fall back to the direct reset
//   - #sticky: an input handler writes the old value back on every change, so
//     both paths fail and Clear must report it
//   - #ro: read-only, must be refused rather than overwritten
//   - #hidden: display:none, so focus() is a no-op — Clear must not send a
//     Backspace to whatever was focused before (#neighbour) and must still
//     empty the hidden value through the reset path
const clearFixture = `<!doctype html><html><body>
	<input id="q" value="stale">
	<textarea id="notes">old notes</textarea>
	<div id="rich" contenteditable="true">rich text</div>
	<input id="swallow" value="kept">
	<input id="sticky" value="glue">
	<input id="ro" value="locked" readonly>
	<input id="neighbour" value="keep me">
	<input id="hidden" value="secret" style="display:none">
	<script>
		document.querySelector('#swallow').addEventListener('keydown', e => {
			if (e.key === 'Backspace') e.preventDefault();
		});
		document.querySelector('#sticky').addEventListener('input', e => { e.target.value = 'glue'; });
	</script>
</body></html>`

func newClearPage(t *testing.T, ctx context.Context) (*Browser, *Page) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(clearFixture))
	}))
	t.Cleanup(srv.Close)

	b := newBrowser(t, ctx)
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		b.Close()
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		b.Close()
		t.Fatalf("navigate: %v", err)
	}
	if err := page.WaitFor(ctx, "#q", testWaitTimeout); err != nil {
		b.Close()
		t.Fatalf("wait: %v", err)
	}
	return b, page
}

func fieldValue(t *testing.T, ctx context.Context, page *Page, sel string) string {
	t.Helper()
	var got string
	if err := page.Eval(ctx, `(()=>{const el=document.querySelector(`+jsString(sel)+`); return ('value' in el)?el.value:el.textContent;})()`, &got); err != nil {
		t.Fatalf("read %s: %v", sel, err)
	}
	return got
}

// Clear must empty a field that already has content and leave it ready for a
// fresh TypeText — the whole point is that type inserts at the caret and cannot
// replace what is there.
func TestClear_EmptiesFieldBeforeRetype(t *testing.T) {
	ctx := context.Background()
	b, page := newClearPage(t, ctx)
	defer b.Close()

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
	if got := fieldValue(t, ctx, page, "#q"); got != "fresh" {
		t.Errorf("value after clear+type = %q, want %q", got, "fresh")
	}

	if err := page.Clear(ctx, "#nope"); err == nil {
		t.Error("clear on a missing selector should error")
	}
}

// When the page cancels the Backspace, Clear falls back to the direct reset
// and still succeeds; when the page fights the reset too, Clear says so.
func TestClear_FallbackAndFailure(t *testing.T) {
	ctx := context.Background()
	b, page := newClearPage(t, ctx)
	defer b.Close()

	if err := page.Clear(ctx, "#swallow"); err != nil {
		t.Fatalf("clear #swallow (fallback path): %v", err)
	}
	if got := fieldValue(t, ctx, page, "#swallow"); got != "" {
		t.Errorf("#swallow = %q after fallback clear, want empty", got)
	}

	err := page.Clear(ctx, "#sticky")
	if err == nil || !strings.Contains(err.Error(), "still has content") {
		t.Fatalf("clear #sticky err = %v, want a still-has-content error", err)
	}
}

// A read-only field is refused, not silently overwritten.
func TestClear_RefusesReadOnly(t *testing.T) {
	ctx := context.Background()
	b, page := newClearPage(t, ctx)
	defer b.Close()

	err := page.Clear(ctx, "#ro")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("clear #ro err = %v, want a read-only error", err)
	}
	if got := fieldValue(t, ctx, page, "#ro"); got != "locked" {
		t.Errorf("#ro = %q, want untouched %q", got, "locked")
	}
}

// focus() on a hidden element is a no-op, so a Backspace sent regardless would
// land on the previously focused field. Clear must leave that neighbour alone
// and still empty the hidden field via the reset path.
func TestClear_HiddenTargetDoesNotHitNeighbour(t *testing.T) {
	ctx := context.Background()
	b, page := newClearPage(t, ctx)
	defer b.Close()

	// Put real focus on the neighbour with the caret at the end, the way a
	// preceding type action would leave it.
	if err := page.Eval(ctx, `(()=>{const el=document.querySelector('#neighbour'); el.focus(); el.setSelectionRange(el.value.length, el.value.length); return document.activeElement === el;})()`, nil); err != nil {
		t.Fatalf("focus neighbour: %v", err)
	}

	if err := page.Clear(ctx, "#hidden"); err != nil {
		t.Fatalf("clear #hidden: %v", err)
	}
	if got := fieldValue(t, ctx, page, "#hidden"); got != "" {
		t.Errorf("#hidden = %q, want empty", got)
	}
	if got := fieldValue(t, ctx, page, "#neighbour"); got != "keep me" {
		t.Errorf("#neighbour = %q, want untouched %q — Backspace leaked to the focused field", got, "keep me")
	}
}
