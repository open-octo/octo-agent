package server

import (
	"os"
	"path/filepath"
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

// TestReloadChannel_SkippedWhenNoChannel: --no-channel disables hot-reload too.
func TestReloadChannel_SkippedWhenNoChannel(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", NoChannel: true})
	srv.reloadChannel("anything") // must be a no-op, not panic
	if srv.channelMgr != nil {
		t.Fatal("no manager should be built when NoChannel is set")
	}
}
