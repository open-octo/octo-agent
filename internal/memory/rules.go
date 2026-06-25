package memory

// rules.go adds an actionable, attention-aware layer on top of the plain
// MEMORY.md injection. MEMORY.md may carry two optional structured sections:
//
//	## 必须遵守        (always-apply rules; restated near every user turn)
//	## 触发提醒        (rules recalled only when user input hits a keyword)
//
// Rules in these sections are written in full (not as pointer links), so the
// binding instruction is present at the point of action rather than one
// read_file away. Everything else in MEMORY.md stays a concise pointer index
// and is unaffected. A MEMORY.md with neither section parses to zero rules and
// the per-turn reminder is silent — fully backward-compatible.

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Lint inspects a memory dir's MEMORY.md for problems that silently degrade
// recall, returning human-readable warnings (nil when clean):
//   - the index exceeds the injection budget, so entries past the cut are not
//     loaded into the session;
//   - a bullet under a 触发提醒 (triggered) section has no parseable
//     "(触发: …)" clause, so it can never be recalled — the most common
//     silent failure, since the rule still looks present in the file.
//
// It lints the injected view (truncated to the budget), matching what is
// actually active this session.
func Lint(dir string) []string {
	raw, truncated := loadIndex(dir)
	var warns []string
	if truncated {
		warns = append(warns, "MEMORY.md exceeds the injection budget (200 lines / 25KB) — entries past the cut are not loaded. Prune it or move detail into topic files.")
	}
	for _, r := range parseRules(raw).Triggered {
		if len(r.Triggers) == 0 {
			warns = append(warns, fmt.Sprintf("rule under 触发提醒 has no (触发: …) clause and will never be recalled — add one or move it to 必须遵守: %q", oneLine(r.Text)))
		}
	}
	return warns
}

// oneLine collapses a rule to a short single-line form for warning output.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 60 {
		s = truncateRunes(s, 57) + "…"
	}
	return s
}

// truncateRunes returns s truncated to at most n runes without splitting a
// multi-byte UTF-8 character (byte slicing CJK runes produces "�").
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Rule is one actionable memory item parsed from MEMORY.md's structured
// sections. Text is the full rule; Triggers is empty for always-apply rules.
type Rule struct {
	Text     string
	Triggers []string
}

// Rules holds the two actionable tiers parsed from MEMORY.md.
type Rules struct {
	Always    []Rule // restated every turn
	Triggered []Rule // recalled when user input matches a trigger
}

// HasAny reports whether there is anything to inject.
func (r *Rules) HasAny() bool { return len(r.Always) > 0 || len(r.Triggered) > 0 }

// Merge merges other into r (in-place). Duplicate rules (by Text) are skipped
// so project rules take precedence over inherited ones. De-duplication is
// cross-section: if a project Triggered rule and an inherited Always rule share
// the same text, the project rule wins.
func (r *Rules) Merge(other *Rules) {
	if other == nil {
		return
	}
	seen := make(map[string]bool)
	for _, rule := range r.Always {
		seen[rule.Text] = true
	}
	for _, rule := range r.Triggered {
		seen[rule.Text] = true
	}
	for _, rule := range other.Always {
		if !seen[rule.Text] {
			r.Always = append(r.Always, rule)
			seen[rule.Text] = true
		}
	}
	for _, rule := range other.Triggered {
		if !seen[rule.Text] {
			r.Triggered = append(r.Triggered, rule)
			seen[rule.Text] = true
		}
	}
}

// section classifies a markdown heading into one of the actionable tiers.
type section int

const (
	sectionNone section = iota
	sectionAlways
	sectionTriggered
)

// bulletPattern matches a list item: "- text" or "* text" (any indent).
var bulletPattern = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)

// triggerClausePattern pulls a leading "(触发: a, b)" / "(triggers: a|b)"
// clause off a triggered-section bullet. Group 1 is the raw keyword list,
// group 2 is the remaining rule text. Half- and full-width parens/colons are
// both accepted so the agent can write whichever it reaches for.
var triggerClausePattern = regexp.MustCompile(`(?i)^[（(]\s*(?:触发词?|triggers?)\s*[:：]\s*([^)）]*)[)）]\s*(.*)$`)

// ParseRules reads MEMORY.md (within the same injection budget as the static
// injection) and extracts the always-apply and triggered rule tiers.
func ParseRules(dir string) *Rules {
	return parseRules(LoadIndex(dir))
}

func parseRules(raw string) *Rules {
	r := &Rules{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), maxInjectBytes+1024)

	cur := sectionNone
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			cur = classifyHeading(line)
			continue
		}
		if cur == sectionNone {
			continue
		}
		m := bulletPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		body := strings.TrimSpace(m[1])
		if body == "" {
			continue
		}
		switch cur {
		case sectionAlways:
			r.Always = append(r.Always, Rule{Text: body})
		case sectionTriggered:
			r.Triggered = append(r.Triggered, parseTriggeredBullet(body))
		}
	}
	return r
}

// classifyHeading maps a heading line to a section by keyword, tolerant of
// emoji/level prefixes. Unrecognized headings end any open rules section.
func classifyHeading(line string) section {
	h := strings.ToLower(line)
	switch {
	case strings.Contains(h, "必须遵守") || strings.Contains(h, "always"):
		return sectionAlways
	case strings.Contains(h, "触发") || strings.Contains(h, "trigger"):
		return sectionTriggered
	default:
		return sectionNone
	}
}

// parseTriggeredBullet splits "(触发: a, b) rule text" into triggers + text.
// A bullet without a trigger clause yields a rule with no triggers, which is
// never recalled — the parser keeps it (so the text isn't lost) rather than
// guessing keywords.
func parseTriggeredBullet(body string) Rule {
	m := triggerClausePattern.FindStringSubmatch(body)
	if m == nil {
		return Rule{Text: body}
	}
	var triggers []string
	for _, part := range strings.FieldsFunc(m[1], func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == '|'
	}) {
		if t := strings.TrimSpace(part); t != "" {
			triggers = append(triggers, t)
		}
	}
	return Rule{Text: strings.TrimSpace(m[2]), Triggers: triggers}
}
