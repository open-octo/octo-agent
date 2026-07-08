# octo-agent Project Rules

The canonical project guidance for contributors and AI coding agents. Keep this short — substantive design context belongs in `dev-docs/`.

## Project

`octo-agent` is a Go AI agent CLI (Go 1.22+, single binary). Module path `github.com/open-octo/octo-agent`. All three surfaces are live — CLI/TUI, `octo serve` (web REST + WebSocket + dashboard), and the IM bridge (runs inside `octo serve`).

## Layering

```
cmd/octo/           CLI entry, flag parsing, REPL/TUI, sessions
internal/agent/     Agent loop, history, content blocks, Sender interfaces
internal/provider/  Provider interface + per-vendor implementations
internal/tools/     Concrete ToolExecutor implementations
internal/skills/    SKILL.md discovery + system-prompt manifest
internal/permission/ allow/deny/ask rule engine gating every tool call
internal/hooks/     Lifecycle-event hook engine (hooks.yml)
internal/mcp/       MCP client (stdio + HTTP, OAuth)
internal/server/    octo serve — HTTP REST + WebSocket + embedded dashboard
internal/channel/   IM bridge — adapter interface + platform adapters
internal/workflow/  Named multi-step workflow execution
internal/browser/   Browser automation (CDP) backend
internal/memory/    Cross-session memory
internal/version/   Version constants overridable via -ldflags
pkg/octoagent/      Public Go SDK re-exporting core agent types for embedding octo in other programs
```

Dependency direction is one-way: `provider → agent`, `tools → agent`, never the other way. `cmd/octo` is the only package allowed to import `provider` directly; everything else talks through `agent.Sender` / `StreamingSender` / `ToolSender` / `ToolStreamingSender`.

## Adding capability

- **New provider** — implement `provider.Provider` (required) and optionally `provider.StreamingProvider`, `provider.ToolProvider`, `provider.ToolStreamingProvider`. Put it under `internal/provider/<name>/`. Each protocol's wire-format quirks are isolated inside the package; the agent layer must not learn about them.
- **New tool** — implement `agent.ToolExecutor` and `Definition() agent.ToolDefinition` returning the JSON Schema the LLM sees. Place it under `internal/tools/<name>.go`. Register it in `tools.DefaultRegistry` and add it to `tools.DefaultTools()` if it belongs in the default set.
- **New skill** — `~/.octo/skills/<name>/SKILL.md` with the same frontmatter format Claude Code uses, or `internal/skills/defaults/<name>/SKILL.md` to ship it as a default. The skill loader composes existing tools — adding a skill should not require new tool code.

## Code style

- Go 1.22 syntax. `gofmt -w` is the formatter; `go vet ./...` must pass.
- Tests live next to code (`foo.go` + `foo_test.go`). Use `httptest.NewServer` for HTTP-mocked tests; do not hit live APIs in `go test ./...`.
- Comments in English. Prefer self-documenting names over comments. Only comment the **why**, never the **what**.
- No new third-party dependencies without justification in the PR description.

## Workflow

- **Branch off latest `main`** before editing. Never commit on `main` directly.
- Push lands via PR only. Squash-and-merge is the project default; force-push only after explicit approval.
- Commit messages and PR descriptions in English.
- One concept per PR. Mass mechanical changes (rename, move) can ride together but should be a single self-contained change set.

## Testing

- `make test` runs `go test -race ./...` — must be green before pushing.
- Integration tests against real provider APIs are run by hand with a real key, not in CI.

## What lives in `dev-docs/`

Architecture decisions, milestone plans, and verified-fact dumps (e.g. the iLink protocol notes in the roadmap). One Markdown file per topic. Don't commit speculative or unverified claims — verify before writing.
