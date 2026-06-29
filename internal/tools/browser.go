package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// ResetBrowserSession closes and clears the active browser session.
func ResetBrowserSession() {
	browserSession.mu.Lock()
	b := browserSession.b
	browserSession.b, browserSession.page = nil, nil
	browserSession.mu.Unlock()
	if b != nil {
		b.Close()
	}
}

// browserEnabled gates the tool: advertised only when a Chrome can be located
// or an explicit debug port is configured.
func browserEnabled() bool {
	cfg, _ := config.Load()
	return cfg.Browser.ConnectPort != 0 || browser.ChromeAvailable(cfg.Browser.ExecPath)
}

// browserPage returns the active page, connecting (or launching) Chrome on first
// use according to config.
func browserPage(ctx context.Context) (*browser.Page, *browser.Browser, error) {
	browserSession.mu.Lock()
	defer browserSession.mu.Unlock()
	if browserSession.page != nil {
		return browserSession.page, browserSession.b, nil
	}
	cfg, _ := config.Load()
	bc := cfg.Browser

	var b *browser.Browser
	var err error
	if bc.ConnectPort != 0 {
		b, err = browser.ConnectByPort(ctx, bc.ConnectPort)
		if err != nil {
			return nil, nil, fmt.Errorf("connect to Chrome on port %d: %w", bc.ConnectPort, err)
		}
	} else {
		b, err = browser.Launch(ctx, browser.LaunchOptions{
			ExecPath:    bc.ExecPath,
			UserDataDir: bc.UserDataDir,
			Headless:    bc.Headless,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("launch Chrome: %w", err)
		}
	}

	// Prefer an already-open tab (the user's logged-in page) when attaching;
	// otherwise open a blank one.
	var page *browser.Page
	if pages, perr := b.Pages(ctx); perr == nil && len(pages) > 0 {
		page, err = b.AttachPage(ctx, pages[0].TargetID)
	} else {
		page, err = b.NewPage(ctx, "about:blank")
	}
	if err != nil {
		b.Close()
		return nil, nil, fmt.Errorf("attach page: %w", err)
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
					"enum":        []string{"navigate", "click", "type", "key", "scroll", "wait", "screenshot", "ax", "download", "pages", "eval"},
					"description": "The browser action to perform.",
				},
				"url":        map[string]any{"type": "string", "description": "Target URL (navigate)."},
				"selector":   map[string]any{"type": "string", "description": "CSS selector of the target element (click/type/scroll/wait/download)."},
				"text":       map[string]any{"type": "string", "description": "Text to type (type)."},
				"keys":       map[string]any{"type": "string", "description": "Key or combo, e.g. enter, escape, ctrl+a (key)."},
				"js":         map[string]any{"type": "string", "description": "JavaScript expression to evaluate (eval)."},
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

	case "click":
		sel := getStr(input, "selector")
		if sel == "" {
			return agent.ToolResult{}, fmt.Errorf("browser: click requires selector")
		}
		if err := page.Click(ctx, sel); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "clicked " + sel}, nil

	case "type":
		sel, text := getStr(input, "selector"), getStr(input, "text")
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
		sel := getStr(input, "selector")
		dx, _ := input["dx"].(float64)
		dy, _ := input["dy"].(float64)
		if err := page.Scroll(ctx, sel, dx, dy); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "scrolled"}, nil

	case "wait":
		sel := getStr(input, "selector")
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
		dir := downloadDir()
		path := filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", len(shot)))
		if err := os.WriteFile(path, shot, 0o644); err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: "screenshot saved to " + path}, nil

	case "ax":
		raw, err := page.AXTree(ctx)
		if err != nil {
			return agent.ToolResult{}, err
		}
		return agent.ToolResult{Text: axDigest(raw)}, nil

	case "download":
		sel := getStr(input, "selector")
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
		for _, p := range pages {
			fmt.Fprintf(&sb, "- %s — %s\n", p.Title, p.URL)
		}
		return agent.ToolResult{Text: sb.String()}, nil

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

	default:
		return agent.ToolResult{}, fmt.Errorf("browser: unknown action %q", action)
	}
}

func getStr(input map[string]any, key string) string {
	s, _ := input[key].(string)
	return s
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
