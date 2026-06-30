package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/browser"
	"github.com/Leihb/octo-agent/internal/config"
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

// Recording + self-heal state. browserHealer is injected by app.WireTools (it
// needs a model Sender, which tools can't import directly).
var (
	recorderMu       sync.Mutex
	activeRecorder   *browser.Recorder
	recorderStartURL string
	browserHealer    browser.Healer
	browserSkillGen  browser.SkillGenerator
)

// browserVision gates whether the browser tool hands the model images. Default
// true (vision-capable); app.WireTools sets it from the active model's config
// so a text-only model (e.g. qwen-max) gets a text note instead of an image
// content block that its endpoint would reject.
var browserVision = func() *atomic.Bool { b := &atomic.Bool{}; b.Store(true); return b }()

// SetBrowserVision enables/disables handing images to the model.
func SetBrowserVision(on bool) { browserVision.Store(on) }

// BrowserVisionEnabled reports the current setting (for tests/diagnostics).
func BrowserVisionEnabled() bool { return browserVision.Load() }

// BrowserHealerSet / BrowserSkillGeneratorSet report whether the LLM-backed
// browser helpers are wired (for tests/diagnostics).
func BrowserHealerSet() bool {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	return browserHealer != nil
}
func BrowserSkillGeneratorSet() bool {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	return browserSkillGen != nil
}

// SetBrowserHealer injects the LLM-backed step healer used by run_skill.
func SetBrowserHealer(h browser.Healer) {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	browserHealer = h
}

// SetBrowserSkillGenerator injects the LLM-backed skill distiller used by
// record_stop (nil falls back to deterministic compilation).
func SetBrowserSkillGenerator(g browser.SkillGenerator) {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	browserSkillGen = g
}

// BrowserSkillsDir is where recorded browser skills live (editable YAML).
func BrowserSkillsDir() string {
	if d := os.Getenv("OCTO_BROWSER_SKILLS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".octo", "browser-skills")
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

// closeSession tears down a browser session. When we attached to the user's own
// Chrome, Close() only drops the WS and leaves our tab behind, so close the tab
// we opened too (don't litter the user's browser); if we launched Chrome
// ourselves, Close() kills it whole. Safe to call with nils.
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

// browserPage returns the active page, connecting (or launching) Chrome on first
// use according to config.
func browserPage(ctx context.Context) (*browser.Page, *browser.Browser, error) {
	browserSession.mu.Lock()
	defer browserSession.mu.Unlock()
	if browserSession.page != nil {
		// The session is package-global and shared across chat sessions, so a tab
		// opened (and since navigated/closed) in an earlier session can leave a
		// stale handle whose CDP target is gone — every action would then fail with
		// "Session with given id not found" (-32001). Probe liveness before reusing
		// it; if dead, tear it down (off the lock) and re-establish below.
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := browserSession.page.Eval(probeCtx, "1", nil)
		cancel()
		if err == nil {
			return browserSession.page, browserSession.b, nil
		}
		go closeSession(browserSession.b, browserSession.page)
		browserSession.b, browserSession.page = nil, nil
	}
	cfg, _ := config.Load()
	bc := cfg.Browser

	var b *browser.Browser
	var err error
	switch {
	case bc.ConnectPort != 0:
		if b, err = browser.ConnectByPort(ctx, bc.ConnectPort); err != nil {
			return nil, nil, fmt.Errorf("connect to Chrome on port %d: %w", bc.ConnectPort, err)
		}
	case bc.AttachRunning && bc.UserDataDir != "":
		if b, err = browser.ConnectViaProfile(ctx, bc.UserDataDir); err != nil {
			return nil, nil, err
		}
	case bc.AttachRunning:
		if b, err = browser.DiscoverRunningChrome(ctx); err != nil {
			return nil, nil, err
		}
	default:
		// No explicit attach config: prefer the user's logged-in Chrome if one
		// is running with remote debugging. Discovery only succeeds when the
		// user deliberately enabled it (the chrome://inspect toggle or
		// --remote-debugging-port), so this never hijacks an ordinary browser —
		// it just means `octo browser setup` users, and anyone who flipped the
		// toggle, get their logged-in session without extra config. Falls back
		// to a fresh throwaway Chrome.
		if b, err = browser.DiscoverRunningChrome(ctx); err != nil {
			if b, err = browser.Launch(ctx, browser.LaunchOptions{
				ExecPath:    bc.ExecPath,
				UserDataDir: bc.UserDataDir,
				Headless:    bc.Headless,
			}); err != nil {
				return nil, nil, fmt.Errorf("launch Chrome: %w", err)
			}
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
			"task genuinely needs operating a web UI (no API available).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"navigate", "back", "click", "hover", "type", "select", "key", "scroll", "wait", "screenshot", "observe", "ax", "cookies", "upload", "download", "pages", "select_page", "close", "eval", "record_start", "record_stop", "run_skill"},
					"description": "The browser action to perform. observe lists the page's URL/title and interactable elements with selectors (text only) — the cheap way to look at an unfamiliar page before acting; works on any model. screenshot returns an image of the page for a vision-capable model to actually see (use when content is visual). cookies returns the current page's cookies (HttpOnly included) for session reuse / token extraction. record_start/record_stop capture the USER's own demonstration into an editable skill — record_start only installs listeners, so after it you MUST hand control to the user: tell them to perform the actions themselves in their browser and to say when they're done, then call record_stop. Do NOT drive the page yourself (navigate/click/type) while recording — your tool actions are not the demonstration and a click that navigates is easily lost; only the user's real gestures are captured. run_skill replays a recording (deterministic, self-healing).",
				},
				"name":       map[string]any{"type": "string", "description": "Skill name (record_stop / run_skill)."},
				"params":     map[string]any{"type": "object", "description": "Param values for {{...}} placeholders (run_skill)."},
				"url":        map[string]any{"type": "string", "description": "Target URL (navigate)."},
				"selector":   map[string]any{"type": "string", "description": "Target element selector (click/hover/type/select/scroll/wait/upload/download). Plain CSS, or a Playwright-style form: :has-text(\"…\")/:text(\"…\")/:contains(\"…\"), text=…, :visible, xpath=…, css=…. Use observe to see real selectors."},
				"frame":      map[string]any{"type": "string", "description": "Optional CSS selector of a same-origin iframe to scope the selector into (e.g. iframe#app)."},
				"text":       map[string]any{"type": "string", "description": "Text to type (type)."},
				"value":      map[string]any{"type": "string", "description": "Option value, text, or label to pick in a <select> (select)."},
				"keys":       map[string]any{"type": "string", "description": "Key or combo, e.g. enter, escape, ctrl+a (key)."},
				"js":         map[string]any{"type": "string", "description": "JavaScript expression to evaluate (eval)."},
				"files":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute file paths to set on a file <input> (upload)."},
				"index":      map[string]any{"type": "integer", "description": "Page index from the pages list to switch to (select_page)."},
				"timeout_ms": map[string]any{"type": "integer", "description": "Wait timeout in ms (wait); default 10000."},
				"dx":         map[string]any{"type": "number", "description": "Horizontal scroll delta (scroll)."},
				"dy":         map[string]any{"type": "number", "description": "Vertical scroll delta (scroll)."},
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
	// the whole turn. run_skill replays many steps and download waits for a file
	// to finish, so they get a much longer ceiling.
	timeout := 45 * time.Second
	switch action {
	case "run_skill", "download":
		timeout = 5 * time.Minute
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
		if err := page.Click(ctx, sel); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "clicked " + sel}, nil

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
		sel := targetSelector(input)
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: wait requires selector")
		}
		timeout := 10 * time.Second
		if ms, ok := input["timeout_ms"].(float64); ok && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
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
		if !browserVision.Load() {
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
			return agent.ToolResult{}, fmt.Errorf("browser: a recording is already in progress")
		}
		rec := browser.NewRecorder(page)
		if err := rec.Start(ctx); err != nil {
			return agent.ToolResult{}, err
		}
		var u string
		_ = page.Eval(ctx, "location.href", &u)
		activeRecorder, recorderStartURL = rec, u
		return agent.ToolResult{Text: "recording started on " + u}, nil

	case "record_stop":
		name := getStr(input, "name")
		if name == "" || filepath.Base(name) != name {
			return agent.ToolResult{}, fmt.Errorf("browser: record_stop requires a valid skill name")
		}
		recorderMu.Lock()
		rec, startURL, gen := activeRecorder, recorderStartURL, browserSkillGen
		activeRecorder = nil
		recorderMu.Unlock()
		if rec == nil {
			return agent.ToolResult{}, fmt.Errorf("browser: no recording in progress")
		}
		rec.Stop()
		skill := browser.GenerateSkill(ctx, name, startURL, rec.Events(), gen)
		dir := BrowserSkillsDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return agent.ToolResult{}, err
		}
		path := filepath.Join(dir, name+".yaml")
		if err := browser.SaveSkill(path, skill); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: fmt.Sprintf("recorded %d step(s) → %s\nReview/edit it there (set params, fix selectors), then replay with action=run_skill name=%q.", len(skill.Steps), path, name)}, nil

	case "run_skill":
		name := getStr(input, "name")
		if name == "" || filepath.Base(name) != name {
			return agent.ToolResult{}, fmt.Errorf("browser: run_skill requires a valid skill name")
		}
		path := filepath.Join(BrowserSkillsDir(), name+".yaml")
		skill, err := browser.LoadSkill(path)
		if err != nil {
			return agent.ToolResult{}, fmt.Errorf("browser: load skill %q: %w", name, err)
		}
		params := map[string]string{}
		if raw, ok := input["params"].(map[string]any); ok {
			for k, v := range raw {
				params[k] = fmt.Sprintf("%v", v)
			}
		}
		recorderMu.Lock()
		healer := browserHealer
		recorderMu.Unlock()
		modified, err := browser.ReplaySkill(ctx, page, &skill, params, browser.ReplayOptions{Healer: healer})
		if err != nil {
			return agent.ToolResult{}, fmt.Errorf("browser: run_skill %q: %w", name, err)
		}
		msg := fmt.Sprintf("ran skill %q (%d steps)", name, len(skill.Steps))
		if modified {
			if werr := browser.SaveSkill(path, skill); werr == nil {
				msg += " — self-healed, skill updated at " + path
			}
		}
		return agent.ToolResult{Text: msg}, nil

	default:
		return agent.ToolResult{}, fmt.Errorf("browser: unknown action %q", action)
	}
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
