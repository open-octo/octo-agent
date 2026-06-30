package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/browser"
)

// toolFixtureHTML mirrors the awkward target system: the download button only
// appears after a search, and the file is produced client-side via a Blob.
const toolFixtureHTML = `<!doctype html><html><head><meta charset="utf-8"><title>fixture</title></head>
<body>
<input id="q" />
<button id="search">Search</button>
<div id="results"></div>
<script>
document.getElementById('search').addEventListener('click', function () {
  setTimeout(function () {
    var btn = document.createElement('button');
    btn.id = 'download';
    btn.textContent = 'Download Excel';
    btn.addEventListener('click', function () {
      var blob = new Blob([new Uint8Array([1,2,3,4])], { type: 'application/octet-stream' });
      var a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = 'report.xlsx';
      document.body.appendChild(a);
      a.click();
    });
    document.getElementById('results').appendChild(btn);
  }, 200);
});
</script>
</body></html>`

// TestBrowserTool_SearchDownloadFlow drives the wife's workflow shape through
// the browser tool's action dispatch (navigate→type→click→wait→download).
func TestBrowserTool_SearchDownloadFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60_000_000_000) // 60s
	defer cancel()
	if !browser.ChromeAvailable("") {
		t.Skip("chrome not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(toolFixtureHTML))
	}))
	defer srv.Close()

	b, err := browser.Launch(ctx, browser.LaunchOptions{Headless: true})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	run := func(input map[string]any) (string, error) {
		res, err := tool.Execute(ctx, "browser", input)
		return res.Text, err
	}

	if _, err := run(map[string]any{"action": "navigate", "url": srv.URL}); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if _, err := run(map[string]any{"action": "wait", "selector": "#search"}); err != nil {
		t.Fatalf("wait search: %v", err)
	}
	if _, err := run(map[string]any{"action": "type", "selector": "#q", "text": "alpha"}); err != nil {
		t.Fatalf("type: %v", err)
	}
	if _, err := run(map[string]any{"action": "click", "selector": "#search"}); err != nil {
		t.Fatalf("click: %v", err)
	}
	if _, err := run(map[string]any{"action": "wait", "selector": "#download"}); err != nil {
		t.Fatalf("wait download: %v", err)
	}
	out, err := run(map[string]any{"action": "download", "selector": "#download"})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	path := strings.TrimPrefix(out, "downloaded to ")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat downloaded %q: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("downloaded file %q is empty", path)
	}
	os.Remove(path)
}

// TestBrowserTool_RecordRunRoundTrip records a demonstration through the tool,
// saves it as an editable skill, then replays it via run_skill on a fresh load.
func TestBrowserTool_RecordRunRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60_000_000_000)
	defer cancel()
	if !browser.ChromeAvailable("") {
		t.Skip("chrome not available")
	}
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<!doctype html><title>rr</title><button id="b">Go</button>
<script>window.clicks=0;document.getElementById('b').addEventListener('click',function(){window.clicks++});</script>`))
	}))
	defer srv.Close()

	b, err := browser.Launch(ctx, browser.LaunchOptions{Headless: true})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	page, err := b.NewPage(ctx, srv.URL)
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	run := func(in map[string]any) (string, error) { r, e := tool.Execute(ctx, "browser", in); return r.Text, e }

	if _, err := run(map[string]any{"action": "wait", "selector": "#b"}); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if _, err := run(map[string]any{"action": "record_start"}); err != nil {
		t.Fatalf("record_start: %v", err)
	}
	if _, err := run(map[string]any{"action": "click", "selector": "#b"}); err != nil {
		t.Fatalf("click: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let the capture event arrive
	if _, err := run(map[string]any{"action": "record_stop", "name": "demo"}); err != nil {
		t.Fatalf("record_stop: %v", err)
	}

	// Replay: navigates back to the start URL (reset clicks) and re-clicks.
	if _, err := run(map[string]any{"action": "run_skill", "name": "demo"}); err != nil {
		t.Fatalf("run_skill: %v", err)
	}
	var clicks int
	if err := page.Eval(ctx, "window.clicks", &clicks); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if clicks < 1 {
		t.Fatalf("replayed skill did not click (clicks=%d)", clicks)
	}
}

// TestBrowserTool_RequiresAction surfaces a clean error for a missing action.
func TestBrowserTool_RequiresAction(t *testing.T) {
	_, err := BrowserTool{}.Execute(context.Background(), "browser", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}

// TestBrowserTool_CookiesIncludesHttpOnly verifies the cookies action returns
// the current page's cookies including HttpOnly ones (which document.cookie via
// eval cannot see) — the reason this needs CDP.
func TestBrowserTool_CookiesIncludesHttpOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60_000_000_000) // 60s
	defer cancel()
	if !browser.ChromeAvailable("") {
		t.Skip("chrome not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sess", Value: "secret123", Path: "/", HttpOnly: true})
		w.Write([]byte("<!doctype html><html><head><title>c</title></head><body>hi</body></html>"))
	}))
	defer srv.Close()

	b, err := browser.Launch(ctx, browser.LaunchOptions{Headless: true})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	if _, err := tool.Execute(ctx, "browser", map[string]any{"action": "navigate", "url": srv.URL}); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	res, err := tool.Execute(ctx, "browser", map[string]any{"action": "cookies"})
	if err != nil {
		t.Fatalf("cookies: %v", err)
	}
	if !strings.Contains(res.Text, "sess") || !strings.Contains(res.Text, "secret123") {
		t.Errorf("cookies missing the HttpOnly session cookie; got:\n%s", res.Text)
	}
}

// TestBrowserTool_ObserveAndScreenshotVision checks the decoupling: screenshot
// returns a vision image block, while observe is text-only (URL/title + the
// interactable elements with selectors) so it works on any model.
func TestBrowserTool_ObserveAndScreenshotVision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60_000_000_000) // 60s
	defer cancel()
	if !browser.ChromeAvailable("") {
		t.Skip("chrome not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(toolFixtureHTML))
	}))
	defer srv.Close()

	b, err := browser.Launch(ctx, browser.LaunchOptions{Headless: true})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	if _, err := tool.Execute(ctx, "browser", map[string]any{"action": "navigate", "url": srv.URL}); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	hasImage := func(res agent.ToolResult) bool {
		for _, blk := range res.Blocks {
			if blk.Type == "image" && blk.Image != nil && len(blk.Image.Data) > 0 {
				return true
			}
		}
		return false
	}

	shot, err := tool.Execute(ctx, "browser", map[string]any{"action": "screenshot"})
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if !hasImage(shot) {
		t.Errorf("screenshot returned no image block; blocks=%d text=%q", len(shot.Blocks), shot.Text)
	}

	obs, err := tool.Execute(ctx, "browser", map[string]any{"action": "observe"})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	// observe is text-only — no image block (it must work on non-vision models).
	if hasImage(obs) {
		t.Errorf("observe should not return an image block (vision is decoupled to screenshot)")
	}
	// The fixture's #search button must surface in the interactable digest.
	if !strings.Contains(obs.Text, "#search") {
		t.Errorf("observe text missing #search selector; got:\n%s", obs.Text)
	}
}
