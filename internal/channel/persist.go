package channel

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/permission"
)

// IM sessions persist their conversation history to the same store web
// sessions use (~/.octo/sessions, agent.Session JSONL). The store ID is
// derived deterministically from the session key, so after a server restart
// the first message from a chat reloads its history — before this, IM
// context lived only in process memory and a restart (now a routine event:
// upgrades, config changes) wiped every conversation.

// sessionStoreID maps a SessionKey onto a filename-safe, deterministic
// agent.Session ID. The sanitized key keeps the file recognisable in the
// sessions directory; the FNV suffix disambiguates keys that collide after
// sanitization.
func sessionStoreID(key SessionKey) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))

	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, string(key))
	if len(safe) > 48 {
		safe = safe[:48]
	}
	return fmt.Sprintf("im-%s-%08x", safe, h.Sum32())
}

// newChannelStore builds a fresh, persisted-shape store for a channel
// session: source "channel", bound to EntryChannel. Title stays at
// agent.NewSession's "*Octo Agent" placeholder — never the raw SessionKey
// (e.g. "weixin:o9cq…@im.wechat:o9cq…@im.wechat") — until the first turn's
// async title generation replaces it; meanwhile DisplayTitle() surfaces the
// first user message snippet instead of the placeholder. Shared by
// restoreOrInitStore (first-ever store for a session) and EnsureStoreExists
// (#1079 — recovering after the file was deleted out from under an
// already-running session), so both give a fresh channel session the exact
// same shape.
func newChannelStore(id, model string) *agent.Session {
	st := agent.NewSession(model, "")
	st.ID = id
	st.CreatedAt = time.Now()
	st.Source = "channel"
	st.BoundEntry = agent.EntryChannel
	st.BoundAt = time.Now()
	_ = st.SetPermissionMode(string(permission.ResolveDefaultMode()))
	return st
}

// restoreOrInitStore attaches the persistent store to a freshly built
// session: an existing file rehydrates the agent's history, otherwise a new
// store is initialised. The store ID is resolved by the manager (a /bind
// override or the deterministic default) and passed in. Best-effort — a
// corrupt or unreadable file degrades to a fresh conversation rather than
// blocking the chat.
//
// For a restored session the existing BoundEntry is preserved; the caller
// (Manager) must explicitly Bind(EntryChannel) before using it, so entry
// ownership is enforced consistently across CLI/TUI/Web/IM.
func (s *Session) restoreOrInitStore(id string) {
	if loaded, err := agent.LoadSession(id); err == nil {
		s.Store = loaded
		if len(loaded.Messages) > 0 {
			s.Agent.History = loaded.ToHistory()
		}
		return
	}
	// Persist immediately so the entry binding is visible to other processes
	// (and to the server's authoritative LoadSession in handleChannelMessage).
	st := newChannelStore(id, s.Agent.Model)
	_ = st.Save()
	s.Store = st
}

// EnsureStoreExists recreates the backing store if its file was deleted out
// from under this session (e.g. the user deleted it from the web UI) after
// the session was already loaded into the manager's in-memory cache (#1079).
// restoreOrInitStore only runs once, when a session is first created; a
// session that then sits in the cache while its file disappears externally
// keeps a stale Store pointer, and the server's authoritative reload
// (acquireSessionBinding's LoadSession) fails with a confusing "session not
// found" error instead of the chat just starting fresh.
//
// The existence check is a stat (SessionMTime), not a full LoadSession — this
// runs on every inbound message, so it must not pay a full read-and-parse of
// the session's history just to confirm the file is still there. That means
// a corrupt-but-present file (rather than a deleted one) isn't recovered
// here; it still surfaces as an error via acquireSessionBinding's own reload
// immediately after this call, unchanged from before this fix. Only the
// reported scenario — the file is gone — is what this recovers from.
func (s *Session) EnsureStoreExists() {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.Store == nil {
		return
	}
	if _, err := agent.SessionMTime(s.Store.ID); err == nil {
		return
	}
	st := newChannelStore(s.Store.ID, s.Agent.Model)
	_ = st.Save()
	s.Store = st
	s.Agent.History = agent.NewHistory()
}

// Persist writes the agent's current history to the session store. Called by
// the server after each IM turn; errors are the caller's to log — losing one
// round of persistence must not fail the chat reply.
//
// storeMu makes deleteStore a tombstone: /unbind and /bind run on the
// adapter callback goroutine while a turn may still be in flight, and that
// zombie turn's Persist would otherwise recreate the just-deleted file
// (Save's append path opens with O_CREATE) — resurrecting history the user
// explicitly cleared, as a malformed meta-less session file.
func (s *Session) Persist() error {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.Store == nil {
		return nil
	}
	s.Store.SyncFrom(s.Agent.History)
	if err := s.Store.Save(); err != nil {
		return err
	}
	// Record the real context-token count under the same storeMu as the save
	// (Store is only ever touched while holding it), so an idle/resumed session
	// shows accurate usage in the Web UI — parity with web/desktop turns.
	// Best-effort and idempotent (a no-op when the count is unchanged).
	_ = s.Agent.PersistContextUsage(s.Store)
	return nil
}

// AdoptGeneratedTitle records an async-generated session title, replacing the
// "*Octo Agent" placeholder (or an empty title). A title the user set
// themselves — anything else — always wins and the adoption is refused.
// storeMu serializes the write with Persist/UnbindStore/deleteStore, the same
// discipline Persist follows; a tombstoned store (concurrent /unbind) just
// reports false. Returns true when the placeholder was replaced.
//
// Durability: on a transcript that already carries messages SetTitle appends
// a title record itself; on a meta-only store (no turn persisted yet) the
// title rides the caller's next Persist, which folds it into the meta header
// — the server's channel persist closure always adopts right before Persist.
func (s *Session) AdoptGeneratedTitle(title string) bool {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.Store == nil {
		return false
	}
	if t := strings.TrimSpace(s.Store.Title); t != "" && t != "*Octo Agent" {
		return false
	}
	return s.Store.SetTitle(title) == nil
}

// UnbindStore releases the store's entry binding and persists the change.
// Guarded by storeMu like Persist/deleteStore: /unbind runs on the adapter
// callback goroutine while a turn may still be in flight, and that turn's
// Persist (now called per event, not just once at turn end) mutates the same
// *agent.Session fields (Messages, persisted) with no locking of its own —
// storeMu is what serializes the two callers.
func (s *Session) UnbindStore(entry string) bool {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.Store == nil {
		return false
	}
	released := s.Store.Unbind(entry)
	_ = s.Store.Save()
	return released
}

// GoalStore returns the persisted backing session, which carries the
// conversation's goal, or nil when a concurrent /unbind tombstoned it.
// Accessor because Store mutates under storeMu.
func (s *Session) GoalStore() *agent.Session {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	return s.Store
}

// deleteStore removes the persisted history and tombstones the store so an
// in-flight turn can't write it back; used by /unbind and /bind, whose
// contracts are "history cleared" / "start fresh". Returns the delete error
// for callers that want to warn the user (a missing file is not an error).
func (s *Session) deleteStore() error {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.Store == nil {
		return nil
	}
	id := s.Store.ID
	s.Store = nil
	if err := agent.DeleteSession(id); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
