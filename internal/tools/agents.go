package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Leihb/octo-agent/internal/memory"
	"gopkg.in/yaml.v3"
)

// agentFrontmatter is the subset of an agent definition file's YAML frontmatter
// that we consume. Unmapped keys are ignored.
type agentFrontmatter struct {
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description"`
	ReadOnly        bool     `yaml:"read_only"`
	Tools           []string `yaml:"tools"`
	DisallowedTools []string `yaml:"disallowed_tools"`
	Model           string   `yaml:"model"`
}

// userAgentsRoot returns ~/.octo/agents, or "" when the home dir can't be
// resolved. It's a var so tests can point discovery at a temp directory.
var userAgentsRoot = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octo", "agents")
}

// projectAgentsRoot returns <project-root>/.octo/agents for the current working
// directory's repository, or "" when it can't be resolved. Project-level agents
// override user-level ones of the same name (matching .claude/agents semantics).
// A var so tests can point it at a temp directory.
var projectAgentsRoot = func() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return ""
	}
	root := memory.ProjectRoot(cwd)
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".octo", "agents")
}

// discoveredAgents holds the last scanned user-defined agents.
var (
	discoveredAgentsMu sync.RWMutex
	discoveredAgents   map[string]agentPreset
)

// discoverAgents scans ~/.octo/agents/*.md and populates the package-level
// discoveredAgents cache. It is safe to call concurrently; callers that need
// the freshest set call it before lookupAgentPreset.
func discoverAgents() {
	fresh := make(map[string]agentPreset)
	// User-level first, then project-level — project entries override
	// user-level ones of the same name.
	for _, root := range []string{userAgentsRoot(), projectAgentsRoot()} {
		scanAgentsRoot(root, fresh)
	}
	discoveredAgentsMu.Lock()
	discoveredAgents = fresh
	discoveredAgentsMu.Unlock()
}

// scanAgentsRoot reads *.md agent definitions from root into dst (existing keys
// are overwritten). A missing or unreadable root is a no-op.
func scanAgentsRoot(root string, dst map[string]agentPreset) {
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return // missing/unreadable root: nothing to add
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		p, ok := parseAgentFile(filepath.Join(root, name))
		if !ok {
			continue
		}
		// The file name (without .md) is the authoritative trigger name.
		p.name = strings.TrimSuffix(name, ".md")
		dst[p.name] = p
	}
}

// parseAgentFile reads a single agent definition markdown file. It expects YAML
// frontmatter between `---` fences; the markdown body after the closing fence
// becomes the agent persona. ok is false when the file has no frontmatter or
// the YAML doesn't parse.
func parseAgentFile(path string) (agentPreset, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return agentPreset{}, false
	}
	front, body, ok := splitFrontmatter(string(b))
	if !ok {
		return agentPreset{}, false
	}
	var fm agentFrontmatter
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		return agentPreset{}, false
	}
	if fm.Description == "" {
		return agentPreset{}, false
	}
	// "inherit" (CC's default) means no override — treat it as empty.
	model := strings.TrimSpace(fm.Model)
	if strings.EqualFold(model, "inherit") {
		model = ""
	}
	return agentPreset{
		name:            "", // filled by caller from file name
		description:     fm.Description,
		persona:         strings.TrimSpace(body),
		readOnly:        fm.ReadOnly,
		tools:           fm.Tools,
		disallowedTools: fm.DisallowedTools,
		model:           model,
	}, true
}

// splitFrontmatter returns the text between the opening and closing `---`
// fences and everything after the closing fence. ok is false unless the first
// non-empty content is a `---` line with a matching closing fence.
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
