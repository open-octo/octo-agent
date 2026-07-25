package agentprofile

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store loads profiles from the user-level agent directory plus the
// code-defined builtins, and answers every query by rescanning: it is
// deliberately read-through, so a change made through any path (REST API,
// Web UI, direct .md edit) is visible on the next read. The directory holds
// a handful of small files, so a rescan is cheap.
type Store struct {
	userDir string
}

// New builds a Store over the user-level directory (~/.octo/agents).
func New(userDir string) *Store {
	return &Store{userDir: userDir}
}

// load scans user profiles and returns the id → profile map, with builtins as
// the base layer. Files that fail to parse are skipped.
func (s *Store) load() map[string]*Profile {
	profiles := make(map[string]*Profile)
	for _, p := range builtinProfiles() {
		profiles[p.ID] = p
	}
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
// code-defined and always available.
func (s *Store) Get(id string) (*Profile, bool) {
	if id == DefaultID {
		return DefaultProfile(), true
	}
	p, ok := s.load()[id]
	return p, ok
}

// List returns every user-level profile, sorted by ID for stable output.
func (s *Store) List() []*Profile {
	var out []*Profile
	for _, p := range s.load() {
		if p.Source == SourceBuiltin {
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

// Update rewrites an existing user-level profile.
func (s *Store) Update(p *Profile) error {
	if err := s.validateForStoreWrite(p); err != nil {
		return err
	}
	path := filepath.Join(s.userDir, p.ID+".md")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("profile %q not found", p.ID)
	}
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

// Delete removes a profile. Builtin profiles are protected. A profile with
// channel bindings must be unbound first.
func (s *Store) Delete(id string) error {
	p, ok := s.Get(id)
	if !ok {
		return fmt.Errorf("profile %q not found", id)
	}
	if p.Source == SourceBuiltin {
		return fmt.Errorf("profile %q is builtin and cannot be deleted", id)
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
