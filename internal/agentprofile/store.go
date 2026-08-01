package agentprofile

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Store loads profiles from the user-level agent directory plus the
// code-defined builtins, and answers every query by rescanning: it is
// deliberately read-through, so a change made through any path (REST API,
// Web UI, direct .md edit) is visible on the next read. The directory holds
// a handful of small files, so a rescan is cheap.
type Store struct {
	userDir string

	mu               sync.RWMutex
	disabledDefaults map[string]bool // curated-expert IDs hidden by the user; mirrors skills.Registry's disabled set
}

// New builds a Store over the user-level directory (~/.octo/agents).
func New(userDir string) *Store {
	return &Store{userDir: userDir}
}

// SetDisabledDefaults replaces the set of curated-expert IDs hidden from
// Get/List. Called once at startup with the persisted config value, and again
// whenever the toggle endpoint flips one. Hidden, not deleted — the
// underlying ~/.octo/agents-default/<id>.md is untouched and can be re-shown.
func (s *Store) SetDisabledDefaults(ids []string) {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	s.mu.Lock()
	s.disabledDefaults = m
	s.mu.Unlock()
}

func (s *Store) isDisabledDefault(p *Profile) bool {
	if p == nil || p.Source != SourceDefault {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disabledDefaults[p.ID]
}

// load scans default (curated) and user profiles and returns the id →
// profile map, with builtins as the base layer and user files taking highest
// precedence. Files that fail to parse are skipped.
func (s *Store) load() map[string]*Profile {
	profiles := make(map[string]*Profile)
	for _, p := range builtinProfiles() {
		profiles[p.ID] = p
	}
	s.scanDir(defaultAgentsRoot(), SourceDefault, profiles)
	s.scanDir(s.userDir, SourceUser, profiles)
	return profiles
}

// scanDir merges *.md profiles from dir into dst, overwriting same-named
// entries. A missing or unreadable dir is a no-op.
func (s *Store) scanDir(dir string, src Source, dst map[string]*Profile) {
	s.scanDirFiltered(dir, src, dst, nil)
}

// scanDirFiltered is scanDir with an optional ID filter (nil = accept all).
func (s *Store) scanDirFiltered(dir string, src Source, dst map[string]*Profile, accept func(string) bool) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		if id == "" || id == DefaultID {
			continue
		}
		if accept != nil && !accept(id) {
			continue
		}
		p, err := parseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Printf("agentprofile: skipping %s: %v", filepath.Join(dir, e.Name()), err)
			continue
		}
		p.ID = id
		p.Source = src
		dst[id] = p
	}
}

// Load performs a full scan.
func (s *Store) Load() error {
	s.load()
	return nil
}

// Get returns the profile with the given id. The default profile is
// code-defined and always available. A hidden (toggled-off) curated expert is
// treated as not found, matching skills.Registry's "disabled == non-existent"
// semantics — use LookupAny to see it regardless.
func (s *Store) Get(id string) (*Profile, bool) {
	if id == DefaultID {
		return DefaultProfile(), true
	}
	p, ok := s.load()[id]
	if ok && s.isDisabledDefault(p) {
		return nil, false
	}
	return p, ok
}

// LookupAny returns the profile with the given id regardless of whether it's
// a currently-hidden curated expert — used by the toggle handler, which must
// find a disabled default to re-enable it.
func (s *Store) LookupAny(id string) (*Profile, bool) {
	p, ok := s.load()[id]
	return p, ok
}

// List returns every user-level and curated-default profile (excluding
// builtins and hidden curated experts), sorted by ID for stable output.
func (s *Store) List() []*Profile {
	var out []*Profile
	for _, p := range s.load() {
		if p.Source == SourceBuiltin {
			continue
		}
		if s.isDisabledDefault(p) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Create validates p and writes it as <userDir>/<id>.md.
func (s *Store) Create(p *Profile) error {
	if err := s.validateForStoreWrite(p); err != nil {
		return err
	}
	path := filepath.Join(s.userDir, p.ID+".md")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("profile %q already exists", p.ID)
	}
	return s.writeFile(path, p)
}

// Update rewrites a profile. For a user profile this rewrites its existing
// file; for a curated (SourceDefault) expert with no user-dir file yet, this
// forks it into a permanent ~/.octo/agents/<id>.md override — same semantics
// as a skill override, and the intended way "edit this expert" works from the
// gallery UI. Once forked, that ID stops receiving future curated-content
// refreshes (accepted trade-off, mirrors skill overrides).
func (s *Store) Update(p *Profile) error {
	if err := s.validateForStoreWrite(p); err != nil {
		return err
	}
	existing, ok := s.LookupAny(p.ID)
	if !ok {
		return fmt.Errorf("profile %q not found", p.ID)
	}
	if existing.Source == SourceBuiltin {
		return fmt.Errorf("profile %q is builtin and cannot be modified", p.ID)
	}
	path := filepath.Join(s.userDir, p.ID+".md")
	return s.writeFile(path, p)
}

func (s *Store) validateForStoreWrite(p *Profile) error {
	if err := validateForWrite(p); err != nil {
		return err
	}
	if p.ID == DefaultID {
		return fmt.Errorf("id %q is reserved for the code-defined default agent", DefaultID)
	}
	return nil
}

// Delete removes a profile. Builtin and curated-default profiles are
// protected (curated experts can only be hidden via SetDisabledDefaults, not
// deleted — same as skills.Registry refusing to delete Source=="default"). A
// profile with channel bindings must be unbound first. Deleting a user
// override of a curated expert works as usual and correctly falls back to the
// shipped default on the next load.
func (s *Store) Delete(id string) error {
	p, ok := s.LookupAny(id)
	if !ok {
		return fmt.Errorf("profile %q not found", id)
	}
	if p.Source == SourceBuiltin || p.Source == SourceDefault {
		return fmt.Errorf("profile %q is %s and cannot be deleted", id, p.Source)
	}
	if len(p.ChannelBindings) > 0 {
		return fmt.Errorf("profile %q still has %d channel binding(s): unbind them first", id, len(p.ChannelBindings))
	}
	return os.Remove(filepath.Join(s.userDir, id+".md"))
}

// writeFile serializes p and writes it via temp-file + rename.
func (s *Store) writeFile(path string, p *Profile) error {
	b, err := serialize(p)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".profile-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// userProfiles scans only the user-level directory for IM routing.
func (s *Store) userProfiles() map[string]*Profile {
	m := make(map[string]*Profile)
	s.scanDirFiltered(s.userDir, SourceUser, m, IsValidID)
	return m
}

// ByChannel returns the user-level profiles bound to the given IM chat.
func (s *Store) ByChannel(platform, adapterID, chatID string) []*Profile {
	var out []*Profile
	for _, p := range s.userProfiles() {
		for _, b := range p.ChannelBindings {
			if b.Platform != platform || b.ChatID != chatID {
				continue
			}
			if adapterID != "" && b.AdapterID != "" && b.AdapterID != adapterID {
				continue
			}
			out = append(out, p)
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
