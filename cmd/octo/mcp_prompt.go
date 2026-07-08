package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/mcp"
)

// authCodeTimeout bounds how long a CLI/TUI session waits for the browser
// callback before giving up — the user closed the tab, or never got there.
const authCodeTimeout = 5 * time.Minute

// openBrowser best-effort launches the OS's default browser at url. Errors
// are non-fatal — the caller still prints the URL for the user to open by
// hand.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// "start" is a cmd.exe builtin, not a standalone executable; the
		// empty quoted arg is start's window-title placeholder, required
		// whenever the target itself might be quoted.
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// awaitLocalCallback binds nothing itself — it serves exactly one request on
// the given listener, matches its "state" query param, and returns the
// resulting "code" (or the "error"/"error_description" params as an error).
// Shared by cliOAuthPrompt and tuiOAuthPrompt since the wait logic is
// identical; only how the URL/status get surfaced to the user differs.
func awaitLocalCallback(ctx context.Context, ln net.Listener, state string) (string, error) {
	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if errCode := q.Get("error"); errCode != "" {
			fmt.Fprint(w, "<html><body>Authorization failed — you can close this tab.</body></html>")
			resCh <- result{err: fmt.Errorf("%s: %s", errCode, q.Get("error_description"))}
			return
		}
		if q.Get("state") != state {
			// Don't resolve on a mismatched/forged hit — keep waiting for the
			// real one.
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<html><body>Invalid state — you can close this tab.</body></html>")
			return
		}
		fmt.Fprint(w, "<html><body>Authorization received — you can close this tab.</body></html>")
		resCh <- result{code: q.Get("code")}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	select {
	case res := <-resCh:
		return res.code, res.err
	case <-time.After(authCodeTimeout):
		return "", errors.New("timed out waiting for browser authorization")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// cliOAuthPrompt is the user-facing implementation of mcp.OAuthPrompt for
// `octo`. A bare CLI session has no HTTP listener of its own, so it opens a
// one-shot loopback listener for the duration of each authorization attempt
// and auto-launches the user's browser at the authorize URL.
//
// The card looks like:
//
//	┌─ Authorize "lark-project" ──────────────────────────────
//	│ Open: https://project.larksuite.com/authorize?...
//	│ Waiting for authorization…
//	│ Authorized.
//	└────────────────────────────────────────────────────────────
type cliOAuthPrompt struct {
	out        io.Writer
	serverName string

	listener net.Listener
}

func newCLIOAuthPrompt(out io.Writer, serverName string) *cliOAuthPrompt {
	return &cliOAuthPrompt{out: out, serverName: serverName}
}

// RedirectURI binds a one-shot loopback listener for this authorization
// attempt and returns its callback URL. AwaitAuthorizationCode serves
// exactly one request on it.
func (p *cliOAuthPrompt) RedirectURI() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(p.out, "oauth: failed to open a local callback listener: %v\n", err)
		return ""
	}
	p.listener = ln
	return fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
}

func (p *cliOAuthPrompt) AwaitAuthorizationCode(ctx context.Context, authorizeURL, state string) (string, error) {
	if p.listener == nil {
		return "", errors.New("oauth: no local callback listener")
	}
	defer p.listener.Close()

	border := strings.Repeat("─", 56)
	fmt.Fprintf(p.out, "\n┌─ Authorize %q %s\n", p.serverName, border)
	fmt.Fprintf(p.out, "│ Open: %s\n", authorizeURL)
	if err := openBrowser(authorizeURL); err != nil {
		fmt.Fprintf(p.out, "│ (couldn't auto-open a browser: %v — open the link above manually)\n", err)
	}
	fmt.Fprint(p.out, "│ Waiting for authorization…\n")

	return awaitLocalCallback(ctx, p.listener, state)
}

func (p *cliOAuthPrompt) Done() {
	fmt.Fprintf(p.out, "│ Authorized.\n└%s\n\n", strings.Repeat("─", 64))
}

// Compile-time check that we still implement the interface.
var _ mcp.OAuthPrompt = (*cliOAuthPrompt)(nil)

// tuiOAuthPrompt surfaces the OAuth flow through the TUI sink rather than the
// raw terminal, which bubbletea owns once the program is running. It only
// fires when a server has no usable cached token — the steady-state silent
// refresh path never calls AwaitAuthorizationCode, so background-connecting
// under the TUI normally produces no prompt at all.
type tuiOAuthPrompt struct {
	sink       *tuiSink
	serverName string

	listener net.Listener
}

func newTUIOAuthPrompt(sink *tuiSink, serverName string) *tuiOAuthPrompt {
	return &tuiOAuthPrompt{sink: sink, serverName: serverName}
}

func (p *tuiOAuthPrompt) RedirectURI() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if p.sink != nil {
			p.sink.Notice(fmt.Sprintf("oauth: failed to open a local callback listener: %v", err))
		}
		return ""
	}
	p.listener = ln
	return fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
}

func (p *tuiOAuthPrompt) AwaitAuthorizationCode(ctx context.Context, authorizeURL, state string) (string, error) {
	if p.listener == nil {
		return "", errors.New("oauth: no local callback listener")
	}
	defer p.listener.Close()

	if p.sink != nil {
		if err := openBrowser(authorizeURL); err != nil {
			p.sink.Notice(fmt.Sprintf("MCP %q needs authorization — open %s", p.serverName, authorizeURL))
		} else {
			p.sink.Notice(fmt.Sprintf("MCP %q needs authorization — opening %s in your browser", p.serverName, authorizeURL))
		}
	}

	return awaitLocalCallback(ctx, p.listener, state)
}

func (p *tuiOAuthPrompt) Done() {
	if p.sink != nil {
		p.sink.Notice(fmt.Sprintf("MCP %q authorized.", p.serverName))
	}
}

var _ mcp.OAuthPrompt = (*tuiOAuthPrompt)(nil)

// sinkWriter adapts the TUI sink into an io.Writer for ConnectAll's warn param,
// so a "server skipped" line shows up as a scrollback notice instead of
// corrupting the bubbletea frame.
type sinkWriter struct{ sink *tuiSink }

func (w sinkWriter) Write(b []byte) (int, error) {
	if w.sink != nil {
		if s := strings.TrimRight(string(b), "\n"); s != "" {
			w.sink.Notice(s)
		}
	}
	return len(b), nil
}
