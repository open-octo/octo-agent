# Expert skills

Skills bundled for the built-in experts (`internal/agentprofile/defaults/`).
Each subdirectory holding a `SKILL.md` ships embedded in the binary and is
materialized to `~/.octo/skills-expert` at startup, discovered with
`Source: "expert"`.

Unlike `../defaults/`, these skills never appear in the global "Available
skills" manifest — they are visible only to an expert whose profile names
them in `tool_skills`, and the skill tool refuses to load them for anyone
else (except the builtin Default agent). Add content here when an expert
needs a capability that makes no sense in every session's prompt.

This file is an embed placeholder (`go:embed` refuses an empty directory)
and is skipped by discovery, which only reads subdirectories.
