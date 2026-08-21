package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/open-octo/octo-agent/internal/agent"
)

// Asker presents a structured question set to the user and waits for their
// answer. Implementations live in cmd/octo (the REPL prompt reader) and
// internal/server (web + IM); tests substitute fakes. Like Spawner, this
// interface stays free of the stdin/terminal mechanics so the tools package
// doesn't depend on cmd/octo.
type Asker interface {
	Ask(ctx context.Context, q AskRequest) (AskResponse, error)
}

// AskRequest is the question set, parsed from the tool input. Up to four
// questions are asked in one call; surfaces render them as tabs with a final
// review/submit step rather than a forced sequence.
type AskRequest struct {
	Questions []AskQuestion
}

// AskQuestion is one question in the set.
type AskQuestion struct {
	// Question is the prompt shown to the user, complete with punctuation.
	Question string

	// Header labels this question's tab. Short (~12 chars); the model is
	// required to supply one, and an empty value falls back to "Q1"/"Q2".
	Header string

	// MultiSelect lets the user pick more than one option. Previews and
	// notes are single-select only, mirroring Claude Code.
	MultiSelect bool

	// Options are the choices. The model must supply 2-4; the internal
	// secret-collection path passes none, which renders as a bare input.
	Options []AskOption
}

// AskOption is one choice. Label and Description stay separate so surfaces
// can render the label prominently with its description beneath, instead of
// receiving one pre-folded string.
type AskOption struct {
	Label       string
	Description string

	// Preview is a multi-line artifact worth comparing side by side (a
	// mockup, a snippet, a config variant). A question with any preview is
	// rendered in the two-column preview layout instead of the flat list.
	Preview string
}

// AskOutcome is how the user left the picker. Three outcomes, not two: a
// user who answers one question and then dismisses the picker has withdrawn,
// and reporting that one answer as a success is how a model ends up acting on
// a decision the user backed out of.
type AskOutcome int

const (
	// AskSubmitted: the user submitted the answer set (possibly partial).
	AskSubmitted AskOutcome = iota
	// AskClarify: the user chose "Chat about this" — they want to talk
	// rather than pick. Answers given so far ride along in the result, and
	// nothing waits for a follow-up message: the turn continues, the model
	// asks what they want to clarify, and their next message is an ordinary
	// turn.
	AskClarify
	// AskRejected: the user dismissed the picker. Answers are discarded.
	AskRejected
)

// AskResponse is what the user did.
type AskResponse struct {
	Outcome AskOutcome

	// Answers is parallel to AskRequest.Questions. A zero-valued entry means
	// the user submitted without answering that question.
	Answers []AskAnswer
}

// AskAnswer is one question's answer.
type AskAnswer struct {
	// Choices are the labels the user selected.
	Choices []string

	// Custom is the free text typed into the "Other" row (flat layout only).
	Custom string

	// Notes is a note the user attached to the question (preview layout
	// only). Claude Code shares one input slot between Other and notes, so a
	// question there can never carry both; keeping them separate is harmless
	// but widens what the label check below must handle.
	Notes string

	// Preview is the chosen option's preview, echoed back so the model sees
	// what the user actually picked. Filled by Execute from the request —
	// askers never populate it, which keeps a large preview off the wire.
	Preview string
}

// notesOnlyAnswer marks a question the user annotated without selecting an
// option. It renders as "(no option selected)" and does not push the result
// into the "user answered" phrasing.
const notesOnlyAnswer = "(notes only)"

// maxPreviewChars caps an echoed preview. A preview is a comparison aid, not
// a payload: past this size it would ride the WebSocket frame, wreck a TUI
// frame, and land verbatim in the tool result.
const maxPreviewChars = 2000

// previewWithheld replaces an over-long preview, for display and for the
// echo-back alike.
const previewWithheld = "(preview cannot be shown in full — compare the option labels and descriptions instead)"

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

// AskUserQuestionTool lets the model ask the user up to four structured
// clarifying questions in one call. The point isn't to chat with the user —
// it's to resolve a branch where the model genuinely doesn't have enough
// information to pick a default and asking via free-form prose would produce
// a sloppy, hard-to-parse answer.
//
// The schema and the validation mirror Claude Code's AskUserQuestion: the
// shape the model was trained to emit and the shape we advertise are the
// same, and a malformed call is refused the way Claude Code's host refuses it
// — including telling the model NOT to retry a single-option question rather
// than inviting it to invent a filler choice.
type AskUserQuestionTool struct{}

func (AskUserQuestionTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "ask_user_question",
		Description: "Ask the user up to four structured clarifying questions and wait for their answers. " +
			"Use this only when you're blocked on a decision that is genuinely the user's to make: one you " +
			"cannot resolve from the request, the code, or sensible defaults — preferences (\"which library?\"), " +
			"trade-offs (\"prioritize speed or readability?\"), or scope (\"include the migration too?\"). " +
			"Don't use it for information you could find in the repo, or for choices with a conventional " +
			"default — pick the obvious option, say so, and continue. " +
			"Every question needs 2-4 mutually exclusive options; if you recommend one, make it the first " +
			"option and add \"(Recommended)\" to its label. Don't add an \"Other\" entry — the user always " +
			"gets one, plus a way to reply in prose instead of picking. " +
			"Set multiSelect=true when the choices are NOT mutually exclusive and the user may pick several. " +
			"Use `preview` only when presenting concrete artifacts the user needs to compare side by side " +
			"(ASCII mockups of a layout, code snippets of competing implementations, config or diagram " +
			"variants) — not for simple preference questions, where labels and descriptions suffice. " +
			"Previews are single-select only.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"minItems":    1,
					"maxItems":    4,
					"description": "The questions to ask (1-4). Question texts must be unique.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{
								"type":        "string",
								"description": "The complete question, with punctuation. Should be ONE sentence asking ONE thing. Do not pack multiple questions (\"1) … 2) …\") into a single string — add another entry to `questions` instead. If you need more context, put it in the option labels and descriptions.",
							},
							"header": map[string]any{
								"type":        "string",
								"description": "Very short label for this question's tab (max 12 chars). Examples: \"Auth method\", \"Scope\", \"Library\".",
							},
							"multiSelect": map[string]any{
								"type":        "boolean",
								"description": "Set true when the choices are NOT mutually exclusive (e.g. \"which features should we enable?\"). The user can then pick more than one. Previews are ignored for multi-select questions.",
							},
							"options": map[string]any{
								"type":        "array",
								"minItems":    2,
								"maxItems":    4,
								"description": "2-4 distinct choices. Labels must be unique within the question. A question with only one option has no decision in it — don't ask it.",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"label": map[string]any{
											"type":        "string",
											"description": "The choice, as a complete label (\"OAuth with PKCE\" not just \"OAuth\"). Concise — 1-5 words.",
										},
										"description": map[string]any{
											"type":        "string",
											"description": "What this option means or what happens if chosen — trade-offs and implications. Shown beneath the label.",
										},
										"preview": map[string]any{
											"type":        "string",
											"description": "Concrete artifact for this option, rendered in a monospace pane beside the choices: an ASCII mockup, a code snippet, a config or diagram variant. Multi-line text is supported. Only use it when the user needs to compare artifacts, and only on single-select questions.",
										},
									},
									"required": []string{"label"},
								},
							},
						},
						"required": []string{"question", "header", "options"},
					},
				},
			},
			"required": []string{"questions"},
		},
	}
}

// Steering messages for a malformed call. These deliberately don't read like
// field-validation errors: a model that genuinely has one path cannot invent
// a second option, so "options must have 2-4 entries" would push it into
// filler choices or a retry loop. Claude Code's wording tells it to stop and
// proceed instead, and that is the recovery we want too.
const (
	steerTooFewOptions = "This call included a question with fewer than 2 options, so it was rejected and the person never saw it. " +
		"A question with a single option has no decision in it. Do not retry this call and do not invent a filler second option. " +
		"Instead, state the one path you were going to offer as the approach you are taking, then continue with the task. " +
		"If this call also contained questions with 2 to 4 options (each with distinct labels), you may re-ask those questions alone in a new call. " +
		"Ask a question only when the person has at least two genuinely distinct choices."

	steerTooManyOptions = "This call included a question with more than 4 options, so it was rejected and the person never saw it. " +
		"Do not retry this call verbatim. Re-ask with at most 4 options, keeping the choices that actually differ and folding the rest away — " +
		"the person always gets a free-text option, so a long tail of near-duplicates buys nothing."

	steerTooManyQuestions = "This call included more than 4 questions, so it was rejected and the person never saw it. " +
		"Do not retry this call verbatim. Ask the 4 that most change what you do next, then ask again later if the answers make more questions necessary."

	steerNotUnique = "This call was rejected and the person never saw it: question texts must be unique, and option labels must be unique within each question. " +
		"Re-ask with distinct wording so each choice is identifiable."
)

func (AskUserQuestionTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	asker := askerFrom(ctx)
	if asker == nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("ask_user_question: not available in this mode (REPL only)")
	}
	req, err := parseAskRequest(input)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("ask_user_question: %w", err)
	}

	res, err := asker.Ask(ctx, req)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("ask_user_question: %w", err)
	}
	fillPreviews(req, &res)
	return agent.ToolResult{Text: formatAskResponse(req, res)}, nil
}

// parseAskRequest reads the question set and enforces what Claude Code's host
// enforces before the picker ever renders. `questions` is the only accepted
// shape — there is no flat-shape fallback.
func parseAskRequest(input map[string]any) (AskRequest, error) {
	raw, ok := input["questions"].([]any)
	if !ok || len(raw) == 0 {
		return AskRequest{}, fmt.Errorf("questions is required: an array of 1-4 {question, header, options} objects")
	}
	if len(raw) > 4 {
		return AskRequest{}, fmt.Errorf("%s", steerTooManyQuestions)
	}

	seenQuestions := make(map[string]bool, len(raw))
	out := make([]AskQuestion, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return AskRequest{}, fmt.Errorf("each entry in questions must be an object")
		}
		text := strings.TrimSpace(stringArg(obj, "question"))
		if text == "" {
			return AskRequest{}, fmt.Errorf("each question needs a question string")
		}
		if seenQuestions[text] {
			return AskRequest{}, fmt.Errorf("%s", steerNotUnique)
		}
		seenQuestions[text] = true

		opts, err := parseAskOptions(obj["options"])
		if err != nil {
			return AskRequest{}, err
		}
		out = append(out, AskQuestion{
			Question:    text,
			Header:      strings.TrimSpace(stringArg(obj, "header")),
			MultiSelect: askBool(obj, "multiSelect") || askBool(obj, "multi_select"),
			Options:     opts,
		})
	}
	return AskRequest{Questions: out}, nil
}

// parseAskOptions reads one question's options. The schema asks for objects
// with label/description/preview; a bare string is still read as a label so a
// model that sends `["A","B"]` gets its options rendered rather than a
// confusing "no options" rejection.
func parseAskOptions(raw any) ([]AskOption, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s", steerTooFewOptions)
	}
	out := make([]AskOption, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		var opt AskOption
		switch v := item.(type) {
		case string:
			opt.Label = strings.TrimSpace(v)
		case map[string]any:
			opt.Label = strings.TrimSpace(stringArg(v, "label"))
			opt.Description = strings.TrimSpace(stringArg(v, "description"))
			opt.Preview = stringArg(v, "preview")
		default:
			continue
		}
		if opt.Label == "" {
			continue
		}
		opt.Label = sanitizeLabel(opt.Label)
		if seen[opt.Label] {
			return nil, fmt.Errorf("%s", steerNotUnique)
		}
		seen[opt.Label] = true
		if len([]rune(opt.Preview)) > maxPreviewChars {
			opt.Preview = previewWithheld
		}
		out = append(out, opt)
	}
	switch {
	case len(out) < 2:
		return nil, fmt.Errorf("%s", steerTooFewOptions)
	case len(out) > 4:
		return nil, fmt.Errorf("%s", steerTooManyOptions)
	}
	return out, nil
}

// sanitizeLabel replaces control characters so a label can't corrupt a TUI
// frame or a chat message. Tabs and newlines have no place in a one-line
// choice either, so they go too.
func sanitizeLabel(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}

// HeaderOrDefault is the tab label for question i: the model-supplied header,
// or "Q1"/"Q2" when it sent an empty one. Surfaces share this so a blank
// header renders identically everywhere.
func (q AskQuestion) HeaderOrDefault(i int) string {
	if q.Header != "" {
		return q.Header
	}
	return fmt.Sprintf("Q%d", i+1)
}

// fillPreviews copies the chosen option's preview into the answer. Only
// single-select questions carry previews, and only a lone selection maps to
// one option, so nothing else is filled.
func fillPreviews(req AskRequest, res *AskResponse) {
	if res.Outcome == AskRejected {
		return
	}
	for i := range res.Answers {
		if i >= len(req.Questions) {
			break
		}
		q := req.Questions[i]
		a := &res.Answers[i]
		if q.MultiSelect || len(a.Choices) != 1 {
			continue
		}
		for _, opt := range q.Options {
			if opt.Label == a.Choices[0] {
				a.Preview = opt.Preview
				break
			}
		}
	}
}

// Result texts. formatAskResponse reproduces Claude Code's mapper: a clarify
// instruction, a rejection, or one of three submit phrasings.
const (
	clarifyPreamble = "The user wants to clarify these questions.\n" +
		"This means they may have additional information, context or questions for you.\n" +
		"Take their response into account and then reformulate the questions if appropriate.\n" +
		"Start by asking them what they would like to clarify.\n\n" +
		"Questions asked:"

	rejectedText = "The user doesn't want to proceed with this tool use. The tool use was rejected. " +
		"STOP what you are doing and wait for the user to tell you how to proceed."

	answeredPrefix = "Your questions have been answered: "
	answeredSuffix = ". You can now continue with these answers in mind."

	respondedPrefix = "The user answered: "
	respondedSuffix = ". Read the answers carefully — they may request clarification, changes, " +
		"or that you not proceed — and follow what they actually say."

	unansweredText = "The user did not answer the questions."
)

// formatAskResponse turns the structured reply into the text the LLM reads as
// its tool_result. It needs the request as well as the response: the segments
// are keyed by question text, and choosing between the two submit phrasings
// requires each question's declared label set.
func formatAskResponse(req AskRequest, res AskResponse) string {
	switch res.Outcome {
	case AskRejected:
		return rejectedText
	case AskClarify:
		return formatClarify(req, res)
	}

	var segments []string
	clean := true
	for i, q := range req.Questions {
		a := answerAt(res, i)
		seg, ok := answerSegment(q, a)
		if ok {
			segments = append(segments, seg)
		}
		if !answerIsPlainSelection(q, a) {
			clean = false
		}
	}
	if len(segments) == 0 {
		return unansweredText
	}
	joined := strings.Join(segments, ", ")
	if clean {
		return answeredPrefix + joined + answeredSuffix
	}
	return respondedPrefix + joined + respondedSuffix
}

// formatClarify lists every question with whatever the user had supplied
// before choosing "Chat about this", so the model can reformulate instead of
// starting over.
func formatClarify(req AskRequest, res AskResponse) string {
	var b strings.Builder
	b.WriteString(clarifyPreamble)
	for i, q := range req.Questions {
		a := answerAt(res, i)
		fmt.Fprintf(&b, "\n- %q", q.Question)
		if ans := answerText(a); ans != "" {
			fmt.Fprintf(&b, "\n  Answer: %s", ans)
		} else {
			b.WriteString("\n  (No answer provided)")
		}
		if a.Notes != "" {
			fmt.Fprintf(&b, "\n  User notes: %s", a.Notes)
		}
	}
	return b.String()
}

// answerSegment renders one question's segment, reporting false when the
// question carries neither an answer nor a note and is therefore omitted.
func answerSegment(q AskQuestion, a AskAnswer) (string, bool) {
	ans := answerText(a)
	if ans == "" && a.Notes == "" {
		return "", false
	}
	seg := fmt.Sprintf("%q=%q", q.Question, ans)
	if ans == "" || ans == notesOnlyAnswer {
		seg = fmt.Sprintf("%q=(no option selected)", q.Question)
	}
	parts := []string{seg}
	if a.Preview != "" {
		parts = append(parts, "selected preview:\n"+a.Preview)
	}
	if a.Notes != "" {
		parts = append(parts, "notes: "+a.Notes)
	}
	return strings.Join(parts, " "), true
}

// answerText is the user's answer as one string: selected labels, with any
// free text appended as a further entry (Claude Code concatenates it into the
// answer array the same way).
func answerText(a AskAnswer) string {
	parts := make([]string, 0, len(a.Choices)+1)
	for _, c := range a.Choices {
		if c = strings.TrimSpace(c); c != "" {
			parts = append(parts, c)
		}
	}
	if c := strings.TrimSpace(a.Custom); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

// answerIsPlainSelection reports whether this answer is "just a pick" — every
// selected label is one the model declared, with no free text and no notes.
// One impure answer switches the whole result to the cautioning phrasing,
// because a user who typed rather than picked often typed a correction.
//
// An unanswered question passes: skipping a question is not the same as
// answering it in prose.
func answerIsPlainSelection(q AskQuestion, a AskAnswer) bool {
	if a.Notes != "" || strings.TrimSpace(a.Custom) != "" {
		return false
	}
	if len(a.Choices) == 0 {
		return true
	}
	if !q.MultiSelect && len(a.Choices) > 1 {
		return false
	}
	declared := make(map[string]bool, len(q.Options))
	for _, opt := range q.Options {
		declared[opt.Label] = true
	}
	for _, c := range a.Choices {
		if !declared[strings.TrimSpace(c)] {
			return false
		}
	}
	return true
}

func answerAt(res AskResponse, i int) AskAnswer {
	if i < len(res.Answers) {
		return res.Answers[i]
	}
	return AskAnswer{}
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
