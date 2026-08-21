package server

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// cwdProbeSender drives one real tool turn: round 1 asks the terminal to
// print the shell's cwd, round 2 captures the tool_result it got back.
type cwdProbeSender struct {
	mu         sync.Mutex
	round      int
	toolResult string
}

func (s *cwdProbeSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	return agent.Reply{Content: "side-call"}, nil
}

func (s *cwdProbeSender) StreamMessages(_ context.Context, _, _ string, _ []agent.Message, _ int, _ func(string), _ func(string)) (agent.Reply, error) {
	return agent.Reply{Content: "side-call"}, nil
}

func (s *cwdProbeSender) SendMessagesWithTools(_ context.Context, _, _ string, _ []agent.Message, _ int, _ []agent.ToolDefinition) (agent.Reply, error) {
	return agent.Reply{Content: "side-call"}, nil
}

func (s *cwdProbeSender) StreamMessagesWithTools(_ context.Context, _, _ string, msgs []agent.Message, _ int, _ []agent.ToolDefinition, _ func(string), _ agent.ToolInputDeltaFunc, _ agent.ThinkingDeltaFunc) (agent.Reply, error) {
	s.mu.Lock()
	s.round++
	round := s.round
	s.mu.Unlock()
	if round == 1 {
		cmd := "pwd"
		if runtime.GOOS == "windows" {
			cmd = "(Get-Location).Path"
		}
		return agent.Reply{
			Blocks: []agent.ContentBlock{
				agent.NewToolUseBlock("tu1", "terminal", map[string]any{"command": cmd}),
			},
			StopReason: "tool_use",
		}, nil
	}
	s.mu.Lock()
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" {
				s.toolResult = b.Result
			}
		}
	}
	s.mu.Unlock()
	return agent.Reply{Content: "done"}, nil
}

// TestDoAgentTurn_TerminalRunsInSessionWorkingDir is the end-to-end guard for
// the field bug where the composer chip and the system prompt showed the
// session's working directory while every shell command ran in the serve
// process's cwd (its launch directory): the terminal tool routes all commands
// through BackgroundManager.Start, which used to build the shell from a bare
// context.Background() and so dropped the WithWorkingDir stamp. Drives the
// REAL turn path — doAgentTurn → buildAgent → prepareToolTurn → terminal.
func TestDoAgentTurn_TerminalRunsInSessionWorkingDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sender := &cwdProbeSender{}
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: true})
	srv.sender = sender
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "fixed title" // suppress the async title side-call
	if err := sess.SetPermissionMode("auto"); err != nil {
		t.Fatal(err)
	}
	srv.applyDefaultWorkspaceDir(sess)
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	want := filepath.Join(tmp, "Octo", "tasks", sess.ID)
	if sess.WorkingDir != want {
		t.Fatalf("precondition: seeded WorkingDir = %q, want the task workspace %q", sess.WorkingDir, want)
	}

	srv.doAgentTurn(sess, "where are you", nil, nil)

	got := strings.TrimSpace(sender.toolResult)
	gotReal, _ := filepath.EvalSymlinks(got)
	wantReal, _ := filepath.EvalSymlinks(want)
	if gotReal == "" || gotReal != wantReal {
		procCwd, _ := filepath.Abs(".")
		t.Errorf("shell ran in %q, want session working dir %q (process cwd %q)", got, want, procCwd)
	}
}
