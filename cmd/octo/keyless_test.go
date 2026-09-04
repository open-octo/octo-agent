package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

// A keyless Custom entry (local Ollama/vLLM) resolves to an empty key with no
// error and no stderr nagging — it must not trigger the setup wizard. A named
// vendor without a key keeps the errMissingAPIKey path.
func TestResolveAPIKey_CustomKeyOptional(t *testing.T) {
	t.Setenv("CUSTOM_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	var stderr bytes.Buffer
	key, err := resolveAPIKey("custom", config.ModelEntry{Provider: "custom", Protocol: "openai", BaseURL: "http://localhost:11434/v1"}, &stderr)
	if err != nil || key != "" {
		t.Fatalf("resolveAPIKey(custom, no key) = %q, %v; want \"\", nil", key, err)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr for a keyless custom entry:\n%s", stderr.String())
	}

	stderr.Reset()
	_, err = resolveAPIKey("openai", config.ModelEntry{Provider: "openai"}, &stderr)
	if !errors.Is(err, errMissingAPIKey) {
		t.Fatalf("resolveAPIKey(openai, no key) = %v; want errMissingAPIKey", err)
	}
	if !strings.Contains(stderr.String(), "OPENAI_API_KEY") {
		t.Errorf("stderr should name the env var:\n%s", stderr.String())
	}
}

// `octo doctor` must not count a keyless Custom endpoint as a problem.
func TestRunDoctor_CustomEndpointNeedsNoKey(t *testing.T) {
	doctorHome(t)
	t.Setenv("CUSTOM_API_KEY", "")
	cfg := config.Config{
		Endpoints: []config.Endpoint{{
			ID: "ollama", Provider: "custom", Protocol: "openai", BaseURL: "http://localhost:11434/v1",
			Models: []config.EndpointModel{{Model: "qwen3-coder:30b"}},
		}},
		Default: "ollama::qwen3-coder:30b",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a keyless custom endpoint; out=%q err=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "optional for a custom endpoint") {
		t.Errorf("output should say the key is optional:\n%s", stdout.String())
	}
}
