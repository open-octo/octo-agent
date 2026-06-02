package memory

import "testing"

func TestParseRules_Sections(t *testing.T) {
	raw := `# Memory index

- [Some topic](topic.md) — a pointer, not a rule

## 必须遵守

- never commit on main; push lands via PR only
- coding tasks run in an isolated git worktree

## 触发提醒

- (触发: deploy, 部署, 上311) 部署走 Lark bot：git 分支转 CI image tag 再发消息
- （触发：设计文档、design doc） 设计文档只描述当前状态，不写过程/历史

## 索引

- [Another](another.md) — also just a pointer
`
	r := parseRules(raw)

	if len(r.Always) != 2 {
		t.Fatalf("Always = %d, want 2: %+v", len(r.Always), r.Always)
	}
	if r.Always[0].Text != "never commit on main; push lands via PR only" {
		t.Errorf("Always[0] = %q", r.Always[0].Text)
	}

	if len(r.Triggered) != 2 {
		t.Fatalf("Triggered = %d, want 2: %+v", len(r.Triggered), r.Triggered)
	}
	got := r.Triggered[0]
	wantTriggers := []string{"deploy", "部署", "上311"}
	if len(got.Triggers) != len(wantTriggers) {
		t.Fatalf("Triggered[0].Triggers = %v, want %v", got.Triggers, wantTriggers)
	}
	for i, w := range wantTriggers {
		if got.Triggers[i] != w {
			t.Errorf("trigger[%d] = %q, want %q", i, got.Triggers[i], w)
		}
	}
	if got.Text != "部署走 Lark bot：git 分支转 CI image tag 再发消息" {
		t.Errorf("Triggered[0].Text = %q", got.Text)
	}
	// Full-width parens / 、 separator.
	if len(r.Triggered[1].Triggers) != 2 || r.Triggered[1].Triggers[0] != "设计文档" {
		t.Errorf("Triggered[1].Triggers = %v", r.Triggered[1].Triggers)
	}
}

func TestParseRules_PointerOnlyIsBackwardCompatible(t *testing.T) {
	// The current real MEMORY.md format: one-line pointers, no rule sections.
	raw := `- [Go rewrite](project_go_rewrite.md) — repo renamed
- [Branch workflow](feedback_branch_workflow.md) — PR-only into main
`
	r := parseRules(raw)
	if r.HasAny() {
		t.Errorf("pointer-only MEMORY.md should yield no rules, got %+v", r)
	}
}

func TestParseRules_EmojiHeadingAndSectionEnd(t *testing.T) {
	raw := `## 🔴 必须遵守

- rule one

## Unrelated heading

- this bullet is not a rule
`
	r := parseRules(raw)
	if len(r.Always) != 1 || r.Always[0].Text != "rule one" {
		t.Fatalf("Always = %+v", r.Always)
	}
	if len(r.Triggered) != 0 {
		t.Errorf("Triggered should be empty, got %+v", r.Triggered)
	}
}

func TestParseRules_TriggeredBulletWithoutClause(t *testing.T) {
	raw := "## 触发提醒\n\n- a rule with no trigger clause\n"
	r := parseRules(raw)
	if len(r.Triggered) != 1 {
		t.Fatalf("Triggered = %d, want 1", len(r.Triggered))
	}
	if r.Triggered[0].Text != "a rule with no trigger clause" || len(r.Triggered[0].Triggers) != 0 {
		t.Errorf("got %+v", r.Triggered[0])
	}
}

func TestTriggerHit(t *testing.T) {
	cases := []struct {
		input, trigger string
		want           bool
	}{
		{"deploy to 311", "deploy", true},
		{"Deploy To 311", "deploy", true},    // case-insensitive
		{"deployment plan", "deploy", false}, // ASCII word boundary
		{"redeploy now", "deploy", false},
		{"帮我部署一下到 311", "部署", true},     // CJK substring
		{"用 deploy 部署", "deploy", true}, // ASCII flanked by CJK/space
		{"just chatting", "deploy", false},
		{"de", "deploy", false}, // no reverse match (trigger contains input)
	}
	for _, c := range cases {
		if got := triggerHit(c.input, c.trigger); got != c.want {
			t.Errorf("triggerHit(%q, %q) = %v, want %v", c.input, c.trigger, got, c.want)
		}
	}
}
