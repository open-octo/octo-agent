package tools

import (
	"os"
	"path/filepath"
)

// ResolveWorkspaceDir turns the config workspace_dir value into the directory
// path a newly created web session should default its WorkingDir to.
//
//   - "" (unset) -> "" — no override, session keeps today's behavior (the
//     server's own launch directory).
//   - "auto" -> ~/Desktop/octo, a discoverable default for non-technical
//     users. Not created here — the caller MkdirAll's it lazily the first
//     time a session actually needs it.
//   - anything else -> a power-user literal path override, with a leading
//     "~" expanded to the user's home directory.
func ResolveWorkspaceDir(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw != "auto" {
		return expandHome(raw), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Desktop", "octo"), nil
}
