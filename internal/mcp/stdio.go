package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// StdioTransport speaks JSON-RPC 2.0 over a subprocess's stdin/stdout. One
// JSON object per line, UTF-8, no chunking. The subprocess inherits stderr
// from the parent so its logs reach the user (and don't fill an unbounded
// pipe). The transport owns the *exec.Cmd lifecycle: Start runs it, Close
// closes stdin (signalling EOF to the child) and Wait()s for exit.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	dec    *json.Decoder

	closeMu sync.Mutex
	closed  atomic.Bool

	// sendMu serialises writes so concurrent Send calls don't interleave
	// JSON bytes on stdin.
	sendMu sync.Mutex
}

// StdioConfig is the wire-format-agnostic spawn config. Env overrides extend
// (not replace) the parent's environment so PATH / HOME stay available to
// the child by default.
type StdioConfig struct {
	Command string
	Args    []string
	Env     map[string]string
	// Stderr receives the child's diagnostic output. nil defaults to os.Stderr.
	// Callers running an alternate-screen TUI MUST set this to a non-terminal
	// sink (a log file or io.Discard): the child writes its stderr at arbitrary
	// times during the session, and a direct terminal write corrupts the
	// rendered frame.
	Stderr io.Writer
}

// NewStdioTransport spawns the configured subprocess and wires its stdin /
// stdout to a line-delimited JSON-RPC pipe. The child's stderr goes to
// cfg.Stderr (os.Stderr when unset) — callers under a TUI must redirect it
// off the terminal.
//
// The returned transport is already running — Close will signal shutdown
// by closing stdin (the conventional MCP "I'm done, please exit") and then
// Wait()ing for exit so resources are released.
func NewStdioTransport(ctx context.Context, cfg StdioConfig) (*StdioTransport, error) {
	if cfg.Command == "" {
		return nil, errors.New("mcp: stdio transport: empty command")
	}
	// Use exec.Command (not CommandContext) so the child outlives the
	// connect-time context. connectOne creates a 10s timeout context for the
	// initialize handshake; if we bound the subprocess to that context the
	// child would be killed when connectOne returns, breaking every later
	// tool call with "broken pipe". The transport's Close method handles
	// graceful shutdown via stdin EOF.
	cmd := exec.Command(cfg.Command, cfg.Args...)
	// Inherit env + add/override the configured entries. Building a fresh
	// slice keeps os.Environ() intact for the parent.
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	// Child diagnostics go to cfg.Stderr (a log file under a TUI; os.Stderr in
	// plain/headless mode). Writing a child's async stderr straight to the
	// terminal corrupts an alternate-screen TUI frame.
	if cfg.Stderr != nil {
		cmd.Stderr = cfg.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("mcp: start %s: command not found in PATH (use an absolute path in mcp.json, or add the directory to PATH)", cfg.Command)
		}
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Command, err)
	}

	t := &StdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		// bufio.Reader on stdout lets us read line-by-line; json.Decoder
		// accepts a reader and tokenises whitespace-separated JSON values.
		// Using a Decoder over a raw scanner lets us hand back nested
		// objects without re-marshalling.
		dec: json.NewDecoder(bufio.NewReaderSize(stdout, 64*1024)),
	}
	return t, nil
}

// Send writes msg as one line of JSON to the subprocess stdin. Concurrent
// callers are serialised via sendMu so frames don't interleave.
func (t *StdioTransport) Send(ctx context.Context, msg *Message) error {
	if t.closed.Load() {
		return errors.New("mcp: stdio transport: closed")
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: marshal: %w", err)
	}
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	// We append a newline so older MCP servers that read line-by-line (and
	// many community implementations do) see a clean delimiter; newer
	// JSON-aware decoders don't care either way.
	b = append(b, '\n')
	if _, err := t.stdin.Write(b); err != nil {
		return fmt.Errorf("mcp: write: %w", err)
	}
	return nil
}

// Receive blocks for the next JSON object on stdout. The Decoder skips
// whitespace between objects, so the wire format can be either one-object-
// per-line or stream-with-no-delimiters — both work.
//
// Context cancellation is handled in two layers: (1) if ctx is already done
// we return immediately; (2) cancelling ctx after Receive has blocked has
// no direct hook (Decoder doesn't take ctx), so the canonical interrupt is
// Close, which closes stdout and surfaces here as io.EOF.
func (t *StdioTransport) Receive(ctx context.Context) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.closed.Load() {
		return nil, io.EOF
	}
	var m Message
	if err := t.dec.Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Close shuts the subprocess down. The order matters: close stdin first so
// the child sees EOF and can exit on its own (the polite shutdown signal
// MCP servers expect), then Wait so process resources are reclaimed.
// Idempotent — repeat calls return nil.
func (t *StdioTransport) Close() error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	// stdin.Close() can race with the Send path; we hold closeMu but Send
	// only checks closed atomically before acquiring sendMu. The window is
	// tiny and a failed write surfaces as an error to the caller — fine.
	_ = t.stdin.Close()
	_ = t.stdout.Close()
	// Give the child a moment to exit gracefully after seeing stdin EOF.
	// If it doesn't, force-kill so Wait doesn't hang on a stuck process.
	done := make(chan struct{})
	go func() {
		_ = t.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		<-done
	}
	return nil
}
