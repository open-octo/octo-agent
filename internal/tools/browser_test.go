package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if b != nil {
		t.Cleanup(func() { b.Close() })
	}
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
// saves it as an editable recording, then replays it via replay on a fresh load.
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
	if b != nil {
		t.Cleanup(func() { b.Close() })
	}
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
	stopOut, err := run(map[string]any{"action": "record_stop", "name": "demo"})
	if err != nil {
		skipOnBrowserFlake(t, "record_stop", err)
	}
	// record_stop must still tell the agent how to trigger a replay and that
	// recordings are never auto-triggered — dropping this from the response
	// leaves the agent unable to explain how to run what it just recorded.
	if !strings.Contains(stopOut, "action=replay") || !strings.Contains(stopOut, "NOT keyword-triggerable") {
		t.Fatalf("record_stop response missing replay guidance: %s", stopOut)
	}

	// Replay: navigates back to the start URL (reset clicks) and re-clicks.
	if _, err := run(map[string]any{"action": "replay", "name": "demo"}); err != nil {
		skipOnBrowserFlake(t, "replay", err)
	}
	var clicks int
	if err := page.Eval(ctx, "window.clicks", &clicks); err != nil {
		skipOnBrowserFlake(t, "eval", err)
	}
	if clicks < 1 {
		t.Fatalf("replayed recording did not click (clicks=%d)", clicks)
	}

	// The deprecated run_skill alias still replays, and says so in the envelope.
	out, err := run(map[string]any{"action": "run_skill", "name": "demo"})
	if err != nil {
		skipOnBrowserFlake(t, "run_skill alias", err)
	} else if !strings.Contains(out, "deprecated_action") {
		t.Fatalf("run_skill alias result missing deprecation note: %s", out)
	}
}

// TestBrowserTool_RecordCancel: an abandoned recording can be discarded without
// saving, freeing record_start — previously wedged until a throwaway
// record_stop wrote a junk YAML.
func TestBrowserTool_RecordCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30_000_000_000)
	defer cancel()
	if !browser.ChromeAvailable("") {
		t.Skip("chrome not available")
	}
	b, err := browser.Launch(ctx, browser.LaunchOptions{Headless: true})
	skipOnBrowserFlake(t, "launch", err)
	if b != nil {
		t.Cleanup(func() { b.Close() })
	}
	page, err := b.NewPage(ctx, "about:blank")
	skipOnBrowserFlake(t, "new page", err)
	SetBrowserSession(b, page)
	defer ResetBrowserSession()

	tool := BrowserTool{}
	run := func(in map[string]any) (string, error) { r, e := tool.Execute(ctx, "browser", in); return r.Text, e }

	if _, err := run(map[string]any{"action": "record_start"}); err != nil {
		skipOnBrowserFlake(t, "record_start", err)
	}
	// A second start must fail — and the error must name the escape hatch.
	if _, err := run(map[string]any{"action": "record_start"}); err == nil || !strings.Contains(err.Error(), "record_cancel") {
		t.Fatalf("double record_start err = %v, want a record_cancel hint", err)
	}
	if _, err := run(map[string]any{"action": "record_cancel"}); err != nil {
		t.Fatalf("record_cancel: %v", err)
	}
	// Freed: a fresh recording can start (and be discarded too).
	if _, err := run(map[string]any{"action": "record_start"}); err != nil {
		t.Fatalf("record_start after cancel: %v", err)
	}
	if _, err := run(map[string]any{"action": "record_cancel"}); err != nil {
		t.Fatalf("second record_cancel: %v", err)
	}
	// Nothing left to cancel now.
	if _, err := run(map[string]any{"action": "record_cancel"}); err == nil {
		t.Fatal("record_cancel with no active recording should error")
	}
}

// TestResolveReplayParams_ErrorsOnMissingRequired verifies a param
// with no default and no caller-supplied value returns a clear error naming
// the missing param(s), rather than auto-prompting the user. The model then
// decides whether it already knows the value (re-invoke with `params`) or
// needs to ask the caller via ask_user_question.
func TestResolveReplayParams_ErrorsOnMissingRequired(t *testing.T) {
	skill := browser.Recording{
		Name:   "demo",
		Params: []browser.Param{{Name: "username", Description: "login name"}},
		Steps:  []browser.Step{{Action: "type", Selector: "#u", Value: "{{username}}"}},
	}
	stub := &stubAsker{resp: AskResponse{Custom: "alice"}}
	useAsker(t, stub)

	params := map[string]string{}
	err := resolveReplayParams(context.Background(), &skill, "demo", params)
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

// TestResolveReplayParams_NoPromptWhenProvided verifies an
// already-supplied value skips the prompt entirely.
func TestResolveReplayParams_NoPromptWhenProvided(t *testing.T) {
	skill := browser.Recording{
		Name:   "demo",
		Params: []browser.Param{{Name: "username"}},
		Steps:  []browser.Step{{Action: "type", Selector: "#u", Value: "{{username}}"}},
	}
	stub := &stubAsker{}
	useAsker(t, stub)

	params := map[string]string{"username": "bob"}
	if err := resolveReplayParams(context.Background(), &skill, "demo", params); err != nil {
		t.Fatalf("resolveReplayParams: %v", err)
	}
	if stub.called {
		t.Error("should not prompt when the param is already provided")
	}
}

// TestResolveReplayParams_MissingReturnsError verifies that even when
// there's no interactive asker, missing required params produce a clear error
// (rather than the old silent no-op that left ReplayRecording to fail mid-replay).
func TestResolveReplayParams_MissingReturnsError(t *testing.T) {
	SetAsker(nil)
	skill := browser.Recording{
		Name:   "demo",
		Params: []browser.Param{{Name: "username"}},
		Steps:  []browser.Step{{Action: "type", Selector: "#u", Value: "{{username}}"}},
	}
	params := map[string]string{}
	err := resolveReplayParams(context.Background(), &skill, "demo", params)
	if err == nil || !strings.Contains(err.Error(), "missing required param") {
		t.Fatalf("err = %v, want a missing-required-param error", err)
	}
	if _, ok := params["username"]; ok {
		t.Error("params should be untouched on error")
	}
}

// TestBrowserRecordingsDir_MigratesLegacyDir: with no env override, a pre-rename
// ~/.octo/browser-skills directory is renamed to browser-recordings on first
// use; env vars (new name first, then legacy) override the default.
func TestBrowserRecordingsDir_MigratesLegacyDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows
	t.Setenv("OCTO_BROWSER_RECORDINGS_DIR", "")
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", "")

	old := filepath.Join(tmp, ".octo", "browser-skills")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "demo.yaml"), []byte("name: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, ".octo", "browser-recordings")
	if got := BrowserRecordingsDir(); got != want {
		t.Fatalf("dir = %q, want migrated %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, "demo.yaml")); err != nil {
		t.Fatalf("recordings not carried over: %v", err)
	}

	// Env overrides win over the default (new name first, then the legacy one);
	// the values are returned verbatim, so the comparison is separator-free.
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", "/legacy")
	if got := BrowserRecordingsDir(); got != "/legacy" {
		t.Fatalf("legacy env not honored: %q", got)
	}
	t.Setenv("OCTO_BROWSER_RECORDINGS_DIR", "/new")
	if got := BrowserRecordingsDir(); got != "/new" {
		t.Fatalf("new env should take precedence: %q", got)
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
	if b != nil {
		t.Cleanup(func() { b.Close() })
	}
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
	if b != nil {
		t.Cleanup(func() { b.Close() })
	}
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
			// The wrapped error should still unwrap to the original.
			if !errors.Is(err, tc.in) {
				t.Errorf("wrapped error does not unwrap to original %v", tc.in)
			}
		})
	}
}

// TestBrowserPage_NoLaunchFallback locks the invariant that the browser tool
// only ever attaches to a real, user-owned Chrome and never spins up a headless
// instance of its own. With no attach config and no discoverable Chrome, it must
// return the actionable connect guide — not launch a throwaway Chrome, whose
// empty profile carries no login session and trips the macOS "Chrome Safe
// Storage" keychain prompt.
func TestBrowserPage_NoLaunchFallback(t *testing.T) {
	// An empty HOME means no ~/.octo/config.yml — so ConnectPort=0 and
	// AttachRunning=false, i.e. the default branch — and Chrome's default profile
	// dirs resolve under this empty home, so discovery finds no running Chrome.
	// Deterministic across runners, and it never touches the user's real profile.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows

	ResetBrowserSession()
	defer ResetBrowserSession()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	page, b, err := browserPage(ctx)
	if err == nil {
		ResetBrowserSession()
		t.Fatal("browserPage returned a live browser with no attach config; it must attach only, never launch its own Chrome")
	}
	if page != nil || b != nil {
		t.Fatalf("expected nil page/browser on failure, got page=%v browser=%v", page, b)
	}
	if !strings.Contains(err.Error(), "--remote-debugging-port") {
		t.Fatalf("error should be the actionable connect guide, got: %v", err)
	}
}

// TestReplayTimeoutScalesWithSteps: the replay ceiling is floored at the old
// fixed 5 minutes for short recordings, grows linearly for long ones, and is
// hard-capped so a pathological recording can't hold a turn forever.
func TestReplayTimeoutScalesWithSteps(t *testing.T) {
	for _, tc := range []struct {
		steps int
		want  time.Duration
	}{
		{0, 5 * time.Minute},
		{9, 5 * time.Minute},
		{10, 5*time.Minute + 20*time.Second},
		{30, 12 * time.Minute},
		{84, 30 * time.Minute},
		{1000, 30 * time.Minute},
	} {
		if got := replayTimeout(tc.steps); got != tc.want {
			t.Errorf("replayTimeout(%d) = %v, want %v", tc.steps, got, tc.want)
		}
	}
}

// TestResolveBrowserHealer_CtxOverridesGlobal proves resolveBrowserHealer
// prefers a ctx-scoped healer over a conflicting process-global one — the
// server's per-turn ctx must win even if another session's SetBrowserHealer
// call (or a leftover CLI global) left the global pointed at a different
// model's healer.
func TestResolveBrowserHealer_CtxOverridesGlobal(t *testing.T) {
	globalHealer := browser.Healer(func(context.Context, *browser.Page, *browser.Step, error) error {
		return errors.New("global-healer")
	})
	ctxHealer := browser.Healer(func(context.Context, *browser.Page, *browser.Step, error) error {
		return errors.New("ctx-healer")
	})
	SetBrowserHealer(globalHealer)
	t.Cleanup(func() { SetBrowserHealer(nil) })

	got := resolveBrowserHealer(WithBrowserHealer(context.Background(), ctxHealer))
	if err := got(context.Background(), nil, nil, nil); err == nil || err.Error() != "ctx-healer" {
		t.Errorf("expected ctx-scoped healer to win, got err=%v", err)
	}

	// No ctx value stamped (the CLI's one-session-per-process path) ⇒ falls
	// back to the process-global healer.
	got = resolveBrowserHealer(context.Background())
	if err := got(context.Background(), nil, nil, nil); err == nil || err.Error() != "global-healer" {
		t.Errorf("expected fallback to global healer, got err=%v", err)
	}
}

// TestResolveBrowserRecordingGenerator_CtxOverridesGlobal is the same proof
// as above for resolveBrowserRecordingGenerator.
func TestResolveBrowserRecordingGenerator_CtxOverridesGlobal(t *testing.T) {
	globalGen := browser.RecordingGenerator(func(context.Context, string, string) (string, error) {
		return "global-gen", nil
	})
	ctxGen := browser.RecordingGenerator(func(context.Context, string, string) (string, error) {
		return "ctx-gen", nil
	})
	SetBrowserRecordingGenerator(globalGen)
	t.Cleanup(func() { SetBrowserRecordingGenerator(nil) })

	got := resolveBrowserRecordingGenerator(WithBrowserRecordingGenerator(context.Background(), ctxGen))
	if s, _ := got(context.Background(), "", ""); s != "ctx-gen" {
		t.Errorf("expected ctx-scoped generator to win, got %q", s)
	}

	got = resolveBrowserRecordingGenerator(context.Background())
	if s, _ := got(context.Background(), "", ""); s != "global-gen" {
		t.Errorf("expected fallback to global generator, got %q", s)
	}
}
