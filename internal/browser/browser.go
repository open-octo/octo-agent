package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
)

// Browser is a CDP connection to a Chrome instance, optionally one this process
// launched.
type Browser struct {
	cmd         *exec.Cmd
	cli         *cdpClient
	ownsProcess bool
}

// Launch starts a Chrome and connects to it.
func Launch(ctx context.Context, opts LaunchOptions) (*Browser, error) {
	cmd, wsURL, err := launchChrome(ctx, opts)
	if err != nil {
		return nil, err
	}
	cli, err := dial(ctx, wsURL)
	if err != nil {
		killProcessGroup(cmd)
		return nil, err
	}
	return &Browser{cmd: cmd, cli: cli, ownsProcess: true}, nil
}

// Connect attaches to an already-running Chrome via its browser-level CDP
// websocket URL (e.g. one the user started with --remote-debugging-port so
// their logged-in session is reused).
func Connect(ctx context.Context, wsURL string) (*Browser, error) {
	cli, err := dial(ctx, wsURL)
	if err != nil {
		return nil, err
	}
	return &Browser{cli: cli, ownsProcess: false}, nil
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

func dial(ctx context.Context, wsURL string) (*cdpClient, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial cdp %s: %w", wsURL, err)
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
// Chrome process too.
func (b *Browser) Close() error {
	b.cli.close()
	if b.ownsProcess && b.cmd != nil && b.cmd.Process != nil {
		killProcessGroup(b.cmd)
		b.cmd.Wait()
	}
	return nil
}

// Page is one attached page target (a flattened CDP session).
type Page struct {
	cli       *cdpClient
	sessionID string
	targetID  string
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
	p := &Page{cli: b.cli, sessionID: attached.SessionID, targetID: targetID}
	for _, domain := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if _, err := p.cli.call(ctx, p.sessionID, domain, nil); err != nil {
			return nil, fmt.Errorf("%s: %w", domain, err)
		}
	}
	// Install the in-flight network monitor on this and every future document so
	// WaitForNetworkIdle has data to poll (best-effort; failures are non-fatal).
	_, _ = p.cli.call(ctx, p.sessionID, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": netMonitorScript})
	_ = p.Eval(ctx, netMonitorScript, nil)
	return p, nil
}

// netMonitorScript maintains window.__octoNet.{n,idleSince} by wrapping fetch and
// XMLHttpRequest, so replay can wait for XHR/fetch activity to settle. It counts
// only fetch/XHR (not sub-resources) — which is what "the SPA finished loading
// its data" means in practice — and resets naturally per document. Idempotent.
const netMonitorScript = `(function(){
  if(window.__octoNet) return;
  var s=window.__octoNet={n:0, idleSince:Date.now()};
  function inc(){ s.n++; s.idleSince=0; }
  function dec(){ s.n=Math.max(0,s.n-1); if(s.n===0) s.idleSince=Date.now(); }
  try{ var of=window.fetch; if(of){ window.fetch=function(){ inc(); return of.apply(this,arguments).then(function(r){dec();return r;},function(e){dec();throw e;}); }; } }catch(_){}
  try{ var send=XMLHttpRequest.prototype.send; XMLHttpRequest.prototype.send=function(){ inc(); try{ this.addEventListener('loadend',function(){dec();}); }catch(_){ dec(); } return send.apply(this,arguments); }; }catch(_){}
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
	before := map[string]bool{}
	if ps, err := b.Pages(ctx); err == nil {
		for _, p := range ps {
			before[p.TargetID] = true
		}
	}
	if err := page.Click(ctx, target); err != nil {
		return page, err
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if ps, err := b.Pages(ctx); err == nil {
			for _, ti := range ps {
				if before[ti.TargetID] || ti.TargetID == page.TargetID() {
					continue
				}
				if np, aerr := b.AttachPage(ctx, ti.TargetID); aerr == nil {
					return np, nil
				}
			}
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return page, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
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
	case <-time.After(30 * time.Second):
		return fmt.Errorf("navigate %s: timed out waiting for load", url)
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
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return fmt.Errorf("eval threw: %s", r.ExceptionDetails.Text)
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
