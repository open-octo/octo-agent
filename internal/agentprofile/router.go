package agentprofile

// RouteInput carries the fields of an inbound IM event the router needs. It
// is deliberately defined here rather than reusing channel.InboundEvent so
// agentprofile stays importable from both internal/channel (PR2) and
// internal/server without an import cycle.
type RouteInput struct {
	Platform  string
	ChatID    string
	AdapterID string
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

// Route returns the profile that should handle the event, or nil when the
// message must be dropped (group chat with multiple bound profiles — the
// "stay silent" rule). RouteInput.AdapterID disambiguates: when non-empty,
// only bindings with a matching AdapterID are considered; when empty,
// the legacy two-dimensional (platform, chat_id) match is used.
//
//	| adapterID | bindings | result            |
//	|-----------|----------|-------------------|
//	| set       | 0        | default           |
//	| set       | 1        | the bound profile |
//	| set       | 2+       | nil (drop)        |
//	| empty     | 0        | default           |
//	| empty     | 1        | the bound profile |
//	| empty     | 2+       | nil (drop)        |
func (r *Router) Route(in RouteInput) *Profile {
	switch bound := r.store.ByChannel(in.Platform, in.AdapterID, in.ChatID); len(bound) {
	case 0:
		return DefaultProfile()
	case 1:
		return bound[0]
	default:
		return nil
	}
}
