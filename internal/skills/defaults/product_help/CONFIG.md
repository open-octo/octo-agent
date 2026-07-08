# octo Configuration Reference

## Config file

Path: `~/.octo/config.yml`

Created interactively via `octo config` or edited directly. (A pre-rename `~/.octo/config.yaml` is read as a fallback if `config.yml` is absent, and gets migrated to `config.yml` — with the old file parked as `config.yaml.bak` — on the first save.)

## Fields

| Field | Type | Description |
|-------|------|-------------|
| `provider` | string | `"anthropic"`, `"openai"`, or `"custom"` (any Anthropic/OpenAI-compatible endpoint via `CUSTOM_API_KEY`/`CUSTOM_BASE_URL`) |
| `model` | string | Model name (e.g. `claude-sonnet-4-20250514`, `gpt-4.1`) |
| `base_url` | string | Optional custom API endpoint |
| `permission_mode` | string | `"interactive"` (default), `"strict"`, or `"auto"` |
| `coauthor` | bool | Append `Co-authored-by` to git commits |
| `show_reasoning` | bool | Surface the reasoning/thinking trace for the web UI to display (default off; the terminal never renders it) |
| `reasoning_effort` | string | `"low"`, `"medium"`, `"high"`, `"xhigh"`, `"max"`, or `""` (off) |
| `api_key` | string | Plaintext fallback (discouraged — use env var instead) |
| `workspace_dir` | string | Default working directory for new **web** sessions only. Empty (default) = the server's own launch directory; `"auto"` = `~/Desktop/octo`; anything else = a literal path. Doesn't affect CLI/TUI sessions |
| `goal.enabled` | bool | Gates the `/goal` surface and goal tools (default true — inert until a goal is actually created) |
| `browser.connect_port` | int | Chrome remote-debugging port to attach to (set by `octo browser setup`) |
| `browser.attach_running` | bool | Attach to your already-running Chrome via its default profile, reusing the logged-in session |
| `language` | string | UI language preference: `"en"` (default) or `"zh"` |
| `tools.tool_search` | object | MCP Tool Search settings — `enabled: auto\|on\|off`, `threshold_pct: <int>`. See `MCP.md` |
| `memory_backend` | object | Optional external semantic memory (hindsight/mem0/memos) — `type`, `base_url`, `api_key`, `namespace`. Unset disables it entirely. See `MEMORY.md` |

## Precedence

CLI flags (`--provider`, `--model`, etc.) > env vars (`OCTO_PROVIDER`, `ANTHROPIC_API_KEY`, etc.) > config file > built-in defaults.

## Permission modes

- **interactive** (default) — Prompt for approval when a rule says `ask`.
- **strict** — Auto-deny when a rule says `ask`. Used for non-interactive callers (HTTP server, IM bridge) by default.
- **auto** — Auto-allow when a rule says `ask`. Useful for trusted, repetitive workflows — use with caution.

Cycle modes in the TUI with **Shift+Tab**; a web session can also override its own mode via the composer status bar (per-session, doesn't touch this global default). See `PERMISSIONS.md` for the full rule engine.

## Example

```yaml
provider: anthropic
model: claude-sonnet-4-20250514
coauthor: true
show_reasoning: true
reasoning_effort: medium
workspace_dir: auto
```
