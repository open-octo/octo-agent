package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/config"
)

// doctorHome isolates HOME (via the shared isolateHome helper) and additionally
// clears provider API keys so the environment checks are deterministic.
func doctorHome(t *testing.T) string {
	t.Helper()
	home := isolateHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	return home
}

func TestRunDoctor_HealthyWithKey(t *testing.T) {
	doctorHome(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	cfg := config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q err=%q", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "config.yml parses") || !strings.Contains(out, "All checks passed") {
		t.Errorf("healthy output missing expected lines:\n%s", out)
	}
}

func TestRunDoctor_MissingKeyIsProblem(t *testing.T) {
	doctorHome(t)
	cfg := config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when the API key is missing; out=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "OPENAI_API_KEY") {
		t.Errorf("output should name the missing env var:\n%s", stdout.String())
	}
}

func TestRunDoctor_UnparseableConfig(t *testing.T) {
	home := doctorHome(t)
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("models: [oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("exit = 0, want non-zero on an unparseable config")
	}
	out := stdout.String()
	if !strings.Contains(out, "does not parse") || !strings.Contains(out, "octo config --fix") {
		t.Errorf("unparseable output should point at --fix:\n%s", out)
	}
}

func TestRunDoctor_SemanticProblem(t *testing.T) {
	doctorHome(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	// Dangling default_model — parses fine, fails Validate.
	cfg := config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "does-not-exist",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("exit = 0, want non-zero on a semantic problem")
	}
	if !strings.Contains(stdout.String(), "default_model") {
		t.Errorf("output should surface the dangling default_model:\n%s", stdout.String())
	}
}

func TestRunConfigFix_RestoresBrokenConfig(t *testing.T) {
	home := doctorHome(t)
	good := config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	}
	if err := good.Save(); err != nil { // writes config.yml + .bak
		t.Fatal(err)
	}
	// Corrupt it.
	path := filepath.Join(home, ".octo", "config.yml")
	if err := os.WriteFile(path, []byte("models: [oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runConfig([]string{"--fix"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; err=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Restored config.yml") {
		t.Errorf("output should confirm the restore:\n%s", stdout.String())
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("config still broken after --fix: %v", err)
	}
	if got.DefaultModel != "gpt-4o" {
		t.Errorf("restored default_model = %q, want gpt-4o", got.DefaultModel)
	}
}

func TestRunConfigFix_RepairsSemanticProblem(t *testing.T) {
	doctorHome(t)
	cfg := config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gone",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runConfig([]string{"fix"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; err=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Fixed:") {
		t.Errorf("output should report the fix:\n%s", stdout.String())
	}
	got, _ := config.Load()
	if got.DefaultModel != "gpt-4o" {
		t.Errorf("default_model = %q, want reset to gpt-4o", got.DefaultModel)
	}
}

func TestRunConfigFix_HealthyIsNoOp(t *testing.T) {
	doctorHome(t)
	cfg := config.Config{
		Models:       []config.ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runConfig([]string{"fix"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "healthy") {
		t.Errorf("healthy config should report nothing to fix:\n%s", stdout.String())
	}
}

func TestRunConfigFix_ReportsUnfixable(t *testing.T) {
	home := doctorHome(t)
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Duplicate model — parses, but Repair can't guess intent.
	yml := "models:\n  - {provider: openai, model: gpt-4o}\n  - {provider: openai, model: gpt-4o}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runConfig([]string{"fix"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("exit = 0, want non-zero when unfixable problems remain")
	}
	if !strings.Contains(stdout.String(), "manual attention") {
		t.Errorf("output should flag the unfixable duplicate:\n%s", stdout.String())
	}
}
