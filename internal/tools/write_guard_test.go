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
		"ghp_short", // too short to be a real token
	}
	for _, content := range ok {
		if got := scanForSecrets(content); got != "" {
			t.Errorf("ordinary content flagged as %q: %q", got, content)
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
