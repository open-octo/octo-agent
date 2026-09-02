# Expert skills

Skills bundled for the built-in experts (`internal/agentprofile/defaults/`).
Each subdirectory holding a `SKILL.md` ships embedded in the binary and is
materialized to `~/.octo/skills-expert` at startup, discovered with
`Source: "expert"`.

Unlike `../defaults/`, these skills never appear in the global "Available
skills" manifest — they are visible only to an expert whose profile names
them in `tool_skills`. The skill tool refuses to load them for any OTHER
non-builtin expert; builtin profiles and context-less CLI/TUI sessions can
still load one by name. Add content here when an expert needs a capability
that makes no sense in every session's prompt.

Naming discipline: a skill here must not share a name with one in
`../defaults/` — discovery scans this root after the defaults, so a
same-named expert skill would shadow the default one and silently drop it
from every global manifest (a test enforces this).

This file is an embed placeholder (`go:embed` refuses an empty directory)
and is skipped by discovery, which only reads subdirectories.
