package tools

import (
	"os"
	"path/filepath"
)

// ResolveWorkspaceDir turns the config workspace_dir value into the directory
// path a newly created web session should default its WorkingDir to.
//
//   - "" (unset) -> ~/Octo, the discoverable default for every user. Not
//     created here — the caller MkdirAll's it lazily the first time a
//     session actually needs it.
//   - anything else -> an explicit override, with a leading "~" expanded to
//     the user's home directory.
func ResolveWorkspaceDir(raw string) (string, error) {
	if raw != "" {
		return expandHome(raw), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Octo"), nil
}
