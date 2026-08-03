// Package uploads holds shared primitives for the on-disk lifetime of files
// the agent receives as attachments: web uploads and IM-channel images under
// ~/.octo/uploads (managed by internal/server), and IM-channel document/file
// attachments under per-adapter OS-temp directories (this package). Neither
// location is ever swept by the code that writes into it — this package lets
// a single startup housekeeping pass (cmd/octo) age both out, without
// internal/channel/adapters/* needing to import internal/server (which
// already imports internal/channel).
package uploads

import (
	"os"
	"path/filepath"
	"time"
)

// ChannelTempDir returns (creating if necessary) the namespaced OS-temp
// subdirectory an IM channel adapter should write inbound file attachments
// into, e.g. "<tmp>/octo-telegram". Namespacing keeps Sweep from touching
// unrelated files that happen to sit in the shared OS temp root.
func ChannelTempDir(adapter string) (string, error) {
	dir := filepath.Join(os.TempDir(), "octo-"+adapter)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Sweep removes regular files directly under dir whose modification time is
// older than maxAge. maxAge <= 0 disables it (returns immediately). A missing
// dir is not an error — there's nothing to sweep. Returns the count removed
// and bytes freed; per-file removal errors are skipped rather than aborting
// the sweep, since a locked/already-gone file shouldn't block the rest.
func Sweep(dir string, maxAge time.Duration) (removed int, freed int64, err error) {
	if maxAge <= 0 {
		return 0, 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		if rerr := os.Remove(filepath.Join(dir, e.Name())); rerr == nil {
			removed++
			freed += info.Size()
		}
	}
	return removed, freed, nil
}
