---
title: Sandbox the agent
description: OS-enforced confinement for the terminal tool.
---

`--sandbox` confines the `terminal` tool to the project directory plus temp, with no network,
enforced by the OS — macOS Seatbelt, Linux Landlock + seccomp. It's off by default and **fails
closed** when the OS mechanism is unavailable, rather than silently running unconfined.

```bash
octo --sandbox                              # confine, deny network
octo --sandbox --sandbox-allow-net          # allow network
octo --sandbox --sandbox-write ./build      # extra writable dir (repeatable)
octo --sandbox --sandbox-read /opt/data     # extra readable dir (repeatable)
```

## Platform support

Linux and macOS are first-class. **`--sandbox` is unavailable on Windows** — OS confinement is
Seatbelt/Landlock only, so on Windows `--sandbox` refuses to run rather than pretend to confine
anything. The permission engine (interactive prompts before each tool call) is the safety layer
there instead.

## Recycle bin

Confinement stops the agent from reaching *outside* the project. The recycle bin is the safety net
*inside* it: an agent that goes off the rails and deletes or overwrites the wrong file shouldn't cost
you your work. octo keeps a file-level trash at `~/.octo/trash/`, scoped per project.

- **Deletes are intercepted.** Agent-issued `rm` / `del` / `Remove-Item` are wrapped so the targets
  are copied to the trash *before* removal. The same guard covers deletions the agent makes through
  sessions, skills, workflows, the scheduler, and memory.
- **Overwrites are backed up.** Before `write_file` / `edit_file` overwrites an existing file, its
  previous contents are staged into the trash and the tool result offers a one-click **Undo**. Files
  that are git-tracked and clean are skipped — `git checkout` already recovers those, so the bin
  stays lean.
- **Restore never clobbers.** Restoring a file that has since been recreated at the same path won't
  silently overwrite it: octo aborts, restores alongside, or moves the current file to the trash
  first — your choice.
- **Provenance is recorded.** Each entry knows what removed it (`rm`, `write_file`, a session and its
  title, a skill, a workflow…) and when.
- **Bounded automatically.** Entries age out after 14 days and the bin is capped at 10 GiB, evicting
  oldest-first. Configure both in `config.yml`:

```yaml
trash:
  retention_days: 14   # 0 = default (14); negative disables age-out
  max_size_mb: 10240   # 0 = default (10 GiB); negative disables the cap
  overwrite_backup: true  # back up files before write_file/edit_file overwrites
```

Restore, undo, and empty the bin from the Web UI's **File Recall** panel (`octo serve`), or from
the CLI:

```bash
octo trash list                  # what's in the bin, newest first
octo trash restore <id|path>     # put a file back (fuzzy path match works)
octo trash restore --as-copy …   # restore under a timestamped name if the original path is taken
octo trash rm <id> / empty       # delete one entry / empty the bin
```

Next: pair sandboxing with [hooks](/docs/guides/hooks/) for a fully automated, still-confined loop,
or read the full boundary in the [security model](/docs/reference/security/).
