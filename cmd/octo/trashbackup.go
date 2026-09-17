package main

import (
	"os"

	"github.com/open-octo/octo-agent/internal/trash"
)

// runTrashBackup copies the given paths into the trash WITHOUT deleting them —
// the Windows safe-delete wrapper (see internal/tools/sandbox.go) calls
// `octo __trash-backup -- <path>...` before the real Remove-Item, so an
// agent-issued delete is recoverable, matching the POSIX rm-to-trash wrapper.
//
// Best-effort by design: per-path failures (a provider path like Env:\X, a
// permission error, a path that isn't a real file) are ignored, and it always
// exits 0 so it can never block the delete the user/model actually asked for.
// The project root must come from OCTO_TRASH_PROJECT (set by the wrapper). If
// it is absent, no backup is attempted because there is no trusted project.
func runTrashBackup(paths []string) int {
	project := os.Getenv("OCTO_TRASH_PROJECT")
	if project == "" {
		return 0
	}
	for _, p := range paths {
		if p == "" || p == "--" {
			continue
		}
		_, _ = trash.Backup(p, project, trash.Options{DeletedBy: "rm", Kind: "delete"})
	}
	return 0
}
