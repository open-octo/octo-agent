package server

import "testing"

// TestSetChannelsEnabled_Transitions covers the desktop hub's runtime
// "run channels on this machine" toggle: SetChannelsEnabled flips the state,
// repeat calls are no-ops, and NoChannel hard-disables regardless.
func TestSetChannelsEnabled_Transitions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// A directly-built server starts with channels enabled (zero value), which
	// mirrors the historical serve default that a NoChannel-less New produces.
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	if !srv.ChannelsEnabled() {
		t.Fatalf("default: want channels enabled")
	}

	srv.SetChannelsEnabled(false)
	if srv.ChannelsEnabled() {
		t.Fatalf("after disable: want disabled")
	}
	srv.SetChannelsEnabled(false) // idempotent no-op
	if srv.ChannelsEnabled() {
		t.Fatalf("repeat disable: want still disabled")
	}

	srv.SetChannelsEnabled(true)
	if !srv.ChannelsEnabled() {
		t.Fatalf("after re-enable: want enabled")
	}
	srv.SetChannelsEnabled(true) // idempotent no-op
	if !srv.ChannelsEnabled() {
		t.Fatalf("repeat enable: want still enabled")
	}

	// NoChannel hard-disables: the toggle can never turn channels on.
	no := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false, NoChannel: true})
	no.SetChannelsEnabled(true)
	if no.ChannelsEnabled() {
		t.Fatalf("NoChannel: want channels to stay disabled")
	}
}
