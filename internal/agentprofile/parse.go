package agentprofile

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatter is the YAML header of a profile .md file. Unmapped keys are
// ignored, so files written for newer versions remain readable.
type frontmatter struct {
	Name            string           `yaml:"name,omitempty"`
	Description     string           `yaml:"description"`
	Model           string           `yaml:"model,omitempty"`
	Tools           []string         `yaml:"tools,omitempty"`
	ToolSkills      []string         `yaml:"tool_skills,omitempty"`
	DisallowedTools []string         `yaml:"disallowed_tools,omitempty"`
	ReadOnly        bool             `yaml:"read_only,omitempty"`
	LeanContext     bool             `yaml:"lean_context,omitempty"`
	WorkingDir      string           `yaml:"working_dir,omitempty"`
	MentionAs       []string         `yaml:"mention_as,omitempty"`
	ChannelBindings []ChannelBinding `yaml:"channel_bindings,omitempty"`
}

// parseFile reads one profile .md file. The profile ID is the file name
// without the .md suffix, assigned by the caller (Store.scanDir). The
// markdown body after the closing frontmatter fence becomes the system
// prompt. A missing description is an error, matching the pre-existing agent
// file parser's rule.
func parseFile(path string) (*Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	front, body, ok := splitFrontmatter(string(b))
	if !ok {
		return nil, fmt.Errorf("no YAML frontmatter")
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	if strings.TrimSpace(fm.Description) == "" {
		return nil, fmt.Errorf("description is required")
	}
	// "inherit" means no model override — treat it as empty (matches the
	// pre-existing parser and the .claude/agents convention).
	model := strings.TrimSpace(fm.Model)
	if strings.EqualFold(model, "inherit") {
		model = ""
	}
	p := &Profile{
		Name:        fm.Name,
		Description: fm.Description,
		CapabilitySpec: CapabilitySpec{
			Model:           model,
			SystemPrompt:    strings.TrimSpace(body),
			Tools:           fm.Tools,
			ToolSkills:      fm.ToolSkills,
			ReadOnly:        fm.ReadOnly,
			DisallowedTools: fm.DisallowedTools,
			LeanContext:     fm.LeanContext,
		},
		WorkingDir:      fm.WorkingDir,
		MentionAs:       fm.MentionAs,
		ChannelBindings: fm.ChannelBindings,
	}
	if info, err := os.Stat(path); err == nil {
		p.CreatedAt = info.ModTime()
		p.UpdatedAt = info.ModTime()
	}
	return p, nil
}

// serialize renders p as a .md file: YAML frontmatter between --- fences,
// then the system prompt as the markdown body. Field order follows the
// frontmatter struct so output is stable for tests and reviews.
func serialize(p *Profile) ([]byte, error) {
	fm := frontmatter{
		Name:            p.Name,
		Description:     p.Description,
		Model:           p.Model,
		Tools:           p.Tools,
		ToolSkills:      p.ToolSkills,
		DisallowedTools: p.DisallowedTools,
		ReadOnly:        p.ReadOnly,
		LeanContext:     p.LeanContext,
		WorkingDir:      p.WorkingDir,
		MentionAs:       p.MentionAs,
		ChannelBindings: p.ChannelBindings,
	}
	head, err := yaml.Marshal(&fm)
	if err != nil {
		return nil, fmt.Errorf("marshaling frontmatter: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(head)
	b.WriteString("---\n")
	if body := strings.TrimSpace(p.SystemPrompt); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// splitFrontmatter returns the text between the opening and closing ---
// fences and everything after the closing fence. ok is false unless the
// first line is a --- fence with a matching closing fence. (Same rule as the
// pre-existing agent file parser in internal/tools/agents.go; PR3 retires
// that copy in favor of this one.)
func splitFrontmatter(content string) (front, body string, ok bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", "", false
}

// maxSystemPrompt mirrors the design doc's 10000-char body limit; enforced by
// validateForWrite before any file is written.
const maxSystemPrompt = 10000

// validateForWrite is the Store-level write gate: schema validation plus the
// system prompt length cap.
func validateForWrite(p *Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if len(p.SystemPrompt) > maxSystemPrompt {
		return fmt.Errorf("system_prompt too long: %d chars (max %d)", len(p.SystemPrompt), maxSystemPrompt)
	}
	return nil
}
