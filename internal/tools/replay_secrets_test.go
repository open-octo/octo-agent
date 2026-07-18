package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/browser"
)

// stubSecretAsker is an Asker that also implements SecretAsker (the TUI/Web
// shape). It records how often a secret was requested and what question was
// shown; the answer (or a cancellation) is queued by the test.
type stubSecretAsker struct {
	stubAsker
	secretCalls int
	lastSecretQ string
	answer      string
	cancelled   bool
}

func (s *stubSecretAsker) AskSecret(_ context.Context, question string) (string, bool, error) {
	s.secretCalls++
	s.lastSecretQ = question
	return s.answer, s.cancelled, nil
}

// secretRec builds a recording with one secret param (password) and one
// defaultless non-secret param (username), both referenced by a type step.
func secretRec() *browser.Recording {
	return &browser.Recording{
		Name: "login",
		Params: []browser.Param{
			{Name: "username"},
			{Name: "password", Secret: true, Description: "secret value (not stored; provide at replay)"},
		},
		Steps: []browser.Step{
			{Action: "type", Selector: "#u", Value: "{{username}}"},
			{Action: "type", Selector: "#p", Value: "{{password}}"},
		},
	}
}

// passwordOnlyRec drops the non-secret dimension for tests that focus on the
// secret resolution ladder.
func passwordOnlyRec() *browser.Recording {
	return &browser.Recording{
		Name:   "login",
		Params: []browser.Param{{Name: "password", Secret: true}},
		Steps:  []browser.Step{{Action: "type", Selector: "#p", Value: "{{password}}"}},
	}
}

func resetReplaySecrets(t *testing.T, sids ...string) {
	t.Helper()
	for _, sid := range sids {
		CloseSessionReplaySecrets(sid)
	}
	t.Cleanup(func() {
		for _, sid := range sids {
			CloseSessionReplaySecrets(sid)
		}
	})
}

// Non-secret missing params keep the status-quo error even when secret params
// are also missing — the model owns the non-secret decision.
func TestResolveReplayParams_NonSecretMissingKeepsPlainError(t *testing.T) {
	stub := &stubSecretAsker{answer: "hunter2"}
	ctx := WithAsker(context.Background(), stub)
	params := map[string]string{}

	err := resolveReplayParams(ctx, secretRec(), "login", params)
	if err == nil || !strings.Contains(err.Error(), "missing required param") {
		t.Fatalf("err = %v, want a missing-required-param error", err)
	}
	if !strings.Contains(err.Error(), "username") {
		t.Errorf("err = %v, want it to name the non-secret param", err)
	}
	if stub.secretCalls != 0 {
		t.Errorf("secret asker must not fire while a non-secret param is missing")
	}
}

// Secret missing → resolved from OCTO_BROWSER_SECRET_<NAME> without touching
// the asker; the resolved value lands in params (in memory only).
func TestResolveReplayParams_SecretFromEnv(t *testing.T) {
	t.Setenv("OCTO_BROWSER_SECRET_PASSWORD", "s3cret")
	resetReplaySecrets(t, "")
	stub := &stubSecretAsker{}
	ctx := WithSessionID(WithAsker(context.Background(), stub), "")

	params := map[string]string{}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params); err != nil {
		t.Fatalf("resolveReplayParams: %v", err)
	}
	if params["password"] != "s3cret" {
		t.Fatalf("password = %q, want env value", params["password"])
	}
	if stub.secretCalls != 0 {
		t.Error("env hit must not prompt")
	}
}

// Secret missing, no env → masked ask; the question names recording + param,
// the value lands in params, and a second resolve in the same session is
// served from the session cache without re-asking.
func TestResolveReplayParams_SecretFromAskerThenCache(t *testing.T) {
	resetReplaySecrets(t, "s1")
	stub := &stubSecretAsker{answer: "hunter2"}
	ctx := WithSessionID(WithAsker(context.Background(), stub), "s1")

	params := map[string]string{}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if params["password"] != "hunter2" {
		t.Fatalf("password = %q, want asked value", params["password"])
	}
	if stub.secretCalls != 1 {
		t.Fatalf("secretCalls = %d, want 1", stub.secretCalls)
	}
	if !strings.Contains(stub.lastSecretQ, "login") || !strings.Contains(stub.lastSecretQ, "password") {
		t.Errorf("question %q should name the recording and the param", stub.lastSecretQ)
	}

	// Second replay, same session: cache serves it, no re-ask.
	params2 := map[string]string{}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params2); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if stub.secretCalls != 1 {
		t.Errorf("session cache should serve the second resolve, secretCalls = %d", stub.secretCalls)
	}
	if params2["password"] != "hunter2" {
		t.Errorf("cached password = %q", params2["password"])
	}
}

// Priority: an explicitly passed param is never re-resolved (and never asked).
func TestResolveReplayParams_ExplicitParamWins(t *testing.T) {
	t.Setenv("OCTO_BROWSER_SECRET_PASSWORD", "from-env")
	resetReplaySecrets(t, "s1")
	stub := &stubSecretAsker{answer: "from-asker"}
	ctx := WithSessionID(WithAsker(context.Background(), stub), "s1")

	params := map[string]string{"password": "explicit"}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params); err != nil {
		t.Fatalf("resolveReplayParams: %v", err)
	}
	if params["password"] != "explicit" {
		t.Fatalf("explicit param overwritten: %q", params["password"])
	}
	if stub.secretCalls != 0 {
		t.Error("explicit param must not trigger resolution")
	}
}

// Priority within the ladder: session cache beats env (an env change
// mid-session doesn't reshuffle a value the user already typed), env beats
// the asker.
func TestResolveReplayParams_CacheBeatsEnvBeatsAsker(t *testing.T) {
	t.Setenv("OCTO_BROWSER_SECRET_PASSWORD", "env-v1")
	resetReplaySecrets(t, "s1")
	stub := &stubSecretAsker{answer: "asked"}
	ctx := WithSessionID(WithAsker(context.Background(), stub), "s1")

	// First resolve: env hit populates the cache.
	params := map[string]string{}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if params["password"] != "env-v1" || stub.secretCalls != 0 {
		t.Fatalf("want env resolution without prompt, got %q (calls=%d)", params["password"], stub.secretCalls)
	}

	// Env changes: the cached v1 still wins within the session.
	t.Setenv("OCTO_BROWSER_SECRET_PASSWORD", "env-v2")
	params2 := map[string]string{}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params2); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if params2["password"] != "env-v1" {
		t.Fatalf("session cache should beat the changed env, got %q", params2["password"])
	}

	// Closing the session drops the cache: the new env value surfaces.
	CloseSessionReplaySecrets("s1")
	params3 := map[string]string{}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params3); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if params3["password"] != "env-v2" {
		t.Fatalf("after CloseSessionReplaySecrets want fresh env value, got %q", params3["password"])
	}
}

// A cancellation aborts the whole replay with a clean error naming the param
// — never the value.
func TestResolveReplayParams_AskerCancelled(t *testing.T) {
	resetReplaySecrets(t, "s1")
	stub := &stubSecretAsker{cancelled: true}
	ctx := WithSessionID(WithAsker(context.Background(), stub), "s1")

	params := map[string]string{}
	err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want a cancellation error", err)
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("err = %v, want it to name the param", err)
	}
	if _, ok := params["password"]; ok {
		t.Error("no value may be injected on cancellation")
	}
}

// No SecretAsker (IM / headless — here a plain Asker, which only implements
// Ask): the error points at serve.env and the Web UI / TUI, names the env var,
// and never leaks a value.
func TestResolveReplayParams_NoSecretAskerPointsTheWay(t *testing.T) {
	resetReplaySecrets(t, "im:1")
	plain := &stubAsker{} // IM shape: Asker without SecretAsker
	ctx := WithSessionID(WithAsker(context.Background(), plain), "im:1")

	params := map[string]string{}
	err := resolveReplayParams(ctx, passwordOnlyRec(), "login", params)
	if err == nil {
		t.Fatal("want an error when secrets can't be collected")
	}
	msg := err.Error()
	for _, want := range []string{"password", "OCTO_BROWSER_SECRET_PASSWORD", "serve.env", "Web UI"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q: %s", want, msg)
		}
	}
}

// Sessions are isolated: a secret cached under one sid is invisible to another.
func TestResolveReplayParams_CacheIsolatedPerSession(t *testing.T) {
	resetReplaySecrets(t, "s1", "s2")
	stub := &stubSecretAsker{answer: "hunter2"}

	ctx1 := WithSessionID(WithAsker(context.Background(), stub), "s1")
	if err := resolveReplayParams(ctx1, passwordOnlyRec(), "login", map[string]string{}); err != nil {
		t.Fatalf("s1 resolve: %v", err)
	}
	if stub.secretCalls != 1 {
		t.Fatalf("calls = %d", stub.secretCalls)
	}

	ctx2 := WithSessionID(WithAsker(context.Background(), stub), "s2")
	if err := resolveReplayParams(ctx2, passwordOnlyRec(), "login", map[string]string{}); err != nil {
		t.Fatalf("s2 resolve: %v", err)
	}
	if stub.secretCalls != 2 {
		t.Errorf("a different session must re-ask, calls = %d", stub.secretCalls)
	}
}

// The cache key includes the recording name: two recordings sharing a param
// name don't see each other's asked values (they DO share an env value — same
// name, same env var, by design).
func TestResolveReplayParams_CacheKeyedByRecording(t *testing.T) {
	resetReplaySecrets(t, "s1")
	stub := &stubSecretAsker{answer: "hunter2"}
	ctx := WithSessionID(WithAsker(context.Background(), stub), "s1")

	other := &browser.Recording{
		Name:   "other-login",
		Params: []browser.Param{{Name: "password", Secret: true}},
		Steps:  []browser.Step{{Action: "type", Selector: "#p", Value: "{{password}}"}},
	}
	if err := resolveReplayParams(ctx, passwordOnlyRec(), "login", map[string]string{}); err != nil {
		t.Fatalf("resolve login: %v", err)
	}
	if err := resolveReplayParams(ctx, other, "other-login", map[string]string{}); err != nil {
		t.Fatalf("resolve other-login: %v", err)
	}
	if stub.secretCalls != 2 {
		t.Errorf("a different recording must re-ask even for the same param name, calls = %d", stub.secretCalls)
	}
}

func TestSecretEnvName(t *testing.T) {
	cases := map[string]string{
		"password":  "OCTO_BROWSER_SECRET_PASSWORD",
		"api_token": "OCTO_BROWSER_SECRET_API_TOKEN",
		"token2":    "OCTO_BROWSER_SECRET_TOKEN2",
	}
	for in, want := range cases {
		if got := secretEnvName(in); got != want {
			t.Errorf("secretEnvName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionIDContextRoundtrip(t *testing.T) {
	if got := SessionIDFrom(context.Background()); got != "" {
		t.Errorf("SessionIDFrom(empty) = %q, want \"\"", got)
	}
	ctx := WithSessionID(context.Background(), "abc")
	if got := SessionIDFrom(ctx); got != "abc" {
		t.Errorf("SessionIDFrom = %q, want abc", got)
	}
}
