package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

// writeTestConfig writes a config with the given models to ~/.octo/config.yml
// in a temp HOME dir.
func writeTestConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir .octo: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// TestEnsureSender_ConfigLoadFailure verifies that when config.Load() fails
// (e.g. corrupt YAML), ensureSender returns an error and leaves cfg untouched.
func TestEnsureSender_ConfigLoadFailure(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir .octo: %v", err)
	}
	// Write invalid YAML to force config.Load() to fail.
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(": not valid yaml: ["), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stub := &stubSender{reply: "ok"}
	a := agent.New(stub, "gpt-4o")
	origEntry := config.ModelEntry{Model: "gpt-4o", Provider: "openai"}
	cfg := &replConfig{
		a:            a,
		providerName: "openai",
		configEntry:  origEntry,
		stderr:       io.Discard,
	}

	if err := cfg.ensureSender("kimi-for-coding", senderTuning{}); err == nil {
		t.Fatal("expected error when config.Load() fails, got nil")
	}
	if cfg.providerName != "openai" {
		t.Errorf("providerName = %q, want openai (unchanged)", cfg.providerName)
	}
	if cfg.configEntry != origEntry {
		t.Errorf("configEntry = %+v, want unchanged %+v", cfg.configEntry, origEntry)
	}
	if cfg.a.Sender != stub {
		t.Error("sender should be unchanged after config.Load() failure")
	}
}

// TestEnsureSender_RebuildsOnProviderChange verifies that switching to a model
// on a different provider rebuilds the sender — the bug that produced
// "openai: HTTP 500" when /model kimi-for-coding was issued against an
// OpenAI-bound session.
func TestEnsureSender_RebuildsOnProviderChange(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	writeTestConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "kimi-for-coding", Provider: "anthropic", BaseURL: "https://api.moonshot.cn/anthropic"},
		},
		DefaultModel: "gpt-4o",
	})

	stub := &stubSender{reply: "ok"}
	a := agent.New(stub, "gpt-4o")
	cfg := &replConfig{
		a:            a,
		providerName: "openai",
		configEntry:  config.ModelEntry{Model: "gpt-4o", Provider: "openai"},
		stderr:       io.Discard,
	}

	if err := cfg.ensureSender("kimi-for-coding", senderTuning{}); err != nil {
		t.Fatalf("ensureSender: %v", err)
	}
	if cfg.providerName != "anthropic" {
		t.Errorf("providerName = %q, want anthropic", cfg.providerName)
	}
	if cfg.configEntry.BaseURL != "https://api.moonshot.cn/anthropic" {
		t.Errorf("configEntry.BaseURL = %q, want https://api.moonshot.cn/anthropic", cfg.configEntry.BaseURL)
	}
	if cfg.a.Sender == nil {
		t.Fatal("sender should be rebuilt, got nil")
	}
	if _, ok := cfg.a.Sender.(*stubSender); ok {
		t.Error("sender should be a real provider sender, not the original stub")
	}
}

// TestEnsureSender_NoRebuildWhenUnchanged verifies that switching between
// models on the same provider + base URL does NOT rebuild the sender (keeps
// the prompt cache key warm).
func TestEnsureSender_NoRebuildWhenUnchanged(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	writeTestConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "gpt-4o-mini", Provider: "openai"},
		},
		DefaultModel: "gpt-4o",
	})

	stub := &stubSender{reply: "ok"}
	a := agent.New(stub, "gpt-4o")
	cfg := &replConfig{
		a:            a,
		providerName: "openai",
		configEntry:  config.ModelEntry{Model: "gpt-4o", Provider: "openai"},
		stderr:       io.Discard,
	}

	if err := cfg.ensureSender("gpt-4o-mini", senderTuning{}); err != nil {
		t.Fatalf("ensureSender: %v", err)
	}
	// Sender must be the same stub — no rebuild happened.
	if cfg.a.Sender != stub {
		t.Error("sender should NOT be rebuilt when provider and base URL match")
	}
}

// TestEnsureSender_RebuildsOnBaseURLChange verifies that switching to a model
// on the same provider but a different base URL rebuilds the sender.
func TestEnsureSender_RebuildsOnBaseURLChange(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")

	writeTestConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "claude-sonnet-4-6", Provider: "anthropic"},
			{Model: "kimi-for-coding", Provider: "anthropic", BaseURL: "https://api.moonshot.cn/anthropic"},
		},
		DefaultModel: "claude-sonnet-4-6",
	})

	stub := &stubSender{reply: "ok"}
	a := agent.New(stub, "claude-sonnet-4-6")
	cfg := &replConfig{
		a:            a,
		providerName: "anthropic",
		configEntry:  config.ModelEntry{Model: "claude-sonnet-4-6", Provider: "anthropic"},
		stderr:       io.Discard,
	}

	if err := cfg.ensureSender("kimi-for-coding", senderTuning{}); err != nil {
		t.Fatalf("ensureSender: %v", err)
	}
	if cfg.configEntry.BaseURL != "https://api.moonshot.cn/anthropic" {
		t.Errorf("configEntry.BaseURL = %q, want https://api.moonshot.cn/anthropic", cfg.configEntry.BaseURL)
	}
	if cfg.a.Sender == stub {
		t.Error("sender should be rebuilt when base URL changes")
	}
}

// TestEnsureSender_ErrorLeavesConfigUnchanged verifies that a rebuild failure
// (e.g. missing API key for the target provider) aborts the switch cleanly:
// providerName, configEntry, and sender all stay as they were.
func TestEnsureSender_ErrorLeavesConfigUnchanged(t *testing.T) {
	// No ANTHROPIC_API_KEY set → buildSender for anthropic will fail.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	writeTestConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "kimi-for-coding", Provider: "anthropic", BaseURL: "https://api.moonshot.cn/anthropic"},
		},
		DefaultModel: "gpt-4o",
	})

	stub := &stubSender{reply: "ok"}
	a := agent.New(stub, "gpt-4o")
	origEntry := config.ModelEntry{Model: "gpt-4o", Provider: "openai"}
	cfg := &replConfig{
		a:            a,
		providerName: "openai",
		configEntry:  origEntry,
		stderr:       io.Discard,
	}

	if err := cfg.ensureSender("kimi-for-coding", senderTuning{}); err == nil {
		t.Fatal("expected error for missing anthropic API key, got nil")
	}
	if cfg.providerName != "openai" {
		t.Errorf("providerName = %q, want openai (unchanged)", cfg.providerName)
	}
	if cfg.configEntry != origEntry {
		t.Errorf("configEntry = %+v, want unchanged %+v", cfg.configEntry, origEntry)
	}
	if cfg.a.Sender != stub {
		t.Error("sender should be unchanged after failed rebuild")
	}
}

// TestEnsureSender_UnconfiguredModel verifies that switching to a model not
// present in the config returns a clear error (listing what is configured)
// and leaves cfg untouched — the case that used to fall through to the current
// provider and hit the wrong endpoint (e.g. longcat base URL + deepseek model
// name → HTTP 500).
func TestEnsureSender_UnconfiguredModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	writeTestConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
		},
		DefaultModel: "gpt-4o",
	})

	stub := &stubSender{reply: "ok"}
	a := agent.New(stub, "gpt-4o")
	origEntry := config.ModelEntry{Model: "gpt-4o", Provider: "openai"}
	cfg := &replConfig{
		a:            a,
		providerName: "openai",
		configEntry:  origEntry,
		stderr:       io.Discard,
	}

	if err := cfg.ensureSender("deepseek-v4-pro", senderTuning{}); err == nil {
		t.Fatal("expected error for unconfigured model, got nil")
	}
	if cfg.providerName != "openai" {
		t.Errorf("providerName = %q, want openai (unchanged)", cfg.providerName)
	}
	if cfg.configEntry != origEntry {
		t.Errorf("configEntry = %+v, want unchanged %+v", cfg.configEntry, origEntry)
	}
	if cfg.a.Sender != stub {
		t.Error("sender should be unchanged after rejected switch")
	}
}
