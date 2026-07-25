package agentprofile

import "testing"

// routerStore builds a store with user profiles for channel-binding routing tests.
func routerStore(t *testing.T) *Store {
	t.Helper()
	s, userDir, _ := newTestStore(t)
	writeMD(t, userDir, "code.md", "---\ndescription: dc\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n---\nbody\n")
	writeMD(t, userDir, "ops.md", "---\ndescription: do\nchannel_bindings:\n  - {platform: weixin, chat_id: g1}\n  - {platform: weixin, chat_id: g2}\n---\nbody\n")
	writeMD(t, userDir, "solo.md", "---\ndescription: ds\nchannel_bindings:\n  - {platform: feishu, chat_id: dm1}\n---\nbody\n")
	return s
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
		{"group multi-binding → drop",
			RouteInput{Platform: "weixin", ChatID: "g1", Text: "hello"}, ""},
		{"group single-binding → bound profile",
			RouteInput{Platform: "weixin", ChatID: "g2", Text: "hello"}, "ops"},
		{"group unbound → default",
			RouteInput{Platform: "weixin", ChatID: "g9", Text: "hello"}, "default"},
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
