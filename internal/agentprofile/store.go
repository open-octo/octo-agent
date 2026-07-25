package agentprofile

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store loads profiles from the user-level and project-level agent
// directories plus the code-defined builtins, and answers every query by
// rescanning: it is deliberately read-through, so a change made through any
// path (REST API, Web UI, direct .md edit) is visible on the next read. The
// directories hold a handful of small files, so a rescan is cheap — this is
// exactly how the pre-existing discoverAgents loader already behaved.
//
// Precedence is project > user > builtin: a user .md can shadow a builtin
// (e.g. a personal "explore"), and a project .md can shadow both.
type Store struct {
	userDir    string
	projectDir func() string // resolved per scan (cwd-dependent); nil disables
}

// New builds a Store over the user-level directory. projectDir, when non-nil,
// is consulted on every scan for the delegation-only project-level directory.
func New(userDir string, projectDir func() string) *Store {
	return &Store{userDir: userDir, projectDir: projectDir}
}

// load scans all sources and returns the merged id → profile map. Files that
// fail to parse are skipped (a broken profile must never take the others down
// with it, and the next read self-heals once the file is fixed).
func (s *Store) load() map[string]*Profile {
	profiles := make(map[string]*Profile)
	for _, p := range builtinProfiles() {
		profiles[p.ID] = p
	}
	s.scanDir(s.userDir, SourceUser, profiles)
	if s.projectDir != nil {
		s.scanDir(s.projectDir(), SourceProject, profiles)
	}
	return profiles
}

// scanDir merges *.md profiles from dir into dst, overwriting same-named
// entries of lower precedence. A missing or unreadable dir is a no-op.
//
// The read path accepts any .md basename as an ID (zero-migration
// compatibility with the pre-existing agent loader); the slug rule is
// enforced on writes instead. The reserved "default" ID is skipped so a
// stray default.md can never shadow the code-defined default agent.
func (s *Store) scanDir(dir string, src Source, dst map[string]*Profile) {
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
		p, err := parseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Printf("agentprofile: skipping %s: %v", filepath.Join(dir, e.Name()), err)
			continue // broken file: skip, self-heal next read
		}
		p.ID = id
		p.Source = src
		if src == SourceProject {
			// Delegation-only: the platform slice from a project file must
			// never influence IM routing.
			p.MentionAs = nil
			p.ChannelBindings = nil
		}
		dst[id] = p
	}
}

// Load performs a full scan. With the read-through design it has no cached
// state to refresh and unreadable directories are tolerated as empty — it
// exists to keep the documented API shape and as a scan smoke hook for tests.
func (s *Store) Load() error {
	s.load()
	return nil
}

// Get returns the profile with the given id, honoring the project > user >
// builtin precedence. The default profile is code-defined and always
// available.
func (s *Store) Get(id string) (*Profile, bool) {
	if id == DefaultID {
		return DefaultProfile(), true
	}
	p, ok := s.load()[id]
	return p, ok
}

// List returns every non-builtin profile (user- and project-level), sorted by
// ID for stable output.
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

// Create validates p and writes it as <userDir>/<id>.md. It refuses to
// overwrite an existing file (including one shadowing a builtin — shadowing
// stays a deliberate, hand-written affair), to use the reserved default ID,
// or to claim a mention alias another profile already holds.
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

// Update rewrites an existing user-level profile. Builtin and project-level
// profiles are immutable through the Store: edit code or the project file.
func (s *Store) Update(p *Profile) error {
	if err := s.validateForStoreWrite(p); err != nil {
		return err
	}
	path := filepath.Join(s.userDir, p.ID+".md")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("profile %q not found at user level (builtin/project profiles are read-only here)", p.ID)
	}
	return s.writeFile(path, p)
}

// validateForStoreWrite is the write gate shared by Create and Update: schema
// validation, the reserved default ID, and global mention-alias uniqueness
// (the design's anti-impersonation rule — only the Store can see all
// profiles, so the check lives here).
func (s *Store) validateForStoreWrite(p *Profile) error {
	if err := validateForWrite(p); err != nil {
		return err
	}
	if p.ID == DefaultID {
		return fmt.Errorf("id %q is reserved for the code-defined default agent", DefaultID)
	}
	for id, other := range s.load() {
		if id == p.ID || other.Source != SourceUser {
			continue
		}
		for _, alias := range p.MentionAs {
			for _, taken := range other.MentionAs {
				if alias == taken {
					return fmt.Errorf("mention alias %q is already claimed by profile %q", alias, id)
				}
			}
		}
	}
	return nil
}

// Delete removes a user-level profile. A profile with channel bindings must
// be unbound first, so an IM chat is never left routing to a ghost.
func (s *Store) Delete(id string) error {
	p, ok := s.Get(id)
	if !ok {
		return fmt.Errorf("profile %q not found", id)
	}
	if p.Source != SourceUser {
		return fmt.Errorf("profile %q is %s-level and cannot be deleted through the Store", id, p.Source)
	}
	if len(p.ChannelBindings) > 0 {
		return fmt.Errorf("profile %q still has %d channel binding(s): unbind them first", id, len(p.ChannelBindings))
	}
	return os.Remove(filepath.Join(s.userDir, id+".md"))
}

// writeFile serializes p and writes it via temp-file + rename, so a
// concurrent read-through scan never observes a partially written file.
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

// userProfiles scans only the user-level directory. IM routing queries
// (ByChannel/ByMention) build on this — not on the merged map — so a
// project-level file shadowing a user profile's ID (delegation override)
// can never silence that profile's conversation-mode routing.
func (s *Store) userProfiles() map[string]*Profile {
	m := make(map[string]*Profile)
	s.scanDir(s.userDir, SourceUser, m)
	return m
}

// ByChannel returns the user-level profiles bound to the given IM chat. The
// router treats >1 as "ambiguous: stay silent unless @-mentioned".
func (s *Store) ByChannel(platform, chatID string) []*Profile {
	var out []*Profile
	for _, p := range s.userProfiles() {
		for _, b := range p.ChannelBindings {
			if b.Platform == platform && b.ChatID == chatID {
				out = append(out, p)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ByMention resolves an @-alias (including the @) to a user-level profile.
func (s *Store) ByMention(alias string) (*Profile, bool) {
	for _, p := range s.userProfiles() {
		for _, a := range p.MentionAs {
			if a == alias {
				return p, true
			}
		}
	}
	return nil, false
}
