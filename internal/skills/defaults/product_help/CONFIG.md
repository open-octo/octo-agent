# octo Configuration Reference

Path: `~/.octo/config.yml`. Every field is optional — a missing file or field falls back to the built-in default. Manage with `octo config` rather than hand-editing where possible. (A pre-rename `~/.octo/config.yaml` is read as a fallback if `config.yml` is absent, and migrates automatically on first save — old file parked as `config.yaml.bak`.)

## Top-level keys

| Key | Type | Description |
|---|---|---|
| `models` | list | Named model configurations — `provider` (`anthropic`\|`openai`\|`custom`), `model`, `protocol` (custom vendor only), `base_url`, `api_key`, `reasoning_effort`, `show_reasoning`, `vision` |
| `default_model` | string | Entry used when nothing else selects one |
| `permission_mode` | string | `interactive` (default) \| `strict` \| `auto` |
| `coauthor` | bool | Append `Co-authored-by` to git commits (default true) |
| `show_reasoning` | bool | Global default for surfacing the reasoning trace to the web UI (default off; terminal never renders it) |
| `workspace_dir` | string | Default working dir for new **web** sessions only; `"auto"` → `~/Desktop/octo` |
| `goal.enabled` | bool | Gates `/goal` and the goal tools (default true) |
| `browser.connect_port` / `browser.attach_running` | int / bool | Chrome connection settings — see `octo browser setup` |
| `memory_backend` | object | Optional external semantic memory (hindsight/mem0/memos) — see `MEMORY.md` |
| `tools.tool_search` | object | MCP Tool Search settings — see `MCP.md` |

## Example

```yaml
models:
  - provider: anthropic
    model: claude-sonnet-5
default_model: claude-sonnet-5
permission_mode: interactive
coauthor: true
workspace_dir: auto
```

Full field reference (every model-entry field, `tools.disabled_skills`, compaction thresholds): **https://octo-agent.dev/docs/reference/config-file/** (`web_fetch`).
