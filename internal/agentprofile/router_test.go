package agentprofile

import "testing"

// routerStore builds a store with two user profiles bound as in the design
// doc's decision table: "code" (@review, bound to weixin:g1) and "ops"
// (@ops, bound to weixin:g1 as well — the ambiguous group).
func routerStore(t *testing.T) *Store {
	t.Helper()
	s, userDir, _ := newTestStore(t)
	writeMD(t, userDir, "code.md", "---\ndescription: dc\nmention_as: [\"@review\"]\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n---\nbody\n")
	writeMD(t, userDir, "ops.md", "---\ndescription: do\nmention_as: [\"@ops\"]\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n  - {platform: weixin, chat_id: g2}\n---\nbody\n")
	writeMD(t, userDir, "solo.md", "---\ndescription: ds\nchannel_bindings:\n  - {platform: feishu, chat_id: dm1}\n---\nbody\n")
	return s
}

func TestMentionAlias(t *testing.T) {
	cases := map[string]string{
		"@review this PR":   "@review",
		"please @ops check": "@ops",
		"no mention":        "",
		"email a@b.com":     "@b", // documented limitation: first @-token wins
		"@":                 "",
		"@${not}":           "",
		"@code-review hi":   "@code-review",
	}
	for in, want := range cases {
		if got := MentionAlias(in); got != want {
			t.Errorf("MentionAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRouterDecisionTable(t *testing.T) {
	r := NewRouter(routerStore(t))
	tests := []struct {
		name string
		in   RouteInput
		want string // profile ID; "" means drop (nil); "default" for fallback
	}{
		{"DM unbound → default",
			RouteInput{Platform: "weixin", ChatID: "dm-9", Text: "hi"}, "default"},
		{"DM bound uniquely → bound profile",
			RouteInput{Platform: "feishu", ChatID: "dm1", Text: "hi"}, "solo"},
		{"group @unknown alias → default fallback",
			RouteInput{Platform: "weixin", ChatID: "g1", Text: "@nobody hi"}, "default"},
		{"group @review → code",
			RouteInput{Platform: "weixin", ChatID: "g1", Text: "@review pr 123"}, "code"},
		{"group multi-binding @ops → ops",
			RouteInput{Platform: "weixin", ChatID: "g1", Text: "@ops check logs"}, "ops"},
		{"group multi-binding no @ → drop",
			RouteInput{Platform: "weixin", ChatID: "g1", Text: "hello"}, ""},
		{"group single-binding no @ → bound profile",
			RouteInput{Platform: "weixin", ChatID: "g2", Text: "hello"}, "ops"},
		{"group unbound → default",
			RouteInput{Platform: "weixin", ChatID: "g9", Text: "hello"}, "default"},
		{"@ mention wins over channel binding",
			RouteInput{Platform: "feishu", ChatID: "dm1", Text: "@ops look"}, "ops"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Route(tt.in)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("Route() = %q, want nil (drop)", got.ID)
				}
				return
			}
			if got == nil {
				t.Fatalf("Route() = nil, want %q", tt.want)
			}
			if got.ID != tt.want {
				t.Fatalf("Route() = %q, want %q", got.ID, tt.want)
			}
		})
	}
}
