package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
)

// Asker presents a structured question to the user and waits for their
// answer. Implementations live in cmd/octo (the REPL prompt reader); tests
// substitute fakes. Like Spawner, this interface stays free of the
// stdin/terminal mechanics so the tools package doesn't depend on cmd/octo.
type Asker interface {
	Ask(ctx context.Context, q AskRequest) (AskResponse, error)
}

// AskRequest is the structured question, parsed from the tool input.
type AskRequest struct {
	// Question is the prompt shown to the user, complete with punctuation.
	Question string

	// Options are the mutually-exclusive (or, when MultiSelect, jointly
	// selectable) labels. Empty means "free text answer" (the asker prompts
	// for an open-ended response with no list).
	Options []string

	// MultiSelect lets the user pick more than one option. The asker is
	// responsible for parsing a comma-separated selection and returning all
	// chosen labels.
	MultiSelect bool

	// Header is an optional short tag (≤12 chars) shown in the prompt UI.
	// Helpful when several questions share visual real estate.
	Header string
}

// AskResponse is what the user provided. Cancelled is true when the user
// dismissed the prompt (Ctrl-C, empty enter on a forced-pick, etc.); in
// that case Choices and Custom are empty.
type AskResponse struct {
	// Choices are the labels the user selected from Options. Empty when
	// the user picked "Other" (then Custom carries their free text) or
	// when the question had no Options (free text only).
	Choices []string

	// Custom is the free-text answer for "Other" picks or option-less
	// questions. Empty for plain selections.
	Custom string

	// Cancelled reports user dismissal. The tool surfaces this to the LLM
	// as a non-error result so the model can decide what to do next.
	Cancelled bool
}

// activeAsker, when non-nil, backs the ask_user_question tool and gates its
// advertisement in DefaultTools. Set by the REPL at session start; nil in
// single-turn / unattended modes, where prompting the user is impossible.
var activeAsker Asker

// SetAsker registers the asker the ask_user_question tool delegates to.
// Pass nil to disable (the tool then doesn't appear in DefaultTools).
func SetAsker(a Asker) { activeAsker = a }

func askerEnabled() bool { return activeAsker != nil }

// ctxKeyAsker carries a turn-scoped Asker. The process-global asker is wrong
// for transports that share the process but not the prompt surface: the
// server's global asker broadcasts to browser tabs, which an IM turn doesn't
// have — its questions must go to the chat instead. Same ctx-scoping pattern
// as the sub-agent manager and task store.
type ctxKeyAsker struct{}

// WithAsker stamps a turn-scoped asker that takes precedence over the
// process-global one for the duration of this turn.
func WithAsker(ctx context.Context, a Asker) context.Context {
	return context.WithValue(ctx, ctxKeyAsker{}, a)
}

// askerFrom resolves the asker for this turn: ctx-scoped first, then the
// process-global fallback (CLI/web).
func askerFrom(ctx context.Context) Asker {
	if a, ok := ctx.Value(ctxKeyAsker{}).(Asker); ok && a != nil {
		return a
	}
	return activeAsker
}

// AskUserQuestionTool lets the model ask the user a single structured
// clarifying question. The point isn't to chat with the user — it's to
// resolve a branch where the model genuinely doesn't have enough
// information to pick a default and asking via free-form prose would
// produce a sloppy, hard-to-parse answer.
//
// The declared schema mirrors Claude Code's AskUserQuestion natively — a
// `questions` array of {question, header, multiSelect, options:[{label,
// description}]} — so the shape the model was trained to emit and the shape we
// advertise are the same. That alignment is the point: a flat snake_case schema
// drifted from the model's prior, and it would intermittently revert to the CC
// shape, fail validation, and the prompt would never reach the user (the
// "web modal didn't pop up" bug). We still cap at ONE question per call
// (maxItems:1, per the M11-prep design — simpler REPL/web/IM UX; the model
// fires multiple calls when it needs multiple), and Execute still tolerates the
// old flat shape as a fallback. See normalizeAskInput.
type AskUserQuestionTool struct{}

func (AskUserQuestionTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "ask_user_question",
		Description: "Ask the user one structured clarifying question and wait for their answer. " +
			"Use this when you're at a branch where you genuinely lack the information to pick a " +
			"reasonable default — preferences (\"which library?\"), trade-offs (\"prioritize speed " +
			"or readability?\"), or scope (\"include the migration too?\"). Don't use it for " +
			"information you could find in the repo or for questions you should have an opinion " +
			"on yourself. Pass exactly one question in the `questions` array; fire another call if " +
			"you need to ask more. Each question needs 2-4 mutually exclusive options; set " +
			"multiSelect=true when the choices are NOT mutually exclusive and the user may pick " +
			"several. An \"Other\" tail with a free-text follow-up is always added. Result text is " +
			"shaped like 'User chose: <label>' or, for Other, 'User chose: Other — <free text>'.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    1,
					"description": "The question to ask, as a single-element array. octo asks one question per call — fire another call for the next question.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{
								"type":        "string",
								"description": "The question to ask, complete with punctuation. Should be one sentence; if you need more context, put it in the option labels instead of the question itself.",
							},
							"header": map[string]any{
								"type":        "string",
								"description": "Optional short tag shown in the prompt UI (≤12 chars). Example: 'auth_method', 'scope'.",
							},
							"multiSelect": map[string]any{
								"type":        "boolean",
								"description": "Set true when the choices are NOT mutually exclusive (e.g. \"which features should we enable?\"). The user can then pick more than one.",
							},
							"options": map[string]any{
								"type":     "array",
								"minItems": 2,
								"maxItems": 4,
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"label": map[string]any{
											"type":        "string",
											"description": "The choice, as a complete label (\"OAuth with PKCE\" not just \"OAuth\"). Don't add an \"Other\" entry — one is appended automatically.",
										},
										"description": map[string]any{
											"type":        "string",
											"description": "Optional extra context for this choice, shown alongside the label.",
										},
									},
									"required": []string{"label"},
								},
								"description": "2-4 mutually exclusive choices.",
							},
						},
						"required": []string{"question", "options"},
					},
				},
			},
			"required": []string{"questions"},
		},
	}
}

func (AskUserQuestionTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	asker := askerFrom(ctx)
	if asker == nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("ask_user_question: not available in this mode (REPL only)")
	}
	input = normalizeAskInput(input)
	question := strings.TrimSpace(stringArg(input, "question"))
	if question == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("ask_user_question: question is required")
	}
	options := optionLabels(input["options"])
	if len(options) < 2 || len(options) > 4 {
		return agent.ToolResult{Text: ""}, fmt.Errorf("ask_user_question: options must have 2-4 entries (got %d)", len(options))
	}
	multi := askBool(input, "multi_select") || askBool(input, "multiSelect")
	header := strings.TrimSpace(stringArg(input, "header"))

	res, err := asker.Ask(ctx, AskRequest{
		Question:    question,
		Options:     options,
		MultiSelect: multi,
		Header:      header,
	})
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("ask_user_question: %w", err)
	}
	return agent.ToolResult{Text: formatAskResponse(res)}, nil
}

// formatAskResponse turns the asker's structured reply into the text the LLM
// reads as its tool_result. Three shapes:
//
//	cancelled                  → "(user cancelled)"
//	plain selection(s)         → "User chose: A" or "User chose: A, B"
//	"Other" with free text     → "User chose: Other — <text>"
func formatAskResponse(r AskResponse) string {
	if r.Cancelled {
		return "(user cancelled)"
	}
	if r.Custom != "" && len(r.Choices) == 0 {
		return "User chose: Other — " + r.Custom
	}
	if len(r.Choices) == 0 {
		return "(user cancelled)" // defensive: no choices and no custom → nothing to report
	}
	return "User chose: " + strings.Join(r.Choices, ", ")
}

// normalizeAskInput flattens the question fields into a single map regardless
// of which shape the model used. The declared schema is the nested CC shape
// (a `questions` array wrapping {question, header, options, multiSelect}); the
// old flat shape (top-level question/options/multi_select) is still accepted as
// a fallback for any model that reverts to it. When a `questions` array carries
// entries, promote the first one's fields; we cap at one question, so extra
// entries are dropped — the model is told to fire multiple calls when it needs
// to. A present top-level `question` always wins, so a flat call is left as-is.
func normalizeAskInput(input map[string]any) map[string]any {
	if strings.TrimSpace(stringArg(input, "question")) != "" {
		return input // flat shape — use it directly
	}
	arr, ok := input["questions"].([]any)
	if !ok || len(arr) == 0 {
		return input
	}
	if first, ok := arr[0].(map[string]any); ok {
		return first
	}
	return input
}

// askBool reads a boolean tool argument, tolerating the JSON-string forms
// ("true"/"false") some models emit instead of a real bool.
func askBool(input map[string]any, key string) bool {
	switch v := input[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// optionLabels extracts the option labels from the tool input, tolerating both
// shapes the model emits. The schema asks for an array of strings, but Claude
// models trained on Claude Code's AskUserQuestion habitually send an array of
// {label, description} objects instead; a strict string-only parse drops those
// silently and the tool fails with "got 0 options". So we accept either:
//
//	["A", "B"]                                          → "A", "B"
//	[{"label":"A","description":"short"}, {"label":"B"}] → "A — short", "B"
//
// For object options a non-empty description is folded into the displayed label
// so the extra context the model provided still reaches the user.
func optionLabels(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		// Already-typed []string (e.g. from tests) takes the simple path.
		if ss, ok := raw.([]string); ok {
			out := make([]string, 0, len(ss))
			for _, s := range ss {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		switch v := it.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				out = append(out, s)
			}
		case map[string]any:
			label := strings.TrimSpace(stringArg(v, "label"))
			if label == "" {
				continue
			}
			if desc := strings.TrimSpace(stringArg(v, "description")); desc != "" {
				label += " — " + desc
			}
			out = append(out, label)
		}
	}
	return out
}
