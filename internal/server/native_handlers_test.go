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
