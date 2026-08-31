package agentprofile

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

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
	ChannelBindings []ChannelBinding `yaml:"channel_bindings,omitempty"`

	// Gallery display metadata — only meaningful for curated (SourceDefault)
	// experts; ordinary user profiles simply omit these.
	Category         string   `yaml:"category,omitempty"`
	Tags             []string `yaml:"tags,omitempty"`
	TagsEN           []string `yaml:"tags_en,omitempty"`
	ExamplePrompts   []string `yaml:"example_prompts,omitempty"`
	ExamplePromptsEN []string `yaml:"example_prompts_en,omitempty"`
	Icon             string   `yaml:"icon,omitempty"`
	NameEN           string   `yaml:"name_en,omitempty"`
	DescriptionEN    string   `yaml:"description_en,omitempty"`
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
			Tools:           stripRetiredTools(fm.Tools),
			ToolSkills:      fm.ToolSkills,
			ReadOnly:        fm.ReadOnly,
			DisallowedTools: fm.DisallowedTools,
			LeanContext:     fm.LeanContext,
		},
		WorkingDir:      fm.WorkingDir,
		ChannelBindings: fm.ChannelBindings,

		Category:         fm.Category,
		Tags:             fm.Tags,
		TagsEN:           fm.TagsEN,
		ExamplePrompts:   fm.ExamplePrompts,
		ExamplePromptsEN: fm.ExamplePromptsEN,
		Icon:             fm.Icon,
		NameEN:           fm.NameEN,
		DescriptionEN:    fm.DescriptionEN,
	}
	if info, err := os.Stat(path); err == nil {
		p.CreatedAt = info.ModTime()
		p.UpdatedAt = info.ModTime()
	}
	return p, nil
}

// retiredTools are tool names that no longer exist but may linger in profile
// files written when they did. Parsing strips them so an old profile keeps
// loading — and, crucially, keeps SAVING: the API layer rejects unknown tool
// names, so a name left in place would 400 the next edit of an otherwise
// valid expert.
//
//   - enable_own_skill let an expert add skills to its own profile; removed
//     when experts became configurable only by the Default agent. Its own
//     forks (~/.octo/agents overrides) are exactly the files that carry it.
var retiredTools = map[string]bool{
	"enable_own_skill": true,
}

// stripRetiredTools returns tools without any retired names, preserving order.
// Nil in, nil out — an absent list keeps meaning "builtin: all, expert: none".
// (A list that held ONLY retired names comes back empty but non-nil; runtime
// filtering treats both as "no tools" for an expert, so nothing changes for
// the author beyond losing the retired grant.)
func stripRetiredTools(tools []string) []string {
	kept := tools[:0:0]
	for _, t := range tools {
		if !retiredTools[t] {
			kept = append(kept, t)
		}
	}
	return kept
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
		ChannelBindings: p.ChannelBindings,

		Category:         p.Category,
		Tags:             p.Tags,
		TagsEN:           p.TagsEN,
		ExamplePrompts:   p.ExamplePrompts,
		ExamplePromptsEN: p.ExamplePromptsEN,
		Icon:             p.Icon,
		NameEN:           p.NameEN,
		DescriptionEN:    p.DescriptionEN,
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

// maxSystemPrompt mirrors the design doc's 10000-char body limit (counted in
// runes, not bytes); enforced by validateForWrite before any file is written.
const maxSystemPrompt = 10000

// validateForWrite is the Store-level write gate: schema validation plus the
// system prompt length cap.
func validateForWrite(p *Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if utf8.RuneCountInString(p.SystemPrompt) > maxSystemPrompt {
		return fmt.Errorf("system_prompt too long: %d chars (max %d)",
			utf8.RuneCountInString(p.SystemPrompt), maxSystemPrompt)
	}
	return nil
}
