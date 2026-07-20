# octo Configuration Reference

Path: `~/.octo/config.yml`. Every field is optional — a missing file or field falls back to the built-in default. Manage with `octo config` rather than hand-editing where possible. (A pre-rename `~/.octo/config.yaml` is read as a fallback if `config.yml` is absent, and migrates automatically on first save — old file parked as `config.yaml.bak`. A flat top-level `models:` list from before the two-level schema is likewise rewritten into `endpoints:` on first save, each entry becoming a `legacy-<host>-<n>` endpoint.)

## Top-level keys

| Key | Type | Description |
|---|---|---|
| `endpoints` | list | Configured providers. Each bundles connection params (`id`, `name`, `provider` (`anthropic`\|`openai`\|`custom`), `base_url`, `api_key`, `protocol` (custom vendor only), optional `lite_model`) and a `models` list whose entries are `{ model, vision }` |
| `default` | string | Composite id `<endpoint-id>::<model>` used when nothing else selects one; empty → first endpoint's first model |
| `lite` | string | Composite id `<endpoint-id>::<model>` for cheap internal calls (compaction summaries, session titles) |
| `permission_mode` | string | `interactive` (default) \| `strict` \| `auto` |
| `coauthor` | bool | Append `Co-authored-by` to git commits (default true) |
| `reasoning_effort` | string | Global reasoning intensity: `low`\|`medium`\|`high`\|`xhigh`\|`max`; empty = off |
| `show_reasoning` | bool | Global default for surfacing the reasoning trace to the web UI (default off; terminal never renders it) |
| `workspace_dir` | string | Default working dir for new **web** sessions only; `"auto"` → `~/Desktop/octo` |
| `goal.enabled` | bool | Gates `/goal` and the goal tools (default true) |
| `browser.connect_port` / `browser.attach_running` | int / bool | Chrome connection settings — see `octo browser setup` |
| `memory_backend` | object | Optional external semantic memory (hindsight/mem0/agentmemory) — see `MEMORY.md` |
| `tools.tool_search` | object | MCP Tool Search settings — see `MCP.md` |

## Example

```yaml
endpoints:
  - id: anthropic
    provider: anthropic
    models:
      - model: claude-sonnet-5
        vision: true
default: anthropic::claude-sonnet-5
permission_mode: interactive
coauthor: true
workspace_dir: auto
```

Full field reference (every endpoint/model field, `tools.disabled_skills`, compaction thresholds): **https://octo-agent.dev/docs/reference/config-file/** (`web_fetch`).
