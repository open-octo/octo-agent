package channel

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
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
	st := agent.NewSession(s.Agent.Model, "")
	st.ID = id
	st.CreatedAt = time.Now()
	st.Source = "channel"
	st.BoundEntry = agent.EntryChannel
	st.BoundAt = time.Now()
	st.Title = string(s.Key)
	// Persist immediately so the entry binding is visible to other processes
	// (and to the server's authoritative LoadSession in handleChannelMessage).
	_ = st.Save()
	s.Store = st
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
	return s.Store.Save()
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
