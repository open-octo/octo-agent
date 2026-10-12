package responses

import (
	"encoding/json"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// inputJSON round-trips toInput through JSON so assertions see the wire shape.
func inputJSON(t *testing.T, model string, msgs []agent.Message) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(toInput(model, msgs))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func reasoningBlock(id, enc, model string) agent.ContentBlock {
	b := agent.NewThinkingBlock("", enc)
	b.ID = id
	b.Model = model
	return b
}

func TestToInput_ToolRound(t *testing.T) {
	img, ok := agent.NewImageBlock("image/png", []byte("\x89PNG\r\n\x1a\n0000"))
	if !ok {
		t.Fatal("NewImageBlock rejected the PNG")
	}
	msgs := []agent.Message{
		agent.NewSystemMessage("ignored"),
		agent.NewUserMessage("read it"),
		{Role: agent.RoleAssistant, Blocks: []agent.ContentBlock{
			reasoningBlock("rs_1", "ENC", "m"),
			agent.NewTextBlock("reading"),
			agent.NewToolUseBlock("call_1", "read_file", nil),
		}},
		{Role: agent.RoleUser, Blocks: []agent.ContentBlock{
			agent.NewToolResultBlock("call_1", "contents", false),
			img,
			agent.NewTextBlock("steer"),
		}},
	}
	got := inputJSON(t, "m", msgs)
	wantTypes := []string{"", "reasoning", "", "function_call", "function_call_output", ""}
	if len(got) != len(wantTypes) {
		t.Fatalf("input = %v", got)
	}
	for i, w := range wantTypes {
		if typ, _ := got[i]["type"].(string); typ != w {
			t.Errorf("input[%d].type = %q, want %q (%v)", i, typ, w, got[i])
		}
	}
	if r := got[1]; r["id"] != "rs_1" || r["encrypted_content"] != "ENC" {
		t.Errorf("reasoning item = %v", r)
	}
	if s, ok := got[1]["summary"].([]any); !ok || len(s) != 0 {
		t.Errorf("summary = %v, want []", got[1]["summary"])
	}
	if a := got[2]; a["role"] != "assistant" || a["content"] != "reading" {
		t.Errorf("assistant text = %v", a)
	}
	if fc := got[3]; fc["call_id"] != "call_1" || fc["arguments"] != "{}" {
		t.Errorf("function_call = %v", fc)
	}
	if fo := got[4]; fo["call_id"] != "call_1" || fo["output"] != "contents" {
		t.Errorf("function_call_output = %v", fo)
	}
	parts, _ := got[5]["content"].([]any)
	if got[5]["role"] != "user" || len(parts) != 2 {
		t.Fatalf("trailing user message = %v", got[5])
	}
	if p := parts[0].(map[string]any); p["type"] != "input_text" || p["text"] != "steer" {
		t.Errorf("text part = %v", p)
	}
	if p := parts[1].(map[string]any); p["type"] != "input_image" || p["detail"] != "auto" {
		t.Errorf("image part = %v", p)
	}
}

// Reasoning is replayed only when it is certain to be accepted; every other
// case drops it rather than risk a 400.
func TestToInput_ReasoningReplayRules(t *testing.T) {
	anthropicThinking := agent.NewThinkingBlock("claude thought", "anthropic-sig")
	call := agent.NewToolUseBlock("call_1", "x", nil)
	for name, tc := range map[string]struct {
		blocks []agent.ContentBlock
		want   bool
	}{
		"same model before a call":   {[]agent.ContentBlock{reasoningBlock("rs", "ENC", "m"), call}, true},
		"same model before text":     {[]agent.ContentBlock{reasoningBlock("rs", "ENC", "m"), agent.NewTextBlock("hi")}, true},
		"other model":                {[]agent.ContentBlock{reasoningBlock("rs", "ENC", "other"), call}, false},
		"no encrypted content":       {[]agent.ContentBlock{reasoningBlock("rs", "", "m"), call}, false},
		"anthropic thinking (no id)": {[]agent.ContentBlock{anthropicThinking, call}, false},
		"nothing after it":           {[]agent.ContentBlock{agent.NewTextBlock("hi"), reasoningBlock("rs", "ENC", "m")}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got := inputJSON(t, "m", []agent.Message{{Role: agent.RoleAssistant, Blocks: tc.blocks}})
			found := false
			for _, it := range got {
				if it["type"] == "reasoning" {
					found = true
				}
			}
			if found != tc.want {
				t.Errorf("reasoning replayed = %v, want %v (input %v)", found, tc.want, got)
			}
		})
	}
}

// An assistant message's phase is read from the output and resent unchanged.
func TestPhaseRoundTrip(t *testing.T) {
	blocks := toBlocks("m", []outputItem{
		{Type: "message", Phase: "commentary", Content: []outputContent{{Type: "output_text", Text: "looking"}}},
		{Type: "message", Phase: "final_answer", Content: []outputContent{{Type: "output_text", Text: "done"}}},
	})
	if len(blocks) != 2 || blocks[0].Phase != "commentary" || blocks[1].Phase != "final_answer" {
		t.Fatalf("blocks = %+v", blocks)
	}
	got := inputJSON(t, "m", []agent.Message{{Role: agent.RoleAssistant, Blocks: blocks}})
	if got[0]["phase"] != "commentary" || got[1]["phase"] != "final_answer" {
		t.Errorf("input = %v, want phases resent", got)
	}
	plain := inputJSON(t, "m", []agent.Message{agent.NewAssistantMessage("hi")})
	if _, ok := plain[0]["phase"]; ok {
		t.Errorf("phase sent on a message that never had one: %v", plain[0])
	}
}
