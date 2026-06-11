package channel

import (
	"fmt"
	"hash/fnv"
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
// store is initialised. Best-effort — a corrupt or unreadable file degrades
// to a fresh conversation rather than blocking the chat.
func (s *Session) restoreOrInitStore() {
	id := sessionStoreID(s.Key)
	if loaded, err := agent.LoadSession(id); err == nil {
		s.Store = loaded
		if len(loaded.Messages) > 0 {
			s.Agent.History = loaded.ToHistory()
		}
		return
	}
	st := &agent.Session{
		ID:        id,
		CreatedAt: time.Now(),
		Model:     s.Agent.Model,
		Source:    "channel",
		Title:     string(s.Key),
	}
	s.Store = st
}

// Persist writes the agent's current history to the session store. Called by
// the server after each IM turn; errors are the caller's to log — losing one
// round of persistence must not fail the chat reply.
func (s *Session) Persist() error {
	if s.Store == nil {
		return nil
	}
	s.Store.SyncFrom(s.Agent.History)
	return s.Store.Save()
}

// deleteStore removes the persisted history; used by /unbind, whose contract
// is "history cleared".
func (s *Session) deleteStore() {
	if s.Store == nil {
		return
	}
	_ = agent.DeleteSession(s.Store.ID)
}
