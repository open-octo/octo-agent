// Package agentprofile defines octo's agent profiles — the single schema for
// an agent's capability definition (system prompt, model, tool whitelist,
// skills) plus the platform slice (mention aliases, channel bindings) used
// when a profile is addressed directly by users.
//
// Profiles come from three sources, in increasing precedence:
//
//   - builtin: code-defined (default, explore, general, code-review)
//   - default: ~/.octo/agents-default/<id>.md (curated experts, officially shipped)
//   - user:    ~/.octo/agents/<id>.md       (conversation + delegation modes)
//
// A profile is consumed in two modes:
//
//   - conversation: a persistent channel.Manager session the user talks to
//     directly (IM routing, web sessions, CLI --agent)
//   - delegation: an ephemeral sub_agent run that uses only the capability
//     slice — fresh context, never enters the profile's session pool
//
// The Store is read-through: every read rescans the profile directories, so
// changes made through any path (REST API, Web UI, direct .md edits) take
// effect on the next read. There is no fsnotify watcher and no reload API.
package agentprofile

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Source marks where a profile comes from.
type Source string

const (
	// SourceBuiltin profiles are code-defined (default, explore, general,
	// code-review) and have no .md file.
	SourceBuiltin Source = "builtin"
	// SourceDefault profiles are officially-curated expert personas shipped in
	// the binary and materialized to ~/.octo/agents-default (mirrors
	// internal/skills' "default" source). Unlike SourceBuiltin they ARE
	// surfaced through Store.List()/the REST API — they're user-facing
	// content, not internal capability tiers. Like SourceBuiltin they are
	// read-only: a user file cannot shadow one and the write paths refuse to
	// modify one, so an official expert is identical on every machine and
	// keeps receiving content updates. The user-facing knob is hiding it
	// (SetDisabledDefaults), not editing it.
	SourceDefault Source = "default"
	// SourceUser profiles live in ~/.octo/agents/*.md and support both
	// conversation and delegation modes.
	SourceUser Source = "user"
)

// DefaultID is the reserved ID of the code-defined default agent.
const DefaultID = "default"

// CapabilitySpec is the profile's capability slice: prompt / model / tools /
// skills. It is shared by both run modes; the platform slice (MentionAs,
// ChannelBindings) only applies to conversation mode.
type CapabilitySpec struct {
	Model        string   // frontmatter model; empty = inherit the caller's
	SystemPrompt string   // .md body (never carried in frontmatter)
	Tools        []string // frontmatter tools allowlist; empty → builtin: all, user: none
	ToolSkills   []string // frontmatter tool_skills: skills exposed as tools

	// Delegation refinements, parsed for zero-migration compatibility with
	// the pre-existing .md agent format (internal/tools/agents.go).
	ReadOnly        bool     // frontmatter read_only: strip write-capable tools
	DisallowedTools []string // frontmatter disallowed_tools: subtracted from the tool set
	LeanContext     bool     // frontmatter lean_context: seed with the lean system prompt
}

// ChannelBinding pins a profile to one IM bot+chat combination: messages
// from that adapter in that chat route to this profile.
type ChannelBinding struct {
	Platform  string `yaml:"platform" json:"platform"`
	AdapterID string `yaml:"adapter_id,omitempty" json:"adapter_id,omitempty"`
	ChatID    string `yaml:"chat_id" json:"chat_id"`
}

// Profile describes one agent: identity, capability slice, and platform
// slice. User-level profiles are stored as ~/.octo/agents/<id>.md (Markdown
// body = system prompt, YAML frontmatter = everything else).
type Profile struct {
	ID          string // file-name slug (without .md); fixed name for builtins
	Name        string
	Description string // required, both by validation and the .md parser
	CapabilitySpec

	WorkingDir      string
	ChannelBindings []ChannelBinding // conversation mode only

	// Gallery display metadata for SourceDefault (curated) profiles — kept
	// outside CapabilitySpec so it can never leak into the sub_agent
	// delegation path. Empty/zero for ordinary user profiles, which the
	// gallery UI renders with graceful fallbacks (initials avatar, no
	// tags/example-prompt sections).
	Category         string   // slug, e.g. "content-creation" — frontend maps to a localized chip label
	Tags             []string // 擅长领域 chips (zh)
	TagsEN           []string // en variant
	ExamplePrompts   []string // 试试这样问我 (zh)
	ExamplePromptsEN []string // en variant
	Icon             string   // iconify icon name; empty → initials+hash-color fallback
	NameEN           string   // en display name
	DescriptionEN    string   // en description

	Source Source

	// Timestamps track the profile file's mtime (there is no separate creation
	// record on most filesystems): CreatedAt advances on every rewrite, so it
	// is best-effort "last modified" rather than a true birth time. Empty for
	// profiles predating this field.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsDefault reports whether p is the code-defined default agent.
func (p *Profile) IsDefault() bool { return p != nil && p.ID == DefaultID }

// DefaultProfile returns the built-in default agent: full tool access, system
// prompt assembled by the server (base prompt + onboard soul.md/user.md), no
// .md file. It is the routing fallback and therefore always exists.
func DefaultProfile() *Profile {
	return &Profile{
		ID:          DefaultID,
		Name:        "Default",
		Description: "Default agent with full access",
		Source:      SourceBuiltin,
	}
}

// idRule is the profile ID shape: a lowercase slug usable as a file name.
var idRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// IsValidID reports whether id is a well-formed profile ID slug. Exported for
// the channel manager, which must distinguish a real "<agentID>#" key prefix
// from a chat ID that merely contains '#'.
func IsValidID(id string) bool { return idRule.MatchString(id) }

// Validate checks the fields every write path (REST API, meta-skill, Store)
// must enforce. Model-against-config validation lives in the API layer, which
// is the only place that can see the server config.
func (p *Profile) Validate() error {
	if !idRule.MatchString(p.ID) {
		return fmt.Errorf("invalid id %q: must match [a-z0-9][a-z0-9-]{0,31}", p.ID)
	}
	if strings.TrimSpace(p.Description) == "" {
		return errors.New("description is required")
	}
	if utf8.RuneCountInString(p.Name) > 32 {
		return fmt.Errorf("name too long: %d chars (max 32)", utf8.RuneCountInString(p.Name))
	}
	return nil
}
