package protean

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
)

// Sender is the subset of agent.Sender that GenerateSkill needs. It is
// satisfied by app.NewSender, but using an interface here avoids an import
// cycle with internal/app.
type Sender interface {
	SendMessages(ctx context.Context, model, system string, msgs []agent.Message, maxTokens int) (agent.Reply, error)
}

// GenerateOptions bundles everything needed to turn a Protean recording into a
// SKILL.md using octo's configured LLM instead of Protean's own LLM client.
type GenerateOptions struct {
	RecordingDir string
	SkillsDir    string
	TaskDesc     string
	ModelName    string
	Sender       Sender
}

// GenerateSkill synthesizes a Protean SKILL.md from a recording directory using
// the supplied sender (typically octo's app.Sender). It returns the generated
// skill metadata.
func GenerateSkill(ctx context.Context, opts GenerateOptions) (SkillInfo, error) {
	rec := &recording{dir: opts.RecordingDir}
	if err := rec.load(); err != nil {
		return SkillInfo{}, fmt.Errorf("load recording: %w", err)
	}

	blocks := rec.toContentBlocks(opts.TaskDesc)

	var streamingSender interface {
		StreamMessages(ctx context.Context, model, system string, msgs []agent.Message, maxTokens int, onChunk func(string), onThinking func(string)) (agent.Reply, error)
	}
	streamingSender, _ = opts.Sender.(interface {
		StreamMessages(ctx context.Context, model, system string, msgs []agent.Message, maxTokens int, onChunk func(string), onThinking func(string)) (agent.Reply, error)
	})

	var reply agent.Reply
	var err error
	if streamingSender != nil {
		reply, err = streamingSender.StreamMessages(ctx, opts.ModelName, generateSkillSystemPrompt, []agent.Message{
			{Role: agent.RoleUser, Blocks: blocks},
		}, 8192, nil, nil)
	} else {
		reply, err = opts.Sender.SendMessages(ctx, opts.ModelName, generateSkillSystemPrompt, []agent.Message{
			{Role: agent.RoleUser, Blocks: blocks},
		}, 8192)
	}
	if err != nil {
		return SkillInfo{}, fmt.Errorf("model request: %w", err)
	}

	md := reply.Content
	name, err := extractSkillName(md)
	if err != nil {
		return SkillInfo{}, fmt.Errorf("extract skill name: %w", err)
	}

	skillDir := filepath.Join(opts.SkillsDir, name)
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		return SkillInfo{}, fmt.Errorf("create skill dir: %w", err)
	}
	mdPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(mdPath, []byte(md), 0o600); err != nil {
		return SkillInfo{}, fmt.Errorf("write SKILL.md: %w", err)
	}

	desc := extractDescription(md)
	return SkillInfo{Name: name, Description: desc, SkillDir: skillDir}, nil
}

// SkillInfo describes a generated skill.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SkillDir    string `json:"skill_dir"`
}

type recording struct {
	dir    string
	events []recordingEvent
}

type recordingEvent struct {
	Type      string  `json:"event_type"`
	Timestamp float64 `json:"timestamp"`
	X         int     `json:"x"`
	Y         int     `json:"y"`
	Text      string  `json:"text,omitempty"`
	Keys      string  `json:"keys,omitempty"`
	Window    struct {
		ProcessName string `json:"process_name"`
		WindowTitle string `json:"window_title"`
		BundleID    string `json:"bundle_id"`
	} `json:"window"`
	Screenshot struct {
		Overview string `json:"overview"`
		Detail   string `json:"detail"`
	} `json:"screenshot"`
}

func (r *recording) load() error {
	path := filepath.Join(r.dir, "events.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var payload struct {
		Events []recordingEvent `json:"events"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	r.events = payload.Events
	return nil
}

func (r *recording) toContentBlocks(taskDesc string) []agent.ContentBlock {
	var blocks []agent.ContentBlock
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n\nRecorded events:\n", taskDesc))
	for i, e := range r.events {
		sb.WriteString(fmt.Sprintf("%d. [%s] at (%.3fs) app=%q window=%q", i+1, e.Type, e.Timestamp, e.Window.ProcessName, e.Window.WindowTitle))
		switch e.Type {
		case "mouse_click":
			sb.WriteString(fmt.Sprintf(" pos=(%d,%d)", e.X, e.Y))
		case "mouse_scroll":
			sb.WriteString(fmt.Sprintf(" pos=(%d,%d)", e.X, e.Y))
		case "text_input":
			sb.WriteString(fmt.Sprintf(" text=%q", e.Text))
		case "key_press":
			sb.WriteString(fmt.Sprintf(" keys=%q", e.Keys))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nGenerate a Protean SKILL.md from this recording. The screenshots are provided below in order.")
	blocks = append(blocks, agent.NewTextBlock(sb.String()))

	// Limit screenshots to keep multimodal requests fast and cheap. We keep the
	// first frame plus frames around app switches and text input, capped at 10.
	maxImages := 10
	imageCount := 0
	for i, e := range r.events {
		if e.Screenshot.Overview == "" {
			continue
		}
		keep := i == 0 || e.Type == "app_switch" || e.Type == "text_input" || e.Type == "key_combo"
		if !keep && imageCount >= maxImages {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dir, e.Screenshot.Overview))
		if err != nil {
			continue
		}
		mime := mimeTypeFromData(data)
		blocks = append(blocks, agent.NewImageBlock(mime, data))
		imageCount++
	}
	return blocks
}

func mimeTypeFromData(data []byte) string {
	if len(data) > 1 && data[0] == 0xFF && data[1] == 0xD8 {
		return "image/jpeg"
	}
	if len(data) > 3 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	return "image/jpeg"
}

func extractSkillName(md string) (string, error) {
	re := regexp.MustCompile(`(?m)^name:\s*(\S+)`)
	m := re.FindStringSubmatch(md)
	if len(m) < 2 {
		return "", fmt.Errorf("SKILL.md frontmatter missing 'name'")
	}
	return strings.TrimSpace(m[1]), nil
}

func extractDescription(md string) string {
	re := regexp.MustCompile(`(?m)^description:\s*(.+)$`)
	m := re.FindStringSubmatch(md)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

var generateSkillSystemPrompt = `You are a Protean skill generator. Convert a screen recording (event log + screenshots) into a valid Protean SKILL.md file.

Protean skills automate macOS GUI tasks. The output must be a Markdown file with YAML frontmatter followed by a structured body.

Required frontmatter:
---
name: <kebab-case-skill-name>
description: <one-line description>
metadata:
  protean:
    source: recorded
---

Body structure:
# <Human-readable title>

> <description>

## When to Use

- <bullet>

## When NOT to Use

- <bullet>

## Inputs

- **<param>** (optional): <description> (default: ` + "`" + `<value>` + "`" + `) — <constraints>

## Goal

<one-line goal>

## Steps

### 1. <Step Title>

**App:** <localized app name as shown by macOS, e.g. "计算器" for Calculator>

**Tool:** ` + "`" + `activate_app(app="<App Name>")` + "`" + ` or ` + "`" + `key_press(keys="<keys>")` + "`" + ` etc. Chain multiple tool calls with ` + "`" + ` -> ` + "`" + `.

<action description. Use {{param}} for parameters.>

**Verify:** <what should be true after this step>

**Verify Condition:** strategy=<ax_element|text_content|visual>
  description=<description>
For strategy=ax_element use ax_role=<role> and ax_title=<title>.
For strategy=text_content use expected_text=<text>.

## Success Criteria

- <bullet>

Available Protean tools (use EXACTLY these names):
- activate_app(app="<name>") — bring an app to foreground. Use this as the FIRST attempt to open a known app; do NOT use Spotlight unless activate_app fails.
- key_press(keys="<keys>") — press a physical key or combo, e.g. key_press(keys="escape") clears Calculator, key_press(keys="return") confirms.
- type_text(text="<text>") — type literal text into the focused field. Use this for expressions like type_text(text="1+1=").
- left_click(x=<int>, y=<int>) — click at screen coordinates
- wait(seconds=<float>) — pause, e.g. wait(seconds=0.5)

Rules:
1. Open known apps with activate_app, not Spotlight.
2. Before typing a new expression in Calculator, clear any existing value with key_press(keys="escape") followed by wait(seconds=0.2). Do NOT verify the cleared display; only verify the final result.
3. To enter arithmetic in Calculator, use key_press for each character. The '+' operator is the '=' key on the keyboard (no shift). Example for 1+1: key_press(keys="1") -> key_press(keys="=") -> key_press(keys="1") -> key_press(keys="="). Do NOT use type_text for Calculator because it pastes via the clipboard.
4. Prefer keyboard shortcuts (key_press) over absolute mouse coordinates.
5. Include Verify Condition blocks so execution can self-check.

Output ONLY the SKILL.md content, no markdown fences, no commentary.`
