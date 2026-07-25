package agentprofile

import "regexp"

// RouteInput carries the fields of an inbound IM event the router needs. It
// is deliberately defined here rather than reusing channel.InboundEvent so
// agentprofile stays importable from both internal/channel (PR2) and
// internal/server without an import cycle.
type RouteInput struct {
	Platform string
	ChatID   string
	// UserID is reserved for PR3+ (per-user session scoping under a shared
	// group chat). It is currently unused but kept so the router signature is
	// stable once binding modes like BindByChatUser need it.
	UserID string
	Text   string
}

// Router maps an inbound event to the profile that should handle it. It is a
// pure function over the Store (which is read-through), so profile edits take
// effect on the very next message.
type Router struct {
	store *Store
}

// NewRouter builds a Router over store.
func NewRouter(store *Store) *Router { return &Router{store: store} }

// mentionRule matches @alias tokens in message text. The captured alias is
// looked up (with the @) against profiles' mention_as lists.
var mentionRule = regexp.MustCompile(`@([A-Za-z0-9][A-Za-z0-9_-]*)`)

// MentionAlias extracts the first @-alias from text, "" when there is none.
func MentionAlias(text string) string {
	m := mentionRule.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return "@" + m[1]
}

// Route returns the profile that should handle the event, or nil when the
// message must be dropped (group chat with multiple bound profiles and no
// @-mention — the "stay silent" rule). The decision table:
//
//	| scenario                        | result            |
//	|---------------------------------|-------------------|
//	| @alias bound to a profile       | that profile      |
//	| @alias not bound anywhere       | default (fallback)|
//	| exactly one channel binding     | the bound profile |
//	| multiple bindings, no @-mention | nil (drop)        |
//	| no binding                      | default           |
//
// @-mentions win over channel bindings: an explicit @ is never ambiguous.
func (r *Router) Route(in RouteInput) *Profile {
	if alias := MentionAlias(in.Text); alias != "" {
		if p, ok := r.store.ByMention(alias); ok {
			return p
		}
		return DefaultProfile()
	}
	switch bound := r.store.ByChannel(in.Platform, in.ChatID); len(bound) {
	case 0:
		return DefaultProfile()
	case 1:
		return bound[0]
	default:
		return nil
	}
}
