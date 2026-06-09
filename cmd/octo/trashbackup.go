package main

import (
	"os"

	"github.com/Leihb/octo-agent/internal/trash"
)

// runTrashBackup copies the given paths into the trash WITHOUT deleting them —
// the Windows safe-delete wrapper (see internal/tools/sandbox.go) calls
// `octo __trash-backup -- <path>...` before the real Remove-Item, so an
// agent-issued delete is recoverable, matching the POSIX rm-to-trash wrapper.
//
// Best-effort by design: per-path failures (a provider path like Env:\X, a
// permission error, a path that isn't a real file) are ignored, and it always
// exits 0 so it can never block the delete the user/model actually asked for.
// The project root comes from OCTO_TRASH_PROJECT (set by the wrapper), falling
// back to the working directory.
func runTrashBackup(paths []string) int {
	project := os.Getenv("OCTO_TRASH_PROJECT")
	if project == "" {
		project, _ = os.Getwd()
	}
	for _, p := range paths {
		if p == "" || p == "--" {
			continue
		}
		_ = trash.Backup(p, project)
	}
	return 0
}
