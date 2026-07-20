package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/browser"
	"github.com/open-octo/octo-agent/internal/config"
)

// browserSession holds the per-session Chrome connection, reused across tool
// calls so navigate→click→download share one page. Package-global to match the
// codebase's session-resource pattern (task store, sub-agent manager); reset by
// app.WireTools cleanup at session end.
var browserSession struct {
	mu   sync.Mutex
	b    *browser.Browser
	page *browser.Page
}

// SetBrowserSession injects a pre-connected session (tests).
func SetBrowserSession(b *browser.Browser, p *browser.Page) {
	browserSession.mu.Lock()
	defer browserSession.mu.Unlock()
	browserSession.b, browserSession.page = b, p
}

// setActivePage repoints the package-global session at a new tab — e.g. after a
// click (or a replayed recording) opened and switched to one — so subsequent tool
// calls act on the page the user actually ended up on.
func setActivePage(b *browser.Browser, p *browser.Page) {
	browserSession.mu.Lock()
	defer browserSession.mu.Unlock()
	browserSession.b, browserSession.page = b, p
}

// Recording + self-heal state. browserHealer is injected by app.WireTools (it
// needs a model Sender, which tools can't import directly).
var (
	recorderMu          sync.Mutex
	activeRecorder      *browser.Recorder
	recorderStartURL    string
	browserHealer       browser.Healer
	browserRecordingGen browser.RecordingGenerator
)

// SetBrowserVision enables/disables handing images to the model.
// Deprecated: alias of SetModelVision, kept for existing callers — the flag
// is model capability, not browser state.
func SetBrowserVision(on bool) { SetModelVision(on) }

// BrowserVisionEnabled reports the process-global setting (for
// tests/diagnostics) — it does not see a per-turn value stamped via
// WithModelVision. Use ModelVisionEnabled(ctx) to see what a turn actually
// resolves to.
func BrowserVisionEnabled() bool { return modelVision.Load() }

// BrowserHealerSet / BrowserRecordingGeneratorSet report whether the
// process-global LLM-backed browser helpers are wired (for tests/
// diagnostics) — they do not see a per-turn value stamped via
// WithBrowserHealer / WithBrowserRecordingGenerator. Use the ForCtx variants
// below to see what a turn actually resolves to.
func BrowserHealerSet() bool {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	return browserHealer != nil
}
func BrowserRecordingGeneratorSet() bool {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	return browserRecordingGen != nil
}

// BrowserHealerSetForCtx / BrowserRecordingGeneratorSetForCtx report whether a
// helper is available for ctx (ctx-scoped first, then the process-global one)
// — for tests/diagnostics on the server's per-turn wiring.
func BrowserHealerSetForCtx(ctx context.Context) bool {
	return resolveBrowserHealer(ctx) != nil
}
func BrowserRecordingGeneratorSetForCtx(ctx context.Context) bool {
	return resolveBrowserRecordingGenerator(ctx) != nil
}

// SetBrowserHealer injects the LLM-backed step healer used by replay, for the
// CLI's one-session-per-process path. The server instead stamps
// WithBrowserHealer into each turn's ctx — two concurrent sessions on
// different models would otherwise race on this process-global.
func SetBrowserHealer(h browser.Healer) {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	browserHealer = h
}

// SetBrowserRecordingGenerator injects the LLM-backed recording distiller used
// by record_stop (nil falls back to deterministic compilation), for the CLI's
// one-session-per-process path. The server instead stamps
// WithBrowserRecordingGenerator into each turn's ctx.
func SetBrowserRecordingGenerator(g browser.RecordingGenerator) {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	browserRecordingGen = g
}

type browserHealerCtxKeyType struct{}

var browserHealerCtxKey = browserHealerCtxKeyType{}

// WithBrowserHealer returns ctx carrying the per-turn LLM-backed step healer.
func WithBrowserHealer(ctx context.Context, h browser.Healer) context.Context {
	return context.WithValue(ctx, browserHealerCtxKey, h)
}

// resolveBrowserHealer picks the ctx-scoped healer (server, stamped fresh
// every turn) first, then falls back to the process-global one (CLI/TUI).
func resolveBrowserHealer(ctx context.Context) browser.Healer {
	if h, ok := ctx.Value(browserHealerCtxKey).(browser.Healer); ok {
		return h
	}
	recorderMu.Lock()
	defer recorderMu.Unlock()
	return browserHealer
}

type browserRecordingGenCtxKeyType struct{}

var browserRecordingGenCtxKey = browserRecordingGenCtxKeyType{}

// WithBrowserRecordingGenerator returns ctx carrying the per-turn LLM-backed
// recording distiller.
func WithBrowserRecordingGenerator(ctx context.Context, g browser.RecordingGenerator) context.Context {
	return context.WithValue(ctx, browserRecordingGenCtxKey, g)
}

// resolveBrowserRecordingGenerator picks the ctx-scoped generator (server,
// stamped fresh every turn) first, then falls back to the process-global one
// (CLI/TUI).
func resolveBrowserRecordingGenerator(ctx context.Context) browser.RecordingGenerator {
	if g, ok := ctx.Value(browserRecordingGenCtxKey).(browser.RecordingGenerator); ok {
		return g
	}
	recorderMu.Lock()
	defer recorderMu.Unlock()
	return browserRecordingGen
}

// BrowserRecordingsDir is where browser recordings live (editable YAML).
// OCTO_BROWSER_RECORDINGS_DIR overrides it; the legacy OCTO_BROWSER_SKILLS_DIR
// is still honored. The default moved from ~/.octo/browser-skills with the
// recording rename: migrate the old default dir on first sight, and fall back
// to it when the rename can't happen rather than letting recordings vanish.
func BrowserRecordingsDir() string {
	if d := os.Getenv("OCTO_BROWSER_RECORDINGS_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("OCTO_BROWSER_SKILLS_DIR"); d != "" { // pre-rename name
		return d
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".octo", "browser-recordings")
	old := filepath.Join(home, ".octo", "browser-skills")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err := os.Stat(old); err == nil {
			if err := os.Rename(old, dir); err != nil {
				return old
			}
		}
	}
	return dir
}

// RenderBrowserRecordingsManifest lists browser recordings for the L1 system-
// prompt manifest, so the model can discover and replay them in a normal
// conversation instead of the user having to name one explicitly. Returns "" when
// there are none. Unlike SKILL.md skills, these are replayed via the browser tool
// (action=replay), not loaded via the skill tool — the note says so.
func RenderBrowserRecordingsManifest() string {
	list := browser.ListRecordings(BrowserRecordingsDir())
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Browser recordings\n\n")
	b.WriteString("Recorded browser workflows. Replay one with the `browser` tool " +
		"(action=replay, name=<name>, params as declared); it returns the recording's " +
		"declared outputs (downloaded files, extracted values) as JSON. These are " +
		"replayed, not loaded via the `skill` tool. A replay executes every recorded " +
		"step verbatim, end to end — it cannot be partially applied or adapted beyond " +
		"its declared params. Only replay a recording when the request matches what " +
		"it does end to end; if any detail not covered by a declared param differs " +
		"(a different item or target), drive the browser directly instead.\n\n")
	for _, s := range list {
		fmt.Fprintf(&b, "- %s", s.Name)
		if s.Description != "" {
			fmt.Fprintf(&b, ": %s", s.Description)
		} else if d := s.StepDigest(); d != "" {
			// No description (e.g. the LLM distill fell back at record time): show
			// the step path instead, so the model can judge fit — especially the
			// final step, which a bare name hides.
			fmt.Fprintf(&b, ": steps: %s", d)
		}
		if len(s.Params) > 0 {
			names := make([]string, len(s.Params))
			for i, p := range s.Params {
				names[i] = p.Name
			}
			fmt.Fprintf(&b, " [params: %s]", strings.Join(names, ", "))
		}
		if len(s.Outputs) > 0 {
			outs := make([]string, len(s.Outputs))
			for i, o := range s.Outputs {
				if o.Type != "" {
					outs[i] = fmt.Sprintf("%s (%s)", o.Name, o.Type)
				} else {
					outs[i] = o.Name
				}
			}
			fmt.Fprintf(&b, " [outputs: %s]", strings.Join(outs, ", "))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ResetBrowserSession closes and clears the active browser session.
func ResetBrowserSession() {
	browserSession.mu.Lock()
	b := browserSession.b
	p := browserSession.page
	browserSession.b, browserSession.page = nil, nil
	browserSession.mu.Unlock()
	closeSession(b, p)
}

// closeSession tears down a browser session. When attached to the user's own
// Chrome, Close() only drops the WS and leaves our tab behind, so close the tab
// we opened too (don't litter the user's browser). The production path never
// launches its own Chrome (attach-only), so OwnsProcess is always false there;
// integration tests inject a launched browser via SetBrowserSession, where
// OwnsProcess can be true and Close() kills it whole. Safe to call with nils.
func closeSession(b *browser.Browser, p *browser.Page) {
	if b == nil {
		return
	}
	if !b.OwnsProcess() && p != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = b.ClosePage(ctx, p.TargetID())
		cancel()
	}
	b.Close()
}

// browserEnabled gates the tool: advertised only when a Chrome can be located
// or an explicit debug port is configured.
func browserEnabled() bool {
	cfg, _ := config.Load()
	return cfg.Browser.ConnectPort != 0 || cfg.Browser.AttachRunning || browser.ChromeAvailable(cfg.Browser.ExecPath)
}

// browserConnectGuide is the user-facing instruction shown when the browser tool
// cannot attach to Chrome. It is phrased so the LLM can offer to start the
// browser-setup flow, not just report a low-level error.
const browserConnectGuide = "Browser is not correctly set up. To fix: open Chrome, go to chrome://inspect/#remote-debugging, enable remote debugging, and allow the authorization prompt. Alternatively, reconfigure via Octo's Browser settings, or start Chrome from the terminal with --remote-debugging-port=9222 --remote-allow-origins=*"

// wrapBrowserConnectError turns a low-level connection error into an actionable
// message that tells the user (and the LLM) how to run browser setup. It
// preserves the original error for logging via Unwrap.
func wrapBrowserConnectError(err error) error {
	if err == nil {
		return nil
	}
	var dialErr *browser.DialError
	if errors.As(err, &dialErr) {
		if dialErr.IsForbidden() {
			return fmt.Errorf("%s (Chrome refused the connection; if a prompt appeared, click Allow): %w", browserConnectGuide, err)
		}
		if dialErr.StatusCode == 0 {
			return fmt.Errorf("%s (cannot reach the debug port; is Chrome running?): %w", browserConnectGuide, err)
		}
	}
	return fmt.Errorf("%s: %w", browserConnectGuide, err)
}

// browserPage returns the active page, connecting (or launching) Chrome on first
// use according to config.
func browserPage(ctx context.Context) (*browser.Page, *browser.Browser, error) {
	browserSession.mu.Lock()
	defer browserSession.mu.Unlock()
	if browserSession.page != nil {
		// The session is package-global and shared across chat sessions, so a tab
		// opened (and since navigated/closed) in an earlier session can leave a
		// stale handle whose CDP target is gone — every action would then fail with
		// "Session with given id not found" (-32001). Probe liveness before reusing.
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := browserSession.page.Eval(probeCtx, "1", nil)
		cancel()
		if err == nil {
			return browserSession.page, browserSession.b, nil
		}
		// The page's CDP target is gone, but the browser-level WS is almost always
		// still fine. Drop ONLY the dead page and keep the connection — reopening a
		// tab on it below needs no new WS dial. A fresh dial makes Chrome re-show
		// its remote-debugging authorization prompt every single time, which is
		// exactly what we must avoid within one process.
		browserSession.page = nil
	}
	// Reuse the live browser connection if we have one: open a fresh tab on it
	// (same WS → no re-auth). Only if that fails is the connection itself gone
	// (Chrome closed/restarted), so drop it and fall through to a fresh connect.
	if browserSession.b != nil {
		// Probe the browser-level connection with a short timeout. If Chrome was
		// restarted, the old WebSocket may be half-open and hang until the caller's
		// context expires — leaving no time to dial a new connection. We deliberately
		// sacrifice a few seconds here so the reconnect below has a fresh chance.
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		page, err := browserSession.b.NewPage(probeCtx, "about:blank")
		cancel()
		if err == nil {
			browserSession.page = page
			return page, browserSession.b, nil
		}
		// Probe failed — the browser-level WebSocket is dead (Chrome was restarted
		// or crashed). Close it and fall through to a fresh ConnectByPort below:
		// the new dial must happen on a clean socket, otherwise it can land on the
		// stale Chrome still bound to the port (bad handshake, no authorization
		// prompt — the exact "no prompt after restart" bug this guards against).
		// Passing page=nil skips the (dead) tab close and just drops the WS.
		// concurrent close so a Chrome that never answers the close frame can't
		// stall the reconnect — wait up to 3s for it to finish, then move on.
		closeDone := make(chan struct{})
		go func() {
			closeSession(browserSession.b, nil)
			close(closeDone)
		}()
		select {
		case <-closeDone:
		case <-time.After(3 * time.Second):
		}
		browserSession.b = nil
	}
	cfg, _ := config.Load()
	bc := cfg.Browser

	var b *browser.Browser
	var err error
	switch {
	case bc.ConnectPort != 0:
		if b, err = browser.ConnectByPort(ctx, bc.ConnectPort); err != nil {
			return nil, nil, fmt.Errorf("connect to Chrome on port %d: %w", bc.ConnectPort, wrapBrowserConnectError(err))
		}
	case bc.AttachRunning && bc.UserDataDir != "":
		if b, err = browser.ConnectViaProfile(ctx, bc.UserDataDir); err != nil {
			return nil, nil, wrapBrowserConnectError(err)
		}
	case bc.AttachRunning:
		if b, err = browser.DiscoverRunningChrome(ctx); err != nil {
			return nil, nil, wrapBrowserConnectError(err)
		}
	default:
		// No explicit attach config: reuse the user's logged-in Chrome if one is
		// running with remote debugging. Discovery only succeeds when the user
		// deliberately enabled it (the chrome://inspect toggle or
		// --remote-debugging-port), so this never hijacks an ordinary browser.
		// When nothing is reachable we return an actionable error rather than
		// launching a throwaway Chrome: the browser tool only ever drives a real,
		// user-owned Chrome, never a headless instance it spins up itself (which
		// would carry no login session and trip the macOS "Chrome Safe Storage"
		// keychain prompt).
		if b, err = browser.DiscoverRunningChrome(ctx); err != nil {
			// Return the discovery error as-is — it already carries an
			// actionable connect guide ("launch Chrome with
			// --remote-debugging-port or set browser.connect_port"), so
			// wrapping it would repeat the setup instruction.
			return nil, nil, err
		}
	}

	// Always open our own fresh tab — never hijack a tab the user already has
	// open. When attached to the user's real Chrome, pages[0] could be anything
	// they're using (including the octo web UI itself), and navigating it away
	// would clobber their session. Cookies/login are profile-wide, so a new tab
	// still carries the logged-in session. To drive an existing tab, the model
	// uses the pages/select_page actions explicitly.
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		b.Close()
		return nil, nil, fmt.Errorf("open page: %w", err)
	}
	browserSession.b, browserSession.page = b, page
	return page, b, nil
}

// BrowserTool drives a real Chrome over CDP: navigate, click, type, wait,
// capture downloads, etc. One tool, action-multiplexed, like computer-use.
type BrowserTool struct{}

func (BrowserTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "browser",
		Description: "Drive a real Chrome browser to automate a web task: navigate, " +
			"click elements, type, wait for elements, capture file downloads, screenshot, " +
			"and inspect the page. Reuses the user's logged-in session. Use only when the " +
			"task genuinely needs operating a web UI (no API available); for a known URL's " +
			"public content, web_fetch is cheaper. Before a nontrivial web task — an " +
			"anti-bot platform, a login wall, an unfamiliar site to explore, multi-source " +
			"research — load the web-access skill first (via the skill tool, if it appears " +
			"under Available skills): " +
			"it carries tool-routing rules and per-site experience notes from past sessions.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"navigate", "back", "click", "hover", "type", "select", "key", "scroll", "wait", "screenshot", "observe", "ax", "cookies", "upload", "download", "pages", "select_page", "close", "eval", "record_start", "record_stop", "record_cancel", "replay", "run_skill"},
					"description": "The browser action to perform. observe lists the page's URL/title and interactable elements with selectors (text only) — the cheap way to look at an unfamiliar page before acting; works on any model. screenshot returns an image of the page for a vision-capable model to actually see (use when content is visual). ax returns an accessibility-tree digest (roles and names) — a semantic text view of the page, an alternative to observe when document structure matters more than selectors. pages lists open tabs; select_page switches between them. cookies returns the current page's cookies (HttpOnly included) for session reuse / token extraction. record_start/record_stop capture the USER's own demonstration into an editable recording — record_start only installs listeners, so after it you MUST hand control to the user: tell them to perform the actions themselves in their browser and to say when they're done, then call record_stop (or record_cancel to discard the demo without saving). Do NOT drive the page yourself (navigate/click/type) while recording — your tool actions are not the demonstration and a click that navigates is easily lost; only the user's real gestures are captured. replay replays a recording (deterministic, self-healing; run_skill is a deprecated alias of replay).",
				},
				"name":         map[string]any{"type": "string", "description": "Recording name (record_stop / replay)."},
				"params":       map[string]any{"type": "object", "description": "Param values for {{...}} placeholders (replay). Params declared secret:true in the recording are collected by the runtime OUTSIDE the conversation (session cache → OCTO_BROWSER_SECRET_<NAME> env → masked user prompt) — never pass a secret value here, just omit it. Omitting a required NON-secret param fails with a missing-param error; then decide whether to supply a value or ask the user."},
				"url":          map[string]any{"type": "string", "description": "Target URL (navigate)."},
				"selector":     map[string]any{"type": "string", "description": "Target element selector (click/hover/type/select/scroll/wait/upload/download). Plain CSS, or a Playwright-style form: :has-text(\"…\")/:text(\"…\")/:contains(\"…\"), text=…, :visible, xpath=…, css=…. Use observe to see real selectors."},
				"network_idle": map[string]any{"type": "boolean", "description": "wait with no selector: settle until fetch/XHR activity stops (bounded by timeout_ms) instead of a fixed delay — the robust way to wait for an SPA's data to finish loading."},
				"frame":        map[string]any{"type": "string", "description": "Optional CSS selector of a same-origin iframe to scope the selector into (e.g. iframe#app)."},
				"text":         map[string]any{"type": "string", "description": "Text to type (type)."},
				"value":        map[string]any{"type": "string", "description": "Option value, text, or label to pick in a <select> (select)."},
				"keys":         map[string]any{"type": "string", "description": "Key or combo, e.g. enter, escape, ctrl+a (key)."},
				"js":           map[string]any{"type": "string", "description": "JavaScript expression to evaluate (eval). Runs with full page access — use it to recursively traverse shadow roots or read DOM state that CSS selectors can't reach (click/type don't pierce shadow DOM)."},
				"files":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute file paths to set on a file <input> (upload). Use this instead of clicking an upload button — a click opens a native file dialog that cannot be driven."},
				"index":        map[string]any{"type": "integer", "description": "Page index from the pages list to switch to (select_page)."},
				"timeout_ms":   map[string]any{"type": "integer", "description": "Wait timeout in ms (wait); default 10000."},
				"dx":           map[string]any{"type": "number", "description": "Horizontal scroll delta (scroll)."},
				"dy":           map[string]any{"type": "number", "description": "Vertical scroll delta (scroll)."},
			},
			"required": []string{"action"},
		},
	}
}

func (BrowserTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	action, _ := input["action"].(string)
	if action == "" {
		return agent.ToolResult{}, fmt.Errorf("browser: action is required")
	}

	// Bound every action so a CDP call a janky/loading page never acks (e.g. a
	// mouseWheel scroll on a heavy SPA) fails with a timeout instead of hanging
	// the whole turn. download waits for a file to finish, so it gets a longer
	// ceiling. replay gets the hard cap here as a parent bound; the replay case
	// refines it per recording via replayTimeout (a long skill must not be
	// killed mid-run by the old fixed 5 minutes, and a short one keeps it).
	timeout := 45 * time.Second
	switch action {
	case "download":
		timeout = 5 * time.Minute
	case "replay", "run_skill":
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	page, b, err := browserPage(ctx)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("browser: %w", err)
	}

	switch action {
	case "navigate":
		url := getStr(input, "url")
		if url == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: navigate requires url")
		}
		if err := page.Navigate(ctx, url); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "navigated to " + url}, nil

	case "back":
		if err := page.Back(ctx); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "navigated back"}, nil

	case "click":
		sel := targetSelector(input)
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: click requires selector")
		}
		// Follow a new tab the click may open (target=_blank / SPA window.open) so
		// the click doesn't silently look like a no-op on the old page.
		np, err := b.ClickFollow(ctx, page, sel)
		if err != nil {
			return agent.ToolResult{}, err
		}
		text := "clicked " + sel
		if np != page {
			setActivePage(b, np)
			text += " (followed to a new tab)"
		}
		return agent.ToolResult{Text: text}, nil

	case "hover":
		sel := targetSelector(input)
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: hover requires selector")
		}
		if err := page.Hover(ctx, sel); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "hovered " + sel}, nil

	case "select":
		sel, val := targetSelector(input), getStr(input, "value")
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: select requires selector")
		}
		if err := page.SelectOption(ctx, sel, val); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("selected %q in %s", val, sel)}, nil

	case "type":
		sel, text := targetSelector(input), getStr(input, "text")
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: type requires selector")
		}
		if err := page.TypeText(ctx, sel, text); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("typed %q into %s", text, sel)}, nil

	case "key":
		combo := getStr(input, "keys")
		if combo == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: key requires keys")
		}
		if err := page.Key(ctx, combo); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "pressed " + combo}, nil

	case "scroll":
		sel := targetSelector(input)
		dx, _ := input["dx"].(float64)
		dy, _ := input["dy"].(float64)
		if err := page.Scroll(ctx, sel, dx, dy); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "scrolled"}, nil

	case "wait":
		timeout := 10 * time.Second
		if ms, ok := input["timeout_ms"].(float64); ok && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
		sel := targetSelector(input)
		if sel == "" {
			// No selector: settle for network idle when asked (SPA data loads), a
			// robust alternative to guessing a fixed delay.
			if ni, _ := input["network_idle"].(bool); ni {
				if err := page.WaitForNetworkIdle(ctx, 0, timeout); err != nil {
					return agent.ToolResult{}, err
				}
				return agent.ToolResult{Text: "network idle"}, nil
			}
			return agent.ToolResult{}, fmt.Errorf("browser: wait requires selector or network_idle=true")
		}
		if err := page.WaitFor(ctx, sel, timeout); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: sel + " appeared"}, nil

	case "screenshot":
		shot, err := page.Screenshot(ctx)
		if err != nil {
			return agent.ToolResult{}, err
		}
		path := saveScreenshot(shot)
		if !ModelVisionEnabled(ctx) {
			// Text-only model: handing it an image block would be rejected by the
			// endpoint. Return the path and steer the model to the text channels.
			return agent.ToolResult{Text: "screenshot saved to " + path + " (the current model can't view images — use observe/eval/ax to read the page instead)"}, nil
		}
		// Return the image as a vision block so the model actually sees the page
		// (not just a file path), and keep the on-disk copy for artifacts.
		return agent.ToolResult{
			Text:   "screenshot saved to " + path,
			Blocks: []agent.ContentBlock{agent.NewImageBlock("image/png", shot)},
		}, nil

	case "observe":
		// Text-only "look at this page": the current URL/title + the page's
		// interactable elements with selectors. This is the see-before-you-act
		// primitive and works on any model. Vision is decoupled — call screenshot
		// to actually see pixels (and only a multimodal model can use that).
		frame := getStr(input, "frame")
		var meta struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		_ = page.Eval(ctx, `({url: location.href, title: document.title})`, &meta)
		digest, derr := browser.InteractiveDigest(ctx, page, frame, 60)
		var sb strings.Builder
		if meta.URL != "" {
			fmt.Fprintf(&sb, "page: %s — %s\n\n", meta.Title, meta.URL)
		}
		sb.WriteString("interactable elements:\n")
		if derr != nil {
			fmt.Fprintf(&sb, "(could not read elements: %v)\n", derr)
		} else if len(digest) == 0 {
			sb.WriteString("(none found)\n")
		} else {
			for _, e := range digest {
				if e.Text != "" {
					fmt.Fprintf(&sb, "- %s  →  %s\n", e.Text, e.Selector)
				} else {
					fmt.Fprintf(&sb, "- %s\n", e.Selector)
				}
			}
		}
		return agent.ToolResult{Text: sb.String()}, nil

	case "ax":
		raw, err := page.AXTree(ctx)
		if err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: axDigest(raw)}, nil

	case "cookies":
		cookies, err := page.Cookies(ctx)
		if err != nil {
			return agent.ToolResult{}, err
		}
		j, err := json.MarshalIndent(cookies, "", "  ")
		if err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("%d cookie(s) for the current page (HttpOnly included):\n%s", len(cookies), j)}, nil

	case "upload":
		sel := targetSelector(input)
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: upload requires selector (the file input)")
		}
		files := getStrings(input, "files")
		if len(files) == 0 {
			return agent.ToolResult{}, fmt.Errorf("browser: upload requires files")
		}
		if err := page.Upload(ctx, sel, files); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("set %d file(s) on %s", len(files), sel)}, nil

	case "download":
		sel := targetSelector(input)
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: download requires selector (the element that starts the download)")
		}
		path, err := b.CaptureDownload(ctx, downloadDir(), func() error { return page.Click(ctx, sel) })
		if err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "downloaded to " + path}, nil

	case "pages":
		pages, err := b.Pages(ctx)
		if err != nil {
			return agent.ToolResult{}, err
		}
		var sb strings.Builder
		for i, p := range pages {
			fmt.Fprintf(&sb, "%d. %s — %s\n", i, p.Title, p.URL)
		}
		return agent.ToolResult{Text: sb.String()}, nil

	case "select_page":
		idx := 0
		if v, ok := input["index"].(float64); ok {
			idx = int(v)
		}
		pages, err := b.Pages(ctx)
		if err != nil {
			return agent.ToolResult{}, err
		}
		if idx < 0 || idx >= len(pages) {
			return agent.ToolResult{}, fmt.Errorf("browser: page index %d out of range (%d pages)", idx, len(pages))
		}
		np, err := b.AttachPage(ctx, pages[idx].TargetID)
		if err != nil {
			return agent.ToolResult{}, err
		}
		setBrowserPage(np)
		return agent.ToolResult{Text: "switched to page " + pages[idx].URL}, nil

	case "close":
		if err := b.ClosePage(ctx, page.TargetID()); err != nil {
			return agent.ToolResult{}, err
		}
		setBrowserPage(nil)
		return agent.ToolResult{Text: "closed page"}, nil

	case "eval":
		js := getStr(input, "js")
		if js == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: eval requires js")
		}
		var raw json.RawMessage
		if err := page.Eval(ctx, js, &raw); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: string(raw)}, nil

	case "record_start":
		recorderMu.Lock()
		defer recorderMu.Unlock()
		if activeRecorder != nil {
			return agent.ToolResult{}, fmt.Errorf("browser: a recording is already in progress (record_stop to save it, record_cancel to discard it)")
		}
		rec := browser.NewRecorder(page)
		if err := rec.Start(ctx); err != nil {
			return agent.ToolResult{}, err
		}
		var u string
		_ = page.Eval(ctx, "location.href", &u)
		activeRecorder, recorderStartURL = rec, u
		return agent.ToolResult{Text: "recording started on " + u}, nil

	case "record_cancel":
		// Discard an abandoned demonstration without saving it — previously the
		// only way out was a throwaway record_stop (which wrote a junk YAML), and
		// record_start stayed wedged until then.
		recorderMu.Lock()
		rec := activeRecorder
		activeRecorder = nil
		recorderMu.Unlock()
		if rec == nil {
			return agent.ToolResult{}, fmt.Errorf("browser: no recording in progress")
		}
		rec.Stop()
		return agent.ToolResult{Text: "recording discarded (nothing saved)"}, nil

	case "record_stop":
		name := getStr(input, "name")
		if name == "" || filepath.Base(name) != name {
			return agent.ToolResult{}, fmt.Errorf("browser: record_stop requires a valid recording name")
		}
		gen := resolveBrowserRecordingGenerator(ctx)
		recorderMu.Lock()
		rec, startURL := activeRecorder, recorderStartURL
		activeRecorder = nil
		recorderMu.Unlock()
		if rec == nil {
			return agent.ToolResult{}, fmt.Errorf("browser: no recording in progress")
		}
		rec.Stop()
		recording := browser.GenerateRecording(ctx, name, startURL, rec.Events(), gen)
		dir := BrowserRecordingsDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return agent.ToolResult{}, err
		}
		path := filepath.Join(dir, name+".yaml")
		if err := browser.SaveRecording(path, recording); err != nil {
			return agent.ToolResult{}, err
		}
		msg := fmt.Sprintf("recorded %d step(s) → %s\nReview/edit it there (set params, fix selectors). Replay it with the Replay button in the Browser view, or action=replay name=%q. (Recordings are NOT keyword-triggerable — they only run when explicitly replayed.)", len(recording.Steps), path, name)
		if recording.Description == "" {
			// The LLM distill fell back (or omitted a description). Surface it here
			// — the stderr warning never reaches the model — so the recording doesn't
			// silently stay a bare name in the manifest.
			msg += "\nNo description was distilled; the recordings manifest will show a step digest instead. Add a description: line to the YAML to improve discovery."
		}
		return agent.ToolResult{Text: msg}, nil

	case "replay", "run_skill":
		name := getStr(input, "name")
		if name == "" || filepath.Base(name) != name {
			return agent.ToolResult{}, fmt.Errorf("browser: replay requires a valid recording name")
		}
		path := filepath.Join(BrowserRecordingsDir(), name+".yaml")
		recording, err := browser.LoadRecording(path)
		if err != nil {
			return agent.ToolResult{}, fmt.Errorf("browser: load recording %q: %w", name, err)
		}
		params := map[string]string{}
		if raw, ok := input["params"].(map[string]any); ok {
			for k, v := range raw {
				params[k] = fmt.Sprintf("%v", v)
			}
		}
		if err := resolveReplayParams(ctx, &recording, name, params); err != nil {
			return agent.ToolResult{}, err
		}
		healer := resolveBrowserHealer(ctx)
		// Refine the call's parent bound (30 min) by the recording's length, so a
		// long but healthy replay isn't killed by a one-size-fits-all ceiling.
		rctx, rcancel := context.WithTimeout(ctx, replayTimeout(len(recording.Steps)))
		defer rcancel()
		modified, finalPage, outputs, err := browser.ReplayRecording(rctx, page, &recording, params, browser.ReplayOptions{Healer: healer, Browser: b, DownloadDir: downloadDir()})
		if err != nil {
			return agent.ToolResult{}, fmt.Errorf("browser: replay %q: %w", name, err)
		}
		// A click in the recording may have opened (and switched to) a new tab; keep
		// the session pointed there so follow-up actions act on the right page.
		if finalPage != nil && finalPage != page {
			setActivePage(b, finalPage)
		}
		// Return a structured envelope (not just a step count) so the recording's
		// declared outputs — downloaded file paths, extracted values — can flow to
		// a downstream step or be parsed by an orchestrating workflow.
		env := map[string]any{
			"recording": name,
			"steps":     len(recording.Steps),
			"outputs":   outputs,
		}
		if modified {
			// Self-heal write-back: persists the selector that just verified. Note it
			// re-marshals the whole YAML — hand-written comments in the file are
			// dropped (fields keep their values). Documented behavior, not data loss.
			if werr := browser.SaveRecording(path, recording); werr == nil {
				env["self_healed"] = true
			}
		}
		if action == "run_skill" {
			// Deprecated alias kept so saved workflows and habits don't break.
			env["deprecated_action"] = "run_skill is deprecated; use action=replay"
		}
		j, err := json.MarshalIndent(env, "", "  ")
		if err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: string(j)}, nil

	default:
		return agent.ToolResult{}, fmt.Errorf("browser: unknown action %q", action)
	}
}

// The replay param collection point is resolveReplayParams in
// replay_secrets.go, shared by this tool's replay action and the workflow
// recording() primitive: non-secret missing params keep the plain-error
// semantics (the model decides whether to re-invoke with `params` filled or
// surface an ask_user_question); secret params are resolved by the runtime
// out-of-band (session cache → env → masked ask) so their values never enter
// the conversation.

// replayTimeout bounds one replay by the recording's length: a base for the
// browser attach and page loads, plus a per-step budget (a step waits up to
// StepTimeout for its target, and a failing step may consult the healer for
// several rounds). Floored at the old fixed 5 minutes so short recordings
// behave exactly as before; capped so a pathological recording can't hold a
// turn indefinitely.
func replayTimeout(steps int) time.Duration {
	t := 2*time.Minute + time.Duration(steps)*20*time.Second
	if t < 5*time.Minute {
		return 5 * time.Minute
	}
	if t > 30*time.Minute {
		return 30 * time.Minute
	}
	return t
}

func getStr(input map[string]any, key string) string {
	s, _ := input[key].(string)
	return s
}

// targetSelector returns the element selector, scoped into a same-origin iframe
// when a frame is given (via the backend's " >>> " piercing convention).
func targetSelector(input map[string]any) string {
	sel := getStr(input, "selector")
	if frame := getStr(input, "frame"); frame != "" && sel != "" {
		return frame + " >>> " + sel
	}
	return sel
}

func getStrings(input map[string]any, key string) []string {
	raw, ok := input[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// setBrowserPage swaps the active page, keeping the same browser connection.
func setBrowserPage(p *browser.Page) {
	browserSession.mu.Lock()
	browserSession.page = p
	browserSession.mu.Unlock()
}

// downloadDir resolves the configured download directory, falling back to a
// temp dir under the OS temp root.
func downloadDir() string {
	cfg, _ := config.Load()
	if cfg.Browser.DownloadDir != "" {
		return cfg.Browser.DownloadDir
	}
	return filepath.Join(os.TempDir(), "octo-browser-downloads")
}

// screenshotSeq disambiguates screenshot filenames within a session.
var screenshotSeq atomic.Uint64

// saveScreenshot writes a PNG to the download dir for artifact/preview use and
// returns the path. Best-effort: a write failure yields a note instead of an
// error, because the caller's vision image block — not the file — is what the
// model relies on.
func saveScreenshot(shot []byte) string {
	dir := downloadDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "(not saved: " + err.Error() + ")"
	}
	path := filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", screenshotSeq.Add(1)))
	if err := os.WriteFile(path, shot, 0o644); err != nil {
		return "(not saved: " + err.Error() + ")"
	}
	return path
}

// axNode is the subset of a CDP accessibility node the digest needs.
type axNode struct {
	Role struct {
		Value string `json:"value"`
	} `json:"role"`
	Name struct {
		Value string `json:"value"`
	} `json:"name"`
	Ignored bool `json:"ignored"`
}

// axDigest reduces a full AX tree to a bounded list of named, non-ignored
// elements — the selection-friendly semantic view, not the raw firehose.
func axDigest(raw json.RawMessage) string {
	var tree struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		return "(ax tree unavailable)"
	}
	var sb strings.Builder
	count := 0
	for _, n := range tree.Nodes {
		if n.Ignored || n.Name.Value == "" {
			continue
		}
		fmt.Fprintf(&sb, "%s: %s\n", n.Role.Value, n.Name.Value)
		if count++; count >= 100 {
			fmt.Fprintf(&sb, "… (%d more nodes)\n", len(tree.Nodes)-count)
			break
		}
	}
	if count == 0 {
		return "(no named elements)"
	}
	return sb.String()
}
