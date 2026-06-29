package protean

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// Recorder starts and stops a Protean screen recording.
type Recorder struct {
	bridge *Bridge
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex
	OutDir string
}

// NewRecorder creates a recorder that will write to outDir.
func NewRecorder(b *Bridge, outDir string) *Recorder {
	return &Recorder{bridge: b, OutDir: outDir}
}

// Start begins recording in the background. It returns immediately; the caller
// must later call Stop.
func (r *Recorder) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil {
		return fmt.Errorf("recording already in progress")
	}
	if err := os.MkdirAll(r.OutDir, 0o700); err != nil {
		return fmt.Errorf("create recording dir: %w", err)
	}
	// Pass a nil context so the recorder subprocess outlives any request
	// context; we stop it explicitly via stdin/signal.
	r.cmd = r.bridge.command(nil, "-c", recorderScript, r.OutDir)
	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		r.cmd = nil
		return fmt.Errorf("recorder stdout pipe: %w", err)
	}
	stdin, err := r.cmd.StdinPipe()
	if err != nil {
		r.cmd = nil
		return fmt.Errorf("recorder stdin pipe: %w", err)
	}
	r.stdin = stdin
	r.cmd.Stderr = os.Stderr
	if err := r.cmd.Start(); err != nil {
		r.cmd = nil
		r.stdin = nil
		return fmt.Errorf("start protean recorder: %w", err)
	}
	// Wait for the "started" JSON line before returning so callers know the
	// screen-recording subprocess is alive and permissions are granted. We must
	// read this synchronously (a single reader) before handing stdout to the
	// background drain — two goroutines reading the same pipe would race for the
	// line and could block Start until the recording ends.
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if scanner.Scan() {
		var payload struct {
			Status string `json:"status"`
			Info   any    `json:"info"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &payload); err == nil {
			if payload.Status != "started" {
				r.cmd.Process.Kill()
				r.cmd.Wait()
				r.cmd = nil
				r.stdin = nil
				return fmt.Errorf("recorder failed to start: %s", payload.Error)
			}
		}
	}
	// Now drain the rest of stdout in the background so the pipe never blocks
	// the recorder. The scanner may hold a few buffered bytes past the line, but
	// the recorder emits nothing until stop, so nothing is lost.
	go io.Copy(io.Discard, stdout)
	return nil
}

// Stop signals the recorder process to stop and waits for it to exit.
func (r *Recorder) Stop() error {
	r.mu.Lock()
	cmd := r.cmd
	stdin := r.stdin
	r.cmd = nil
	r.stdin = nil
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("no recording in progress")
	}
	// Primary stop mechanism: write "stop" to the recorder's stdin. This is
	// more reliable than signals when spawned from Go on macOS.
	if stdin != nil {
		_, _ = io.WriteString(stdin, "stop\n")
		_ = stdin.Close()
	}
	// Fallback: SIGINT, then Kill.
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
	}
	return cmd.Wait()
}
