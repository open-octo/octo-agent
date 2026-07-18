package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/browser"
)

// Replay-time secret params (recording YAML `secret: true`) are collected by
// the runtime — never by the model — so the value stays out of the
// conversation (transcript, memory backend, provider context). This file
// holds the three pieces of that machinery:
//
//   - SecretAsker: the optional masked-input capability a transport may
//     implement (TUI password mode, Web password field). IM and headless
//     don't, and the type assertion's failure IS their error path.
//   - WithSessionID / SessionIDFrom: the ctx primitive that scopes the
//     session cache below.
//   - resolveReplayParams: the collection point both replay paths (the
//     browser tool's replay action and the workflow recording() primitive)
//     funnel through before calling ReplayRecording.
//
// Resolution order for a missing secret param: session cache →
// OCTO_BROWSER_SECRET_<NAME> env (process env or ~/.octo/serve.env, which the
// existing serveenv loader injects) → masked ask. Explicit caller params
// always win by virtue of never being "missing". Non-secret missing params
// keep the status-quo plain error (the model decides whether to ask or fill).

// SecretAsker is an optional Asker capability: collect one secret value with
// no echo. Transports that can mask input implement it (TUI password mode,
// Web password field); those that can't (IM's channelAsker — platform message
// history would persist the value — and headless) leave it unimplemented, and
// the failed type assertion at the collection point degrades to an error
// pointing at serve.env and the Web UI / TUI.
//
// The question text and the fact a secret was provided/cancelled may enter
// the conversation; the answer value must NOT — it flows straight into the
// replay params in memory.
type SecretAsker interface {
	AskSecret(ctx context.Context, question string) (answer string, cancelled bool, err error)
}

// ctxKeySessionID carries the turn's session ID for tools-layer machinery
// that keeps per-session state but doesn't own session lifecycle (the replay
// secret cache). Server turn entries stamp it alongside their internal
// ctxKeySessionID; the CLI stamps it in runTurn. Unstamped ("" ) means
// process-level — correct for the CLI, whose process IS the session boundary.
type ctxKeySessionID struct{}

// WithSessionID stamps the turn's session ID.
func WithSessionID(ctx context.Context, sid string) context.Context {
	return context.WithValue(ctx, ctxKeySessionID{}, sid)
}

// SessionIDFrom resolves the stamped session ID, "" when none.
func SessionIDFrom(ctx context.Context) string {
	sid, _ := ctx.Value(ctxKeySessionID{}).(string)
	return sid
}

// replaySecrets is the in-memory, per-session cache of collected secret
// values: sid → "recording:param" → value. It is a memoization of env hits
// and asked values for the session's lifetime only — never persisted, never
// sent to a provider, and reaped by CloseSessionReplaySecrets when the
// session is deleted. Process memory is outside the threat model (the threat
// model is persistence and outbound channels), so a plain map suffices.
var replaySecrets = struct {
	sync.Mutex
	bySession map[string]map[string]string
}{bySession: map[string]map[string]string{}}

func replaySecretCached(sid, key string) (string, bool) {
	replaySecrets.Lock()
	defer replaySecrets.Unlock()
	v, ok := replaySecrets.bySession[sid][key]
	return v, ok
}

func replaySecretStore(sid, key, value string) {
	replaySecrets.Lock()
	defer replaySecrets.Unlock()
	m := replaySecrets.bySession[sid]
	if m == nil {
		m = map[string]string{}
		replaySecrets.bySession[sid] = m
	}
	m[key] = value
}

// CloseSessionReplaySecrets evaporates every secret cached for sid. Wired
// alongside CloseSessionSubAgentManager / CloseSessionReadTracker at the
// server's session-delete handlers.
//
// Scoping note: sub-agent runs build their ctx from context.Background()
// (internal/tools/subagent_manager.go), so a replay inside a sub-agent lands
// in the "" process-level bucket. That's currently safe — the "" bucket can
// only hold env-resolved values, and env is process-shared by definition; a
// TYPED secret can't get there because the askers that work (wsAsker, TUI)
// require a session-stamped ctx the sub-agent also lacks. If sub-agents ever
// gain a working SecretAsker, stamp their owner's sid too.
func CloseSessionReplaySecrets(sid string) {
	replaySecrets.Lock()
	defer replaySecrets.Unlock()
	delete(replaySecrets.bySession, sid)
}

// secretEnvName maps a param name to its env var: OCTO_BROWSER_SECRET_ +
// uppercased name with non-alphanumerics folded to _. Param names come from
// slugParam (lowercase alnum + _), so the mapping is stable: password →
// OCTO_BROWSER_SECRET_PASSWORD, api_token → OCTO_BROWSER_SECRET_API_TOKEN.
// Same-named params across recordings share one env value (documented).
func secretEnvName(param string) string {
	var b strings.Builder
	b.WriteString("OCTO_BROWSER_SECRET_")
	for _, r := range strings.ToUpper(param) {
		switch {
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// resolveReplayParams fills the recording's missing required params before
// replay, mutating params in place. Missing NON-secret params keep the
// status-quo error — the model decides whether to ask the user or supply
// values; that semantic (auto-prompt was deliberately removed) is untouched.
// Missing SECRET params are resolved by the runtime: session cache, then env,
// then a masked SecretAsker prompt, each success memoized into the session
// cache. Any error text names the recording and the param — never a value.
func resolveReplayParams(ctx context.Context, rec *browser.Recording, name string, params map[string]string) error {
	missing := browser.MissingRequiredParams(rec, params)
	if len(missing) == 0 {
		return nil
	}
	secret := map[string]bool{}
	for _, p := range rec.Params {
		if p.Secret {
			secret[p.Name] = true
		}
	}
	var plain, secrets []string
	for _, m := range missing {
		if secret[m] {
			secrets = append(secrets, m)
		} else {
			plain = append(plain, m)
		}
	}
	// Non-secret missing params fail first, exactly as before — the model
	// owns that branch (re-invoke with params filled, or ask the user itself).
	if len(plain) > 0 {
		return fmt.Errorf("browser: replay %q is missing required param(s): %s — pass them in `params`",
			name, strings.Join(plain, ", "))
	}

	sid := SessionIDFrom(ctx)
	for _, p := range secrets {
		key := name + ":" + p
		if v, ok := replaySecretCached(sid, key); ok {
			params[p] = v
			continue
		}
		if v := os.Getenv(secretEnvName(p)); v != "" {
			replaySecretStore(sid, key, v)
			params[p] = v
			continue
		}
		asker, ok := askerFrom(ctx).(SecretAsker)
		if !ok {
			// IM / headless: no masked input exists. Collecting a secret as a
			// plain chat message would persist it in platform history (WeChat
			// can't delete user messages) — manufacturing the very leak this
			// design closes. Point at the two safe paths instead.
			return fmt.Errorf("browser: replay %q requires secret param(s): %s — secrets can't be collected in this chat (messages persist). Set %s in ~/.octo/serve.env, or replay from the Web UI / TUI",
				name, strings.Join(secrets, ", "), secretEnvName(p))
		}
		v, cancelled, err := asker.AskSecret(ctx, fmt.Sprintf("Enter secret for recording %q: %s", name, p))
		if err != nil {
			return fmt.Errorf("browser: replay %q: ask secret param %q: %w", name, p, err)
		}
		if cancelled {
			return fmt.Errorf("browser: replay %q: secret param %q not provided (cancelled)", name, p)
		}
		replaySecretStore(sid, key, v)
		params[p] = v
	}
	return nil
}
