package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

// TestBrowserTool_UnknownAction surfaces a clean error (no Chrome needed since
// it should fail before connecting only if a session exists; guard with skip).
func TestBrowserTool_RequiresAction(t *testing.T) {
	_, err := BrowserTool{}.Execute(context.Background(), "browser", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}
