package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanForSecrets_DetectsHighConfidenceShapes(t *testing.T) {
	cases := map[string]string{
		"private key": "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk...\n-----END OPENSSH PRIVATE KEY-----",
		"rsa key":     "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAK...\n-----END RSA PRIVATE KEY-----",
		"gh token":    "GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"aws secret":  `aws_secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`,
		"slack":       "SLACK_TOKEN=xoxb-1234567890-abcdefghij",
	}
	for label, content := range cases {
		if got := scanForSecrets(content); got == "" {
			t.Errorf("%s: expected a secret to be detected", label)
		}
	}
}

func TestScanForSecrets_AllowsOrdinaryContent(t *testing.T) {
	ok := []string{
		"package main\n\nfunc main() {}\n",
		"# README\n\nThis project does things.\n",
		"AKIAIOSFODNN7EXAMPLE",                                       // AWS docs example access-key ID — not secret, must not trip
		"the aws_secret_access_key is configured in the environment", // prose, no value
		"ghp_short",           // too short to be a real token
		"API_KEY=placeholder", // generic key label with too-short value
	}
	for _, content := range ok {
		if got := scanForSecrets(content); got != "" {
			t.Errorf("ordinary content flagged as %q: %q", got, content)
		}
	}
}

func TestScanForSecrets_DetectsAdditionalKeys(t *testing.T) {
	cases := map[string]string{
		"openai":    "OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH",
		"anthropic": "ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789AB",
		"google":    "GOOGLE_API_KEY=AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abc",
		"jwt":       "token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjMifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		"generic":   "api_key=abcdefghijklmnopqrstuvwxyz0123456789",
	}
	for label, content := range cases {
		if got := scanForSecrets(content); got == "" {
			t.Errorf("%s: expected a secret to be detected", label)
		}
	}
}

func TestWriteFile_RefusesSecretContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "key.pem")
	_, err := WriteFileTool{}.Execute(context.Background(), "write_file", map[string]any{
		"path":    p,
		"content": "-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n-----END RSA PRIVATE KEY-----\n",
	})
	if err == nil || !strings.Contains(err.Error(), "private key") {
		t.Errorf("write_file should refuse private-key content, got %v", err)
	}
	// File must NOT have been created.
	if _, statErr := os.Stat(p); statErr == nil {
		t.Errorf("file should not exist after a refused write")
	}
}

func TestWriteFile_AllowsNormalContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ok.go")
	_, err := WriteFileTool{}.Execute(context.Background(), "write_file", map[string]any{
		"path": p, "content": "package x\n",
	})
	if err != nil {
		t.Errorf("normal write should succeed: %v", err)
	}
}

func TestNewSecretsIntroduced(t *testing.T) {
	const key = "sk-ant-aaaabbbbccccddddeeeeffffgggghhhh"
	oldCfg := "provider: anthropic\napi_key: " + key + "\n"

	// Preserving an existing key while adding an unrelated field is allowed.
	if got := newSecretsIntroduced(oldCfg, oldCfg+"permission_mode: auto\n"); got != "" {
		t.Errorf("preserving an existing key should be allowed, got %q", got)
	}
	// A key in a brand-new file (no prior content) is refused.
	if got := newSecretsIntroduced("", oldCfg); got == "" {
		t.Errorf("a key in new content with no prior should be refused")
	}
	// Changing the key's value counts as introducing a new secret.
	changed := "provider: anthropic\napi_key: sk-ant-zzzzyyyyxxxxwwwwvvvvuuuuttttssss\n"
	if got := newSecretsIntroduced(oldCfg, changed); got == "" {
		t.Errorf("changing the key value should be refused")
	}
	// Keeping the existing key A while ALSO adding a second key B (same pattern,
	// different value) must still refuse — B is newly introduced.
	addB := oldCfg + "backup_key: sk-ant-zzzzyyyyxxxxwwwwvvvvuuuuttttssss\n"
	if got := newSecretsIntroduced(oldCfg, addB); got == "" {
		t.Errorf("adding a second, different key alongside the preserved one should be refused")
	}
}

// A config.yml rewrite that keeps the existing api_key must not trip the guard
// — this is the onboard flow that previously always failed.
func TestWriteFile_PreservesExistingSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	orig := "provider: anthropic\nmodel: claude-sonnet-4-5\napi_key: sk-ant-aaaabbbbccccddddeeeeffffgggghhhh\n"
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := WriteFileTool{}.Execute(context.Background(), "write_file", map[string]any{
		"path":    p,
		"content": orig + "permission_mode: auto\nshow_reasoning: true\n",
	})
	if err != nil {
		t.Errorf("rewrite preserving the existing api_key should succeed, got %v", err)
	}
}

// The same preservation must hold via edit_file: appending a field next to an
// untouched api_key line does not introduce a new secret.
func TestEditFile_PreservesExistingSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	orig := "provider: anthropic\nmodel: claude-sonnet-4-5\napi_key: sk-ant-aaaabbbbccccddddeeeeffffgggghhhh\n"
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       p,
		"old_string": "model: claude-sonnet-4-5",
		"new_string": "model: claude-sonnet-4-5\npermission_mode: auto",
	})
	if err != nil {
		t.Errorf("edit preserving the existing api_key should succeed, got %v", err)
	}
}

// edit_file must still refuse an edit that injects a brand-new credential.
func TestEditFile_RefusesNewSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	orig := "provider: anthropic\nmodel: claude-sonnet-4-5\n"
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       p,
		"old_string": "model: claude-sonnet-4-5",
		"new_string": "model: claude-sonnet-4-5\napi_key: sk-ant-zzzzyyyyxxxxwwwwvvvvuuuuttttssss",
	})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("edit injecting a new key should be refused, got %v", err)
	}
}
