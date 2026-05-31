package server

import (
	"sync"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

// WebSession wraps an agent.Agent together with its persistent Session and
// bookkeeping timestamps so the HTTP layer can track and list active chats.
type WebSession struct {
	ID        string
	Agent     *agent.Agent
	Session   *agent.Session
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SessionManager keeps an in-memory registry of WebSession values keyed by
// session ID. All methods are safe for concurrent use.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*WebSession
}

// NewSessionManager returns an empty SessionManager ready for use.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*WebSession),
	}
}

// Create builds a new WebSession from the given model and system prompt,
// stores it, and returns it. The caller is responsible for wiring a Sender
// into ws.Agent before use.
func (sm *SessionManager) Create(model, system string) *WebSession {
	s := agent.NewSession(model, system)
	now := time.Now()
	ws := &WebSession{
		ID:        s.ID,
		Agent:     agent.New(nil, model),
		Session:   s,
		CreatedAt: now,
		UpdatedAt: now,
	}
	ws.Agent.System = system

	sm.mu.Lock()
	sm.sessions[ws.ID] = ws
	sm.mu.Unlock()
	return ws
}

// Get looks up a WebSession by ID. Returns nil when the ID is unknown.
func (sm *SessionManager) Get(id string) *WebSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[id]
}

// List returns up to n most-recently-updated sessions, newest first.
// n <= 0 returns all sessions.
func (sm *SessionManager) List(n int) []*WebSession {
	sm.mu.RLock()
	all := make([]*WebSession, 0, len(sm.sessions))
	for _, ws := range sm.sessions {
		all = append(all, ws)
	}
	sm.mu.RUnlock()

	// Sort by UpdatedAt descending (newest first).
	for i := 0; i < len(all)-1; i++ {
		for j := i + 1; j < len(all); j++ {
			if all[i].UpdatedAt.Before(all[j].UpdatedAt) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}

	if n > 0 && len(all) > n {
		all = all[:n]
	}
	return all
}
