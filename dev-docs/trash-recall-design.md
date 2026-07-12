# Trash / file recall

Octo never lets an agent-issued deletion or overwrite destroy work irreversibly.
Every destructive filesystem action the agent takes — an `rm`, a `write_file`
that clobbers an existing file, a programmatic delete of a session, skill, or
workflow — first stages the old bytes in a per-project trash under
`~/.octo/trash/`, from which they can be listed, restored, or permanently
discarded. This is the "an AI agent that can't lose your work" guarantee: the
recovery path exists in every interface (CLI, TUI, Web), and the moment right
after a destructive action offers a one-step undo.

The package lives at `internal/trash/`. Every interface reaches it through that
package; nothing else touches the on-disk layout directly.

## On-disk layout

```
~/.octo/trash/
  <project_hash>/                      # sha256(projectDir)[:8], hex
    20260712-153000_a1b2_config.json           # the trashed bytes (file or dir)
    20260712-153000_a1b2_config.json.meta.json # sidecar
```

`project_hash` scopes entries to the project they were deleted from, so listing
and restore never cross project boundaries by accident. The trashed name is
`<timestamp>_<rand>_<basename>`: the `<rand>` token (4 hex chars from the
backing-up process, or `$$`+counter from the shell wrapper) makes the name
collision-proof even when two files with the same basename are deleted in the
same second within one project.

### Entry identity

An entry's `ID` is `<project_hash>.<trash_name>` — a single URL-safe path
segment (the hash is hex, so the first `.` splits it unambiguously). Because the
hash is part of the ID, `Restore(id)` / permanent-delete locate the exact file
without scanning every project and guessing at the first basename match. IDs are
computed fresh by `List()` from the on-disk paths; nothing persists an ID, so
the scheme can evolve without migration.

### Sidecar metadata

```json
{
  "original":    "/abs/path/to/config.json",
  "deleted_at":  "2026-07-12T15:30:00Z",
  "project":     "/abs/path/to/project",
  "deleted_by":  "write_file",
  "kind":        "overwrite"
}
```

`deleted_by` records which surface removed the file (`rm`, `write_file`,
`edit_file`, `session`, `skill`, `workflow`, `scheduler`, `memory`) and `kind`
is `delete` or `overwrite`. Older sidecars that predate these fields read back
as zero values — every reader treats them as optional.

### Human-readable labels

A raw session filename (`20260712-140705-18601496.jsonl`) is meaningless in a
listing — a trash full of them is impossible to tell apart. `Entry.Label`
carries a human-readable name so the UI can lead with something recognizable and
demote the id to a dim secondary line.

`List()` derives the label:

- **Session transcripts** (`~/.octo/sessions/*.jsonl`) → the session title. The
  trashed copy is the complete transcript, so the title is parsed straight from
  it: the first line is the `meta` record (its `title` field is authoritative
  after any rewrite); failing that, a bounded scan picks up a later
  `type:"title"` record. Parsing straight from the trashed transcript recovers
  titles for entries already sitting in the trash, not just future deletions.
- **Other entries** → `Label` is empty and the UI falls back to the basename.

Parsing is bounded (only session-path entries, first line plus a short scan,
skipped for pathologically large files) so listing stays fast.

## What gets captured

| Trigger | Mechanism |
|---|---|
| POSIX `rm` in a shell command | `__octo_safe_rm` wrapper injected by `shellCommand` (`internal/tools/sandbox.go`) hard-links (falls back to copy) each existing target into `$OCTO_TRASH_DIR`, then runs the real `command rm`. |
| Windows `Remove-Item` (and its aliases) | `windowsSafeRmWrapper` shadows the cmdlet, calls `octo __trash-backup -- <path>…` to stage targets, then runs the real cmdlet. |
| `write_file` / `edit_file` overwriting an existing file | The tool calls `trash.Backup` before writing, unless the file is tracked by git and clean (git already holds a recoverable copy). Default on; `trash.overwrite_backup: false` disables it. |
| Programmatic deletes (session, skill, workflow, scheduler, memory) | `trash.Move` — stages then removes, atomically when possible. |

### Preserving `rm` semantics

The shell wrapper stages a *copy* (a hard link when the target and trash share a
filesystem — instant and space-free, which matters for large trees like
`node_modules`; a recursive copy otherwise) and then lets the real `rm` run
against the original path. Staging by moving would make the subsequent `rm`
operate on a missing file and change its exit code and output, which the model
and any surrounding script depend on — so the wrapper never moves, it duplicates
and lets `rm` delete.

The wrapper backs up both relative and absolute path arguments. Relative
arguments are resolved against `$PWD`; absolute arguments are staged as-is. (A
prior version prefixed `$PWD` onto every argument, so `rm /abs/file` silently
staged nothing and the real `rm` still deleted it — absolute-path deletes were
unprotected.)

When no project directory is known (`shellCommand` could not resolve one),
`$OCTO_TRASH_DIR` is unset and the wrapper is a no-op — the delete runs
unprotected, as it must, rather than failing.

### Overwrite protection and git

`write_file` and `edit_file` are the agent's most common destructive action, and
overwriting uncommitted work is the failure this feature most needs to prevent.
Before replacing an existing file, the tool decides whether to stage it:

- **Untracked file** → stage it. Nothing else can recover it.
- **Tracked but dirty** (uncommitted changes) → stage it. The working-tree
  version differs from anything git holds.
- **Tracked and clean** (committed, no local changes) → skip. `git checkout`
  already recovers the exact bytes; staging would only bloat the trash.
- **Not in a git repository / git unavailable** → stage it.

The decision is a helper in `internal/tools`; it runs `git ls-files
--error-unmatch` (tracked?) and `git diff --quiet` + `git diff --cached --quiet`
(clean?) scoped to the single path. The cost is paid only when overwriting an
*existing* file — writing a brand-new file skips the check entirely.

## API

```go
// Stage a copy without removing the original (overwrite protection, Windows rm).
func Backup(originalPath, projectDir string, opts Options) error

// Stage then remove the original, atomically (rename) when same-filesystem,
// falling back to copy+remove across filesystems.
func Move(originalPath, projectDir string, opts Options) error

// List all entries, newest first.
func List() ([]Entry, error)

// Restore an entry to its original path under a conflict policy.
func Restore(id string, policy ConflictPolicy) (RestoreResult, error)

// Remove entries by mode: "all" | "old" (>retention) | "orphans".
func Empty(mode string) (count int, freed int64, err error)

// Permanently delete one entry by ID.
func Delete(id string) (freed int64, err error)

// Enforce retention + size cap (age-out, then evict oldest over the cap).
func Enforce(maxBytes int64, retention time.Duration) (evicted int, freed int64, err error)
```

`Options` carries provenance (`DeletedBy`, `Kind`, `SessionID`). Call sites that
have none pass the zero value.

### Restore conflict policy

`Restore` never silently overwrites a file sitting at the original path (the
common case: you deleted a file, recreated it, and now want the old one back).
`ConflictPolicy` selects the behavior when the destination exists:

- `ConflictAbort` (default) → return `ErrRestoreConflict`; the caller asks the
  user what to do.
- `ConflictBackupExisting` → stage the current file into the trash first, then
  restore. Symmetric and lossless.
- `ConflictRename` → restore alongside as `<name>.restored-<timestamp>` and
  report the new path.

Restore moves across filesystems safely (rename, falling back to copy+remove),
so a trash on a different volume from the project still restores.

## Retention and size cap

Left alone, the trash grows without bound — a single `rm -rf node_modules`
could add gigabytes. `Enforce` runs once at startup (a non-blocking goroutine
from the CLI entry point and from `octo serve`), and is also what the Web UI's
"empty old" and CLI `octo trash empty --old` invoke:

1. Remove every entry older than `retention` (default 14 days).
2. If the remaining total still exceeds `max_size`, evict oldest-first until
   under the cap.

Both bounds are configurable:

```yaml
trash:
  overwrite_backup: true   # stage overwritten files (default true)
  retention_days: 14       # age-out threshold (default 14)
  max_size_mb: 10240       # size cap before oldest-first eviction (default 10240 = 10 GiB)
```

`orphans` (entries whose project directory no longer exists) are surfaced in
listings and cleanable on demand, but are not auto-evicted by age alone —
they're often exactly what a user wants back after a checkout moved a repo.

## Interfaces

### CLI — `octo trash`

```
octo trash [list]                 # table: file, project, age, size, by, tags
octo trash restore <id|substring> # restore; prompts on conflict unless a flag is given
        --overwrite               #   → ConflictBackupExisting
        --as-copy                 #   → ConflictRename
octo trash empty [--all|--old|--orphans]
octo trash rm <id>                # permanent delete of one entry
```

`restore` accepts a unique substring of the id or original path, so a user
doesn't have to copy a full timestamped id. Ambiguous matches list the
candidates instead of guessing.

### TUI — `/trash`

`/trash` lists the current project's recent entries with an index; `/trash
restore <n>` restores by list index, prompting inline on a conflict. This keeps
recovery reachable without leaving the REPL.

### Web — File Recall view

The existing view (`web/src/views/FileRecallView.svelte`) leads each row with
`Entry.Label` when present (the session title), dropping the raw filename to a
dim secondary line, so a trash full of session transcripts is legible at a
glance. It also gains a restore-time conflict prompt (overwrite-with-backup vs
restore-as-copy) and shows each entry's provenance (`deleted_by`, `kind`) and a
"recoverable via git" hint where applicable.

### Inline undo

When `write_file` / `edit_file` overwrites an existing file, its `ToolResult.UI`
carries an `undo_id` — the id of the staged pre-write copy. The Web transcript
renders a one-step "↩ undo" next to that result; clicking it restores the old
bytes with the `backup` conflict policy (the just-written file goes to the trash
first, so the undo is itself reversible). In the TUI the same recovery is one
command away — `/trash restore <id>`. Shell `rm` deletions don't surface an
inline id (the terminal tool can't know what the wrapper staged); those are
recovered from the recall view or `octo trash`.
