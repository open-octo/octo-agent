package channel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// bindingStore is the persistent chat→session redirection table behind /bind.
// A SessionKey present here routes to the recorded store ID instead of its
// deterministic default (sessionStoreID), so a chat stays attached to the
// session a user explicitly chose. It is persisted to disk so the binding
// survives a server restart — without that, /bind would only hold for the
// current process and an upgrade/restart would silently revert every chat to
// its own default session.
type bindingStore struct {
	mu sync.Mutex
	m  map[SessionKey]string // session key -> target store id
}

// newBindingStore builds the store and best-effort loads any persisted
// bindings. A missing or unreadable file degrades to an empty table rather
// than blocking the bridge.
func newBindingStore() *bindingStore {
	b := &bindingStore{m: map[SessionKey]string{}}
	b.load()
	return b
}

// bindingsPath resolves ~/.octo/im-bindings.json. Resolved per call (not
// cached) so a test that overrides HOME stays isolated, mirroring sessionsDir.
func bindingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".octo", "im-bindings.json"), nil
}

func (b *bindingStore) load() {
	path, err := bindingsPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return // missing/unreadable → empty table
	}
	var m map[SessionKey]string
	if json.Unmarshal(data, &m) == nil && m != nil {
		b.mu.Lock()
		b.m = m
		b.mu.Unlock()
	}
}

// save writes the current table atomically enough for a single-writer bridge:
// the file is small and rewritten whole on every change.
func (b *bindingStore) save() error {
	path, err := bindingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b.mu.Lock()
	data, err := json.MarshalIndent(b.m, "", "  ")
	b.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// get returns the bound store ID for a key, if any.
func (b *bindingStore) get(key SessionKey) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, ok := b.m[key]
	return id, ok
}

// set records a binding and persists the table.
func (b *bindingStore) set(key SessionKey, id string) error {
	b.mu.Lock()
	b.m[key] = id
	b.mu.Unlock()
	return b.save()
}

// remove drops a binding and persists the table. Removing an absent key is a
// no-op (no needless write).
func (b *bindingStore) remove(key SessionKey) (existed bool, err error) {
	b.mu.Lock()
	_, existed = b.m[key]
	delete(b.m, key)
	b.mu.Unlock()
	if !existed {
		return false, nil
	}
	return true, b.save()
}
