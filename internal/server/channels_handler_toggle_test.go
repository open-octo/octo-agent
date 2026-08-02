package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/channel"
)

// setupToggleTest registers togglefake (constructs cleanly) and togglebad
// (fails construction, mirroring wecom without credentials), points HOME at a
// temp dir, and returns the server plus the channels.yml path.
func setupToggleTest(t *testing.T) (*Server, string) {
	t.Helper()
	channel.Register("togglefake", func(channel.PlatformConfig) (channel.Adapter, error) {
		return &fullFakeAdapter{}, nil
	})
	channel.Register("togglebad", func(channel.PlatformConfig) (channel.Adapter, error) {
		return nil, errors.New("togglebad: bot_token is required")
	})

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	cfgDir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	t.Cleanup(srv.stopChannels)
	return srv, filepath.Join(cfgDir, "channels.yml")
}

func postChannelSave(t *testing.T, srv *Server, platform, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/channels/"+platform, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Access-Key", srv.AccessKey())
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	return w
}

// TestHandleSaveChannel_DisablePersists is the regression test for the toggle
// that sprang back on: SetPlatform used to force Enabled=true on every save,
// so a toggle-off never reached channels.yml.
func TestHandleSaveChannel_DisablePersists(t *testing.T) {
	srv, cfgPath := setupToggleTest(t)
	if err := os.WriteFile(cfgPath,
		[]byte("channels:\n  togglefake:\n    enabled: true\n    bot_token: real-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := postChannelSave(t, srv, "togglefake", `{"enabled": false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IsEnabled("togglefake") {
		t.Fatal("platform should be disabled after toggle-off save")
	}
	// A flag-only save must not clobber existing credentials.
	inst := cfg.Platform("togglefake")
	if len(inst) == 0 || inst[0].Config["bot_token"] != "real-secret" {
		t.Fatalf("credentials lost on flag-only save: %+v", inst)
	}
}

// TestHandleSaveChannel_EnableWithoutCredentialsRejected: enabling a platform
// whose adapter can't even construct must be refused, not saved — otherwise
// the adapter enters a crash-restart loop and (before the fix) could never be
// switched off again.
func TestHandleSaveChannel_EnableWithoutCredentialsRejected(t *testing.T) {
	srv, _ := setupToggleTest(t)

	w := postChannelSave(t, srv, "togglebad", `{"enabled": true}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400; body=%s", w.Code, w.Body.String())
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Platform("togglebad")) != 0 {
		t.Fatal("rejected enable must not persist a config entry")
	}
}

// TestHandleSaveChannel_CredentialsOnlyAutoEnables preserves the
// "configure ⇒ auto-enable" contract: a fields-only save (no enabled key)
// enables the platform.
func TestHandleSaveChannel_CredentialsOnlyAutoEnables(t *testing.T) {
	srv, _ := setupToggleTest(t)

	w := postChannelSave(t, srv, "togglefake", `{"fields": {"bot_token": "x"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsEnabled("togglefake") {
		t.Fatal("credentials-only save should auto-enable the platform")
	}
}

// TestHandleSaveChannel_ReEnableAfterDisable: a configured channel can be
// switched back on.
func TestHandleSaveChannel_ReEnableAfterDisable(t *testing.T) {
	srv, cfgPath := setupToggleTest(t)
	if err := os.WriteFile(cfgPath,
		[]byte("channels:\n  togglefake:\n    enabled: false\n    bot_token: real-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := postChannelSave(t, srv, "togglefake", `{"enabled": true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	cfg, err := channel.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsEnabled("togglefake") {
		t.Fatal("platform should be enabled after toggle-on save")
	}
}
