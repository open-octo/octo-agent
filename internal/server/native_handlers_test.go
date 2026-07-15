package server

import (
	"context"
	"encoding/base64"
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
	toggleMaxCalls    int
	gotOpenURL        string
	openCalls         int
	gotSaveName       string
	gotSaveContent    string
	saveCalls         int
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
func (f *fakeNative) ToggleMaximise()                 { f.toggleMaxCalls++ }
func (f *fakeNative) Minimise()                        { f.toggleMaxCalls++ }
func (f *fakeNative) Close()                           { f.toggleMaxCalls++ }
func (f *fakeNative) OpenExternal(url string) error {
	f.openCalls++
	f.gotOpenURL = url
	return nil
}
func (f *fakeNative) SaveFile(_ context.Context, defaultName, content string) (string, bool, error) {
	f.saveCalls++
	f.gotSaveName, f.gotSaveContent = defaultName, content
	return f.retPath, f.retCancel, nil
}

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

func TestNativeOpenExternalDelegatesToBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	const link = "https://github.com/open-octo/octo-agent/releases/latest"
	req := httptest.NewRequest(http.MethodPost, "/api/native/open-external", strings.NewReader(`{"url":"`+link+`"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if fake.openCalls != 1 || fake.gotOpenURL != link {
		t.Errorf("bridge.OpenExternal calls=%d url=%q, want 1/%s", fake.openCalls, fake.gotOpenURL, link)
	}
}

func TestNativeOpenExternalAllowsMailtoAndTel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	for _, link := range []string{"mailto:someone@example.com", "tel:+15551234567"} {
		fake := &fakeNative{}
		srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
		req := httptest.NewRequest(http.MethodPost, "/api/native/open-external", strings.NewReader(`{"url":"`+link+`"}`))
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200 (%s)", link, w.Code, w.Body.String())
		}
		if fake.openCalls != 1 || fake.gotOpenURL != link {
			t.Errorf("%s: bridge.OpenExternal calls=%d url=%q, want 1/%s", link, fake.openCalls, fake.gotOpenURL, link)
		}
	}
}

func TestNativeOpenExternalRejectsDisallowedScheme(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	for _, link := range []string{"file:///etc/passwd", "javascript:alert(1)", "custom-app://open"} {
		fake := &fakeNative{}
		srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
		req := httptest.NewRequest(http.MethodPost, "/api/native/open-external", strings.NewReader(`{"url":"`+link+`"}`))
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", link, w.Code)
		}
		if fake.openCalls != 0 {
			t.Errorf("%s: bridge must not be called for a rejected scheme (calls=%d)", link, fake.openCalls)
		}
	}
}

func TestNativeOpenExternalRejectsNonLoopback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: &fakeNative{}})
	req := httptest.NewRequest(http.MethodPost, "/api/native/open-external", strings.NewReader(`{"url":"https://example.com"}`))
	req.RemoteAddr = "203.0.113.5:1000" // non-loopback
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Authorization", "Bearer "+srv.AccessKey())
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-loopback peer: got %d, want 403", w.Code)
	}
}

func TestNativeOpenExternalNotRegisteredWithoutBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodPost, "/api/native/open-external", strings.NewReader(`{"url":"https://example.com"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("without a bridge the route must not exist: got %d, want 404", w.Code)
	}
}

func TestNativeSaveFileDelegatesToBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{retPath: "/Users/x/Downloads/notes.md"}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	req := httptest.NewRequest(http.MethodPost, "/api/native/save-file",
		strings.NewReader(`{"name":"notes.md","content":"# hello"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if fake.saveCalls != 1 || fake.gotSaveName != "notes.md" || fake.gotSaveContent != "# hello" {
		t.Errorf("SaveFile calls=%d name=%q content=%q, want 1/notes.md/# hello",
			fake.saveCalls, fake.gotSaveName, fake.gotSaveContent)
	}
	var resp struct {
		Path      string `json:"path"`
		Cancelled bool   `json:"cancelled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Path != "/Users/x/Downloads/notes.md" || resp.Cancelled {
		t.Errorf("got path=%q cancelled=%v, want the chosen path/false", resp.Path, resp.Cancelled)
	}
}

func TestNativeSaveFileReportsCancel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{retCancel: true}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	req := httptest.NewRequest(http.MethodPost, "/api/native/save-file",
		strings.NewReader(`{"name":"a.txt","content":"x"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Path      string `json:"path"`
		Cancelled bool   `json:"cancelled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Cancelled || resp.Path != "" {
		t.Errorf("dismissed dialog: got path=%q cancelled=%v, want empty/true", resp.Path, resp.Cancelled)
	}
}

// A binary payload (e.g. a skill zip) round-trips through the JSON body as a
// base64 string. The SaveFile bridge receives the decoded bytes as a string,
// so the written file must match the original bytes — not the base64 encoding.
func TestNativeSaveFileDecodesBase64(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	original := []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE}
	b64 := base64.StdEncoding.EncodeToString(original)
	fake := &fakeNative{retPath: "/Users/x/Downloads/b.pk"}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	req := httptest.NewRequest(http.MethodPost, "/api/native/save-file",
		strings.NewReader(`{"name":"b.pk","content":"`+b64+`","encoding":"base64"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if fake.saveCalls != 1 || fake.gotSaveName != "b.pk" {
		t.Fatalf("SaveFile calls=%d name=%q, want 1/b.pk", fake.saveCalls, fake.gotSaveName)
	}
	// The bridge writes the raw decoded bytes, not the base64 string itself.
	if fake.gotSaveContent != string(original) {
		t.Errorf("written bytes differ")
	}
}

// Garbage in the content field must be caught and rejected with 400 rather
// than propagated to a native dialog.
func TestNativeSaveFileRejectsInvalidBase64(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fake := &fakeNative{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Native: fake})
	req := httptest.NewRequest(http.MethodPost, "/api/native/save-file",
		strings.NewReader(`{"name":"x.zip","content":"not_base64!!","encoding":"base64"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 (%s)", w.Code, w.Body.String())
	}
	if fake.saveCalls != 0 {
		t.Errorf("SaveFile called %d times; invalid input should short-circuit before the bridge", fake.saveCalls)
	}
}

func TestNativeSaveFileNotRegisteredWithoutBridge(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodPost, "/api/native/save-file",
		strings.NewReader(`{"name":"a.txt","content":"x"}`))
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("without a bridge the route must not exist: got %d, want 404", w.Code)
	}
}
