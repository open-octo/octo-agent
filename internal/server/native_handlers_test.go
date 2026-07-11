package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeNative records what it was asked and returns canned results.
type fakeNative struct {
	gotStartDir       string
	retPath           string
	retCancel         bool
	gotTitle, gotBody string
	notifyCalls       int
	autostart         bool
	channelsPersisted bool
	channelsSaveCalls int
	toggleMaxCalls    int
}

func (f *fakeNative) PickFolder(_ context.Context, startDir string) (string, bool, error) {
	f.gotStartDir = startDir
	return f.retPath, f.retCancel, nil
}
func (f *fakeNative) PickFile(_ context.Context, startDir string) (string, bool, error) {
	f.gotStartDir = startDir
	return f.retPath, f.retCancel, nil
}
func (f *fakeNative) Notify(title, body string) {
	f.notifyCalls++
	f.gotTitle, f.gotBody = title, body
}
func (f *fakeNative) AutostartEnabled() (bool, error) { return f.autostart, nil }
func (f *fakeNative) SetAutostart(enable bool) error  { f.autostart = enable; return nil }
func (f *fakeNative) PersistChannelsEnabled(enabled bool) error {
	f.channelsPersisted = enabled
	f.channelsSaveCalls++
	return nil
}
func (f *fakeNative) ToggleMaximise() { f.toggleMaxCalls++ }

func TestNativePickFolderNotRegisteredWithoutBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodPost, "/api/native/pick-folder", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("without a bridge the route must not exist: got %d, want 404", w.Code)
	}
}

func TestNativePickFolderDelegatesToBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{retPath: "/picked/dir"}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	req := httptest.NewRequest(http.MethodPost, "/api/native/pick-folder", strings.NewReader(`{"start_dir":"/seed"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if fake.gotStartDir != "/seed" {
		t.Errorf("start_dir handed to bridge = %q, want /seed", fake.gotStartDir)
	}
	var resp struct {
		Path      string `json:"path"`
		Cancelled bool   `json:"cancelled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Path != "/picked/dir" || resp.Cancelled {
		t.Errorf("resp = %+v, want {path:/picked/dir cancelled:false}", resp)
	}
}

func TestNativePickFolderRejectsNonLoopback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{retPath: "/x"}})
	req := httptest.NewRequest(http.MethodPost, "/api/native/pick-folder", strings.NewReader(`{}`))
	req.RemoteAddr = "203.0.113.5:1000" // non-loopback
	req.Host = "127.0.0.1:8080"
	// Valid key clears requireAuth (which would otherwise 401 a non-loopback
	// peer), so the request reaches the handler's own same-machine guard.
	req.Header.Set("Authorization", "Bearer "+srv.AccessKey())
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-loopback peer: got %d, want 403", w.Code)
	}
}

func TestNativeNotifyDelegatesToBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	req := httptest.NewRequest(http.MethodPost, "/api/native/notify", strings.NewReader(`{"title":"Done","body":"turn complete"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if fake.notifyCalls != 1 || fake.gotTitle != "Done" || fake.gotBody != "turn complete" {
		t.Errorf("bridge.Notify got calls=%d title=%q body=%q, want 1/Done/turn complete", fake.notifyCalls, fake.gotTitle, fake.gotBody)
	}
}

func TestNativeNotifyNotRegisteredWithoutBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodPost, "/api/native/notify", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("without a bridge the route must not exist: got %d, want 404", w.Code)
	}
}

func TestNativeAutostartRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})

	getEnabled := func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/native/autostart", nil)
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET autostart: got %d, want 200", w.Code)
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return body.Enabled
	}

	if getEnabled() {
		t.Fatalf("initial autostart should be false")
	}
	req := httptest.NewRequest(http.MethodPut, "/api/native/autostart", strings.NewReader(`{"enabled":true}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT autostart: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if !getEnabled() {
		t.Errorf("autostart should be enabled after PUT true")
	}
}

func TestNativeChannelsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})

	getEnabled := func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/native/channels", nil)
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET channels: got %d, want 200", w.Code)
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return body.Enabled
	}
	put := func(enabled bool) {
		body := `{"enabled":false}`
		if enabled {
			body = `{"enabled":true}`
		}
		req := httptest.NewRequest(http.MethodPut, "/api/native/channels", strings.NewReader(body))
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT channels: got %d, want 200 (%s)", w.Code, w.Body.String())
		}
	}

	// Turn channels off, then on, and confirm both the live state and the
	// persisted preference (handed to the bridge) track each PUT.
	put(false)
	if getEnabled() {
		t.Errorf("after PUT false: channels should be off")
	}
	if fake.channelsPersisted {
		t.Errorf("bridge should have persisted enabled=false")
	}
	put(true)
	if !getEnabled() {
		t.Errorf("after PUT true: channels should be on")
	}
	if !fake.channelsPersisted {
		t.Errorf("bridge should have persisted enabled=true")
	}
	if fake.channelsSaveCalls != 2 {
		t.Errorf("bridge persist calls = %d, want 2", fake.channelsSaveCalls)
	}
}

func TestNativeChannelsNotRegisteredWithoutBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/api/native/channels", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("without a bridge the route must not exist: got %d, want 404", w.Code)
	}
}

func TestNativeToggleMaximise(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	req := httptest.NewRequest(http.MethodPost, "/api/native/window/toggle-maximise", nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if fake.toggleMaxCalls != 1 {
		t.Errorf("ToggleMaximise calls = %d, want 1", fake.toggleMaxCalls)
	}
}

func TestVersionNativeFlag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	get := func(srv *Server) bool {
		req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		native, _ := body["native"].(bool)
		return native
	}

	if get(mustServer(t, Config{Addr: "127.0.0.1:0"})) {
		t.Error("serve build: native flag should be false")
	}
	if !get(mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{}})) {
		t.Error("desktop build: native flag should be true")
	}
}
