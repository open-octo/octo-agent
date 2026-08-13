package server

import (
	"context"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/agentprofile"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/tasks"
	"github.com/open-octo/octo-agent/internal/tools"
)

// The IM path builds its tool environment by hand instead of going through
// app.NewSessionToolEnv, so the turn-end task guard has to be wired there
// explicitly — this pins that it is, and that it reads the session's own task
// store (the one the task_* tools write to across messages in this chat).
func TestHandleChannelMessage_WiresTurnEndTaskGuard(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.channelMgr = channel.NewManager(&channel.Config{}, func(*agentprofile.Profile) *agent.Agent {
		return agent.New(&stubSender{}, "stub-model")
	}, channel.BindByChat)
	ad := &fullFakeAdapter{}

	// Own chat id: sessions are keyed by chat, and the shared "c1" used by the
	// other channel tests would carry their state into this one.
	ev := channel.InboundEvent{Platform: "fake", ChatID: "c-taskguard", UserID: "u1", MessageID: "m1", Text: "hello"}
	sess := srv.channelMgr.GetOrCreateSession(ev, nil)
	if sess.Agent.TurnEndReminder != nil {
		t.Fatal("the session factory must not pre-wire the guard")
	}

	srv.handleChannelMessage(context.Background(), ad, ev, nil)

	if sess.Agent.TurnEndReminder == nil {
		t.Fatal("handleChannelMessage must wire the turn-end task guard")
	}
	if got := sess.Agent.TurnEndReminder(context.Background()); got != "" {
		t.Errorf("empty plan should not fire the guard, got %q", got)
	}

	id, err := sess.Tasks.Create("ship it", "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	st := tasks.InProgress
	if _, err := sess.Tasks.Update(id, tasks.UpdateField{Status: &st}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	// The guard resolves the store from the turn's ctx — the same stamping the
	// handler does — so an unfinished plan is what the next turn ends against.
	ctx := tools.WithTaskStore(context.Background(), sess.Tasks)
	if got := sess.Agent.TurnEndReminder(ctx); got == "" {
		t.Error("a task left in_progress should fire the guard")
	}
}
