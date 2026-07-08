# octo-agent Project Rules

Canonical guidance for contributing to the octo-agent codebase itself (this is about developing octo, not using it — for that see the other bundled docs). Every PR is human-reviewed.

## Project

`octo-agent` is a Go AI agent CLI (Go 1.22+, single binary). Module path `github.com/open-octo/octo-agent`. All three surfaces are live — CLI/TUI, `octo serve` (web REST + WebSocket + dashboard), and the IM bridge (runs inside `octo serve`).

## Layering

```
cmd/octo/            CLI entry, flag parsing, REPL/TUI, sessions
internal/agent/      Agent loop, history, content blocks, Sender interfaces
internal/provider/   Provider interface + per-vendor implementations
internal/tools/      Concrete ToolExecutor implementations
internal/skills/     SKILL.md discovery + system-prompt manifest
internal/permission/ allow/deny/ask rule engine gating every tool call
internal/hooks/      Lifecycle-event hook engine (hooks.yml)
internal/mcp/        MCP client (stdio + HTTP, OAuth)
internal/server/     octo serve — HTTP REST + WebSocket + embedded dashboard
internal/channel/    IM bridge — adapter interface + platform adapters
internal/workflow/   Named multi-step workflow execution
internal/browser/    Browser automation (CDP) backend
internal/memory/     Cross-session memory
internal/version/    Version constants overridable via -ldflags
pkg/octoagent/       Public Go SDK re-exporting core agent types for embedding octo in other programs
```

Dependency direction is one-way: `provider → agent`, `tools → agent`, never the other way. `cmd/octo` is the only package allowed to import `provider` directly; everything else talks through `agent.Sender` / `StreamingSender` / `ToolSender` / `ToolStreamingSender`.

## Adding capability

- **New provider** — implement `provider.Provider` (required) and optionally the streaming/tool variants, under `internal/provider/<name>/`.
- **New tool** — implement `agent.ToolExecutor` under `internal/tools/<name>.go`, register in `tools.DefaultRegistry`.
- **New skill** — `~/.octo/skills/<name>/SKILL.md`, or `internal/skills/defaults/<name>/SKILL.md` to ship it as a default.

## Before opening a PR

Read `.octorules` and `CLAUDE.md` at the repo root — layering, conventions, and common pitfalls, most "will this land" questions answered there. Branch off latest `main` (never commit on `main` directly); one concept per PR; `make test && make vet && make fmt-check` before pushing; squash-and-merge is the default.

Full contributor guide (what reviewers look for, dependency policy, comment style): **https://octo-agent.dev/docs/community/contributing/** (`web_fetch`).
