package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Browser is a CDP connection to a Chrome instance, optionally one this process
// launched.
type Browser struct {
	cmd         *exec.Cmd
	cli         *cdpClient
	ownsProcess bool

	// tempUserDataDir is the throwaway profile this process launched Chrome with
	// (via Launch with empty LaunchOptions.UserDataDir). Close removes it so
	// test runs don't leak octo-chrome-* directories in /tmp. Empty when
	// attached to a user-owned Chrome (Connect*) — those profiles must never be
	// deleted by us.
	tempUserDataDir string

	// oopifs maps a cross-origin (out-of-process) iframe's frameId to its CDP
	// session, populated by the target watcher. Cross-origin iframes live in a
	// separate renderer, so their DOM is unreachable via the parent's
	// contentDocument; interacting with them means routing to their own session.
	oopifMu sync.Mutex
	oopifs  map[string]string
}

// Launch starts a Chrome and connects to it.
func Launch(ctx context.Context, opts LaunchOptions) (*Browser, error) {
	cmd, wsURL, tempDir, err := launchChrome(ctx, opts)
	if err != nil {
		return nil, err
	}
	cli, err := dial(ctx, wsURL)
	if err != nil {
		killProcessGroup(cmd)
		if tempDir != "" {
			os.RemoveAll(tempDir)
		}
		return nil, err
	}
	b := wrapBrowser(cli, cmd, true)
	b.tempUserDataDir = tempDir
	return b, nil
}

// wrapBrowser wraps a live CDP connection and starts the cross-origin iframe
// target watcher.
func wrapBrowser(cli *cdpClient, cmd *exec.Cmd, owns bool) *Browser {
	b := &Browser{cmd: cmd, cli: cli, ownsProcess: owns, oopifs: map[string]string{}}
	b.watchTargets()
	return b
}

// Connect attaches to an already-running Chrome via its browser-level CDP
// websocket URL (e.g. one the user started with --remote-debugging-port so
// their logged-in session is reused).
func Connect(ctx context.Context, wsURL string) (*Browser, error) {
	cli, err := dial(ctx, wsURL)
	if err != nil {
		return nil, err
	}
	return wrapBrowser(cli, nil, false), nil
}

// ConnectByPort attaches to a Chrome already running with
// --remote-debugging-port=<port>, resolving the browser-level CDP endpoint from
// the debug HTTP server. This is how a user's existing, logged-in Chrome is
// reused.
func ConnectByPort(ctx context.Context, port int) (*Browser, error) {
	wsURL, err := browserWebSocketURL(ctx, port)
	if err != nil {
		return nil, err
	}
	return Connect(ctx, wsURL)
}

// ConnectViaProfile attaches to a Chrome already running with remote debugging
// by reading its profile's DevToolsActivePort — reusing the user's logged-in
// session without relaunching, and without the /json HTTP endpoint.
func ConnectViaProfile(ctx context.Context, userDataDir string) (*Browser, error) {
	ws, ok := devToolsWS(userDataDir)
	if !ok {
		return nil, fmt.Errorf("no DevToolsActivePort in %s (is Chrome running with --remote-debugging-port?)", userDataDir)
	}
	return Connect(ctx, ws)
}

// DiscoverRunningChrome attaches to the first default-profile Chrome found
// running with remote debugging.
func DiscoverRunningChrome(ctx context.Context) (*Browser, error) {
	for _, dir := range defaultProfileDirs() {
		ws, ok := devToolsWS(dir)
		if !ok {
			continue
		}
		if b, err := Connect(ctx, ws); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("no running Chrome with remote debugging found; launch Chrome with --remote-debugging-port or set browser.connect_port")
}

// DialError reports a failed WebSocket dial to a CDP endpoint, carrying the HTTP
// response status when the server rejected the upgrade (e.g., Chrome's remote-
// debugging origin check returns 403). Callers can inspect StatusCode to give
// actionable setup guidance.
type DialError struct {
	URL        string
	StatusCode int
	Err        error
}

func (e *DialError) Error() string { return fmt.Sprintf("dial cdp %s: %v", e.URL, e.Err) }
func (e *DialError) Unwrap() error { return e.Err }

// IsForbidden reports whether the dial was rejected by Chrome's remote debugging
// authorization / origin check (HTTP 403). This typically means the user needs
// to approve the Chrome authorization prompt or reconfigure remote debugging.
func (e *DialError) IsForbidden() bool { return e.StatusCode == http.StatusForbidden }

func dial(ctx context.Context, wsURL string) (*cdpClient, error) {
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		de := &DialError{URL: wsURL, Err: err}
		if resp != nil {
			de.StatusCode = resp.StatusCode
		}
		return nil, de
	}
	// Screenshots and full AX trees can be large; lift the read cap.
	conn.SetReadLimit(64 << 20)
	return newCDPClient(conn), nil
}

// OwnsProcess reports whether this process launched the Chrome (true) or merely
// attached to an already-running one (false). Callers use it to decide whether
// teardown may kill the browser or must only close the tabs it opened.
func (b *Browser) OwnsProcess() bool { return b.ownsProcess }

// Close tears down the connection and, if this process launched Chrome, the
// Chrome process too. It also removes any throwaway profile directory created
// for a Launch with an empty UserDataDir, so test runs don't leak octo-chrome-*
// directories in /tmp.
func (b *Browser) Close() error {
	b.cli.close()
	if b.ownsProcess && b.cmd != nil && b.cmd.Process != nil {
		killProcessGroup(b.cmd)
		b.cmd.Wait()
	}
	if b.tempUserDataDir != "" {
		os.RemoveAll(b.tempUserDataDir)
	}
	return nil
}

// Page is one attached page target (a flattened CDP session).
type Page struct {
	cli       *cdpClient
	sessionID string
	targetID  string
	browser   *Browser // back-ref for cross-origin iframe session lookup; may be nil
}

// NewPage opens a fresh tab at url and attaches to it.
func (b *Browser) NewPage(ctx context.Context, url string) (*Page, error) {
	if url == "" {
		url = "about:blank"
	}
	res, err := b.cli.call(ctx, "", "Target.createTarget", map[string]any{"url": url})
	if err != nil {
		return nil, err
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		return nil, err
	}
	return b.AttachPage(ctx, created.TargetID)
}

// AttachPage attaches to an existing page target by id (e.g. the user's already-
// open, logged-in tab discovered via Pages).
func (b *Browser) AttachPage(ctx context.Context, targetID string) (*Page, error) {
	res, err := b.cli.call(ctx, "", "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	})
	if err != nil {
		return nil, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(res, &attached); err != nil {
		return nil, err
	}
	p := &Page{cli: b.cli, sessionID: attached.SessionID, targetID: targetID, browser: b}
	for _, domain := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if _, err := p.cli.call(ctx, p.sessionID, domain, nil); err != nil {
			return nil, fmt.Errorf("%s: %w", domain, err)
		}
	}
	// Auto-attach to child targets (flatten) so a cross-origin iframe surfaces as
	// its own CDP session the target watcher can register — the only way to reach
	// an OOPIF's DOM. Best-effort: a backend that rejects it just means no
	// cross-origin frame support.
	_, _ = p.cli.call(ctx, p.sessionID, "Target.setAutoAttach", map[string]any{
		"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true,
	})
	// Install the in-flight network monitor on this and every future document so
	// WaitForNetworkIdle has data to poll (best-effort; failures are non-fatal).
	_, _ = p.cli.call(ctx, p.sessionID, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": netMonitorScript})
	_ = p.Eval(ctx, netMonitorScript, nil)
	return p, nil
}

// netMonitorScript maintains window.__octoNet.{n,gen,idleSince} by wrapping
// fetch and XMLHttpRequest, so replay can wait for XHR/fetch activity to settle
// and the recorder can tell whether ANY request started since a click (gen is a
// monotonically increasing activity generation — required because this script
// installs at page creation, so the recorder's own copy never wins the
// idempotency check and must rely on this one carrying gen). It counts only
// fetch/XHR (not sub-resources) — which is what "the SPA finished loading its
// data" means in practice — and resets naturally per document. Idempotent.
const netMonitorScript = `(function(){
  if(window.__octoNet) return;
  var s=window.__octoNet={n:0, gen:0, idleSince:Date.now()};
  function inc(){ s.n++; s.gen++; s.idleSince=0; }
  function dec(){ s.n=Math.max(0,s.n-1); if(s.n===0) s.idleSince=Date.now(); }
  try{ var of=window.fetch; if(of){ window.fetch=function(){ inc(); return of.apply(this,arguments).then(function(r){dec();return r;},function(e){dec();throw e;}); }; } }catch(_){}
  try{ var send=XMLHttpRequest.prototype.send; XMLHttpRequest.prototype.send=function(){ inc(); try{ this.addEventListener('loadend',function(){dec();},{once:true}); }catch(_){ dec(); } return send.apply(this,arguments); }; }catch(_){}
})();`

// WaitForNetworkIdle is a best-effort settle: it returns once no fetch/XHR has
// been in flight for `quiet`, or when `timeout` elapses — whichever comes first.
// It never reports a failure (real pages with polling/keep-alive connections may
// never go fully idle, and a settle helper must not turn that into a step error);
// only ctx cancellation returns an error. If the monitor isn't present it returns
// immediately.
func (p *Page) WaitForNetworkIdle(ctx context.Context, quiet, timeout time.Duration) error {
	if quiet <= 0 {
		quiet = 500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		// Returns ms the network has been idle, or -1 while busy / unavailable.
		var idleMS float64 = -1
		_ = p.Eval(ctx, `(function(){var s=window.__octoNet; if(!s) return 1e9; return (s.n===0 && s.idleSince>0) ? (Date.now()-s.idleSince) : -1;})()`, &idleMS)
		if idleMS >= float64(quiet/time.Millisecond) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return nil // best-effort: proceed even if never fully idle
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ClickFollow clicks target on page and, if the click opened a new tab (a
// target=_blank link or window.open — common on SPAs whose results open in a new
// tab, e.g. Zhihu hot items), switches to it. Returns the page to continue on:
// the new tab when one opened, otherwise the same page. Without this a click that
// spawns a tab looks like it "did nothing" because the original page is unchanged.
func (b *Browser) ClickFollow(ctx context.Context, page *Page, target string) (*Page, error) {
	return b.ClickFollowAt(ctx, page, target, 0, 0)
}

// ClickFollowAt is ClickFollow clicking at a fractional position of the
// element's box (fractions outside (0,1] mean the center).
func (b *Browser) ClickFollowAt(ctx context.Context, page *Page, target string, fx, fy float64) (*Page, error) {
	before := map[string]bool{}
	if ps, err := b.Pages(ctx); err == nil {
		for _, p := range ps {
			before[p.TargetID] = true
		}
	}
	if err := page.ClickAt(ctx, target, fx, fy); err != nil {
		return page, err
	}
	myID := page.TargetID()
	deadline := time.Now().Add(2 * time.Second)
	for {
		lastPoll := ctx.Err() != nil || !time.Now().Before(deadline)
		if ps, err := b.Pages(ctx); err == nil {
			var newTargets []TargetInfo
			for _, ti := range ps {
				if before[ti.TargetID] || ti.TargetID == myID {
					continue
				}
				newTargets = append(newTargets, ti)
			}
			for _, ti := range clickFollowCandidates(newTargets, myID, lastPoll) {
				if np, aerr := b.AttachPage(ctx, ti.TargetID); aerr == nil {
					return np, nil
				}
			}
		}
		if lastPoll {
			return page, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// clickFollowCandidates returns, in attempt order, the targets ClickFollow
// should try attaching to on this poll. A target Chrome reports as opened by
// myID always wins — the precise signal, immune to an unrelated tab (the
// user's own browsing, a notification/extension popup) appearing in the same
// window during the poll window. The imprecise "any new target" fallback is
// deliberately held back to the last poll before the deadline: trying it on
// an early iteration risks grabbing an unrelated opener-less tab that
// happens to appear before the real popup does — and since ClickFollow
// returns on the first successful attach, a wrong early guess is never
// revisited. Waiting gives the real popup every remaining poll to show up
// with a matching opener; only when the window is about to close and still
// nobody reports an opener at all (an environment where Chrome omits
// openerId) does the old best-effort behavior kick in, as a last resort.
func clickFollowCandidates(newTargets []TargetInfo, myID string, lastPoll bool) []TargetInfo {
	if candidate, ok := firstByOpener(newTargets, myID); ok {
		return []TargetInfo{candidate}
	}
	if lastPoll && !anyReportsOpener(newTargets) {
		return newTargets
	}
	return nil
}

// firstByOpener returns the first target whose OpenerID matches myID.
func firstByOpener(targets []TargetInfo, myID string) (TargetInfo, bool) {
	for _, ti := range targets {
		if ti.OpenerID == myID {
			return ti, true
		}
	}
	return TargetInfo{}, false
}

// anyReportsOpener reports whether any target in the set carries opener
// information at all — used to tell "Chrome doesn't report openerId here" from
// "none of these were opened by us".
func anyReportsOpener(targets []TargetInfo) bool {
	for _, ti := range targets {
		if ti.OpenerID != "" {
			return true
		}
	}
	return false
}

// ClosePage closes a page target by id.
func (b *Browser) ClosePage(ctx context.Context, targetID string) error {
	_, err := b.cli.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": targetID})
	return err
}

// TargetID returns the page's target id (for close/switch).
func (p *Page) TargetID() string { return p.targetID }

// TargetInfo describes an open browser target (tab).
type TargetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	// OpenerID is the target that spawned this one (window.open / a
	// target=_blank click), when Chrome reports it. Used by ClickFollow to
	// confirm a newly-seen tab was actually opened by the click it's
	// following, not an unrelated tab that happened to appear in the same
	// window (the user's own browsing, a notification/extension popup).
	OpenerID string `json:"openerId,omitempty"`
}

// Pages lists the open page targets — used to attach to an existing logged-in
// tab rather than opening a fresh one.
func (b *Browser) Pages(ctx context.Context) ([]TargetInfo, error) {
	res, err := b.cli.call(ctx, "", "Target.getTargets", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		TargetInfos []TargetInfo `json:"targetInfos"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	pages := r.TargetInfos[:0]
	for _, ti := range r.TargetInfos {
		if ti.Type == "page" {
			pages = append(pages, ti)
		}
	}
	return pages, nil
}

// Navigate loads url and waits for the page load event. When the page is still
// on the initial about:blank (the tab octo opened), it replaces that history
// entry instead of pushing one — otherwise a later Back() lands the model back
// on the blank tab. Navigation between real pages keeps normal history.
func (p *Page) Navigate(ctx context.Context, url string) error {
	events, unsub := p.cli.subscribe("Page.loadEventFired", p.sessionID)
	defer unsub()

	var cur string
	_ = p.Eval(ctx, "location.href", &cur)
	if cur == "" || cur == "about:blank" {
		if err := p.Eval(ctx, fmt.Sprintf("(()=>{location.replace(%s);return true})()", jsString(url)), nil); err != nil {
			return err
		}
	} else if _, err := p.cli.call(ctx, p.sessionID, "Page.navigate", map[string]any{"url": url}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-events:
		return nil
	case <-time.After(navigateLoadTimeout):
		return fmt.Errorf("navigate %s: timed out waiting for load", url)
	}
}

// navigateLoadTimeout caps how long Navigate waits for the load event. A var,
// not a const, so the package tests can raise it: starved CI runners (2-core
// windows-latest especially) intermittently need more than 30s for a cold
// Chrome to finish its first navigation even to a local fixture page.
var navigateLoadTimeout = 30 * time.Second

// evalException mirrors the fields of a CDP Runtime.ExceptionDetails we surface
// when an evaluated expression throws.
type evalException struct {
	Text         string `json:"text"`
	LineNumber   int    `json:"lineNumber"`
	ColumnNumber int    `json:"columnNumber"`
	Exception    *struct {
		ClassName   string          `json:"className"`
		Description string          `json:"description"`
		Value       json.RawMessage `json:"value"`
	} `json:"exception"`
}

// message builds a human-readable one-liner from a thrown exception. CDP's
// top-level Text is almost always just "Uncaught", so the useful detail lives
// in the exception RemoteObject: Description carries an Error's message plus its
// stack, Value carries a thrown non-Error primitive (e.g. `throw "boom"`).
func (e *evalException) message() string {
	detail := ""
	if e.Exception != nil {
		switch {
		case e.Exception.Description != "":
			// Description already includes the message; keep only its first
			// line so the stack trace doesn't bury the actual error.
			detail = strings.SplitN(e.Exception.Description, "\n", 2)[0]
		case len(e.Exception.Value) > 0:
			detail = strings.Trim(string(e.Exception.Value), `"`)
		case e.Exception.ClassName != "":
			detail = e.Exception.ClassName
		}
	}
	text := strings.TrimSpace(e.Text)
	switch {
	case detail == "":
		if text == "" {
			return "unknown error"
		}
		return text
	case text == "" || text == "Uncaught":
		// Bare "Uncaught" adds nothing; the detail is the message.
		return detail
	default:
		return text + ": " + detail
	}
}

// Eval runs a JS expression in the page and unmarshals its return value into
// out (pass nil to ignore the value). The expression may be a Promise.
func (p *Page) Eval(ctx context.Context, expr string, out any) error {
	res, err := p.cli.call(ctx, p.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return err
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *evalException `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return fmt.Errorf("eval threw: %s", r.ExceptionDetails.message())
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

// WaitFor polls until a CSS selector matches an element, or the timeout. This
// is the verify primitive for "the download button appeared after search".
func (p *Page) WaitFor(ctx context.Context, selector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	frame, elem := splitFrame(selector)
	if frame != "" {
		if cp, ok := p.oopifPage(ctx, frame); ok {
			return cp.WaitFor(ctx, elem, timeout)
		}
	}
	expr := "!!(" + elemRefJS(frame, elem) + ")"
	for {
		var present bool
		if err := p.Eval(ctx, expr, &present); err != nil {
			return err
		}
		if present {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for %q: timed out after %s", selector, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// jsString encodes s as a JS string literal via JSON (valid JS string syntax).
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
