package memory

// injector.go turns parsed Rules into the per-turn reminder that cmd/octo
// prepends to each user message. The reminder rides the message stream, not
// the system prompt, so the cached prompt prefix stays byte-stable across the
// session. Always-apply rules are restated every turn; triggered rules surface
// only when user input hits one of their keywords, and at most once per session.
//
// The injector also carries the save-nudge: a one-shot reminder appended to a
// tool result when the session just did something milestone-shaped (a PR was
// created or merged), prompting the agent to record durable decisions in its
// memory directory while the moment is still in front of it.

import (
	"context"
	"regexp"
	"strings"

	"github.com/Leihb/octo-agent/internal/hooks"
)

// Injector holds session-scoped recall state for a parsed rule set.
type Injector struct {
	rules    *Rules
	recalled map[string]bool // triggered rules already surfaced this session, keyed by text
	nudged   bool            // save-nudge already emitted this turn; reset on the next user input
}

// NewInjector builds an injector over the given rules. rules may be nil or
// empty — the injector then serves only the save-nudge.
func NewInjector(rules *Rules) *Injector {
	return &Injector{rules: rules, recalled: make(map[string]bool)}
}

// Reminder returns the memory reminder to prepend to a user message, or "" when
// there is nothing to surface this turn. It combines the always-apply tier
// (every turn) with any newly-triggered rules matched against userInput.
func (in *Injector) Reminder(userInput string) string {
	if in == nil {
		return ""
	}
	// A new user turn re-arms the save-nudge: at most one nudge per turn, not
	// one per session — a long session can hit several milestones.
	in.nudged = false
	if in.rules == nil {
		return ""
	}

	var fresh []Rule
	for _, r := range in.rules.Triggered {
		if in.recalled[r.Text] {
			continue
		}
		if matchesAny(userInput, r.Triggers) {
			in.recalled[r.Text] = true
			fresh = append(fresh, r)
		}
	}

	if len(in.rules.Always) == 0 && len(fresh) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("Reminders from your project memory. Follow these as standing guidance for this session — they record the user's durable preferences and workflow rules, the way project conventions do. They are records, not the user's current message: if one conflicts with what the user just asked or with safety, the current request and safety win.\n")
	if len(in.rules.Always) > 0 {
		b.WriteString("\nAlways apply:\n")
		for _, r := range in.rules.Always {
			b.WriteString("- ")
			b.WriteString(r.Text)
			b.WriteByte('\n')
		}
	}
	if len(fresh) > 0 {
		b.WriteString("\nRelevant to what you're about to do:\n")
		for _, r := range fresh {
			b.WriteString("- ")
			b.WriteString(r.Text)
			b.WriteByte('\n')
		}
	}
	b.WriteString("</system-reminder>")
	return b.String()
}

// matchesAny reports whether userInput hits any of the triggers.
func matchesAny(userInput string, triggers []string) bool {
	for _, t := range triggers {
		if triggerHit(userInput, t) {
			return true
		}
	}
	return false
}

// triggerHit reports whether trigger occurs in input. The match is
// conservative — the only direction checked is "input contains trigger":
//   - ASCII triggers ("deploy") must appear on word boundaries, so "deploy"
//     does not fire on "deployment" or "redeploy".
//   - CJK triggers ("部署") match as a plain substring, since curated multi-rune
//     phrases rarely collide and CJK has no whitespace word boundaries.
//
// It never matches in the reverse direction (trigger contains input), which is
// what made the earlier substring-explosion matcher fire on almost anything.
func triggerHit(input, trigger string) bool {
	t := strings.TrimSpace(strings.ToLower(trigger))
	if t == "" {
		return false
	}
	in := strings.ToLower(input)
	if isASCII(t) {
		return wordBoundaryContains(in, t)
	}
	return strings.Contains(in, t)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// wordBoundaryContains reports whether sub appears in s flanked by non-word
// bytes (or string boundaries). sub is assumed ASCII; non-ASCII bytes in s
// (e.g. CJK) count as boundaries, so "用deploy部署" still matches "deploy".
func wordBoundaryContains(s, sub string) bool {
	for from := 0; from+len(sub) <= len(s); {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return false
		}
		i += from
		startOK := i == 0 || !isWordByte(s[i-1])
		end := i + len(sub)
		endOK := end == len(s) || !isWordByte(s[end])
		if startOK && endOK {
			return true
		}
		from = i + 1
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// milestoneCommand matches terminal commands that mark a unit of work landing:
// a PR created or merged via the gh CLI. Deliberately narrow — a noisy nudge
// trains the model to ignore it.
var milestoneCommand = regexp.MustCompile(`(^|[^[:alnum:]_])gh\s+pr\s+(create|merge)\b`)

// saveNudgeText is appended to the milestone tool call's result, so the model
// reads it in the same turn the milestone happened, not next session.
const saveNudgeText = "<system-reminder>\n" +
	"A pull request was just created or merged. If this work settled anything durable — a decision (especially an approach ruled OUT), a milestone, or a constraint future sessions must respect — record it in your memory directory now, per the Memory section of your instructions. The diff and git log already hold WHAT changed; memory is for the why, the alternatives rejected, and the don't-redo-this. If nothing here is durable, carry on.\n" +
	"</system-reminder>"

// SaveNudge backs the PostToolUse hook (see RegisterHooks): it returns the
// save-nudge reminder when toolName/input is a milestone-shaped terminal
// command, at most once per
// user turn (Reminder re-arms it). It is called serially from the agent run
// loop, so the latch needs no locking.
func (in *Injector) SaveNudge(toolName string, input map[string]any) string {
	if in == nil || in.nudged || toolName != "terminal" {
		return ""
	}
	cmd, _ := input["command"].(string)
	if !milestoneCommand.MatchString(cmd) {
		return ""
	}
	in.nudged = true
	return saveNudgeText
}

// RegisterHooks wires the injector into a hook engine as in-process hooks: the
// per-turn reminder on UserPromptSubmit and the milestone save-nudge on
// PostToolUse. This replaces the old direct assignment to the Agent's
// single-slot UserInputHook/ToolResultHook, so the memory reminders flow through
// the same dispatch path as shell hooks. The injector's per-session latches
// (recall map, nudge flag) live on the receiver, so each session registers its
// own injector on its own engine.
func (in *Injector) RegisterHooks(e *hooks.Engine) {
	if in == nil || e == nil {
		return
	}
	e.RegisterInProc(hooks.EventUserPromptSubmit, func(_ context.Context, p hooks.Payload) string {
		return in.Reminder(p.UserInput)
	})
	e.RegisterInProc(hooks.EventPostToolUse, func(_ context.Context, p hooks.Payload) string {
		return in.SaveNudge(p.ToolName, p.ToolInput)
	})
}
