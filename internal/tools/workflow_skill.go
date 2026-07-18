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

// workflowBrowserMu serializes browser-recording replays within and across
// workflows: browser automation drives a single shared Chrome session, so two
// concurrent replays (from a parallel()/pipeline()) would fight over one page.
var workflowBrowserMu sync.Mutex

// dispatchWorkflowSkill backs the workflow skill() primitive. It resolves name
// (optionally "browser:"/"md:"-prefixed) to a browser recording or a
// SKILL.md skill and runs it, returning the outputs as a JSON string in Reply —
// what skill() parses to native Ruby.
func dispatchWorkflowSkill(ctx context.Context, spawner Spawner, name, paramsJSON, schema string) workflow.AgentResult {
	kind, bare := splitSkillKind(name)

	params, err := parseSkillParams(paramsJSON)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q: %w", name, err)}
	}

	isBrowser := browserRecordingExists(bare)
	_, isMD := skillRegistryGet(bare)

	switch kind {
	case "browser":
		if !isBrowser {
			return workflow.AgentResult{Err: fmt.Errorf("skill %q: no browser recording named %q", name, bare)}
		}
		return runBrowserRecording(ctx, bare, params)
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
			return runBrowserRecording(ctx, bare, params)
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

// parseSkillParams decodes a params JSON object, keeping values structured so an
// array/object (e.g. a file[] handed from an upstream skill) isn't flattened
// before it reaches its consumer. Each engine narrows as it needs.
func parseSkillParams(js string) (map[string]any, error) {
	out := map[string]any{}
	js = strings.TrimSpace(js)
	if js == "" || js == "null" || js == "{}" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		return nil, fmt.Errorf("params must be a JSON object: %w", err)
	}
	return out, nil
}

// stringifyParam renders one param value for browser {{placeholder}} substitution
// (which is string-only). A scalar renders bare; an array/object is JSON-encoded
// rather than %v-flattened, so a file[] survives as valid JSON text instead of a
// corrupted "[a b]" token.
func stringifyParam(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64, bool:
		return fmt.Sprint(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// browserRecordingExists reports whether a recording of that name is on disk.
// The base==name check keeps the name from escaping the recordings dir.
func browserRecordingExists(name string) bool {
	if name == "" || filepath.Base(name) != name {
		return false
	}
	_, err := os.Stat(filepath.Join(BrowserRecordingsDir(), name+".yaml"))
	return err == nil
}

func skillRegistryGet(name string) (skills.Skill, bool) {
	if activeSkills == nil {
		return skills.Skill{}, false
	}
	return activeSkills.Get(name)
}

// runBrowserRecording replays a recording deterministically and returns its
// declared outputs as JSON. Serialized on the shared Chrome session.
func runBrowserRecording(ctx context.Context, name string, params map[string]any) workflow.AgentResult {
	path := filepath.Join(BrowserRecordingsDir(), name+".yaml")
	recording, err := browser.LoadRecording(path)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("recording %q: load: %w", name, err)}
	}

	// Replay substitutes {{placeholder}} with strings, so narrow here.
	strParams := make(map[string]string, len(params))
	for k, v := range params {
		strParams[k] = stringifyParam(v)
	}

	workflowBrowserMu.Lock()
	defer workflowBrowserMu.Unlock()

	page, b, err := browserPage(ctx)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("recording %q: %w", name, err)}
	}
	recorderMu.Lock()
	healer := browserHealer
	recorderMu.Unlock()

	modified, finalPage, outputs, err := browser.ReplayRecording(ctx, page, &recording, strParams, browser.ReplayOptions{
		Healer:      healer,
		Browser:     b,
		DownloadDir: downloadDir(),
	})
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("recording %q: %w", name, err)}
	}
	if finalPage != nil && finalPage != page {
		setActivePage(b, finalPage)
	}
	if modified {
		// Best-effort self-heal write-back. Re-marshals the YAML, so hand-written
		// comments in the file are dropped (field values are kept).
		_ = browser.SaveRecording(path, recording)
	}
	j, err := json.Marshal(outputs)
	if err != nil {
		return workflow.AgentResult{Err: fmt.Errorf("recording %q: marshal outputs: %w", name, err)}
	}
	return workflow.AgentResult{Reply: string(j)}
}

// runMDWorkflowSkill runs a SKILL.md skill as a sub-agent. The reply is returned
// as JSON so skill() always parses valid JSON: with a schema it is the
// structured object the schema produced; without one the free-text reply is
// JSON-encoded to a string.
func runMDWorkflowSkill(ctx context.Context, spawner Spawner, name string, params map[string]any, schema string) workflow.AgentResult {
	sk, ok := skillRegistryGet(name)
	if !ok {
		return workflow.AgentResult{Err: fmt.Errorf("skill %q not found", name)}
	}
	// Hand the sub-agent structured inputs: an array/object param stays JSON, not
	// a flattened string, so a file[] arrives usable.
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
