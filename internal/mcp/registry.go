package mcp

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// Registry owns the live MCP client connections for a session. Built once
// at startup from a Config; tears all clients down on Close. Failures to
// connect or initialize one server degrade — the server is skipped, its
// error logged to stderr, and the registry keeps the survivors.
//
// This separation (Registry here vs. tool-adapter in cmd/octo) lets the
// internal/mcp package stay free of any agent / tool dependencies.
type Registry struct {
	mu     sync.RWMutex
	conns  map[string]*Connection // server name → live client
	errs   map[string]string      // server name → last connect error (no live conn)
	closed bool
}

// NewRegistry returns an empty registry ready for incremental Connect calls.
// ConnectAll builds on it; management UIs use it directly when the first
// server is added at runtime to a session that started with none.
func NewRegistry() *Registry {
	return &Registry{conns: map[string]*Connection{}, errs: map[string]string{}}
}

// Connection is the per-server bundle of state the adapters need: the
// client itself plus the pre-fetched tools/resources/prompts lists so the
// agent loop doesn't re-list on every turn.
type Connection struct {
	Name      string
	Client    *Client
	Tools     []Tool
	Resources []Resource
	Prompts   []Prompt

	// reauthMu guards reauthErr, set by MarkReauthRequired when a call made
	// through this (otherwise still "live") connection surfaces
	// ErrReauthRequired — the OAuth token died and nobody was present to
	// complete an interactive device flow. The connection object itself
	// stays usable/registered; this flag is how a caller with visibility
	// into Registry (e.g. the status endpoint) learns the connection is no
	// longer actually authenticated without waiting for it to be torn down.
	reauthMu  sync.Mutex
	reauthErr error
}

// MarkReauthRequired records that a call through this connection hit
// ErrReauthRequired. Safe for concurrent callers.
func (c *Connection) MarkReauthRequired(err error) {
	c.reauthMu.Lock()
	c.reauthErr = err
	c.reauthMu.Unlock()
}

// ReauthRequired reports the last error recorded by MarkReauthRequired, if
// any. ok is false when no reauth failure has been observed on this
// connection.
func (c *Connection) ReauthRequired() (msg string, ok bool) {
	c.reauthMu.Lock()
	defer c.reauthMu.Unlock()
	if c.reauthErr == nil {
		return "", false
	}
	return c.reauthErr.Error(), true
}

// ConnectAll spins up one client per configured server, runs the
// initialize handshake, and pre-fetches the surface lists. Failures are
// reported via the warn writer (typically os.Stderr) and the server is
// simply omitted — the user sees the error but the session still starts.
//
// The handshake + initial lists run with a per-server timeout so a single
// hung server can't stall startup. Servers are connected sequentially to
// keep startup logs readable; the connect cost is dominated by subprocess
// spawn and a single round-trip, so v1 doesn't need parallelism.
//
// authPromptFor produces the OAuthPrompt for one server. The factory
// shape (rather than a single shared prompt) lets the CLI label each
// device-flow card with the server name it's authorising. May be nil
// when no entry uses OAuth — a nil factory triggers the same "auth
// required but not wired" error as a missing config.
//
// childStderr is where each stdio server's subprocess diagnostics go. It is
// distinct from warn (startup connect failures, printed before any TUI takes
// over): a child's stderr streams throughout the session, so under a TUI it
// must point at a log file or io.Discard, never the terminal. nil falls back to
// os.Stderr inside the transport.
func ConnectAll(ctx context.Context, cfg *Config, info Implementation, authPromptFor func(serverName string) OAuthPrompt, warn, childStderr io.Writer) *Registry {
	r := NewRegistry()
	if cfg == nil {
		return r
	}
	for _, name := range cfg.ServerNames() {
		entry := cfg.Servers[name]
		var prompt OAuthPrompt
		if entry.Auth == "oauth" && authPromptFor != nil {
			prompt = authPromptFor(name)
		}
		conn, err := connectOne(ctx, name, entry, info, prompt, childStderr)
		if err != nil {
			r.errs[name] = err.Error()
			if warn != nil {
				fmt.Fprintf(warn, "mcp: server %q skipped: %v\n", name, err)
			}
			continue
		}
		r.conns[name] = conn
	}
	return r
}

func connectOne(ctx context.Context, name string, entry ServerEntry, info Implementation, authPrompt OAuthPrompt, childStderr io.Writer) (*Connection, error) {
	// Per-server timeout. OAuth-protected servers get a longer window
	// because the user has to do the device-flow dance interactively;
	// stdio + static-auth servers should still snap up in ~10s.
	timeout := 10 * time.Second
	if entry.Auth == "oauth" {
		timeout = 5 * time.Minute
	}
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var transport Transport
	switch entry.Kind() {
	case "stdio":
		t, err := NewStdioTransport(connectCtx, StdioConfig{
			Command: entry.Command,
			Args:    entry.Args,
			Env:     entry.Env,
			Stderr:  childStderr,
		})
		if err != nil {
			return nil, err
		}
		transport = t
	case "http":
		cfg := HTTPConfig{URL: entry.URL, Headers: entry.Headers}
		if entry.Auth == "oauth" {
			oc, err := NewOAuthClient(entry.URL, name, info.Name, authPrompt)
			if err != nil {
				return nil, fmt.Errorf("oauth init: %w", err)
			}
			cfg.OAuth = oc
		}
		t, err := NewHTTPTransport(cfg)
		if err != nil {
			return nil, err
		}
		transport = t
	default:
		return nil, fmt.Errorf("invalid transport for server %q", name)
	}

	client := NewClient(transport, info)
	if err := client.Initialize(connectCtx); err != nil {
		_ = client.Close()
		return nil, err
	}

	conn := &Connection{Name: name, Client: client}
	// Pre-fetch surface listings so per-turn calls don't pay an RPC just
	// to enumerate. We tolerate per-surface failures — a server with
	// broken resources/list still gets its tools/list cached.
	if tools, err := client.ListTools(connectCtx); err == nil {
		conn.Tools = tools
	}
	if resources, err := client.ListResources(connectCtx); err == nil {
		conn.Resources = resources
	}
	if prompts, err := client.ListPrompts(connectCtx); err == nil {
		conn.Prompts = prompts
	}
	return conn, nil
}

// Connect (re)connects one server and installs the connection, replacing —
// and closing — any previous connection under the same name. On failure the
// stale connection (if any) is dropped and the error is recorded so
// ConnectError can report it; the error is also returned. Safe for concurrent
// use; the dial itself runs outside the lock so a slow server doesn't block
// readers.
func (r *Registry) Connect(ctx context.Context, name string, entry ServerEntry, info Implementation, authPrompt OAuthPrompt, childStderr io.Writer) error {
	conn, err := connectOne(ctx, name, entry, info, authPrompt, childStderr)

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		if conn != nil {
			_ = conn.Client.Close()
		}
		return fmt.Errorf("mcp: registry closed")
	}
	old := r.conns[name]
	if err != nil {
		delete(r.conns, name)
		r.errs[name] = err.Error()
	} else {
		r.conns[name] = conn
		delete(r.errs, name)
	}
	r.mu.Unlock()

	if old != nil {
		_ = old.Client.Close()
	}
	return err
}

// Remove closes and forgets the named connection (and any recorded connect
// error). Removing an unknown name is a no-op.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	old := r.conns[name]
	delete(r.conns, name)
	delete(r.errs, name)
	r.mu.Unlock()
	if old != nil {
		_ = old.Client.Close()
	}
}

// ConnectError reports the last connect failure for a server that has no live
// connection. ok is false when the server either connected fine or was never
// attempted.
func (r *Registry) ConnectError(name string) (msg string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	msg, ok = r.errs[name]
	return
}

// Connections returns the live connections in stable order so /mcp output
// is deterministic.
func (r *Registry) Connections() []*Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Connection, 0, len(r.conns))
	for _, name := range sortedKeys(r.conns) {
		out = append(out, r.conns[name])
	}
	return out
}

// Get returns the named connection, or nil if no such server is live.
func (r *Registry) Get(name string) *Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conns[name]
}

// Len reports how many servers connected successfully — used to decide
// whether to register the MCP tool surface at all.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns)
}

// Close tears every connection down. Idempotent. Errors per server are
// dropped — Close is end-of-life and the user can't do anything with the
// errors anyway.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for _, c := range r.conns {
		_ = c.Client.Close()
	}
}

func sortedKeys(m map[string]*Connection) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
