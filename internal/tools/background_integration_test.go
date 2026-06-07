package tools

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBackgroundServerLifecycle launches a real HTTP server via the terminal
// tool in background mode, verifies it responds to curl, inspects startup logs
// via terminal_output, then stops it with SIGTERM and confirms a graceful exit.
func TestBackgroundServerLifecycle(t *testing.T) {
	// Find a free port so we don't collide with anything on the host.
	port := freePort(t)

	// Write a minimal Go HTTP server into a temp directory and compile it.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "srv.go")
	code := fmt.Sprintf(`package main
import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)
func main() {
	port := os.Args[1]
	srv := &http.Server{Addr: ":" + port}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello-bg")
	})
	fmt.Println("Server starting on port", port)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
`)
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

	// Launch the server in the background.
	resLaunch, err := term.Execute(ctx, "terminal", map[string]any{
		"command":           fmt.Sprintf("%s %s", bin, port),
		"run_in_background": true,
	})
	if err != nil {
		t.Fatalf("launch server: %v", err)
	}
	bgID := extractBgID(resLaunch.Text)
	if bgID == "" {
		t.Fatalf("expected bg id in launch result, got: %s", resLaunch.Text)
	}

	// Wait for the server to actually start listening.
	waitFor(t, "server to accept connections", func() bool {
		conn, err := net.Dial("tcp", "127.0.0.1:"+port)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	})

	// Verify it responds via curl.
	resCurl, err := term.Execute(ctx, "terminal", map[string]any{
		"command": fmt.Sprintf("curl -s http://127.0.0.1:%s/", port),
	})
	if err != nil {
		t.Fatalf("curl server: %v", err)
	}
	if !strings.Contains(resCurl.Text, "hello-bg") {
		t.Fatalf("curl did not return expected body: %s", resCurl.Text)
	}

	// Inspect startup logs via terminal_output.
	outTool := TerminalOutputTool{}
	resLogs, err := outTool.Execute(ctx, "terminal_output", map[string]any{"id": bgID})
	if err != nil {
		t.Fatalf("terminal_output: %v", err)
	}
	if !strings.Contains(resLogs.Text, "Server starting") {
		t.Logf("startup logs: %s", resLogs.Text)
		t.Error("expected startup log line from the server")
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

// freePort asks the OS for an ephemeral port and immediately releases it.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
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
