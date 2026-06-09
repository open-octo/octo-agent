# Cross-session memory

octo's memory is a per-repo directory of plain markdown that the agent manages
with its own file tools — the Claude Code model. There is no dedicated
remember/forget tool, no typed-entry store, and no code-driven consolidation:
the agent reads, writes, edits, and deletes memory files directly, so editing
and deletion are first-class and instant.

This is the agent's *automatic* layer. The *hand-written* layers — `~/.octo/soul.md`,
`~/.octo/user.md`, `~/.octo/octorules.md`, and per-repo `.octorules` — are
separate and described in `identity-files-design.md`.

## Layout

```
~/.octo/memories/<repo-slug>/
  MEMORY.md      index, injected into the system prompt every session
  <topic>.md     detail files the agent creates and reads on demand
```

- **Per repo, shared across worktrees.** The directory is keyed by the repo
  root (`memory.ProjectRoot`), so each project has its own memory and facts
  don't bleed across repos. The root is derived from the git *common* dir
  (`<root>/.git`), which the main checkout and every linked worktree share, so a
  worktree doesn't start with empty project memory; the result is symlink-
  resolved so one repo always maps to one slug. Outside a git repo the working
  directory is used. The slug is the repo basename plus a short hash of the full
  path, so two checkouts that share a basename don't collide.
- **Inheritance.** The home directory (`~`) also has its own memory slot.
  When running inside any project, the home MEMORY.md is injected *before* the
  project MEMORY.md, so cross-project preferences and personal facts are
  available everywhere. The agent is instructed to sort new memories by scope:
  project-specific facts go to the project memory; cross-project or personal
  preferences go to the home (inherited) memory.
- **MEMORY.md is the index.** It is loaded into the system prompt at session
  start, truncated to the first 200 lines / 25 KB (whichever comes first),
  mirroring Claude Code's cap. When the file exceeds that budget the injected
  block carries an explicit truncation warning (so entries past the cut aren't
  dropped silently), and `octo memory` lints for it. Topic files are not loaded
  up front — the agent reads them on demand with its file tools when MEMORY.md
  points at one.

## Injection

At session start `cmd/octo` resolves the directory, creates it, and injects
`memory.RenderInjection(dir, inheritedDirs...)` into the composed system prompt
(the `memory` layer of `prompt.Compose`). The injection is a short instruction
block — *where* memory lives and *how* to manage it — followed by inherited
MEMORY.md files (home directory first) and then the project MEMORY.md (or an
"empty" marker so a fresh project knows where to start). The notes are framed
as the agent's own durable record of the user's preferences, workflow rules,
and project facts, to be followed as standing guidance; the current user request
and safety override a conflicting note. The block is frozen for the session:
what the agent writes now surfaces in the *next* session, not the current one.

The session-prompt guidance (`internal/prompt/base.md`, "Memory" section)
covers when to save (lasting preferences, corrections + the why, validated
judgment, external resources), what not to save (one-off task state, anything
derivable from the repo, secrets), grounding answers in memory with a brief
inline attribution, and verifying a remembered file/flag still exists before
acting on it.

## Attention layer — structured rules, re-surfaced at the point of action

A note buried in the frozen system-prompt block is easy for the model to skim
past by the time it matters, many turns later. MEMORY.md may therefore carry two
optional sections whose rules are written **in full** (not as pointer links) and
re-surfaced on the message stream when they're relevant:

```
## 必须遵守        always-apply rules — restated every turn
## 触发提醒        each bullet "(触发: kw1, kw2) rule text" — recalled on a keyword hit
```

`memory.ParseRules` extracts these tiers (section headings are matched by
keyword — `必须遵守`/`always`, `触发`/`trigger` — tolerant of emoji and heading
level). `memory.Injector.Reminder` renders the per-turn `<system-reminder>`:
always-apply rules on every turn, plus any triggered rules whose keywords occur
in the user input, each surfaced at most once per session. Trigger matching is
deliberately conservative and one-directional — *input contains trigger* —
with ASCII keywords matched on word boundaries (`deploy` does not fire on
`deployment`) and CJK keywords matched as substrings (`部署` fires inside
`帮我部署一下`).

`cmd/octo` builds the injector once per session and wires it as
`agent.UserInputHook`, which folds the reminder into the user message at the
single `History.Append` choke point in `Turn`/`TurnStream`/`runLoop` (one
appended message, so the error-path `popLast` rollback still removes exactly one
turn). The reminder rides the message stream rather than the system prompt, so
the cached prompt prefix stays byte-stable across the session.

A MEMORY.md without these sections — the plain pointer-index format — parses to
zero rules, sets no hook, and behaves exactly as before.

## Writing — file tools, whitelisted directory

The agent saves with `write_file` (append to MEMORY.md or a topic file), edits
with `edit_file`, and removes with `terminal` (`rm`/`mv`). The memory directory
lives outside the working directory, where the permission engine's default
`write_file`/`edit_file` rules only auto-allow `$CWD/**`. So `cmd/octo` passes
both the project memory directory and the inherited home memory directory to
`permission.New(..., allowWriteRoots...)`, which prepends an
`allow { path: [<memDir>, <memDir>/**] }` rule to those tools — the agent
manages its memory without a prompt on every save, while CWD and
secret-path rules still apply everywhere else.

## Inspecting

- `octo memory list` — list the project's memory files; `octo memory path` —
  print the directory.
- `/memory` in the TUI — the same listing.

These are viewers/locators only; the files are the source of truth and the
agent owns them.

## Why this shape

The earlier design was a typed one-file-per-fact store written through a
`remember` tool and folded into consolidated summaries by a background
sub-agent. It had no way to remove a fact once consolidated — a wrong or
obsolete entry lived in the summary prose with no addressable handle, re-injected
every session. The file model removes that gap by construction: memory is just
files, so correcting or forgetting is an ordinary edit or delete. It also drops
a large amount of machinery (typed entries, summaries, state, archive, git
auto-commit, the remember/forget tools, the per-turn nudge) in favour of the
tools the agent already has.
