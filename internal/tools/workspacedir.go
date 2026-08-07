package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveWorkspaceDir turns the config workspace_dir value into the directory
// path a newly created web session should default its WorkingDir to.
//
//   - "" (unset) and the legacy "auto" -> ~/Octo, the discoverable default
//     for every user. Not created here — the caller MkdirAll's it lazily the
//     first time a session actually needs it. "auto" was the pre-#1986 magic
//     string; the Windows/macOS installers seeded it into fresh configs, so
//     it must keep resolving to the default rather than becoming a literal
//     relative path named "auto".
//   - anything else -> an explicit override, with a leading "~" expanded to
//     the user's home directory.
func ResolveWorkspaceDir(raw string) (string, error) {
	if v := strings.TrimSpace(raw); v != "" && !strings.EqualFold(v, "auto") {
		return expandHome(v), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Octo"), nil
}
