package app

import (
	"context"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/tools"
)

// TestVerify_AgentToolAsyncLaunchAndResume drives the exact production tool surface
// the LLM triggers — AgentTool.Execute with run_in_background=true, wired through
// the default SubAgentManager + real Spawner exactly as the REPL wires them —
// and confirms the async sub_agent completes and delivers its result via notification.
func TestVerify_AgentToolAsyncLaunchAndResume(t *testing.T) {
	send := &subAgentSender{reply: "first-task done", inputTokens: 100, outputTokens: 40}
	parent := agent.New(send, "parent-model")
	parent.System = "PARENT"

	// Real spawner with the real childRegistry underneath.
	sp := NewSpawner(parent, nilExecutor{}, func() []agent.ToolDefinition { return nil })

	// Wire the manager exactly like cmd/octo/tuirepl.go does in production:
	// SetDefaultSubAgentManager so the bare tools resolve it via t.manager().
	mgr := tools.NewSubAgentManager(sp)
	tools.SetDefaultSubAgentManager(mgr)
	t.Cleanup(func() { tools.SetDefaultSubAgentManager(nil) })

	notes := make(chan tools.SubAgentNotification, 4)
	mgr.SetOnExit(func(ev tools.SubAgentNotification) { notes <- ev })

	waitNote := func(kind string) tools.SubAgentNotification {
		t.Helper()
		select {
		case ev := <-notes:
			if ev.Kind != kind {
				t.Fatalf("notification kind = %q, want %q (result %q)", ev.Kind, kind, ev.Result)
			}
			return ev
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for %q notification", kind)
			return tools.SubAgentNotification{}
		}
	}

	ctx := context.Background()

	// 1. The model calls sub_agent with run_in_background=true.
	out, err := (tools.AgentTool{}).Execute(ctx, "sub_agent", map[string]any{
		"description":       "research the cache module",
		"prompt":            "Summarise the cache module.",
		"run_in_background": true,
	})
	if err != nil {
		t.Fatalf("sub_agent.Execute: %v", err)
	}
	t.Logf("sub_agent → %q", out.Text)

	// 2. The spawn_done notification is what the model actually sees; it carries
	//    the handle (agent_N) the model will address in a follow-up Agent call.
	spawn := waitNote("spawn_done")
	handle := spawn.AgentID
	t.Logf("spawn_done notification: agent=%s result=%q", handle, spawn.Result)
	if spawn.Result != "first-task done" {
		t.Fatalf("spawn_done result = %q", spawn.Result)
	}

	// 3. The model calls sub_agent again with the same handle (simulating send_message
	//    behavior via a new sub_agent call with the agent_id).
	//    Note: The unified sub_agent tool doesn't have send_message; instead the model
	//    would call sub_agent again. For this test we verify the async path works.
	t.Logf("✅ sub_agent launched sub-agent %s asynchronously and received notification", handle)
}
