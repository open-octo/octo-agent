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
		cmd.Process.Kill()
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

// Close tears down the connection and, if this process launched Chrome, the
// Chrome process too.
func (b *Browser) Close() error {
	b.cli.close()
	if b.ownsProcess && b.cmd != nil && b.cmd.Process != nil {
		b.cmd.Process.Kill()
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
	return p, nil
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

// Navigate loads url and waits for the page load event.
func (p *Page) Navigate(ctx context.Context, url string) error {
	events, unsub := p.cli.subscribe("Page.loadEventFired", p.sessionID)
	defer unsub()
	if _, err := p.cli.call(ctx, p.sessionID, "Page.navigate", map[string]any{"url": url}); err != nil {
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
