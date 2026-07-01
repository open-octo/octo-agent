package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/config"
)

// stubReachable swaps the TCP liveness probe (status path) and records its args.
func stubReachable(t *testing.T, reachable bool) (*int, *time.Duration) {
	t.Helper()
	orig := browserPortReachable
	t.Cleanup(func() { browserPortReachable = orig })
	var gotPort int
	var gotTimeout time.Duration
	browserPortReachable = func(port int, timeout time.Duration) bool {
		gotPort, gotTimeout = port, timeout
		return reachable
	}
	return &gotPort, &gotTimeout
}

// stubAttachProbe swaps the CDP probe (verify path) and records its args.
func stubAttachProbe(t *testing.T, ok bool, detail string) (*int, *time.Duration) {
	t.Helper()
	orig := browserAttachProbe
	t.Cleanup(func() { browserAttachProbe = orig })
	var gotPort int
	var gotTimeout time.Duration
	browserAttachProbe = func(port int, timeout time.Duration) (bool, string) {
		gotPort, gotTimeout = port, timeout
		return ok, detail
	}
	return &gotPort, &gotTimeout
}

// failIfAttachProbed makes the CDP probe fail the test if called — used to prove
// the status poll never opens a real connection (which would pop Chrome's auth).
func failIfAttachProbed(t *testing.T) {
	t.Helper()
	orig := browserAttachProbe
	t.Cleanup(func() { browserAttachProbe = orig })
	browserAttachProbe = func(port int, timeout time.Duration) (bool, string) {
		t.Errorf("status must not open a CDP connection (would pop auth dialog)")
		return false, ""
	}
}

func TestBrowserStatus_NotConfigured(t *testing.T) {
	setTestHome(t)
	stubReachable(t, true) // reachable, but must be ignored when unconfigured
	failIfAttachProbed(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodGet, "/api/browser/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var resp browserStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Configured {
		t.Error("Configured = true, want false")
	}
	if resp.Connected {
		t.Error("Connected = true, want false (must not probe when unconfigured)")
	}
	if resp.Port != browserDefaultDebugPort {
		t.Errorf("Port = %d, want default %d", resp.Port, browserDefaultDebugPort)
	}
}

func TestBrowserStatus_ConfiguredUsesTCPProbe(t *testing.T) {
	setTestHome(t)
	seedModels(t, config.Config{Browser: config.BrowserConfig{ConnectPort: 9333}})
	gotPort, gotTimeout := stubReachable(t, true)
	failIfAttachProbed(t) // status must stay popup-free
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodGet, "/api/browser/status", "")
	var resp browserStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Configured || !resp.Connected {
		t.Fatalf("Configured=%v Connected=%v, want both true", resp.Configured, resp.Connected)
	}
	if resp.Port != 9333 || *gotPort != 9333 {
		t.Errorf("port mismatch: resp=%d probe=%d, want 9333", resp.Port, *gotPort)
	}
	if *gotTimeout != browserStatusProbeTimeout {
		t.Errorf("status timeout = %v, want quick %v", *gotTimeout, browserStatusProbeTimeout)
	}
}

func TestBrowserVerify_SuccessSavesPort(t *testing.T) {
	setTestHome(t)
	gotPort, gotTimeout := stubAttachProbe(t, true, "1 open tab(s)")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/browser/verify", `{"port": 9222}`)
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d: %s", w.Code, w.Body.String())
	}
	var resp browserVerifyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || !resp.Saved {
		t.Fatalf("OK=%v Saved=%v, want both true", resp.OK, resp.Saved)
	}
	if *gotPort != 9222 {
		t.Errorf("probe called with port %d, want 9222", *gotPort)
	}
	// Verify must allow time for the user to approve Chrome's auth prompt.
	if *gotTimeout != browserVerifyProbeTimeout {
		t.Errorf("verify timeout = %v, want long %v", *gotTimeout, browserVerifyProbeTimeout)
	}
	cfg, _ := config.Load()
	if cfg.Browser.ConnectPort != 9222 {
		t.Errorf("connect_port = %d, want 9222 saved", cfg.Browser.ConnectPort)
	}
}

func TestBrowserVerify_FailureDoesNotSave(t *testing.T) {
	setTestHome(t)
	stubAttachProbe(t, false, "connection refused")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/browser/verify", `{"port": 9222}`)
	var resp browserVerifyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Saved {
		t.Fatalf("OK=%v Saved=%v, want both false", resp.OK, resp.Saved)
	}
	cfg, _ := config.Load()
	if cfg.Browser.ConnectPort != 0 {
		t.Errorf("connect_port = %d, want 0 (not saved on failure)", cfg.Browser.ConnectPort)
	}
}

func TestBrowserVerify_EmptyBodyUsesDefaultPort(t *testing.T) {
	setTestHome(t)
	gotPort, _ := stubAttachProbe(t, false, "refused")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/browser/verify", "")
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d: %s", w.Code, w.Body.String())
	}
	if *gotPort != browserDefaultDebugPort {
		t.Errorf("probe called with port %d, want default %d", *gotPort, browserDefaultDebugPort)
	}
}
