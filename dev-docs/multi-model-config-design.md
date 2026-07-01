# Multi-Model Configuration

`~/.octo/config.yml` holds a list of model configurations rather than a single
provider/model pair. One entry is the default for new sessions; each web
session can switch to a different entry without affecting others; an optional
lite entry serves history compaction. The file sits alongside
`permissions.yml` and `channels.yml`.

## Goals

- Persist multiple model configs, each with its own provider, base URL, API
  key, and reasoning settings.
- Drive the settings panel's multi-card CRUD and set-default against real
  storage.
- Per-session model selection: `POST /api/sessions` can name a config entry;
  `PATCH /api/sessions/{id}/model` switches that session only, not the global
  default.
- Run compaction summarisation on the lite entry when one is configured.

## Non-goals

- **Mid-turn model switching.** A model change takes effect on the next turn.
- **Per-sub-agent model routing.** Sub-agents inherit the spawning session's
  sender, unchanged.
- **Failover / load balancing** between entries. One session, one model.
- **IM / cron model selection.** Channel and cron sessions use the default
  entry; per-session switching is a web-UI affordance.
- **Merging config files.** `mcp.json`, `permissions.yml`, `channels.yml`
  stay separate (different writers, different lifecycles).

## Config schema

```yaml
# ~/.octo/config.yml
models:
  - provider: anthropic
    model: claude-fable-5                 # the entry's identity
    base_url: https://api.anthropic.com   # optional; empty = vendor default
    api_key: sk-...                       # optional; env var preferred
    reasoning_effort: high                # optional, per-model
    show_reasoning: true                  # optional, per-model
  - provider: kimi
    model: kimi-k2.6

default_model: claude-fable-5   # entry used when nothing else selects one
lite_model: ""                  # optional model for cheap internal calls

# global fields:
permission_mode: interactive
access_key: ...
coauthor: true
compact_auto_pct: 75
compact_batch_threshold: 0
tools:
  ...
```

Decisions:

- **The model string is the identity.** `model` is unique across entries; the
  HTTP API uses it as `{id}`, `--model` selects the whole entry by it, and
  `default_model` / `lite_model` reference it. There is no separate name, so
  editing a model is a single edit — nothing else to keep in sync. Two entries
  may not share a model string; a create request for a model that already
  exists is rejected.
- **`reasoning_effort` / `show_reasoning` are per-entry.** A reasoning model
  and a non-reasoning model need different settings; `app.SenderOptions`
  carries both per-sender (`internal/app/sender.go`). The `show_reasoning` key
  also exists globally as the default when an entry leaves it unset.
- **`permission_mode` is global.** `saveModelRequest` carries it on the model
  card, but the backend applies it globally.
- **default / lite are references, not entry types.** The panel's
  `type: "default" | "lite"` badges are derived by the server from
  `default_model` / `lite_model` when building `GET /api/config`; nothing is
  stored per entry.

## File resolution and legacy migration

- `config.Path()` returns `~/.octo/config.yml`. `Load()` reads it, falling back
  to legacy `~/.octo/config.yaml` when the new file is absent.
- A legacy file (top-level `provider` / `model` / `base_url` / `api_key` /
  `reasoning_effort`, no `models:` block) is normalised in memory: those fields
  become `models[0]` and `default_model` is set to that model string. Global
  fields pass through.
- `Save()` always writes the `models:` schema to `config.yml`. A legacy
  `config.yaml` present at save time is renamed to `config.yaml.bak` —
  non-destructive, and it can never shadow the new file because `config.yml`
  wins the read order.
- A stale `name:` on an entry in an older `config.yml` is ignored on load
  (unknown YAML field). A `default_model` / `lite_model` that referenced such a
  name no longer matches; `DefaultEntry` then falls back to the first entry.

## Resolution precedence (CLI)

`resolveProviderModel` (`cmd/octo/config.go`):

1. `--model <value>`: when the value equals a config entry's model, the whole
   entry applies — provider, model, base URL, API key, reasoning settings.
   Otherwise the value is a raw model string under the separately-resolved
   provider.
2. Env vars (`OCTO_PROVIDER`, `<PROVIDER>_MODEL`, key env vars).
3. Otherwise the `default_model` entry, else the first entry.

## Server: per-session sender binding

- `agent.Session` (`internal/agent/session.go`) carries
  `ModelConfig string \`json:"model_config,omitempty"\`` — the model string of
  the config entry the session is bound to. Empty means "the default entry at
  turn time", which is also the value for every session predating per-session
  binding.
- The server keeps a `senderCache map[string]agent.Sender` keyed by model,
  built lazily via `app.NewSender` from the entry's fields. Any mutation through
  `/api/config/models*` invalidates the cache; the next turn rebuilds. The
  server's default `sender`/`model` pair serves unbound sessions and is rebuilt
  from config on relevant changes; startup resolution doubles as the
  onboarding-mode probe (no key → onboarding).
- The turn path resolves session → entry (by model) → (sender, model string,
  reasoning options) at the start of each turn, so a switch applies from the
  next turn. A binding whose model no longer exists degrades to the default
  sender rather than failing the turn.
- `PATCH /api/sessions/{id}/model`: `model_id` naming a config entry binds the
  session to it; any other value stays a raw model string on the default
  sender. No global config write.
- `POST /api/sessions`: `req.Model` naming a config entry binds the session to
  it; a non-matching value keeps raw-model-string behaviour on the default
  sender.

## Config CRUD API

Handlers live in `internal/server/onboard_config_handlers.go`.

| Route | Behaviour |
|---|---|
| `GET /api/config` | Full `models` list (`id` = model, keys masked via `maskKey`), `default_model_idx`, `type` derived from `default_model` / `lite_model`. |
| `POST /api/config/models` | Append a new entry. A model that already exists is a `409` conflict. The first entry becomes the default. Returns the id (the model). |
| `PATCH /api/config/models/{id}` | Update the entry with model `{id}`. An empty or masked `api_key` keeps the stored key (the panel echoes masked keys). Changing the model re-keys the entry — the id changes with it, a collision with another entry is a `409`, and `default_model` / `lite_model` references carry over. |
| `DELETE /api/config/models/{id}` | Remove the entry. Deleting the default reassigns `default_model` to the first remaining entry; deleting the lite entry clears `lite_model`. Sessions bound to the deleted model fall back to the default sender on their next turn. |
| `POST /api/config/models/{id}/default` | Set `default_model` to `{id}`. |
| `POST /api/config/models/{id}/lite` | Toggle `lite_model`: set it to `{id}`, or clear it when it already points there. |

`POST /api/config/test` validates a provider/base-URL/model/key combination
against the live endpoint. An empty or masked key reuses the stored key of the
matching entry, so testing an edited entry needs no re-typed key.

## Lite model → compaction

`Agent` (`internal/agent/agent.go`) carries an optional pair set together:

```go
// LiteSender/LiteModel, when both set, run history summarisation on a
// cheaper model. Unset falls back to Sender/Model.
LiteSender Sender
LiteModel  string
```

`summarize` (`internal/agent/compaction.go`) sends on the lite pair when
present — including its head-popping overflow protection, sized against
`contextWindow(LiteModel)`, and the streaming path, which type-asserts the lite
sender. On a lite-call error it retries once on the primary sender before giving
up, so a misconfigured lite entry does not break compaction. Both `maybeCompact`
and the overflow compact path go through `summarize`; micro-compaction is
string-level and unaffected.

Each transport that constructs an `Agent` resolves `lite_model` to an entry,
builds its sender through the same cache / `app.NewSender` path, and sets the
pair.

**Implicit lite.** With no `lite_model` configured, the vendor's registry
`LiteModel` (e.g. deepseek → `deepseek-v4-flash`, anthropic →
`claude-haiku-4-5`) serves as the lite model, riding the session's **own
sender** — same endpoint, key, and prompt-cache routing, so no extra
credentials and, where the backend caches by shared prefix, the summarisation
call can reuse the cache the conversation already built.
`app.ImplicitLiteModel(provider, model, baseURL)` resolves it and returns
nothing when the vendor is unknown or has no `LiteModel`, when the primary model
already is the lite model, or when the base URL points off the vendor's own
endpoints (a custom endpoint is a different backend behind a compatible
protocol; its catalogue won't include the vendor's lite model). An explicit
`lite_model` entry always wins. For a session bound to a config entry, the
implicit lookup uses that entry's vendor and endpoint.

## Frontend

The settings panel (`web/src/views/SettingsView.svelte`,
`web/src/components/settings/ModelConfigForm.svelte`) renders one card per
entry with add, remove, per-card test-connection, set-default, and set-lite
actions, and default/lite badges. It addresses entries by the `id` returned in
`GET /api/config` (the model string) for PATCH / DELETE / set-default / set-lite.
The form has no name field: provider, model, base URL, and key are the only
inputs, and the key is never prefilled (the masked value shows as a
placeholder). The session info bar's model picker lists entries from
`GET /api/config` and sends the model to `PATCH /api/sessions/{id}/model`.

## Compatibility

- **Old `config.yaml` files** load through the legacy fallback and in-memory
  normalisation; the first save migrates to `config.yml` and parks the old file
  as `config.yaml.bak`.
- **Old `config.yml` with `name:` entries** load fine — the field is ignored.
  A single-model config keeps working unchanged; a multi-model config whose
  `default_model` / `lite_model` used custom names should repoint them at the
  model strings.
- **Old session JSON** has no `model_config`; empty means the default entry.
- **IM bridge, cron, headless one-shot** resolve through the default entry with
  no behavioural change until the user adds entries.

## Testing

- `internal/config`: legacy-yaml → yml migration (load, save, `.bak` rename);
  `EntryByModel` / `DefaultEntry` / `SetDefaultEntry` keyed by model, including
  a floating default (empty model); default/lite reference repair on delete.
- `internal/server`: CRUD against a temp home; duplicate-model rejection;
  model-change re-keys the entry and repairs the default reference; per-session
  switch persists `model_config` and leaves global config untouched; create
  with an entry model vs a raw model string.
- `internal/agent`: compaction uses the lite sender when set and falls back to
  the primary on lite error (fake senders, no network — per `.octorules`).
