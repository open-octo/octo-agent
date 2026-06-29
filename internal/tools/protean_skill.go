package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/config"
	"github.com/Leihb/octo-agent/internal/protean"
)

// proteanBridge is the package-level bridge, lazily loaded from config. It is
// set on first use so tests don't need Protean installed. proteanBridgeMu
// guards it because the tool gate (proteanSkillEnabled) runs from concurrent
// server sessions.
var (
	proteanBridge   *protean.Bridge
	proteanBridgeMu sync.Mutex
)

func getProteanBridge() *protean.Bridge {
	proteanBridgeMu.Lock()
	defer proteanBridgeMu.Unlock()
	if proteanBridge != nil {
		return proteanBridge
	}
	cfg, _ := config.Load()
	proteanBridge = protean.NewBridge(cfg.Protean)
	return proteanBridge
}

// SetProteanBridge allows tests and server setup to inject a custom bridge.
func SetProteanBridge(b *protean.Bridge) {
	proteanBridgeMu.Lock()
	defer proteanBridgeMu.Unlock()
	proteanBridge = b
}

// proteanSkillEnabled gates the Protean tools. We only advertise them when a
// Protean installation is available so the model doesn't try to use them on a
// plain octo setup.
func proteanSkillEnabled() bool {
	return getProteanBridge().Available()
}

// RunProteanSkillTool executes a Protean skill by name through the local
// Protean runtime. Execution is deterministic (step_by_step executor) and does
// not consume LLM tokens.
type RunProteanSkillTool struct{}

func (RunProteanSkillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "run_protean_skill",
		Description: "Run a Protean skill by name. Protean skills are recorded " +
			"workflows with structured GUI steps (activate_app, key_press, click, etc.). " +
			"Execution is deterministic and does not use an LLM. Only use this when the " +
			"user explicitly asks to run a recorded Protean skill.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The exact name of the Protean skill to run.",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (RunProteanSkillTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	name, _ := input["name"].(string)
	if name == "" {
		return agent.ToolResult{}, fmt.Errorf("run_protean_skill: name is required")
	}
	// Reject names that try to escape the skills directory.
	if filepath.Base(name) != name {
		return agent.ToolResult{}, fmt.Errorf("run_protean_skill: invalid skill name %q", name)
	}
	b := getProteanBridge()
	if !b.Available() {
		return agent.ToolResult{}, fmt.Errorf("run_protean_skill: Protean is not available at %s", b.Venv())
	}
	res, err := b.RunSkill(ctx, name)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("run_protean_skill: %w", err)
	}
	if !res.Success {
		return agent.ToolResult{Text: res.Output}, fmt.Errorf("run_protean_skill: %s", res.Error)
	}
	return agent.ToolResult{Text: res.Output}, nil
}

// ProteanRecordTool starts or stops a Protean screen recording session. It is
// exposed to the agent so users can say "start recording" in chat, but the
// primary UI is the web panel's Record button.
type ProteanRecordTool struct{}

func (ProteanRecordTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "protean_record",
		Description: "Start or stop a Protean screen recording. Use action=start " +
			"to begin recording user actions, action=stop to finish. The recording is " +
			"saved under ~/.octo/protean-recordings/.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"start", "stop"},
					"description": "Whether to start or stop recording.",
				},
			},
			"required": []string{"action"},
		},
	}
}

// activeRecorder holds the in-flight recorder started via the tool or API.
// activeRecorderMu guards it against concurrent start/stop.
var (
	activeRecorder   *protean.Recorder
	activeRecorderMu sync.Mutex
)

func (ProteanRecordTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	action, _ := input["action"].(string)
	switch action {
	case "start":
		b := getProteanBridge()
		if !b.Available() {
			return agent.ToolResult{}, fmt.Errorf("protean_record: Protean is not available at %s", b.Venv())
		}
		activeRecorderMu.Lock()
		defer activeRecorderMu.Unlock()
		if activeRecorder != nil {
			return agent.ToolResult{}, fmt.Errorf("protean_record: recording already in progress")
		}
		outDir := b.RecordingsDir()
		if err := os.MkdirAll(outDir, 0o700); err != nil {
			return agent.ToolResult{}, fmt.Errorf("protean_record: %w", err)
		}
		rec := protean.NewRecorder(b, outDir)
		if err := rec.Start(ctx); err != nil {
			return agent.ToolResult{}, err
		}
		activeRecorder = rec
		return agent.ToolResult{Text: "Recording started: " + outDir}, nil
	case "stop":
		activeRecorderMu.Lock()
		defer activeRecorderMu.Unlock()
		if activeRecorder == nil {
			return agent.ToolResult{}, fmt.Errorf("protean_record: no recording in progress")
		}
		outDir := activeRecorder.OutDir
		if err := activeRecorder.Stop(); err != nil {
			return agent.ToolResult{}, fmt.Errorf("protean_record: %w", err)
		}
		activeRecorder = nil
		return agent.ToolResult{Text: "Recording stopped: " + outDir}, nil
	default:
		return agent.ToolResult{}, fmt.Errorf("protean_record: action must be start or stop")
	}
}
