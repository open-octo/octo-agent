package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/open-octo/octo-agent/internal/browser"
	"github.com/open-octo/octo-agent/internal/skills"
	"github.com/open-octo/octo-agent/internal/workflow"
)

// workflowBrowserMu serializes browser-skill replays within and across
// workflows: browser automation drives a single shared Chrome session, so two
// concurrent replays (from a parallel()/pipeline()) would fight over one page.
var workflowBrowserMu sync.Mutex

// dispatchWorkflowSkill backs the workflow skill() primitive. It resolves name
// (optionally "browser:"/"md:"-prefixed) to a recorded browser skill or a
// SKILL.md skill and runs it, returning the outputs as a JSON string in Reply —
// what skill() parses to native Ruby.
func dispatchWorkflowSkill(ctx context.Context, spawner Spawner, name, paramsJSON, schema string) workflow.AgentResult {
	kind, bare := splitSkillKind(name)

	params, err := parseSkillParams(paramsJSON)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q: %w", name, err)}
	}

	isBrowser := browserSkillExists(bare)
	_, isMD := skillRegistryGet(bare)

	switch kind {
	case "browser":
		if !isBrowser {
			return workflow.AgentResult{Err: fmt.Errorf("skill %q: no browser recording named %q", name, bare)}
		}
		return runBrowserWorkflowSkill(ctx, bare, params)
	case "md":
		if !isMD {
			return workflow.AgentResult{Err: fmt.Errorf("skill %q: no SKILL.md skill named %q", name, bare)}
		}
		return runMDWorkflowSkill(ctx, spawner, bare, params, schema)
	default:
		switch {
		case isBrowser && isMD:
			return workflow.AgentResult{Err: fmt.Errorf("skill %q is ambiguous (both a browser recording and a SKILL.md skill exist); prefix with browser: or md:", bare)}
		case isBrowser:
			return runBrowserWorkflowSkill(ctx, bare, params)
		case isMD:
			return runMDWorkflowSkill(ctx, spawner, bare, params, schema)
		default:
			return workflow.AgentResult{Err: fmt.Errorf("skill %q not found (no browser recording or SKILL.md skill by that name)", bare)}
		}
	}
}

// splitSkillKind peels an optional "browser:"/"md:" engine prefix off a skill
// name, returning the kind ("" when unprefixed) and the bare name.
func splitSkillKind(name string) (kind, bare string) {
	switch {
	case strings.HasPrefix(name, "browser:"):
		return "browser", strings.TrimPrefix(name, "browser:")
	case strings.HasPrefix(name, "md:"):
		return "md", strings.TrimPrefix(name, "md:")
	default:
		return "", name
	}
}

// parseSkillParams turns a params JSON object into a flat string map (values
// stringified), the form both browser replay and the sub-agent inputs expect.
func parseSkillParams(js string) (map[string]string, error) {
	out := map[string]string{}
	js = strings.TrimSpace(js)
	if js == "" || js == "null" || js == "{}" {
		return out, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		return nil, fmt.Errorf("params must be a JSON object: %w", err)
	}
	for k, v := range raw {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out, nil
}

// browserSkillExists reports whether a recording of that name is on disk. The
// base==name check keeps the name from escaping the skills dir.
func browserSkillExists(name string) bool {
	if name == "" || filepath.Base(name) != name {
		return false
	}
	_, err := os.Stat(filepath.Join(BrowserSkillsDir(), name+".yaml"))
	return err == nil
}

func skillRegistryGet(name string) (skills.Skill, bool) {
	if activeSkills == nil {
		return skills.Skill{}, false
	}
	return activeSkills.Get(name)
}

// runBrowserWorkflowSkill replays a recording deterministically and returns its
// declared outputs as JSON. Serialized on the shared Chrome session.
func runBrowserWorkflowSkill(ctx context.Context, name string, params map[string]string) workflow.AgentResult {
	path := filepath.Join(BrowserSkillsDir(), name+".yaml")
	skill, err := browser.LoadSkill(path)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q: load: %w", name, err)}
	}

	workflowBrowserMu.Lock()
	defer workflowBrowserMu.Unlock()

	page, b, err := browserPage(ctx)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q: %w", name, err)}
	}
	recorderMu.Lock()
	healer := browserHealer
	recorderMu.Unlock()

	modified, finalPage, outputs, err := browser.ReplaySkill(ctx, page, &skill, params, browser.ReplayOptions{
		Healer:      healer,
		Browser:     b,
		DownloadDir: downloadDir(),
	})
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q: %w", name, err)}
	}
	if finalPage != nil && finalPage != page {
		setActivePage(b, finalPage)
	}
	if modified {
		_ = browser.SaveSkill(path, skill) // best-effort self-heal write-back
	}
	j, err := json.Marshal(outputs)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q: marshal outputs: %w", name, err)}
	}
	return workflow.AgentResult{Reply: string(j)}
}

// runMDWorkflowSkill runs a SKILL.md skill as a sub-agent. The reply is returned
// as JSON so skill() always parses valid JSON: with a schema it is the
// structured object the schema produced; without one the free-text reply is
// JSON-encoded to a string.
func runMDWorkflowSkill(ctx context.Context, spawner Spawner, name string, params map[string]string, schema string) workflow.AgentResult {
	sk, ok := skillRegistryGet(name)
	if !ok {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q not found", name)}
	}
	inputs := ""
	if len(params) > 0 {
		if pj, err := json.Marshal(params); err == nil {
			inputs = "Inputs (JSON): " + string(pj)
		}
	}
	res, err := spawner.Spawn(ctx, SpawnRequest{
		Description: "skill: " + name,
		Prompt:      skills.RenderSkill(sk, inputs),
		Schema:      schema,
	})
	if err != nil {
		return workflow.AgentResult{Err: err}
	}
	reply := res.Reply
	if strings.TrimSpace(schema) == "" {
		// Free-text reply: JSON-encode it to a string so the boundary stays valid
		// JSON and skill() returns a Ruby String.
		if b, err := json.Marshal(res.Reply); err == nil {
			reply = string(b)
		}
	}
	return workflow.AgentResult{Reply: reply, InputTokens: res.InputTokens, OutputTokens: res.OutputTokens}
}
