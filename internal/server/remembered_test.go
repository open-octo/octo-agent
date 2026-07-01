package server

import (
	"context"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/permission"
)

func TestRememberedFor_SessionScoped(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	a := srv.rememberedFor("sess-a")
	if a == nil {
		t.Fatal("rememberedFor returned nil")
	}
	if srv.rememberedFor("sess-a") != a {
		t.Error("same session must get the same store across turns")
	}
	if srv.rememberedFor("sess-b") == a {
		t.Error("different sessions must not share a store")
	}

	srv.forgetTurnLock("sess-a")
	if srv.rememberedFor("sess-a") == a {
		t.Error("a deleted session's store must not be resurrected")
	}
}

func TestMapConfirmResult(t *testing.T) {
	cases := []struct {
		result   string
		allow    bool
		remember bool
	}{
		{"yes", true, false},
		{"always", true, true},
		{"no", false, false},
		{"", false, false},
		{"whatever", false, false},
	}
	for _, c := range cases {
		allow, remember := mapConfirmResult(c.result)
		if allow != c.allow || remember != c.remember {
			t.Errorf("mapConfirmResult(%q) = (%v,%v), want (%v,%v)", c.result, allow, remember, c.allow, c.remember)
		}
	}
}

// TestChannelPermissionAsk_AlwaysRemembers: the IM reply "总是允许" (or
// "always") approves AND remembers for the session.
func TestChannelPermissionAsk_AlwaysRemembers(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow, remember bool
	go func() {
		allow, remember, _ = ask(context.Background(), "terminal", map[string]any{"command": "sudo ls"})
		close(done)
	}()
	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	if !strings.Contains(ad.texts()[0], "always") && !strings.Contains(ad.texts()[0], "总是允许") {
		t.Errorf("prompt %q should offer the always option", ad.texts()[0])
	}
	if !sess.DeliverAskReply("c1", "", "总是允许") {
		t.Fatal("ask slot not armed")
	}
	<-done
	if !allow || !remember {
		t.Errorf("'总是允许' = allow %v remember %v, want true/true", allow, remember)
	}
}

// TestIMTurnGate_RemembersAcrossEngines wires the pieces the IM turn uses:
// session store + always reply + a fresh engine next turn.
func TestIMTurnGate_RemembersAcrossEngines(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	store := srv.rememberedFor("im:test")
	input := map[string]any{"command": "sudo ls /tmp"}

	e1, err := permission.New(permissionConfigPath(), t.TempDir(), permission.ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	e1.AttachRemembered(store)
	if e1.Check("terminal", input) != permission.Ask {
		t.Fatal("precondition: sudo is ask-class")
	}
	// User answers "always" through the ask path; the gate calls Remember.
	go func() {
		waitFor(t, func() bool { return len(ad.texts()) == 1 })
		sess.DeliverAskReply("c1", "", "always")
	}()
	allow, remember, err := srv.channelPermissionAsk(sess, ad, ev)(context.Background(), "terminal", input)
	if err != nil || !allow || !remember {
		t.Fatalf("always reply: allow=%v remember=%v err=%v", allow, remember, err)
	}
	e1.Remember("terminal", input, permission.Allow)

	// Next turn: fresh engine, same store — no prompt needed.
	e2, err := permission.New(permissionConfigPath(), t.TempDir(), permission.ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	e2.AttachRemembered(store)
	if e2.Check("terminal", input) != permission.Allow {
		t.Error("remembered allow did not survive the per-turn engine rebuild")
	}
}

// TestChannelCommand_UnbindDropsRemembered: /unbind promises "history
// cleared" — the session's always-allows are part of that history, and the
// deterministic session key would otherwise hand them to the next session.
func TestChannelCommand_UnbindDropsRemembered(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}
	ev := evFor("/unbind")

	key := "im:" + string(srv.channelMgr.KeyFor(ev))
	before := srv.rememberedFor(key)

	if !srv.handleChannelCommand(ad, ev) {
		t.Fatal("/unbind not handled as a command")
	}
	if srv.rememberedFor(key) == before {
		t.Error("/unbind must drop the session's remembered permission store")
	}
}

// TestChannelCommand_NewDropsRemembered: /new starts a brand-new session, so
// the previous session's always-allows must not leak into it.
func TestChannelCommand_NewDropsRemembered(t *testing.T) {
	srv := chanServer(t)
	ad := &fullFakeAdapter{}
	ev := evFor("/new")

	key := "im:" + string(srv.channelMgr.KeyFor(ev))
	before := srv.rememberedFor(key)

	if !srv.handleChannelCommand(ad, ev) {
		t.Fatal("/new not handled as a command")
	}
	if srv.rememberedFor(key) == before {
		t.Error("/new must drop the session's remembered permission store")
	}
}
