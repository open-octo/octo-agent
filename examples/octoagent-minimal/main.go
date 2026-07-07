// Command octoagent-minimal demonstrates using pkg/octoagent as a library from
// an external Go module.
//
// It runs a single streaming turn with a stub Sender. A real program would
// replace the stubSender with a provider.NewSender constructed from its own
// provider configuration.
package main

import (
	"context"
	"fmt"

	"github.com/open-octo/octo-agent/pkg/octoagent"
	"github.com/open-octo/octo-agent/pkg/octoagent/approval"
	"github.com/open-octo/octo-agent/pkg/octoagent/toolenv"
)

// stubSender is a minimal octoagent.Sender implementation for the example.
type stubSender struct{}

func (stubSender) SendMessages(ctx context.Context, model, system string, messages []octoagent.Message, maxTokens int) (octoagent.Reply, error) {
	return octoagent.Reply{
		Content: "Hello from the stub sender!",
	}, nil
}

func main() {
	ctx := context.Background()

	// 1. Build a Sender. In production this is usually:
	//    sender, err := provider.NewSender(provider.Options{Provider: "anthropic", APIKey: key, ...})
	sender := stubSender{}

	// 2. Build an Agent.
	a := octoagent.New(sender, "stub-model")
	a.System = "You are a helpful assistant."

	// 3. Set a permission gate. In production this might call a policy service.
	a.Gate = approval.GateFunc(func(ctx context.Context, name string, input map[string]any) (bool, string) {
		fmt.Printf("Gate check: tool=%q\n", name)
		return true, ""
	})

	// 4. Wire a session-scoped tool environment.
	sessionID := "example-session"
	ctx, executor, cleanup := toolenv.WireForSession(ctx, a, sessionID)
	defer cleanup()

	// 5. Decide which tools to advertise. DefaultToolsForCtx reads the
	//    ctx-scoped managers WireForSession just stamped in — pass the ctx it
	//    returned, not the original. A caller that doesn't want octo's native
	//    sub-agent orchestration (e.g. it filters via its own
	//    disallowed_tools: [sub_agent] convention) drops that one entry here.
	toolDefs := make([]octoagent.ToolDefinition, 0)
	for _, td := range toolenv.DefaultToolsForCtx(ctx, a.Model) {
		if td.Name == "sub_agent" {
			continue
		}
		toolDefs = append(toolDefs, td)
	}

	// 6. Run a streaming turn.
	handler := func(ev octoagent.AgentEvent) {
		switch ev.Kind {
		case octoagent.EventTextDelta:
			fmt.Print(ev.Text)
		case octoagent.EventTurnDone:
			fmt.Println("\n[turn done]")
		}
	}

	reply, err := a.RunStream(ctx, "Hello!", toolDefs, executor, handler)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nFinal reply: %q\n", reply.Content)
}
