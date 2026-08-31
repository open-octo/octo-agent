package agentprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleProfileMD = `---
name: 代码审查专家
description: 专责 review PR
model: claude-sonnet-4-20250514
tools: [read_file, grep, glob]
tool_skills: [code-review]
read_only: true
channel_bindings:
  - {platform: weixin, chat_id: dev-group-1}
---
你是代码审查专家。

逐行审查，给出 file:line。
`

func writeMD(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code-review.md")
	writeMD(t, dir, "code-review.md", sampleProfileMD)

	p, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile() error = %v", err)
	}
	if p.Name != "代码审查专家" || p.Description != "专责 review PR" {
		t.Fatalf("identity fields = %q / %q", p.Name, p.Description)
	}
	if p.Model != "claude-sonnet-4-20250514" || !p.ReadOnly {
		t.Fatalf("capability fields = %+v", p.CapabilitySpec)
	}
	if len(p.Tools) != 3 || len(p.ToolSkills) != 1 || p.ToolSkills[0] != "code-review" {
		t.Fatalf("tools = %v skills = %v", p.Tools, p.ToolSkills)
	}
	if len(p.ChannelBindings) != 1 || p.ChannelBindings[0].Platform != "weixin" {
		t.Fatalf("bindings = %+v", p.ChannelBindings)
	}
	if !strings.Contains(p.SystemPrompt, "你是代码审查专家。") || !strings.Contains(p.SystemPrompt, "逐行审查") {
		t.Fatalf("system prompt body = %q", p.SystemPrompt)
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should come from file mtime")
	}
}

func TestParseFileModelInherit(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, dir, "x.md", "---\ndescription: d\nmodel: inherit\n---\nbody\n")
	p, err := parseFile(filepath.Join(dir, "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Model != "" {
		t.Fatalf("model inherit should become empty, got %q", p.Model)
	}
}

func TestParseFileStripsRetiredTools(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, dir, "x.md", "---\ndescription: d\ntools: [skill, enable_own_skill, terminal]\n---\nbody\n")
	p, err := parseFile(filepath.Join(dir, "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"skill", "terminal"}
	if len(p.Tools) != len(want) {
		t.Fatalf("Tools = %v, want %v", p.Tools, want)
	}
	for i := range want {
		if p.Tools[i] != want[i] {
			t.Fatalf("Tools = %v, want %v", p.Tools, want)
		}
	}

	// A profile that never listed tools keeps nil — for a builtin that means
	// "all tools", and stripping must not turn it into "no tools".
	writeMD(t, dir, "y.md", "---\ndescription: d\n---\nbody\n")
	p, err = parseFile(filepath.Join(dir, "y.md"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Tools != nil {
		t.Fatalf("Tools = %v, want nil", p.Tools)
	}
}

func TestParseFileErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no-frontmatter": "just a body\n",
		"no-description": "---\nname: x\n---\nbody\n",
		"bad-yaml":       "---\n: : :\n---\nbody\n",
	}
	for name, content := range cases {
		writeMD(t, dir, name+".md", content)
		if _, err := parseFile(filepath.Join(dir, name+".md")); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	orig := &Profile{
		ID:          "code-review",
		Name:        "代码审查专家",
		Description: "专责 review PR",
		CapabilitySpec: CapabilitySpec{
			Model:        "claude-sonnet-4-20250514",
			SystemPrompt: "你是代码审查专家。",
			Tools:        []string{"read_file", "grep"},
			ToolSkills:   []string{"code-review"},
			ReadOnly:     true,
		},
		ChannelBindings: []ChannelBinding{{Platform: "weixin", ChatID: "g1"}},
	}
	b, err := serialize(orig)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeMD(t, dir, "code-review.md", string(b))
	got, err := parseFile(filepath.Join(dir, "code-review.md"))
	if err != nil {
		t.Fatalf("roundtrip parse: %v", err)
	}
	if got.Name != orig.Name || got.Description != orig.Description ||
		got.Model != orig.Model || got.SystemPrompt != orig.SystemPrompt ||
		!got.ReadOnly || len(got.Tools) != 2 || len(got.ToolSkills) != 1 ||
		len(got.ChannelBindings) != 1 {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", got, orig)
	}
}

func TestValidateForWritePromptCap(t *testing.T) {
	p := &Profile{ID: "ok", Description: "d", CapabilitySpec: CapabilitySpec{
		SystemPrompt: strings.Repeat("x", maxSystemPrompt+1),
	}}
	if err := validateForWrite(p); err == nil || !strings.Contains(err.Error(), "system_prompt too long") {
		t.Fatalf("validateForWrite() = %v", err)
	}
	// The cap counts runes: 10000 CJK chars (30KB) must pass.
	p.SystemPrompt = strings.Repeat("审", maxSystemPrompt)
	if err := validateForWrite(p); err != nil {
		t.Fatalf("10000-rune CJK prompt rejected: %v", err)
	}
}
