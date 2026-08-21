package tools

import (
	"context"
	"strings"
	"testing"
)

// stubAsker records what the tool handed it and replays a canned response.
type stubAsker struct {
	resp    AskResponse
	err     error
	called  bool
	lastReq AskRequest
}

func (s *stubAsker) Ask(_ context.Context, q AskRequest) (AskResponse, error) {
	s.called = true
	s.lastReq = q
	return s.resp, s.err
}

func useAsker(t *testing.T, a Asker) {
	t.Helper()
	SetAsker(a)
	t.Cleanup(func() { SetAsker(nil) })
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// askInput builds a well-formed tool input: the nested `questions` shape is
// the only one accepted.
func askInput(questions ...map[string]any) map[string]any {
	arr := make([]any, 0, len(questions))
	for _, q := range questions {
		arr = append(arr, q)
	}
	return map[string]any{"questions": arr}
}

func question(text string, opts ...any) map[string]any {
	return map[string]any{"question": text, "header": "hdr", "options": opts}
}

func opt(label, desc string) map[string]any {
	return map[string]any{"label": label, "description": desc}
}

func runAsk(t *testing.T, input map[string]any) (string, error) {
	t.Helper()
	res, err := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", input)
	return res.Text, err
}

// The declared schema is Claude Code's: a `questions` array of 1-4 entries,
// each with 2-4 {label, description, preview} options.
func TestAskUserQuestion_Schema(t *testing.T) {
	params := AskUserQuestionTool{}.Definition().Parameters
	props := params["properties"].(map[string]any)
	questions, ok := props["questions"].(map[string]any)
	if !ok {
		t.Fatal("schema must declare a questions array")
	}
	if got := questions["maxItems"]; got != 4 {
		t.Errorf("questions maxItems = %v, want 4", got)
	}
	items := questions["items"].(map[string]any)
	req, _ := items["required"].([]string)
	for _, want := range []string{"question", "header", "options"} {
		found := false
		for _, r := range req {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Errorf("question requires %v, want %q among them", req, want)
		}
	}
	qprops := items["properties"].(map[string]any)
	options := qprops["options"].(map[string]any)
	if got := options["minItems"]; got != 2 {
		t.Errorf("options minItems = %v, want 2", got)
	}
	if got := options["maxItems"]; got != 4 {
		t.Errorf("options maxItems = %v, want 4", got)
	}
	optProps := options["items"].(map[string]any)["properties"].(map[string]any)
	if _, ok := optProps["preview"]; !ok {
		t.Error("option must declare preview — the executor reads it")
	}
}

// Four questions arrive as four questions, with label and description kept
// apart rather than folded into one string.
func TestAskUserQuestion_ParsesQuestionSet(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Answers: []AskAnswer{{Choices: []string{"A"}}}}}
	useAsker(t, stub)

	_, err := runAsk(t, askInput(
		map[string]any{
			"question": "Which auth?", "header": "auth",
			"options": []any{opt("A", "first"), map[string]any{"label": "B", "preview": "mock"}},
		},
		question("Which scope?", opt("X", ""), opt("Y", "")),
	))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(stub.lastReq.Questions) != 2 {
		t.Fatalf("got %d questions, want 2", len(stub.lastReq.Questions))
	}
	q := stub.lastReq.Questions[0]
	if q.Header != "auth" || q.Question != "Which auth?" {
		t.Errorf("question 0 = %+v", q)
	}
	if q.Options[0].Label != "A" || q.Options[0].Description != "first" {
		t.Errorf("option 0 = %+v, want label and description kept apart", q.Options[0])
	}
	if q.Options[1].Preview != "mock" {
		t.Errorf("preview = %q, want it carried through", q.Options[1].Preview)
	}
}

// An empty header still renders as something: Q1/Q2, like Claude Code.
func TestAskUserQuestion_HeaderFallback(t *testing.T) {
	q := AskQuestion{Question: "x"}
	if got := q.HeaderOrDefault(1); got != "Q2" {
		t.Errorf("HeaderOrDefault(1) = %q, want Q2", got)
	}
	q.Header = "scope"
	if got := q.HeaderOrDefault(1); got != "scope" {
		t.Errorf("HeaderOrDefault = %q, want the header", got)
	}
}

// Malformed calls are refused before the user is ever prompted.
func TestAskUserQuestion_Rejections(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{
			// The flat shape is gone: `questions` is the only accepted form.
			name:  "flat shape",
			input: map[string]any{"question": "Which one?", "options": []any{"A", "B"}},
			want:  "questions is required",
		},
		{
			name:  "no questions",
			input: map[string]any{"questions": []any{}},
			want:  "questions is required",
		},
		{
			name: "five questions",
			input: askInput(
				question("a", opt("1", ""), opt("2", "")),
				question("b", opt("1", ""), opt("2", "")),
				question("c", opt("1", ""), opt("2", "")),
				question("d", opt("1", ""), opt("2", "")),
				question("e", opt("1", ""), opt("2", "")),
			),
			want: "more than 4 questions",
		},
		{
			// The steer, not a field error: a model with one path can't
			// invent a second option, so it must be told to proceed instead.
			name:  "one option",
			input: askInput(question("Which one?", opt("Only", ""))),
			want:  "Do not retry this call and do not invent a filler second option",
		},
		{
			name:  "no options",
			input: askInput(map[string]any{"question": "Open ended?", "header": "h"}),
			want:  "fewer than 2 options",
		},
		{
			name: "five options",
			input: askInput(question("Which one?",
				opt("1", ""), opt("2", ""), opt("3", ""), opt("4", ""), opt("5", ""))),
			want: "more than 4 options",
		},
		{
			name: "duplicate questions",
			input: askInput(
				question("Same?", opt("1", ""), opt("2", "")),
				question("Same?", opt("3", ""), opt("4", "")),
			),
			want: "must be unique",
		},
		{
			name:  "duplicate labels",
			input: askInput(question("Which one?", opt("A", ""), opt("A", "second"))),
			want:  "must be unique",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubAsker{}
			useAsker(t, stub)
			_, err := runAsk(t, tc.input)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
			if stub.called {
				t.Error("a rejected call must never reach the user")
			}
		})
	}
}

// A bare string option is still read as a label — a model that sends
// ["A","B"] gets its choices rendered rather than a confusing rejection.
func TestAskUserQuestion_StringOptions(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Answers: []AskAnswer{{Choices: []string{"A"}}}}}
	useAsker(t, stub)
	if _, err := runAsk(t, askInput(question("Which one?", "A", "B"))); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := stub.lastReq.Questions[0].Options[0].Label; got != "A" {
		t.Errorf("label = %q, want A", got)
	}
}

// An over-long preview is replaced everywhere it would travel.
func TestAskUserQuestion_PreviewCapped(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Answers: []AskAnswer{{Choices: []string{"A"}}}}}
	useAsker(t, stub)
	huge := strings.Repeat("x", maxPreviewChars+1)
	_, err := runAsk(t, askInput(question("Which one?",
		map[string]any{"label": "A", "preview": huge}, opt("B", ""))))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := stub.lastReq.Questions[0].Options[0].Preview; got != previewWithheld {
		t.Errorf("preview = %q, want the withheld placeholder", got)
	}
}

// Control characters in a label can't reach a TUI frame.
func TestAskUserQuestion_SanitizesLabels(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Answers: []AskAnswer{{}}}}
	useAsker(t, stub)
	if _, err := runAsk(t, askInput(question("Which one?", opt("A\x1b[31m", ""), opt("B", "")))); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := stub.lastReq.Questions[0].Options[0].Label; strings.ContainsRune(got, '\x1b') {
		t.Errorf("label %q still carries a control character", got)
	}
}

// The chosen option's preview is echoed back so the model sees what was
// picked — filled by Execute, never sent by the asker.
func TestAskUserQuestion_EchoesSelectedPreview(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Answers: []AskAnswer{{Choices: []string{"B"}}}}}
	useAsker(t, stub)
	text, err := runAsk(t, askInput(question("Which layout?",
		map[string]any{"label": "A", "preview": "left"},
		map[string]any{"label": "B", "preview": "right"})))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(text, "selected preview:\nright") {
		t.Errorf("result %q should carry the chosen option's preview", text)
	}
	if strings.Contains(text, "left") {
		t.Errorf("result %q leaked the preview of an option that wasn't chosen", text)
	}
}

func TestFormatAskResponse(t *testing.T) {
	req := AskRequest{Questions: []AskQuestion{
		{Question: "Which auth?", Options: []AskOption{{Label: "OAuth"}, {Label: "Basic"}}},
		{Question: "Which scope?", MultiSelect: true, Options: []AskOption{{Label: "web"}, {Label: "cli"}}},
	}}
	oneQ := AskRequest{Questions: []AskQuestion{
		{Question: "Which auth?", Options: []AskOption{{Label: "OAuth"}, {Label: "Basic"}}},
	}}

	cases := []struct {
		name        string
		req         AskRequest
		res         AskResponse
		wantContain []string
		wantAbsent  []string
	}{
		{
			name: "plain selections",
			req:  req,
			res: AskResponse{Answers: []AskAnswer{
				{Choices: []string{"OAuth"}},
				{Choices: []string{"web", "cli"}},
			}},
			wantContain: []string{
				answeredPrefix,
				`"Which auth?"="OAuth"`,
				`"Which scope?"="web, cli"`,
				answeredSuffix,
			},
		},
		{
			// An unanswered question does not push the result into the
			// cautioning phrasing: skipping is not answering in prose.
			name:        "unanswered question stays clean",
			req:         req,
			res:         AskResponse{Answers: []AskAnswer{{Choices: []string{"OAuth"}}, {}}},
			wantContain: []string{answeredPrefix, `"Which auth?"="OAuth"`},
			wantAbsent:  []string{"Which scope?", respondedPrefix},
		},
		{
			name:        "free text switches to the cautioning phrasing",
			req:         oneQ,
			res:         AskResponse{Answers: []AskAnswer{{Custom: "neither, use mTLS"}}},
			wantContain: []string{respondedPrefix, `"Which auth?"="neither, use mTLS"`, "Read the answers carefully"},
			wantAbsent:  []string{answeredPrefix},
		},
		{
			name:        "a label the model never declared is not a plain pick",
			req:         oneQ,
			res:         AskResponse{Answers: []AskAnswer{{Choices: []string{"Kerberos"}}}},
			wantContain: []string{respondedPrefix},
			wantAbsent:  []string{answeredPrefix},
		},
		{
			name:        "notes force the cautioning phrasing",
			req:         oneQ,
			res:         AskResponse{Answers: []AskAnswer{{Choices: []string{"OAuth"}, Notes: "needs refresh too"}}},
			wantContain: []string{respondedPrefix, `"Which auth?"="OAuth" notes: needs refresh too`},
			wantAbsent:  []string{answeredPrefix},
		},
		{
			name:        "notes without a selection",
			req:         oneQ,
			res:         AskResponse{Answers: []AskAnswer{{Notes: "both are wrong"}}},
			wantContain: []string{`"Which auth?"=(no option selected) notes: both are wrong`},
		},
		{
			name:        "several picks on a single-select question is not plain",
			req:         oneQ,
			res:         AskResponse{Answers: []AskAnswer{{Choices: []string{"OAuth", "Basic"}}}},
			wantContain: []string{respondedPrefix},
			wantAbsent:  []string{answeredPrefix},
		},
		{
			name:        "nothing answered",
			req:         req,
			res:         AskResponse{Answers: []AskAnswer{{}, {}}},
			wantContain: []string{unansweredText},
		},
		{
			name:        "rejected",
			req:         req,
			res:         AskResponse{Outcome: AskRejected, Answers: []AskAnswer{{Choices: []string{"OAuth"}}}},
			wantContain: []string{"STOP what you are doing"},
			// A withdrawn set must not report the one answer as a success.
			wantAbsent: []string{"OAuth", answeredPrefix},
		},
		{
			name: "clarify carries what was answered so far",
			req:  req,
			res: AskResponse{Outcome: AskClarify, Answers: []AskAnswer{
				{Choices: []string{"OAuth"}, Notes: "maybe"},
				{},
			}},
			wantContain: []string{
				"The user wants to clarify these questions.",
				"Start by asking them what they would like to clarify.",
				`- "Which auth?"`,
				"Answer: OAuth",
				"User notes: maybe",
				`- "Which scope?"`,
				"(No answer provided)",
			},
			wantAbsent: []string{answeredPrefix, respondedPrefix},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatAskResponse(tc.req, tc.res)
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("result:\n%s\nmissing %q", got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("result:\n%s\nshould not contain %q", got, absent)
				}
			}
		})
	}
}

// The tool is only advertised where a human can answer.
func TestAskUserQuestion_DisabledWithoutAsker(t *testing.T) {
	SetAsker(nil)
	if askerEnabled() {
		t.Fatal("askerEnabled must be false with no asker")
	}
	_, err := runAsk(t, askInput(question("Which one?", opt("A", ""), opt("B", ""))))
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("error = %v, want 'not available in this mode'", err)
	}
}
