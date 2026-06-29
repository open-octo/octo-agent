package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// LaunchOptions controls how a Chrome instance is started or located.
type LaunchOptions struct {
	// ExecPath overrides Chrome auto-detection.
	ExecPath string
	// UserDataDir is the Chrome profile directory. Empty launches a throwaway
	// temp profile (no login state) — fine for tests. Real workflows point this
	// at the user's profile so logged-in sessions are reused.
	UserDataDir string
	// Port is the remote-debugging port. 0 lets Chrome pick one, which is then
	// read back from the DevToolsActivePort file in the profile dir.
	Port int
	// Headless runs Chrome with --headless=new. Real interactive workflows want
	// this false so the user can watch and intervene.
	Headless bool
	// DownloadDir, when set, is where the page's downloads are directed.
	DownloadDir string
	// ExtraArgs are appended verbatim.
	ExtraArgs []string
}

// chromePaths lists the default Chrome executable locations per platform,
// most-preferred first.
func chromePaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		pf := os.Getenv("ProgramFiles")
		pfx86 := os.Getenv("ProgramFiles(x86)")
		local := os.Getenv("LOCALAPPDATA")
		return []string{
			filepath.Join(pf, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pfx86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pfx86, `Microsoft\Edge\Application\msedge.exe`),
		}
	default: // linux and friends
		return []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/microsoft-edge",
		}
	}
}

// ChromeAvailable reports whether a Chrome executable can be located, so the
// browser tool is only advertised on setups where it could work.
func ChromeAvailable(execPath string) bool {
	_, err := findChrome(execPath)
	return err == nil
}

// findChrome resolves the Chrome executable, honoring an explicit override.
func findChrome(override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("chrome exec %q: %w", override, err)
		}
		return override, nil
	}
	for _, p := range chromePaths() {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("google-chrome"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("chromium"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("chrome not found; set LaunchOptions.ExecPath")
}

// launchChrome starts Chrome and returns the running process plus the browser-
// level CDP websocket URL.
func launchChrome(ctx context.Context, opts LaunchOptions) (*exec.Cmd, string, error) {
	exe, err := findChrome(opts.ExecPath)
	if err != nil {
		return nil, "", err
	}
	dataDir := opts.UserDataDir
	tempDir := ""
	if dataDir == "" {
		tempDir, err = os.MkdirTemp("", "octo-chrome-")
		if err != nil {
			return nil, "", err
		}
		dataDir = tempDir
	}

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", opts.Port),
		"--user-data-dir=" + dataDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"about:blank",
	}
	if opts.Headless {
		args = append([]string{"--headless=new"}, args...)
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		if tempDir != "" {
			os.RemoveAll(tempDir)
		}
		return nil, "", fmt.Errorf("start chrome: %w", err)
	}

	port := opts.Port
	if port == 0 {
		port, err = readDevToolsPort(ctx, dataDir)
		if err != nil {
			cmd.Process.Kill()
			return nil, "", err
		}
	}
	wsURL, err := browserWebSocketURL(ctx, port)
	if err != nil {
		cmd.Process.Kill()
		return nil, "", err
	}
	return cmd, wsURL, nil
}

// readDevToolsPort waits for Chrome to write its chosen debug port to the
// DevToolsActivePort file in the profile dir (first line is the port).
func readDevToolsPort(ctx context.Context, dataDir string) (int, error) {
	path := filepath.Join(dataDir, "DevToolsActivePort")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil {
			var port int
			if _, err := fmt.Sscanf(string(data), "%d", &port); err == nil && port > 0 {
				return port, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("timed out reading DevToolsActivePort in %s", dataDir)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// browserWebSocketURL fetches the browser-level CDP endpoint from the debug
// HTTP server, retrying until Chrome is ready.
func browserWebSocketURL(ctx context.Context, port int) (string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			var v struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			dec := json.NewDecoder(resp.Body)
			derr := dec.Decode(&v)
			resp.Body.Close()
			if derr == nil && v.WebSocketDebuggerURL != "" {
				return v.WebSocketDebuggerURL, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out fetching CDP endpoint on port %d", port)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
