package tools

import (
	"context"
	"errors"
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

func TestAskUserQuestionTool_Schema(t *testing.T) {
	def := AskUserQuestionTool{}.Definition()
	if def.Name != "ask_user_question" {
		t.Errorf("Name = %q", def.Name)
	}
	props, _ := def.Parameters["properties"].(map[string]any)
	for _, want := range []string{"question", "options", "multi_select", "header"} {
		if _, ok := props[want]; !ok {
			t.Errorf("schema missing property %q", want)
		}
	}
	required, _ := def.Parameters["required"].([]string)
	if !sliceContains(required, "question") || !sliceContains(required, "options") {
		t.Errorf("question + options should be required, got %v", required)
	}
}

func TestAskUserQuestionTool_Execute_SingleChoice(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Choices: []string{"OAuth"}}}
	useAsker(t, stub)

	out, err := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Which auth method?",
		"options":  []any{"OAuth", "API key", "mTLS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "User chose: OAuth" {
		t.Errorf("Execute result = %q", out.Text)
	}
	if !stub.called {
		t.Error("asker was never invoked")
	}
	if stub.lastReq.Question != "Which auth method?" {
		t.Errorf("question not forwarded: %q", stub.lastReq.Question)
	}
	if len(stub.lastReq.Options) != 3 {
		t.Errorf("options not forwarded: %v", stub.lastReq.Options)
	}
}

// Claude models trained on Claude Code's AskUserQuestion send options as
// {label, description} objects rather than bare strings. The tool must accept
// that shape instead of dropping every entry and failing with "got 0".
func TestAskUserQuestionTool_Execute_ObjectOptions(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Choices: []string{"简洁型 — 极度简短"}}}
	useAsker(t, stub)

	out, err := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "什么风格?",
		"options": []any{
			map[string]any{"label": "简洁型", "description": "极度简短"},
			map[string]any{"label": "专业型", "description": "精准、结构化"},
			map[string]any{"label": "友好型"}, // description omitted
		},
	})
	if err != nil {
		t.Fatalf("object-shaped options should parse, got error: %v", err)
	}
	if len(stub.lastReq.Options) != 3 {
		t.Fatalf("want 3 options forwarded, got %d: %v", len(stub.lastReq.Options), stub.lastReq.Options)
	}
	// Description is folded into the label so its context still reaches the user.
	if stub.lastReq.Options[0] != "简洁型 — 极度简短" {
		t.Errorf("option[0] = %q, want label folded with description", stub.lastReq.Options[0])
	}
	// A label without a description stays bare.
	if stub.lastReq.Options[2] != "友好型" {
		t.Errorf("option[2] = %q, want bare label", stub.lastReq.Options[2])
	}
	_ = out
}

func TestOptionLabels(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"string []any", []any{"a", "b"}, []string{"a", "b"}},
		{"typed []string", []string{"a", "b"}, []string{"a", "b"}},
		{"objects with label", []any{
			map[string]any{"label": "a"},
			map[string]any{"label": "b", "description": "d"},
		}, []string{"a", "b — d"}},
		{"blank entries dropped", []any{"a", "  ", map[string]any{"label": ""}}, []string{"a"}},
		{"nil", nil, nil},
		{"not a slice", "oops", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := optionLabels(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestAskUserQuestionTool_Execute_MultiChoice(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Choices: []string{"OAuth", "API key"}}}
	useAsker(t, stub)

	out, err := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question":     "Which auth methods should we support?",
		"options":      []any{"OAuth", "API key", "mTLS"},
		"multi_select": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "User chose: OAuth, API key" {
		t.Errorf("Execute result = %q", out.Text)
	}
	if !stub.lastReq.MultiSelect {
		t.Error("multi_select flag not forwarded")
	}
}

func TestAskUserQuestionTool_Execute_OtherFreeText(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Custom: "Kerberos with constrained delegation"}}
	useAsker(t, stub)

	out, _ := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Which auth method?",
		"options":  []any{"OAuth", "API key"},
	})
	if !strings.HasPrefix(out.Text, "User chose: Other — ") {
		t.Errorf("free-text answer should be reported as Other — …, got %q", out.Text)
	}
	if !strings.Contains(out.Text, "Kerberos with constrained delegation") {
		t.Errorf("free-text payload lost: %q", out.Text)
	}
}

func TestAskUserQuestionTool_Execute_Cancelled(t *testing.T) {
	useAsker(t, &stubAsker{resp: AskResponse{Cancelled: true}})
	out, err := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Which?",
		"options":  []any{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "(user cancelled)" {
		t.Errorf("cancellation = %q, want '(user cancelled)'", out.Text)
	}
}

func TestAskUserQuestionTool_Execute_HeaderForwarded(t *testing.T) {
	stub := &stubAsker{resp: AskResponse{Choices: []string{"a"}}}
	useAsker(t, stub)
	_, _ = AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Q?",
		"options":  []any{"a", "b"},
		"header":   "auth_method",
	})
	if stub.lastReq.Header != "auth_method" {
		t.Errorf("header not forwarded: %q", stub.lastReq.Header)
	}
}

func TestAskUserQuestionTool_Execute_AskerError(t *testing.T) {
	useAsker(t, &stubAsker{err: errors.New("stdin closed")})
	_, err := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Q?",
		"options":  []any{"a", "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "stdin closed") {
		t.Errorf("asker error should propagate, got %v", err)
	}
}

func TestAskUserQuestionTool_Execute_NoAsker(t *testing.T) {
	SetAsker(nil)
	_, err := AskUserQuestionTool{}.Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Q?",
		"options":  []any{"a", "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("no asker should error 'not available', got %v", err)
	}
}

func TestAskUserQuestionTool_Execute_Validation(t *testing.T) {
	useAsker(t, &stubAsker{resp: AskResponse{Choices: []string{"a"}}})

	// Missing question.
	if _, err := (AskUserQuestionTool{}).Execute(context.Background(), "ask_user_question", map[string]any{
		"options": []any{"a", "b"},
	}); err == nil || !strings.Contains(err.Error(), "question is required") {
		t.Errorf("missing question should error, got %v", err)
	}

	// Too few options.
	if _, err := (AskUserQuestionTool{}).Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Q?",
		"options":  []any{"only"},
	}); err == nil || !strings.Contains(err.Error(), "2-4 entries") {
		t.Errorf("single option should error, got %v", err)
	}

	// Too many options.
	if _, err := (AskUserQuestionTool{}).Execute(context.Background(), "ask_user_question", map[string]any{
		"question": "Q?",
		"options":  []any{"a", "b", "c", "d", "e"},
	}); err == nil || !strings.Contains(err.Error(), "2-4 entries") {
		t.Errorf("5 options should error, got %v", err)
	}
}

func TestDefaultTools_AskGatedOnAsker(t *testing.T) {
	SetAsker(nil)
	t.Cleanup(func() { SetAsker(nil) })

	has := func() bool {
		for _, d := range DefaultTools() {
			if d.Name == "ask_user_question" {
				return true
			}
		}
		return false
	}

	if has() {
		t.Error("ask_user_question should be absent when no asker is configured")
	}
	useAsker(t, &stubAsker{})
	if !has() {
		t.Error("ask_user_question should appear once an asker is registered")
	}
}

func TestFormatAskResponse(t *testing.T) {
	cases := []struct {
		name string
		in   AskResponse
		want string
	}{
		{"cancelled", AskResponse{Cancelled: true}, "(user cancelled)"},
		{"single", AskResponse{Choices: []string{"OAuth"}}, "User chose: OAuth"},
		{"multi", AskResponse{Choices: []string{"a", "b"}}, "User chose: a, b"},
		{"other", AskResponse{Custom: "Kerberos"}, "User chose: Other — Kerberos"},
		{"empty (defensive)", AskResponse{}, "(user cancelled)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAskResponse(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
