package server

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// seedThemesFS holds the themes shipped with the binary. They are ordinary
// user themes — same directory, same format, same API — that octo drops into
// ~/.octo/themes/ the first time it runs, so a fresh install has something to
// look at and a theme author has three worked examples to read.
//
// Embedding rather than compiling them into app.css is the point: a user can
// open blossom/theme.css, change a colour and reload. Only the default pack
// (azure) still lives in app.css, because something has to render before any
// of this is read.
//
//go:embed themes
var seedThemesFS embed.FS

// seedStampFile records which seed themes have already been placed, one id per
// line. It is what separates "this install has never seen blossom" from "the
// user deleted blossom": without it, every start would put a deleted theme
// back, and the delete button in the picker would be a lie.
//
// It also means an upgrade that adds a fourth theme still delivers it, while
// never rewriting the three the user may have edited.
const seedStampFile = ".seeded"

// readSeedStamp returns the ids already placed. A missing or unreadable stamp
// reads as empty: the worst case is re-placing a theme the user deleted, which
// beats failing the start.
func readSeedStamp(dir string) map[string]bool {
	placed := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(dir, seedStampFile))
	if err != nil {
		return placed
	}
	for _, line := range strings.Split(string(data), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			placed[id] = true
		}
	}
	return placed
}

func writeSeedStamp(dir string, placed map[string]bool) error {
	ids := make([]string, 0, len(placed))
	for id := range placed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return os.WriteFile(filepath.Join(dir, seedStampFile), []byte(strings.Join(ids, "\n")+"\n"), 0o644)
}

// seedThemes places any embedded theme this install has not placed before.
//
// Best-effort by design, and the caller ignores the error: a read-only HOME
// costs the user three themes, not their session. An id already stamped is
// skipped whether or not its directory is still there — that is the whole
// point of the stamp — and so is one whose directory exists, so a theme the
// user has edited is never overwritten.
func seedThemes() error {
	dir := themesDir()
	if dir == "" {
		return nil
	}
	entries, err := fs.ReadDir(seedThemesFS, "themes")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	placed := readSeedStamp(dir)
	changed := false
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if placed[id] {
			continue
		}
		// Stamp regardless of whether the copy happens: a directory that is
		// already there belongs to the user, and re-checking it every start
		// would re-place it the day they delete it.
		placed[id] = true
		changed = true
		if _, err := os.Stat(filepath.Join(dir, id)); err == nil {
			continue
		}
		if err := copySeedTheme(id, filepath.Join(dir, id)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if changed {
		if err := writeSeedStamp(dir, placed); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// copySeedTheme writes one embedded theme directory to dest. Themes are flat —
// a manifest, a stylesheet and its wallpapers — so nested directories are not
// something this has to carry.
func copySeedTheme(id, dest string) error {
	files, err := fs.ReadDir(seedThemesFS, "themes/"+id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		data, err := fs.ReadFile(seedThemesFS, "themes/"+id+"/"+f.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dest, f.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
