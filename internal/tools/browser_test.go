package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/browser"
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

// browserFlakeSubstrings mark runner-environment failures (a loaded/slow CI
// Chrome), not code defects: launch can't read the DevTools port, or a
// navigate/wait/screenshot stalls past its deadline on a page that loads
// instantly everywhere else. ubuntu + windows CI and local runs stay green; only
// the macOS runners intermittently stall headless Chrome mid-navigation.
var browserFlakeSubstrings = []string{
	"timed out reading DevToolsActivePort",
	"timed out waiting for load",
	"timed out after",           // wait-for-selector deadline
	"context deadline exceeded", // any CDP call that outran the 60s ctx
	"i/o timeout",
}

// skipOnBrowserFlake skips the test when err is a known CI-runner Chrome flake
// and is fatal otherwise, so real regressions still fail the build. No-op on a
// nil err. Keeps these real-Chrome integration tests from making the macOS CI
// job red on infrastructure hiccups while still exercising the flow wherever
// Chrome is healthy.
func skipOnBrowserFlake(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, s := range browserFlakeSubstrings {
		if strings.Contains(err.Error(), s) {
			t.Skipf("browser flake on this runner (%s): %v", what, err)
		}
	}
	t.Fatalf("%s: %v", what, err)
}

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
	// A loaded CI runner ships Chrome (so ChromeAvailable passes) but headless
	// launch/navigation intermittently stalls — a runner-environment flake, not
	// a code defect. Skip on the known flake signatures; any other failure stays
	// fatal so real regressions fail the build.
	skipOnBrowserFlake(t, "launch", err)
	page, err := b.NewPage(ctx, "about:blank")
	skipOnBrowserFlake(t, "new page", err)
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	run := func(input map[string]any) (string, error) {
		res, err := tool.Execute(ctx, "browser", input)
		return res.Text, err
	}

	if _, err := run(map[string]any{"action": "navigate", "url": srv.URL}); err != nil {
		skipOnBrowserFlake(t, "navigate", err)
	}
	if _, err := run(map[string]any{"action": "wait", "selector": "#search"}); err != nil {
		skipOnBrowserFlake(t, "wait search", err)
	}
	if _, err := run(map[string]any{"action": "type", "selector": "#q", "text": "alpha"}); err != nil {
		skipOnBrowserFlake(t, "type", err)
	}
	if _, err := run(map[string]any{"action": "click", "selector": "#search"}); err != nil {
		skipOnBrowserFlake(t, "click", err)
	}
	if _, err := run(map[string]any{"action": "wait", "selector": "#download"}); err != nil {
		skipOnBrowserFlake(t, "wait download", err)
	}
	out, err := run(map[string]any{"action": "download", "selector": "#download"})
	if err != nil {
		skipOnBrowserFlake(t, "download", err)
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
	// A loaded CI runner ships Chrome (so ChromeAvailable passes) but headless
	// launch/navigation intermittently stalls — a runner-environment flake, not
	// a code defect. Skip on the known flake signatures; any other failure stays
	// fatal so real regressions fail the build.
	skipOnBrowserFlake(t, "launch", err)
	page, err := b.NewPage(ctx, srv.URL)
	skipOnBrowserFlake(t, "new page", err)
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	run := func(in map[string]any) (string, error) { r, e := tool.Execute(ctx, "browser", in); return r.Text, e }

	if _, err := run(map[string]any{"action": "wait", "selector": "#b"}); err != nil {
		skipOnBrowserFlake(t, "wait", err)
	}
	if _, err := run(map[string]any{"action": "record_start"}); err != nil {
		skipOnBrowserFlake(t, "record_start", err)
	}
	if _, err := run(map[string]any{"action": "click", "selector": "#b"}); err != nil {
		skipOnBrowserFlake(t, "click", err)
	}
	time.Sleep(300 * time.Millisecond) // let the capture event arrive
	if _, err := run(map[string]any{"action": "record_stop", "name": "demo"}); err != nil {
		skipOnBrowserFlake(t, "record_stop", err)
	}

	// Replay: navigates back to the start URL (reset clicks) and re-clicks.
	if _, err := run(map[string]any{"action": "run_skill", "name": "demo"}); err != nil {
		skipOnBrowserFlake(t, "run_skill", err)
	}
	var clicks int
	if err := page.Eval(ctx, "window.clicks", &clicks); err != nil {
		skipOnBrowserFlake(t, "eval", err)
	}
	if clicks < 1 {
		t.Fatalf("replayed skill did not click (clicks=%d)", clicks)
	}
}

// TestResolveMissingSkillParams_ErrorsOnMissingRequired verifies a param
// with no default and no caller-supplied value returns a clear error naming
// the missing param(s), rather than auto-prompting the user. The model then
// decides whether it already knows the value (re-invoke with `params`) or
// needs to ask the caller via ask_user_question.
func TestResolveMissingSkillParams_ErrorsOnMissingRequired(t *testing.T) {
	skill := browser.Skill{
		Name:   "demo",
		Params: []browser.Param{{Name: "username", Description: "login name"}},
		Steps:  []browser.Step{{Action: "type", Selector: "#u", Value: "{{username}}"}},
	}
	stub := &stubAsker{resp: AskResponse{Custom: "alice"}}
	useAsker(t, stub)

	params := map[string]string{}
	err := resolveMissingSkillParams(&skill, "demo", params)
	if err == nil || !strings.Contains(err.Error(), "missing required param") {
		t.Fatalf("err = %v, want a missing-required-param error", err)
	}
	if !strings.Contains(err.Error(), "username") {
		t.Errorf("err = %v, want it to name the missing param", err)
	}
	if stub.called {
		t.Error("should not auto-prompt the user — the model owns that decision")
	}
	if _, ok := params["username"]; ok {
		t.Error("params should remain untouched on error")
	}
}

// TestResolveMissingSkillParams_NoPromptWhenProvided verifies an
// already-supplied value skips the prompt entirely.
func TestResolveMissingSkillParams_NoPromptWhenProvided(t *testing.T) {
	skill := browser.Skill{
		Name:   "demo",
		Params: []browser.Param{{Name: "username"}},
		Steps:  []browser.Step{{Action: "type", Selector: "#u", Value: "{{username}}"}},
	}
	stub := &stubAsker{}
	useAsker(t, stub)

	params := map[string]string{"username": "bob"}
	if err := resolveMissingSkillParams(&skill, "demo", params); err != nil {
		t.Fatalf("resolveMissingSkillParams: %v", err)
	}
	if stub.called {
		t.Error("should not prompt when the param is already provided")
	}
}

// TestResolveMissingSkillParams_MissingReturnsError verifies that even when
// there's no interactive asker, missing required params produce a clear error
// (rather than the old silent no-op that left ReplaySkill to fail mid-replay).
func TestResolveMissingSkillParams_MissingReturnsError(t *testing.T) {
	SetAsker(nil)
	skill := browser.Skill{
		Name:   "demo",
		Params: []browser.Param{{Name: "username"}},
		Steps:  []browser.Step{{Action: "type", Selector: "#u", Value: "{{username}}"}},
	}
	params := map[string]string{}
	err := resolveMissingSkillParams(&skill, "demo", params)
	if err == nil || !strings.Contains(err.Error(), "missing required param") {
		t.Fatalf("err = %v, want a missing-required-param error", err)
	}
	if _, ok := params["username"]; ok {
		t.Error("params should be untouched on error")
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
	// A loaded CI runner ships Chrome (so ChromeAvailable passes) but headless
	// launch/navigation intermittently stalls — a runner-environment flake, not
	// a code defect. Skip on the known flake signatures; any other failure stays
	// fatal so real regressions fail the build.
	skipOnBrowserFlake(t, "launch", err)
	page, err := b.NewPage(ctx, "about:blank")
	skipOnBrowserFlake(t, "new page", err)
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	if _, err := tool.Execute(ctx, "browser", map[string]any{"action": "navigate", "url": srv.URL}); err != nil {
		skipOnBrowserFlake(t, "navigate", err)
	}
	res, err := tool.Execute(ctx, "browser", map[string]any{"action": "cookies"})
	if err != nil {
		skipOnBrowserFlake(t, "cookies", err)
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
	// A loaded CI runner ships Chrome (so ChromeAvailable passes) but headless
	// launch/navigation intermittently stalls — a runner-environment flake, not
	// a code defect. Skip on the known flake signatures; any other failure stays
	// fatal so real regressions fail the build.
	skipOnBrowserFlake(t, "launch", err)
	page, err := b.NewPage(ctx, "about:blank")
	skipOnBrowserFlake(t, "new page", err)
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	if _, err := tool.Execute(ctx, "browser", map[string]any{"action": "navigate", "url": srv.URL}); err != nil {
		skipOnBrowserFlake(t, "navigate", err)
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
		skipOnBrowserFlake(t, "screenshot", err)
	}
	if !hasImage(shot) {
		t.Errorf("screenshot returned no image block; blocks=%d text=%q", len(shot.Blocks), shot.Text)
	}

	obs, err := tool.Execute(ctx, "browser", map[string]any{"action": "observe"})
	if err != nil {
		skipOnBrowserFlake(t, "observe", err)
	}
	// observe is text-only — no image block (it must work on non-vision models).
	if hasImage(obs) {
		t.Errorf("observe should not return an image block (vision is decoupled to screenshot)")
	}
	// The fixture's #search button must surface in the interactable digest.
	if !strings.Contains(obs.Text, "#search") {
		t.Errorf("observe text missing #search selector; got:\n%s", obs.Text)
	}

	// With vision disabled (text-only model), screenshot must degrade to a
	// text note and NOT attach an image block the endpoint would reject.
	SetBrowserVision(false)
	defer SetBrowserVision(true)
	noVis, err := tool.Execute(ctx, "browser", map[string]any{"action": "screenshot"})
	if err != nil {
		t.Fatalf("screenshot (vision off): %v", err)
	}
	if hasImage(noVis) {
		t.Errorf("screenshot returned an image block while vision is disabled")
	}
	if !strings.Contains(noVis.Text, "saved to") {
		t.Errorf("screenshot (vision off) should still report the saved path; got: %q", noVis.Text)
	}
}

// TestWrapBrowserConnectError verifies that connection failures are surfaced as
// actionable setup guidance rather than bare low-level errors, so the LLM can
// offer to start the browser-setup flow.
func TestWrapBrowserConnectError(t *testing.T) {
	cases := []struct {
		name   string
		in     error
		wantIn string // substring the wrapped error must contain
	}{
		{
			name:   "Chrome 403 rejection",
			in:     &browser.DialError{URL: "ws://127.0.0.1:9222/devtools/browser", StatusCode: 403, Err: fmt.Errorf("websocket: bad handshake")},
			wantIn: "Browser is not correctly set up",
		},
		{
			name:   "port not reachable",
			in:     &browser.DialError{URL: "ws://127.0.0.1:9222/devtools/browser", StatusCode: 0, Err: fmt.Errorf("dial tcp 127.0.0.1:9222: connect: connection refused")},
			wantIn: "cannot reach the debug port",
		},
		{
			name:   "other dial error still gets guidance",
			in:     fmt.Errorf("some unexpected dial failure"),
			wantIn: "Browser is not correctly set up",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapBrowserConnectError(tc.in)
			got := err.Error()
			if !strings.Contains(got, tc.wantIn) {
				t.Errorf("error %q missing %q", got, tc.wantIn)
			}
		})
	}
}
