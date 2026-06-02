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
	"regexp"
	"strings"
)

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
