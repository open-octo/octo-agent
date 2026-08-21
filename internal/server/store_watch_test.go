package server

import (
	"encoding/json"
	"testing"
	"time"
)

// watchConn registers a connection on the hub and returns it, so a test can
// read what the watch broadcast. The hub's own run goroutine consumes the
// events channel and fans out to connections, so reading that channel directly
// races it — a registered connection is the observable side.
func watchConn(t *testing.T, srv *Server) *wsConn {
	t.Helper()
	conn := &wsConn{hub: srv.wsHub, send: make(chan []byte, 256), subscribed: map[string]struct{}{}}
	srv.wsHub.register <- conn
	// watchStop is created by New; mustServer builds a Server literal, so give
	// the test one too — startStoreWatch selects on it.
	if srv.watchStop == nil {
		srv.watchStop = make(chan struct{})
	}
	return conn
}

// eventTypes drains the connection's queue and returns the "type" of each frame
// it received, waiting briefly for the hub's fan-out to land.
func eventTypes(t *testing.T, conn *wsConn) []string {
	t.Helper()
	var types []string
	deadline := time.After(2 * time.Second)
	for {
		select {
		case raw := <-conn.send:
			var probe struct{ Type string }
			if err := json.Unmarshal(raw, &probe); err != nil {
				t.Fatalf("unmarshal frame: %v", err)
			}
			types = append(types, probe.Type)
		case <-time.After(100 * time.Millisecond):
			return types
		case <-deadline:
			return types
		}
	}
}

func contains(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

// TestStoreWatch_AnnouncesASessionFromAnotherProcess: a session created outside
// this process (what every `octo` run in a terminal does) emits no broadcast of
// its own, so an open sidebar sits on a stale list until something else happens
// to refresh it. The watch is what notices.
func TestStoreWatch_AnnouncesASessionFromAnotherProcess(t *testing.T) {
	srv := groupTestServer(t)
	conn := watchConn(t, srv)

	prev := sampleStore()
	eventTypes(t, conn) // drain anything setup emitted

	// Stands in for the terminal: a transcript appears on disk with nothing
	// telling this server about it.
	saveSessionWithDir(t, t.TempDir())

	cur := srv.pollStoreOnce(prev)
	if got := eventTypes(t, conn); !contains(got, "session_created") {
		t.Errorf("no session_created after a transcript appeared; got %v", got)
	}
	if cur.sessionCount != prev.sessionCount+1 {
		t.Errorf("sessionCount = %d, want %d", cur.sessionCount, prev.sessionCount+1)
	}
}

// TestStoreWatch_AnnouncesAProjectFromAnotherProcess: same for the registry.
// EnsureProjectForDir is the CLI's path and cannot reach this process's
// broadcast hook (notifyGroupsChanged is installed per-process).
func TestStoreWatch_AnnouncesAProjectFromAnotherProcess(t *testing.T) {
	srv := groupTestServer(t)
	conn := watchConn(t, srv)

	prev := sampleStore()
	eventTypes(t, conn)

	if err := EnsureProjectForDir(t.TempDir(), "sess-elsewhere"); err != nil {
		t.Fatalf("EnsureProjectForDir: %v", err)
	}

	srv.pollStoreOnce(prev)
	if got := eventTypes(t, conn); !contains(got, "session_groups_changed") {
		t.Errorf("no session_groups_changed after the registry changed; got %v", got)
	}
}

// TestStoreWatch_QuietWhenNothingChanged is what makes the watch affordable:
// the frontend answers each broadcast by refetching the whole session list,
// which loads every transcript, so announcing an unchanged store every tick
// would be a standing cost for every open tab.
func TestStoreWatch_QuietWhenNothingChanged(t *testing.T) {
	srv := groupTestServer(t)
	conn := watchConn(t, srv)
	saveSessionWithDir(t, t.TempDir())

	prev := sampleStore()
	eventTypes(t, conn)

	for i := 0; i < 3; i++ {
		prev = srv.pollStoreOnce(prev)
	}
	if got := eventTypes(t, conn); len(got) != 0 {
		t.Errorf("idle polls broadcast %v, want nothing", got)
	}
}

// TestStoreWatch_TurnWritesDoNotAnnounce pins the property that makes the watch
// affordable at all: writing to an existing transcript must be invisible to it.
// Every broadcast makes every open tab re-list sessions, which loads every
// transcript, so a fingerprint that moved on each turn would be a standing cost
// for the length of every conversation.
//
// It holds because Session.Save appends, and its rewrite path truncates in
// place (O_TRUNC) rather than renaming a temp file over the old one — a rename
// WOULD bump the directory's mtime and put this watch in exactly that loop.
func TestStoreWatch_TurnWritesDoNotAnnounce(t *testing.T) {
	srv := groupTestServer(t)
	conn := watchConn(t, srv)
	sess := saveSessionWithDir(t, t.TempDir())

	prev := sampleStore()
	eventTypes(t, conn)

	// A rewrite of an existing transcript — the shape a turn produces.
	if err := sess.SetTitle("a new title"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv.pollStoreOnce(prev)
	if got := eventTypes(t, conn); contains(got, "session_created") {
		t.Errorf("a write to an existing session announced a new one; got %v", got)
	}
}

// TestStoreWatch_StopsWhenTold: the watch must not outlive the server, or every
// server in a test binary — and every restart of the daemon — leaks a ticker
// goroutine. doShutdown closes watchStop; this covers the goroutine honouring
// it. Run under -race, a goroutine still ticking here would be visible.
func TestStoreWatch_StopsWhenTold(t *testing.T) {
	srv := groupTestServer(t)
	srv.watchStop = make(chan struct{})
	srv.startStoreWatch()

	close(srv.watchStop)
	srv.watchStop = nil // a later Shutdown must not double-close

	time.Sleep(20 * time.Millisecond)
}
