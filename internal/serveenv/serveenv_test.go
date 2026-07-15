package serveenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_NoFileIsSilent(t *testing.T) {
	dir := t.TempDir()
	oldPath := envPath
	envPath = func() string { return filepath.Join(dir, "serve.env") }
	defer func() { envPath = oldPath }()

	// File does NOT exist — Load must be a silent no-op, not error.
	Load()
}

func TestLoad_SetsMissingVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.env")
	content := "# a comment\nTAVILY_API_KEY=tvly-abc123\n\nexport BRAVE_SEARCH_API_KEY=brave-xyz\nSERPER_API_KEY = serp-789\nNO_EQUAL_SIGN\n=emptykey\n  # indented comment\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	oldPath := envPath
	envPath = func() string { return path }
	defer func() { envPath = oldPath }()

	// Clear any pre-existing values so we can observe the set.
	_ = os.Unsetenv("TAVILY_API_KEY")
	_ = os.Unsetenv("BRAVE_SEARCH_API_KEY")
	defer func() {
		_ = os.Unsetenv("TAVILY_API_KEY")
		_ = os.Unsetenv("BRAVE_SEARCH_API_KEY")
		_ = os.Unsetenv("SERPER_API_KEY")
	}()

	Load()

	if got := os.Getenv("TAVILY_API_KEY"); got != "tvly-abc123" {
		t.Errorf("TAVILY_API_KEY = %q, want tvly-abc123", got)
	}
	if got := os.Getenv("BRAVE_SEARCH_API_KEY"); got != "brave-xyz" {
		t.Errorf("BRAVE_SEARCH_API_KEY = %q, want brave-xyz", got)
	}
	// Whitespace-trimmed key, verbatim value.
	if got := os.Getenv("SERPER_API_KEY"); got != "serp-789" {
		t.Errorf("SERPER_API_KEY = %q, want serp-789", got)
	}
	// Line with no "=" is skipped → not set.
	if _, ok := os.LookupEnv("NO_EQUAL_SIGN"); ok {
		t.Error("NO_EQUAL_SIGN should not be set (no '=' in line)")
	}
}

func TestLoad_DoesNotOverrideExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.env")
	if err := os.WriteFile(path, []byte("TAVILY_API_KEY=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldPath := envPath
	envPath = func() string { return path }
	defer func() { envPath = oldPath }()

	// Explicit env must win over the file.
	t.Setenv("TAVILY_API_KEY", "from-cli")

	Load()

	if got := os.Getenv("TAVILY_API_KEY"); got != "from-cli" {
		t.Errorf("explicit env was overridden: got %q, want from-cli", got)
	}
}

func TestLoad_NoHomeDir(t *testing.T) {
	oldPath := envPath
	envPath = func() string { return "" }
	defer func() { envPath = oldPath }()

	// Must not panic or error when home dir is unavailable.
	Load()
}
