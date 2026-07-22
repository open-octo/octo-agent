package tunnel

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateIdentity_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.json")

	created, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(created.TunnelID()) != 32 { // 16 bytes hex
		t.Errorf("tunnel id = %q, want 32 hex chars", created.TunnelID())
	}
	if created.PublicKeyBase64() == "" {
		t.Error("public key is empty")
	}

	// The identity file must not be world-readable — it holds a private key.
	// Windows does not honor Unix mode bits (os.WriteFile's 0o600 surfaces as
	// 0666 there), so this invariant is only meaningful on Unix.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("identity file perm = %o, want 600", perm)
		}
	}

	// Reloading returns the same identity, not a new one.
	reloaded, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TunnelID() != created.TunnelID() {
		t.Errorf("tunnel id changed on reload: %q -> %q", created.TunnelID(), reloaded.TunnelID())
	}
	if reloaded.PublicKeyBase64() != created.PublicKeyBase64() {
		t.Error("public key changed on reload")
	}
	if !bytes.Equal(reloaded.static.Private, created.static.Private) {
		t.Error("private key changed on reload")
	}
}

// TestLoadOrCreateIdentity_CreatesParentDir covers a fresh machine where
// ~/.octo does not exist yet (e.g. access key came from the environment, so
// server startup never created it). Creating the identity must make the dir.
func TestLoadOrCreateIdentity_CreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist-yet", "tunnel.json")

	id, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatalf("create in missing dir: %v", err)
	}
	if id.TunnelID() == "" {
		t.Error("tunnel id is empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("identity file was not written: %v", err)
	}
}

func TestDecodeIdentity_Rejects(t *testing.T) {
	if _, err := decodeIdentity([]byte("{not json")); err == nil {
		t.Error("malformed JSON should error")
	}
	if _, err := decodeIdentity([]byte(`{"tunnel_id":"","private_key":"","public_key":""}`)); err == nil {
		t.Error("empty identity should error")
	}
	if _, err := decodeIdentity([]byte(`{"tunnel_id":"abc","private_key":"!!notbase64","public_key":"AA=="}`)); err == nil {
		t.Error("invalid base64 private key should error")
	}
}
