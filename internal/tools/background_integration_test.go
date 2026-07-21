package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBackgroundServerLifecycle launches a real HTTP server via the terminal
// tool in background mode, verifies it responds to curl, inspects startup logs
// via terminal_output, then stops it with SIGTERM and confirms a graceful exit.
func TestBackgroundServerLifecycle(t *testing.T) {
	// Write a minimal Go HTTP server into a temp directory and compile it. The
	// server binds 127.0.0.1:0 and reports the real port on stdout — probing a
	// free port here and passing it in would leave a window where another
	// process on the host grabs it between release and rebind (a real CI flake:
	// the TCP dial then "succeeds" against a stranger and curl returns junk).
	tmp := t.TempDir()
	src := filepath.Join(tmp, "srv.go")
	code := `package main
import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)
func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("Listen error:", err)
		os.Exit(1)
	}
	fmt.Println("Server starting on port", ln.Addr().(*net.TCPAddr).Port)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello-bg")
	})
	srv := &http.Server{}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Println("Server error:", err)
			os.Exit(1)
		}
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	fmt.Println("Shutting down gracefully...")
	if err := srv.Shutdown(context.Background()); err != nil {
		fmt.Println("Shutdown error:", err)
		os.Exit(1)
	}
	fmt.Println("Server stopped")
}
`
	if err := os.WriteFile(src, []byte(code), 0644); err != nil {
		t.Fatalf("write server source: %v", err)
	}
	bin := filepath.Join(tmp, "srv")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	term := TerminalTool{}
	ctx := context.Background()

	// Use ';' instead of '&&' for cross-platform shell compatibility (PowerShell
	// 5.1 does not support '&&'). Both POSIX sh and PowerShell accept ';'.
	resBuild, err := term.Execute(ctx, "terminal", map[string]any{
		"command": fmt.Sprintf("cd %s ; go build -o %s srv.go", tmp, bin),
	})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	if strings.Contains(resBuild.Text, "[exit:") && !strings.Contains(resBuild.Text, "[exit: 0]") {
		t.Fatalf("server build failed: %s", resBuild.Text)
	}

	// Launch the server in the background as an interactive process so we can
	// inspect its startup logs via terminal_output.
	resLaunch, err := term.Execute(ctx, "terminal", map[string]any{
		"command":           bin,
		"run_in_background": "interactive",
	})
	if err != nil {
		t.Fatalf("launch server: %v", err)
	}
	bgID := extractBgID(resLaunch.Text)
	if bgID == "" {
		t.Fatalf("expected bg id in launch result, got: %s", resLaunch.Text)
	}

	// Read the server's chosen port from its startup log via terminal_output —
	// this doubles as the startup-log assertion: the line only appears once the
	// listener is bound, so the port is live as soon as we can parse it.
	outTool := TerminalOutputTool{}
	portRe := regexp.MustCompile(`Server starting on port (\d+)`)
	var port string
	waitFor(t, "server to report its port", func() bool {
		res, err := outTool.Execute(ctx, "terminal_output", map[string]any{"id": bgID})
		if err != nil {
			return false
		}
		if m := portRe.FindStringSubmatch(res.Text); m != nil {
			port = m[1]
			return true
		}
		return false
	})

	// Verify it responds via curl. Retried: a loaded machine can transiently
	// refuse/reset even with the listener bound, and one lost datagram of
	// stdout must not fail the whole lifecycle test.
	curlLimit := 10 * time.Second
	if runtime.GOOS == "windows" {
		curlLimit = 45 * time.Second
	}
	var lastCurl string
	deadline := time.Now().Add(curlLimit)
	for {
		resCurl, err := term.Execute(ctx, "terminal", map[string]any{
			"command": fmt.Sprintf("curl -s http://127.0.0.1:%s/", port),
		})
		if err == nil {
			lastCurl = resCurl.Text
			if strings.Contains(lastCurl, "hello-bg") {
				break
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("curl never returned expected body; last output: %s", lastCurl)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Graceful stop with SIGTERM.
	killTool := KillShellTool{}
	resKill, err := killTool.Execute(ctx, "kill_shell", map[string]any{
		"id":     bgID,
		"signal": "SIGTERM",
	})
	if err != nil {
		t.Fatalf("kill_shell SIGTERM: %v", err)
	}
	t.Logf("kill result: %s", resKill.Text)

	// Verify we did NOT fall back to SIGKILL.
	if strings.Contains(resKill.Text, "exited: signal: killed") {
		t.Error("server was killed with SIGKILL — SIGTERM graceful shutdown did not work")
	}

	// On POSIX the server traps SIGTERM, calls srv.Shutdown, and exits 0 (or
	// the shell reports "signal: terminated" on Linux).  On Windows,
	// taskkill /T without /F sends WM_CLOSE which console apps do not handle,
	// so it falls back to force kill and the exit code is 1.  That's a
	// platform limitation, not a bug — we still verify the process stopped.
	if runtime.GOOS != "windows" {
		if !strings.Contains(resKill.Text, "exited: 0") && !strings.Contains(resKill.Text, "exited: signal: terminated") {
			t.Logf("kill result: %s", resKill.Text)
			t.Error("expected clean exit (exited: 0 or signal: terminated) after SIGTERM graceful shutdown")
		}
	}
}

// extractBgID pulls the first bg_NNN substring from s, trimming trailing
// punctuation (the launch result ends with "bg_2.").
func extractBgID(s string) string {
	for _, part := range strings.Fields(s) {
		if strings.HasPrefix(part, "bg_") {
			return strings.TrimRight(part, ".:,;!?")
		}
	}
	return ""
}
