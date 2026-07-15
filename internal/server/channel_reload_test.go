package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/channel"
)

// TestReloadChannel_StartAndStop verifies a channel config change is hot-applied
// without a full server restart: enabling starts the adapter, disabling stops
// it, and the manager is built lazily on first reload (mustServer leaves it nil).
func TestReloadChannel_StartAndStop(t *testing.T) {
	channel.Register("reloadfake", func(channel.PlatformConfig) (channel.Adapter, error) {
		return &fullFakeAdapter{}, nil
	})

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	cfgDir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "channels.yml")
	write := func(yml string) {
		if err := os.WriteFile(cfgPath, []byte(yml), 0600); err != nil {
			t.Fatal(err)
		}
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	t.Cleanup(srv.stopChannels)

	// Fresh add + enable → adapter starts, manager built lazily.
	write("channels:\n  reloadfake:\n    enabled: true\n")
	srv.reloadChannel("reloadfake")
	if srv.channelMgr == nil {
		t.Fatal("manager should be built on first reload")
	}
	if !srv.isAdapterRunning("reloadfake") {
		t.Fatal("adapter should be running after enable + reload")
	}

	// Disable → adapter stops.
	write("channels:\n  reloadfake:\n    enabled: false\n")
	srv.reloadChannel("reloadfake")
	if srv.isAdapterRunning("reloadfake") {
		t.Fatal("adapter should stop after disable + reload")
	}
}

// TestReloadChannel_ConcurrentKnownChats guards the data race the lazy
// channelMgr build could introduce: reloadChannel writes s.channelMgr while the
// send_message tool reads it via serverMessenger.KnownChats on another
// goroutine. Run with -race; must be clean.
func TestReloadChannel_ConcurrentKnownChats(t *testing.T) {
	channel.Register("reloadrace", func(channel.PlatformConfig) (channel.Adapter, error) {
		return &fullFakeAdapter{}, nil
	})

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	cfgDir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "channels.yml"),
		[]byte("channels:\n  reloadrace:\n    enabled: true\n"), 0600); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	t.Cleanup(srv.stopChannels)
	msgr := serverMessenger{s: srv}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			srv.reloadChannel("reloadrace")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = msgr.KnownChats()
		}
	}()
	wg.Wait()
}

// TestReloadChannel_SkippedWhenNoChannel: --no-channel disables hot-reload too.
func TestReloadChannel_SkippedWhenNoChannel(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", NoChannel: true})
	srv.reloadChannel("anything") // must be a no-op, not panic
	if srv.channelMgr != nil {
		t.Fatal("no manager should be built when NoChannel is set")
	}
}

// TestHandleReloadChannel_BodyLess verifies the reload endpoint applies config
// without a JSON body — the agent calls it after writing channels.yml.
func TestHandleReloadChannel_BodyLess(t *testing.T) {
	channel.Register("reloadfake", func(channel.PlatformConfig) (channel.Adapter, error) {
		return &fullFakeAdapter{}, nil
	})

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	cfgDir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "channels.yml"),
		[]byte("channels:\n  reloadfake:\n    enabled: true\n"), 0600); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	t.Cleanup(srv.stopChannels)

	// POST with no body + access key header → authenticated, no loopback needed.
	req := httptest.NewRequest("POST", "/api/channels/reloadfake/reload", nil)
	req.Header.Set("X-Access-Key", srv.AccessKey())
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !srv.isAdapterRunning("reloadfake") {
		t.Fatal("adapter should be running after reload endpoint")
	}
}
