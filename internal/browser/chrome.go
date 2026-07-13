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
	"strings"
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

// defaultProfileDirs lists the default Chrome/Chromium/Edge user-data
// directories per platform — where DevToolsActivePort is written.
func defaultProfileDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library/Application Support")
		return []string{
			filepath.Join(base, "Google/Chrome"),
			filepath.Join(base, "Google/Chrome Canary"),
			filepath.Join(base, "Chromium"),
			filepath.Join(base, "Microsoft Edge"),
		}
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		return []string{
			filepath.Join(local, `Google\Chrome\User Data`),
			filepath.Join(local, `Chromium\User Data`),
			filepath.Join(local, `Microsoft\Edge\User Data`),
		}
	default:
		return []string{
			filepath.Join(home, ".config/google-chrome"),
			filepath.Join(home, ".config/chromium"),
			filepath.Join(home, ".config/microsoft-edge"),
		}
	}
}

// devToolsWS reads <userDataDir>/DevToolsActivePort (line 1 = port, line 2 = ws
// path) and builds the browser-level CDP websocket URL. Chrome writes this when
// started with remote debugging; using it avoids the /json HTTP discovery
// endpoint, which recent Chrome disables — this is how a running, logged-in
// Chrome is attached without relaunching.
func devToolsWS(userDataDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(userDataDir, "DevToolsActivePort"))
	if err != nil {
		return "", false
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(lines) < 2 {
		return "", false
	}
	port := strings.TrimSpace(lines[0])
	path := strings.TrimSpace(lines[1])
	if port == "" || path == "" {
		return "", false
	}
	return fmt.Sprintf("ws://127.0.0.1:%s%s", port, path), true
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
	setProcessGroup(cmd)
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
			killProcessGroup(cmd)
			return nil, "", err
		}
	}
	wsURL, err := browserWebSocketURL(ctx, port)
	if err != nil {
		killProcessGroup(cmd)
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

// browserWebSocketURL resolves the browser-level CDP WebSocket endpoint for a
// Chrome on the given debug port.
//
// Classic `--remote-debugging-port` launches serve /json/version, whose
// webSocketDebuggerUrl carries a per-launch UUID path. The chrome://inspect
// "Allow remote debugging for this browser instance" toggle, by contrast,
// serves the CDP socket but NOT the /json HTTP endpoints (they 404) — there the
// browser socket is reachable at the fixed, UUID-less /devtools/browser path.
// So once the HTTP server answers without a debugger URL, fall back to that
// fixed path rather than failing. (Matches the web-access skill's CDP proxy.)
//
// A connection error (port not up yet) keeps retrying to tolerate a
// just-launched Chrome; only a live-but-/json-less responder triggers the
// fallback.
func browserWebSocketURL(ctx context.Context, port int) (string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	fallback := fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser", port)
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
			// HTTP server answered but gave no debugger URL → /json is disabled
			// (chrome://inspect toggle path). Use the fixed browser socket.
			return fallback, nil
		}
		if time.Now().After(deadline) {
			return "", &DialError{
				URL:        url,
				StatusCode: 0,
				Err:        fmt.Errorf("timed out fetching CDP endpoint on port %d", port),
			}
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return "", &DialError{
					URL:        url,
					StatusCode: 0,
					Err:        fmt.Errorf("timed out fetching CDP endpoint on port %d", port),
				}
			}
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
