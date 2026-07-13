package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/skills"
)

// skillRegFor builds a project-level skill registry from name→SKILL.md content,
// with HOME pointed at an empty dir so no real user skills leak in.
func skillRegFor(t *testing.T, m map[string]string) *skills.Registry {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)

	cwd := t.TempDir()
	for name, content := range m {
		dir := filepath.Join(cwd, ".octo", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, skills.SkillFile), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return skills.Discover(cwd)
}

func TestSkillTrigger(t *testing.T) {
	reg := skillRegFor(t, map[string]string{
		"greet": "---\ndescription: d\n---\nbody",
		"help":  "---\ndescription: d\n---\nshadow attempt",
	})

	if _, _, ok := skillTrigger(nil, "/greet"); ok {
		t.Error("nil registry should never match")
	}
	if _, _, ok := skillTrigger(reg, "greet"); ok {
		t.Error("non-slash input should not match")
	}
	if _, _, ok := skillTrigger(reg, "/help"); ok {
		t.Error("reserved command /help must not be hijacked by a same-named skill")
	}
	if _, _, ok := skillTrigger(reg, "/nope"); ok {
		t.Error("unknown skill should not match")
	}
	s, args, ok := skillTrigger(reg, "/greet")
	if !ok || s.Name != "greet" || args != "" {
		t.Errorf("/greet → %q, args=%q, ok=%v", s.Name, args, ok)
	}
	s, args, ok = skillTrigger(reg, "/greet  hello world")
	if !ok || args != "hello world" {
		t.Errorf("/greet args → args=%q, ok=%v", args, ok)
	}
}

// noopExec is a do-nothing ToolExecutor: enough to make a replConfig look
// tool-enabled so dispatchSlash takes its with-tools path.
type noopExec struct{}

func (noopExec) Execute(context.Context, string, map[string]any) (agent.ToolResult, error) {
	return agent.ToolResult{}, nil
}

// skillDispatchModel builds a TUI model backed by a stub sender and fake
// program, with the given skill registry and (optionally) tools, so
// dispatchSlash can be driven without a real provider or terminal.
func skillDispatchModel(reg *skills.Registry, withTools bool) *tuiModel {
	cfg := replConfig{a: agent.New(&stubSender{reply: "ok"}, "m"), skillReg: reg, noSave: true}
	if withTools {
		cfg.tools = []agent.ToolDefinition{{Name: "noop"}}
		cfg.executor = noopExec{}
	}
	m := newTUIModel(cfg)
	m.sink = &tuiSink{prog: &fakeProg{}}
	return m
}

// A /<skill> trigger with tools present is NOT expanded inline — it falls
// through to the default branch and starts an ordinary turn with the literal
// "/name" text, so the model loads the skill via the `skill` tool (parity with
// the web and IM transports).
func TestDispatchSlash_SkillWithTools_FallsThrough(t *testing.T) {
	reg := skillRegFor(t, map[string]string{"greet": "---\ndescription: d\n---\nbody"})
	m := skillDispatchModel(reg, true)

	_, cmd := m.dispatchSlash("/greet")

	if got := strings.Join(m.printlnBuf, "\n"); strings.Contains(got, "needs tools") {
		t.Errorf("with tools, a skill trigger must not be refused; got:\n%s", got)
	}
	if !m.turnRunning || cmd == nil {
		t.Error("with tools, /greet should fall through and start an ordinary turn")
	}
}

// Without tools the `skill` tool doesn't exist, so a /<skill> trigger can't
// work — it is refused (mirroring /init) rather than starting a dead turn.
func TestDispatchSlash_SkillWithoutTools_Refused(t *testing.T) {
	reg := skillRegFor(t, map[string]string{"greet": "---\ndescription: d\n---\nbody"})
	m := skillDispatchModel(reg, false)

	_, cmd := m.dispatchSlash("/greet")

	if cmd != nil {
		t.Errorf("refusal must not start a turn, got cmd=%v", cmd)
	}
	if m.turnRunning {
		t.Error("no turn should start when a skill trigger is refused")
	}
	if got := strings.Join(m.printlnBuf, "\n"); !strings.Contains(got, "/greet needs tools") {
		t.Errorf("expected a 'needs tools' refusal for /greet; got:\n%s", got)
	}
}

// skillTrigger remains unit-tested above since dispatchSlash relies on it to
// detect a skill name (to refuse without tools); printSkills (the /skills
// renderer) is tested directly here.

func TestPrintSkills_ListsSkills(t *testing.T) {
	reg := skillRegFor(t, map[string]string{"greet": "---\ndescription: say hi\n---\nbody"})
	var out bytes.Buffer
	printSkills(&out, reg)
	if !strings.Contains(out.String(), "/greet") || !strings.Contains(out.String(), "say hi") {
		t.Errorf("printSkills missing skill:\n%s", out.String())
	}
}

func TestPrintSkills_None(t *testing.T) {
	var out bytes.Buffer
	printSkills(&out, skillRegFor(t, nil))
	if !strings.Contains(out.String(), "No skills found") {
		t.Errorf("expected 'No skills found':\n%s", out.String())
	}
}
