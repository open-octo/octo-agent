package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testWaitTimeout caps how long the browser tests wait for a selector to
// appear. WaitFor returns the instant the element is present, so this ceiling
// is free on a healthy run — it only matters when a slow/loaded CI runner
// (Windows cold-starts Chrome notably slower) needs more than a few seconds to
// finish the first navigation. 5s was too tight there and flaked TestUpload.
const testWaitTimeout = 20 * time.Second

// fixtureHTML reproduces the awkward shape of the real target system:
//   - the download button only appears after a search (no pre-constructable URL)
//   - clicking it generates the file client-side via a Blob (the server never
//     returns the final file), so the only way to obtain it is to drive the
//     real page and capture what lands on disk.
const fixtureHTML = `<!doctype html><html><head><meta charset="utf-8"><title>fixture</title></head>
<body>
<input id="q" />
<button id="search">Search</button>
<div id="results"></div>
<script>
document.getElementById('search').addEventListener('click', function () {
  var r = document.getElementById('results');
  r.innerHTML = '';
  setTimeout(function () {
    var btn = document.createElement('button');
    btn.id = 'download';
    btn.textContent = 'Download Excel';
    btn.addEventListener('click', function () {
      var bytes = new Uint8Array([0x50, 0x4b, 0x03, 0x04, 1, 2, 3, 4]);
      var blob = new Blob([bytes], { type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' });
      var a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = 'report-' + (document.getElementById('q').value || 'x') + '.xlsx';
      document.body.appendChild(a);
      a.click();
    });
    r.appendChild(btn);
  }, 300);
});
</script>
</body></html>`

// newBrowser launches a headless Chrome, skipping the test when Chrome is not
// installed (so CI without a browser stays green).
func newBrowser(t *testing.T, ctx context.Context) *Browser {
	t.Helper()
	if _, err := findChrome(""); err != nil {
		t.Skipf("chrome not available: %v", err)
	}
	b, err := Launch(ctx, LaunchOptions{Headless: true})
	if err != nil {
		// A loaded CI runner ships Chrome (so findChrome passes) but launching
		// it headless intermittently times out reading DevToolsActivePort —
		// a runner-environment flake, not a code defect. It surfaces on the
		// windows-latest runner and, under load, on ubuntu-latest too. Skip on
		// that specific timeout regardless of OS; keep every other launch
		// failure fatal so real regressions still fail the build.
		if strings.Contains(err.Error(), "timed out reading DevToolsActivePort") {
			t.Skipf("chrome launch flake on this runner: %v", err)
		}
		t.Fatalf("launch: %v", err)
	}
	return b
}

// TestSearchThenDownload exercises the wife's workflow shape end-to-end against
// the owned backend: search, wait for the dynamically-appearing button, click
// it, and capture the client-side-generated file.
func TestSearchThenDownload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(fixtureHTML))
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
	if err := page.WaitFor(ctx, "#search", testWaitTimeout); err != nil {
		t.Fatalf("wait search: %v", err)
	}

	if err := page.TypeText(ctx, "#q", "alpha"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := page.Click(ctx, "#search"); err != nil {
		t.Fatalf("click search: %v", err)
	}
	// The download button is absent until the (simulated) search completes.
	if err := page.WaitFor(ctx, "#download", testWaitTimeout); err != nil {
		t.Fatalf("wait download button: %v", err)
	}

	dir := t.TempDir()
	path, err := b.CaptureDownload(ctx, dir, func() error {
		return page.Click(ctx, "#download")
	})
	if err != nil {
		t.Fatalf("capture download: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat download %q: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("download %q is empty", path)
	}
	if !strings.Contains(info.Name(), "report-alpha") || !strings.HasSuffix(info.Name(), ".xlsx") {
		t.Fatalf("unexpected download name %q", info.Name())
	}
}

// TestAttachExistingPage covers the real-use path: discover an already-open tab
// and attach to it (rather than opening a fresh one), as when reusing the user's
// logged-in session.
func TestAttachExistingPage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(fixtureHTML))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()

	opened, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := opened.WaitFor(ctx, "#search", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}

	pages, err := b.Pages(ctx)
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	var targetID string
	for _, p := range pages {
		if strings.HasPrefix(p.URL, srv.URL) {
			targetID = p.TargetID
		}
	}
	if targetID == "" {
		t.Fatalf("fixture tab not found among %d pages", len(pages))
	}

	page, err := b.AttachPage(ctx, targetID)
	if err != nil {
		t.Fatalf("attach existing: %v", err)
	}
	var title string
	if err := page.Eval(ctx, "document.title", &title); err != nil {
		t.Fatalf("eval on attached page: %v", err)
	}
	if title != "fixture" {
		t.Fatalf("attached page title = %q, want fixture", title)
	}
}

// TestUpload sets a file on a file input via DOM.setFileInputFiles (no OS
// dialog) and verifies the page sees it — the upload primitive.
func TestUpload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>up</title><input type="file" id="f">`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#f", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}

	dir := t.TempDir()
	xlsx := dir + "/report.xlsx"
	if err := os.WriteFile(xlsx, []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := page.Upload(ctx, "#f", []string{xlsx}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	var name string
	if err := page.Eval(ctx, "document.querySelector('#f').files[0].name", &name); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if name != "report.xlsx" {
		t.Fatalf("uploaded file name = %q, want report.xlsx", name)
	}
}

// TestHover verifies a trusted pointer move fires real hover DOM events (which
// synthetic JS events / CSS :hover can't be driven by otherwise).
func TestHover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>h</title>
<div id="t" style="width:100px;height:40px">target</div>
<script>window.hovered=false;
document.getElementById('t').addEventListener('mouseover',function(){window.hovered=true});</script>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#t", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if err := page.Hover(ctx, "#t"); err != nil {
		t.Fatalf("hover: %v", err)
	}
	var hovered bool
	if err := page.Eval(ctx, "window.hovered", &hovered); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !hovered {
		t.Fatal("hover did not fire mouseover")
	}
}

// TestSelectOption picks a native <select> option and verifies the value + that
// change fired.
func TestSelectOption(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>s</title>
<select id="s"><option value="a">Apple</option><option value="b">Banana</option></select>
<script>window.changed='';document.getElementById('s').addEventListener('change',function(e){window.changed=e.target.value});</script>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#s", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if err := page.SelectOption(ctx, "#s", "Banana"); err != nil {
		t.Fatalf("select: %v", err)
	}
	var value, changed string
	if err := page.Eval(ctx, "document.querySelector('#s').value", &value); err != nil {
		t.Fatalf("eval value: %v", err)
	}
	if err := page.Eval(ctx, "window.changed", &changed); err != nil {
		t.Fatalf("eval changed: %v", err)
	}
	if value != "b" {
		t.Fatalf("select value = %q, want b", value)
	}
	if changed != "b" {
		t.Fatalf("change event value = %q, want b", changed)
	}
}

// TestSameOriginFrame drives an element inside a same-origin iframe via the
// " >>> " piercing convention (wait, then a trusted click that lands through
// the computed frame offset). Same-origin as in Klook's admin system.
func TestSameOriginFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/child" {
			w.Write([]byte(`<!doctype html><title>child</title>
<button id="b" style="position:absolute;left:30px;top:30px;width:120px;height:40px">Go</button>
<script>document.getElementById('b').addEventListener('click',function(){document.body.setAttribute('data-clicked','1')});</script>`))
			return
		}
		w.Write([]byte(`<!doctype html><title>parent</title>
<iframe id="fr" src="/child" style="position:absolute;left:50px;top:60px;width:400px;height:300px;border:0"></iframe>`))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	// Frame-aware wait: the button lives inside the same-origin child frame.
	if err := page.WaitFor(ctx, "#fr >>> #b", testWaitTimeout); err != nil {
		t.Fatalf("wait in frame: %v", err)
	}
	if err := page.Click(ctx, "#fr >>> #b"); err != nil {
		t.Fatalf("click in frame: %v", err)
	}
	var clicked string
	if err := page.Eval(ctx, "document.querySelector('#fr').contentDocument.body.getAttribute('data-clicked')", &clicked); err != nil {
		t.Fatalf("eval pierce: %v", err)
	}
	if clicked != "1" {
		t.Fatalf("frame click did not register (data-clicked=%q)", clicked)
	}
}

// TestDevToolsWS parses the DevToolsActivePort file into a ws URL (no Chrome).
func TestDevToolsWS(t *testing.T) {
	dir := t.TempDir()
	if _, ok := devToolsWS(dir); ok {
		t.Fatal("expected not-ok with no DevToolsActivePort file")
	}
	if err := os.WriteFile(dir+"/DevToolsActivePort", []byte("9222\n/devtools/browser/abc-123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, ok := devToolsWS(dir)
	if !ok {
		t.Fatal("expected ok")
	}
	if ws != "ws://127.0.0.1:9222/devtools/browser/abc-123" {
		t.Fatalf("ws = %q", ws)
	}
}

// hostPort extracts the numeric port from an httptest server URL (always
// 127.0.0.1:N), so browserWebSocketURL's hard-coded 127.0.0.1 host reaches it.
func hostPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestBrowserWebSocketURL_JSONVersion uses the webSocketDebuggerUrl from
// /json/version when the classic --remote-debugging-port endpoint serves it.
func TestBrowserWebSocketURL_JSONVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:9999/devtools/browser/uuid-77"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := browserWebSocketURL(ctx, hostPort(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if got != "ws://127.0.0.1:9999/devtools/browser/uuid-77" {
		t.Fatalf("ws = %q, want the /json/version URL", got)
	}
}

// TestBrowserWebSocketURL_Fallback covers the chrome://inspect toggle path:
// /json/version 404s, so the resolver must fall back to the fixed UUID-less
// /devtools/browser socket instead of failing.
func TestBrowserWebSocketURL_Fallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // no /json endpoints, like the toggle path
	}))
	defer srv.Close()

	port := hostPort(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := browserWebSocketURL(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser", port); got != want {
		t.Fatalf("ws = %q, want fallback %q", got, want)
	}
}

// TestConnectViaProfile attaches to a running Chrome by reading its profile's
// DevToolsActivePort — the path used to reuse a logged-in browser without
// relaunching (and without /json, which recent Chrome disables).
func TestConnectViaProfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if !ChromeAvailable("") {
		t.Skip("chrome not available")
	}
	// Not t.TempDir(): Chrome's profile dir may still be settling when the test
	// ends, and t.TempDir's mandatory RemoveAll would fail the test. Clean up
	// best-effort after Chrome is killed instead.
	profile, err := os.MkdirTemp("", "octo-test-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(profile)
	launched, err := Launch(ctx, LaunchOptions{Headless: true, UserDataDir: profile})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer launched.Close()

	attached, err := ConnectViaProfile(ctx, profile)
	if err != nil {
		t.Fatalf("connect via profile: %v", err)
	}
	defer attached.Close()
	if _, err := attached.Pages(ctx); err != nil {
		t.Fatalf("pages over attached connection: %v", err)
	}
}

// TestPrimitives covers eval / screenshot / ax-tree / key on the fixture.
func TestPrimitives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(fixtureHTML))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.WaitFor(ctx, "#search", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}

	var title string
	if err := page.Eval(ctx, "document.title", &title); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if title != "fixture" {
		t.Fatalf("title = %q, want fixture", title)
	}

	shot, err := page.Screenshot(ctx)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if len(shot) == 0 {
		t.Fatal("empty screenshot")
	}

	ax, err := page.AXTree(ctx)
	if err != nil {
		t.Fatalf("ax tree: %v", err)
	}
	if len(ax) == 0 {
		t.Fatal("empty ax tree")
	}

	if err := page.Key(ctx, "escape"); err != nil {
		t.Fatalf("key: %v", err)
	}
}

// TestNewPageLeavesOtherTabsUntouched locks the invariant the browser tool
// relies on: octo opens its own tab and never disturbs tabs the user already
// has open. Regression guard for the bug where attaching to a running Chrome
// hijacked pages[0] (the octo web UI itself) and navigated it away.
func TestNewPageLeavesOtherTabsUntouched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := newBrowser(t, ctx)
	defer b.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html><html><head><title>t</title></head><body>" + r.URL.Path + "</body></html>"))
	}))
	defer srv.Close()

	// The user's tab, sitting on /user.
	userPage, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("user page: %v", err)
	}
	if err := userPage.Navigate(ctx, srv.URL+"/user"); err != nil {
		t.Fatalf("navigate user: %v", err)
	}

	// octo's own tab, driven to /octo — must not affect the user's tab.
	octoPage, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("octo page: %v", err)
	}
	if octoPage.TargetID() == userPage.TargetID() {
		t.Fatal("octo reused the user's tab instead of opening its own")
	}
	if err := octoPage.Navigate(ctx, srv.URL+"/octo"); err != nil {
		t.Fatalf("navigate octo: %v", err)
	}

	var userPath string
	if err := userPage.Eval(ctx, "location.pathname", &userPath); err != nil {
		t.Fatalf("eval user path: %v", err)
	}
	if userPath != "/user" {
		t.Errorf("user tab was disturbed: pathname = %q, want /user", userPath)
	}
}

// TestClick_InvalidSelectorMessage: a Playwright-style :has-text selector is
// not valid CSS; querySelector throws. We should surface an actionable message
// (not a bare "eval threw: Uncaught") so the model switches to a CSS selector.
func TestClick_InvalidSelectorMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := newBrowser(t, ctx)
	defer b.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><head><title>t</title></head><body><a href="#">x</a></body></html>`))
	}))
	defer srv.Close()

	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	// Genuinely malformed CSS (not a supported Playwright form) must still
	// produce an actionable message, not a bare "eval threw".
	err = page.Click(ctx, `a:not(`)
	if err == nil {
		t.Fatal("expected an error for a malformed selector")
	}
	if !strings.Contains(err.Error(), "invalid selector") {
		t.Errorf("error should name the invalid selector clearly; got: %v", err)
	}
}

// TestClick_PlaywrightSelectors verifies the supported Playwright subset
// actually resolves and clicks the right element.
func TestClick_PlaywrightSelectors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := newBrowser(t, ctx)
	defer b.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><head><title>t</title></head><body>
<a id="a1" href="#" onclick="document.title='clicked-'+this.id;return false">Read the report</a>
<a id="a2" href="#" onclick="document.title='clicked-'+this.id;return false">Other link</a>
</body></html>`))
	}))
	defer srv.Close()

	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}

	cases := map[string]string{
		`a:has-text("Read the report")`: "clicked-a1",
		`text=Other link`:               "clicked-a2",
		`xpath=//a[@id="a1"]`:           "clicked-a1",
	}
	for sel, wantTitle := range cases {
		if err := page.Navigate(ctx, srv.URL); err != nil {
			t.Fatalf("navigate: %v", err)
		}
		if err := page.Click(ctx, sel); err != nil {
			t.Fatalf("click %q: %v", sel, err)
		}
		var title string
		if err := page.Eval(ctx, "document.title", &title); err != nil {
			t.Fatalf("read title: %v", err)
		}
		if title != wantTitle {
			t.Errorf("selector %q clicked wrong element: title=%q, want %q", sel, title, wantTitle)
		}
	}
}

// TestNavigate_ReplacesInitialBlank: the first navigation off about:blank must
// replace that entry, so going Back from a later page lands on the first real
// page — not the blank tab octo opened.
func TestNavigate_ReplacesInitialBlank(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := newBrowser(t, ctx)
	defer b.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html><html><head><title>" + r.URL.Path + "</title></head><body>" + r.URL.Path + "</body></html>"))
	}))
	defer srv.Close()

	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL+"/first"); err != nil {
		t.Fatalf("navigate first: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL+"/second"); err != nil {
		t.Fatalf("navigate second: %v", err)
	}
	if err := page.Back(ctx); err != nil {
		t.Fatalf("back: %v", err)
	}
	// Back should settle on /first, not about:blank. history.back() is async, so
	// poll briefly.
	deadline := time.Now().Add(5 * time.Second)
	var path string
	for time.Now().Before(deadline) {
		_ = page.Eval(ctx, "location.pathname", &path)
		if path == "/first" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if path != "/first" {
		t.Errorf("after back, pathname = %q, want /first (about:blank should not be in history)", path)
	}
}

// TestFirstByOpener_PrefersActualOpener guards ClickFollow's race fix: when
// an unrelated tab (the user's own browsing, a notification/extension popup)
// appears in the polling window alongside the tab the click actually opened,
// the opener-matched one must win — not whichever new target is seen first.
func TestFirstByOpener_PrefersActualOpener(t *testing.T) {
	targets := []TargetInfo{
		{TargetID: "unrelated", OpenerID: "some-other-page"},
		{TargetID: "popup", OpenerID: "me"},
	}
	got, ok := firstByOpener(targets, "me")
	if !ok || got.TargetID != "popup" {
		t.Fatalf("firstByOpener(targets, \"me\") = %+v, %v, want {popup} true", got, ok)
	}
	if _, ok := firstByOpener(targets, "nobody"); ok {
		t.Error("no target was opened by \"nobody\"")
	}
}

// TestAnyReportsOpener distinguishes "Chrome didn't report openerId for any
// candidate here" (fall back to the old best-effort behavior) from "none of
// these were opened by us" (opener info is available; trust it).
func TestAnyReportsOpener(t *testing.T) {
	if anyReportsOpener([]TargetInfo{{TargetID: "a"}, {TargetID: "b"}}) {
		t.Error("no target in this set reports an opener")
	}
	if !anyReportsOpener([]TargetInfo{{TargetID: "a"}, {TargetID: "b", OpenerID: "x"}}) {
		t.Error("one target in this set reports an opener")
	}
}
