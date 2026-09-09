# octo Configuration Reference

Path: `~/.octo/config.yml`. Every field is optional — a missing file or field falls back to the built-in default. Manage with `octo config` rather than hand-editing where possible. (A pre-rename `~/.octo/config.yaml` is read as a fallback if `config.yml` is absent, and migrates automatically on first save — old file parked as `config.yaml.bak`. A flat top-level `models:` list from before the two-level schema is likewise rewritten into `endpoints:` on first save, each entry becoming a `legacy-<host>-<n>` endpoint.)

## Top-level keys

| Key | Type | Description |
|---|---|---|
| `endpoints` | list | Configured providers. Each bundles connection params (`id`, `name`, `provider` (`anthropic`\|`openai`\|`custom`), `base_url`, `api_key`, `protocol` (custom vendor only), optional `lite_model`, optional `rpm` / `max_concurrency` — see [Rate-limiting an endpoint](#rate-limiting-an-endpoint)) and a `models` list whose entries are `{ model, vision }` |
| `default` | string | Composite id `<endpoint-id>::<model>` used when nothing else selects one; empty → first endpoint's first model. **Only seeds new sessions** — a session that already has turns keeps the model it was bound to until you switch it explicitly (TUI/web chip/IM `/model`/`/api/sessions/{id}/model`). |
| `lite` | string | Composite id `<endpoint-id>::<model>` for cheap internal calls (compaction summaries, session titles) |
| `permission_mode` | string | `interactive` (default) \| `strict` \| `auto` |
| `coauthor` | bool | Append `Co-authored-by` to git commits (default true) |
| `reasoning_effort` | string | Global reasoning intensity: `low`\|`medium`\|`high`\|`xhigh`\|`max`; empty = off |
| `show_reasoning` | bool | Global default for surfacing the reasoning trace to the web UI (default off; terminal never renders it) |
| `workspace_dir` | string | Default working dir for new **web** sessions only; empty → `~/Octo`, or set a literal path to override |
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
workspace_dir: ~/projects
```

## Rate-limiting an endpoint

Free tiers usually cap requests per minute and simultaneous requests, and answer `HTTP 429` past
either. Two optional per-endpoint keys make octo hold its own requests back instead:

| Key | Type | Description |
|---|---|---|
| `rpm` | int | Most requests octo starts against this endpoint per rolling 60-second window. `0`/absent = unlimited |
| `max_concurrency` | int | Most requests in flight at once against this endpoint. `0`/absent = unlimited |

```yaml
endpoints:
  - id: free-tier
    provider: custom
    base_url: https://api.example.com/v1
    protocol: openai
    rpm: 8              # the provider's documented per-minute quota
    max_concurrency: 1  # and its concurrent-request quota
    models:
      - model: glm-4-flash
        vision: false
```

What to tell a user asking about this:

- **Both gates cover every caller sharing the endpoint** — the main conversation, each sub-agent,
  each workflow step, background session-title generation and the vision helper. They queue behind
  one shared limiter, so the quota holds no matter how many run at once.
- **Requests wait, they don't fail.** A call over the limit blocks until the window frees up. With
  a tight `rpm` a busy turn feels slower — that's the trade, and it beats a 429 killing the turn.
- **Set them from the provider's published quota**, not by guesswork. `rpm` alone is usually the
  one that matters: a single conversation is already serial, yet one turn can easily issue a dozen
  requests a minute through tool-call rounds.
- **There is no UI for these** — no settings page, no `octo config` prompt. Hand-edit
  `~/.octo/config.yml`. Editing an endpoint in the Web UI (rename, key, headers) leaves them alone.
- **When an edit takes effect:** a new CLI run picks it up immediately; a running `octo serve`
  caches its senders, so restart it (`octo serve stop`, then relaunch) after editing the file.
- **They're per endpoint, not per key or per model.** Two endpoints pointing at the same base URL
  and provider share one limiter; every model under an endpoint shares its budget.
- Transient 429s are retried with backoff regardless of these settings (up to 4 attempts, honouring
  a `Retry-After` header for up to 2 minutes), so a limit that's slightly too loose degrades rather
  than breaking.

Full field reference (every endpoint/model field, `tools.disabled_skills`, compaction thresholds): **https://octo-agent.dev/docs/reference/config-file/** (`web_fetch`).
