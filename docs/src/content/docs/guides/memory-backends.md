---
title: Connect a memory backend
description: Optional semantic recall via hindsight, mem0, or agentmemory — separate from MEMORY.md.
---

octo can optionally connect to a self-hosted external memory service that indexes your
conversations and lets the agent search them later. This is separate from
[`MEMORY.md`](/docs/guides/memory/): `MEMORY.md` is the agent's curated standing guidance
(preferences, rules, project decisions), frozen into the system prompt every session. A memory
backend is free-form semantic recall over raw conversation text — the backend does its own
extraction and indexing, and octo doesn't touch or duplicate the `MEMORY.md` layer to support it.

Three backends are supported; pick at most one:

- [hindsight](https://github.com/vectorize-io/hindsight) — self-hosted, no auth by default; a managed
  [Hindsight Cloud](https://docs.hindsight.vectorize.io/) option also exists if you'd rather not run
  the container yourself (see below).
- [mem0](https://github.com/mem0ai/mem0) — self-hosted (`server/` in the mem0ai/mem0 repo), auth on
  by default; a managed [mem0 Platform](https://docs.mem0.ai/platform/quickstart) (cloud) option
  also exists (see below).
- [agentmemory](https://github.com/rohitg00/agentmemory) — self-hosted Node/TypeScript server, no
  auth by default, and the lightest to run: it ships local embeddings and needs no external LLM or
  API key to get started.

hindsight and mem0 need an LLM (for fact extraction) and an embedding model (for search) — either
your own OpenAI-compatible endpoint (DashScope/Bailian, DeepSeek, etc.) or, for hindsight, a fully
local setup. agentmemory runs entirely locally out of the box (local embeddings, LLM optional). The
steps below are a tested, copy-pasteable quick start for each — a supplement to their own docs, not a
replacement.

## Running a backend locally

Pick one. Each assumes Docker is installed and running.

### hindsight

Simplest to start: no database to configure, no auth by default.

```bash
docker run -d --name hindsight \
  -p 8888:8888 -p 9999:9999 \
  -v hindsight-data:/home/hindsight/.pg0 \
  -v hindsight-hf-cache:/home/hindsight/.cache \
  -e HINDSIGHT_API_LLM_PROVIDER=openai \
  -e HINDSIGHT_API_LLM_MODEL=<your-model> \
  -e HINDSIGHT_API_LLM_BASE_URL=<your-openai-compatible-base-url> \
  -e HINDSIGHT_API_LLM_API_KEY=<your-api-key> \
  -e HINDSIGHT_API_EMBEDDINGS_LOCAL_MODEL=BAAI/bge-small-en-v1.5 \
  ghcr.io/vectorize-io/hindsight:latest
```

`HINDSIGHT_API_LLM_*` can point at any OpenAI-compatible endpoint (DashScope's
`https://dashscope.aliyuncs.com/compatible-mode/v1`, DeepSeek, real OpenAI, etc.) — hindsight uses it
only for consolidating what it retains, not for embeddings (those run locally via the
`HINDSIGHT_API_EMBEDDINGS_LOCAL_MODEL`, no key needed). First start takes **~1-2 minutes** while it
downloads the embedding/reranker models and boots an embedded Postgres — don't be alarmed if the API
doesn't answer immediately.

Verify it's up:

```bash
curl http://localhost:8888/v1/default/banks
# {"banks":[]}  — empty is fine, a bank is created automatically on first write
```

No auth is required unless you explicitly set `HINDSIGHT_API_TENANT_API_KEY` on the container.

#### Hindsight Cloud (no Docker required)

Vectorize also runs a managed version — [Hindsight Cloud](https://docs.hindsight.vectorize.io/) — for
anyone who'd rather not run the container themselves. It speaks the same REST API at
`https://api.hindsight.vectorize.io` with the same `/v1/default/banks/...` paths as the self-hosted
container, so pointing octo at it is a config change, not a code change:

```yaml
memory_backend:
  type: hindsight
  base_url: https://api.hindsight.vectorize.io
  api_key: "<your Hindsight Cloud API key>"
  namespace: octo-agent
```

Unlike the self-hosted default, Hindsight Cloud requires the API key — generate one from its
dashboard and set it here. octo sends it as `Authorization: Bearer <api_key>`, matching what the
cloud API expects.

You can also drop the `base_url` and set `mode: cloud` instead — octo then fills in
`https://api.hindsight.vectorize.io` for you. Because the cloud API is identical to self-hosted in
auth and route shape, `mode: cloud` is purely this base_url shortcut here (unlike mem0, where it also
switches paths and headers):

```yaml
memory_backend:
  type: hindsight
  mode: cloud            # fills base_url = https://api.hindsight.vectorize.io
  api_key: "<your Hindsight Cloud API key>"
  namespace: octo-agent
```

### mem0

Needs Postgres (with pgvector) — the official `server/` stack bundles it via Docker Compose.

```bash
git clone https://github.com/mem0ai/mem0
cd mem0/server
cp .env.example .env
```

Edit `.env`:

```bash
OPENAI_API_KEY=<your-api-key>
POSTGRES_PASSWORD=<pick-anything>
AUTH_DISABLED=true   # local development only — see "Auth" below for the real thing
MEM0_DEFAULT_LLM_MODEL=<your-model>
MEM0_DEFAULT_EMBEDDER_MODEL=<your-embedding-model>
```

Then:

```bash
make bootstrap
```

**If you're using a non-OpenAI, OpenAI-compatible provider** (DashScope, DeepSeek, ...), the `.env`
model names alone aren't enough — mem0's OpenAI client defaults to `api.openai.com`. Point it at your
provider's base URL via the `/configure` endpoint, **before you store anything**:

```bash
curl -X POST http://localhost:8888/configure \
  -H "Content-Type: application/json" \
  -d '{
    "llm": {"provider": "openai", "config": {"model": "<your-model>", "openai_base_url": "<your-base-url>"}},
    "embedder": {"provider": "openai", "config": {"model": "<your-embedding-model>", "embedding_dims": <dims>, "openai_base_url": "<your-base-url>"}}
  }'
```

(No auth header needed with `AUTH_DISABLED=true`.) `embedding_dims` matters — see
[Troubleshooting](#troubleshooting) if you skip this and hit a dimension error.

#### Auth

`AUTH_DISABLED=true` is fine for a local trial but skips real access control. For anything longer-lived,
drop it, run `make bootstrap` as-is, and it prints an admin email/password/API key on first start —
use that generated API key as `api_key` in octo's config instead of leaving it blank.

#### mem0 Cloud (no Postgres/Docker required)

mem0 also runs a managed version — the [mem0 Platform](https://docs.mem0.ai/platform/quickstart) —
for anyone who'd rather not self-host the `server/` stack. Unlike Hindsight Cloud, this **isn't**
a drop-in swap: the Platform API uses different endpoint paths and a different auth header than the
self-hosted server, so octo needs `mode: cloud` to talk to it correctly:

```yaml
memory_backend:
  type: mem0
  mode: cloud
  api_key: "<your mem0 Platform API key>"
  namespace: octo-agent
```

`base_url` can be omitted — it defaults to `https://api.mem0.ai` when `mode: cloud` and no
`base_url` is set. `api_key` is required (the Platform has no unauthenticated mode); octo sends it
as `Authorization: Token <api_key>`, matching what the Platform API expects.

### agentmemory

The lightest of the three: no Docker, no database, no API key. It ships an embedded SQLite store and
runs its embedding model locally (`all-MiniLM-L6-v2` via `@xenova/transformers`), so it works
out of the box.

```bash
npm install -g @agentmemory/agentmemory
agentmemory
```

(Or run it without installing: `npx @agentmemory/agentmemory`.) The REST API binds to
`127.0.0.1:3111` by default; a live viewer runs on `3113`.

:::caution[Start it from a fixed, writable directory]
agentmemory's storage engine writes its state store to `./data/` **relative to the current
working directory**. Launch it from a different directory each time — or as a service whose
working directory defaults to `/` (launchd, systemd) — and it can't write there: `state::set`
calls hang until a 180-second timeout, and **nothing persists across restarts** (every start
comes up with an empty index, silently losing everything stored since the last run). Always
run it from a stable, writable directory (e.g. `~/.agentmemory`), and set that directory
explicitly under a process manager (`WorkingDirectory` in a launchd plist, `WorkingDirectory=`
in a systemd unit). Confirm it took: after storing something and restarting the server, the
same query should still return it, and `~/.agentmemory/data/state_store.db` should exist.
:::

Verify it's up:

```bash
curl http://localhost:3111/agentmemory/health
# {"status":"healthy",...}
```

No auth is required by default. To lock it down, start the server with `AGENTMEMORY_SECRET` set and
put the same value in octo's `api_key` — octo sends it as `Authorization: Bearer <api_key>`.

An external LLM is optional (it enables automatic observation summarization) — set `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, or `GEMINI_API_KEY` in `~/.agentmemory/.env` if you want it. octo only uses two
endpoints: it stores each turn via `/agentmemory/remember` and recalls via `/agentmemory/search`
(narrative format), which returns the full stored text — neither requires an LLM to be configured.

## How it works

- **Storing is automatic.** After every turn, octo sends that turn's content to the backend in the
  background — there's no `memory_store` tool and nothing for the agent to decide. This matches how
  these backends are designed to be used: you feed them raw text and they do their own
  extraction/dedup. It's fire-and-forget — a failed store doesn't surface anywhere and doesn't slow
  down your turn.
- **Recall is a tool by default.** The agent calls `memory_recall` when it suspects something
  relevant was discussed in a prior session or conversation. This one *does* block on the network
  round trip and its errors do surface, since it's an explicit, visible action rather than a
  background side effect. Whether to call it is a model judgment call — it can miss an isolated
  factual question that doesn't read as "resume a prior conversation" (see `auto_recall` below if
  you'd rather not rely on that).

## Configuring

Add a `memory_backend` block to `~/.octo/config.yml`:

```yaml
memory_backend:
  type: hindsight        # hindsight | mem0 | agentmemory
  mode: ""               # meaningful for mem0 & hindsight: "cloud" or "" (self-hosted, default)
  base_url: http://localhost:8888
  api_key: ""            # optional — see per-backend notes below
  namespace: my-project  # scopes stored/recalled memories; defaults to "default"
  auto_recall: false     # optional — see "Automatic recall" below
```

- **`type`** selects the backend. Leaving it unset (or omitting the whole block) disables the
  feature entirely — no tool is advertised, nothing is sent anywhere.
- **`mode`** matters for `type: mem0` and `type: hindsight`: set it to `cloud` to talk to the
  vendor's hosted Platform instead of a self-hosted server (see "mem0 Cloud" / "Hindsight Cloud"
  above). For mem0 the cloud and self-hosted APIs differ in endpoint paths and auth headers, so this
  isn't inferred from `base_url`; for hindsight they're identical, so `mode: cloud` just fills in the
  cloud `base_url`. Ignored by agentmemory.
- **`base_url`** is the backend's REST endpoint — wherever you're running its server (`http://localhost:8888`
  for hindsight/mem0 as set up above, `http://localhost:3111` for agentmemory). Can be omitted with
  `mode: cloud`, which defaults it to `https://api.mem0.ai` (mem0) or
  `https://api.hindsight.vectorize.io` (hindsight).
- **`api_key`** is optional and backend-dependent:
  - self-hosted hindsight has no auth by default; set an API key only if you've enabled
    `HINDSIGHT_API_TENANT_API_KEY` on the server. Hindsight Cloud is the exception — it always
    requires the API key from its dashboard.
  - self-hosted mem0 requires auth by default — set the server's `X-API-Key`-compatible key here,
    or run the server with `AUTH_DISABLED=true` for local development and leave this blank. mem0
    Cloud (`mode: cloud`) always requires the API key from its dashboard.
  - agentmemory has no auth by default; leave this blank unless you started the server with
    `AGENTMEMORY_SECRET`, in which case set the same value here (sent as `Authorization: Bearer`).
- **`namespace`** scopes what gets stored/recalled — hindsight's `bank_id`, mem0's `user_id`, or
  agentmemory's `project`. Use something stable per project (or leave it as the default single bucket).
- **`auto_recall`** — see below. Defaults to `false`.

Restart `octo` (or `octo serve`) after changing this — it's read once at session start, the same as
every other config-file setting.

Sanity-check the wiring: start `octo`, have a short exchange, then ask something that requires
recalling it (in a fresh session, or after `octo` restarts) — you should see it call `memory_recall`
and get the earlier fact back.

### Automatic recall

Set `auto_recall: true` to call `Recall` with the user's message on **every turn** and fold the
result into that turn's context automatically — instead of waiting on the model to decide to call
`memory_recall`. The tool stays available either way, for a deeper or differently-worded search;
the injected context tells the model not to bother re-calling it for the same question.

This trades a small, bounded amount of latency (recall is synchronous, capped at a 3s timeout, and
silently skipped on error or timeout) for not depending on the model's judgment about when to
check memory — useful if you've noticed it answering "I don't know" to something that's actually in
the backend, rather than trying `memory_recall` first. Leave it off if you'd rather keep every turn
free of the extra round trip and rely on the tool alone.

## Troubleshooting

- **mem0: `psycopg.errors.DataException: expected 1536 dimensions, not N`** — mem0's Postgres vector
  column size is fixed the first time anything is stored (1536, matching OpenAI's default embedding
  model). If your embedder returns a different size, you must set `embedding_dims` via `/configure`
  **before** the first `/memories` call. If you already stored something with the wrong size, there's
  no in-place fix — wipe and start over: `docker compose down -v && docker compose up -d`, then
  `/configure` again immediately, before storing anything.
- **mem0: `provider_auth_failed` / 401 from `api.openai.com`** — your LLM/embedder config is
  still pointing at real OpenAI. Set the base URL via `/configure` (not just `.env`).
- **agentmemory: recall returns nothing right after storing** — confirm the server is actually up
  (`curl http://localhost:3111/agentmemory/health`) and that octo's `namespace` matches across
  restarts; it maps to agentmemory's `project`, which scopes what search returns.
- **agentmemory: everything is gone after a restart, or logs show `Invocation timeout after
  180000ms: state::set` / `index persistence: failed`** — the server was started from a working
  directory it can't persist into (its state store is `./data/` relative to the CWD; a service
  defaulting to `/` can't write there). Restart it from a fixed, writable directory and set
  `WorkingDirectory` under your process manager — see the caution in the agentmemory setup section.
  `~/.agentmemory/data/state_store.db` appearing (and surviving a restart) is the signal it's fixed.
- **agentmemory: startup hangs or the local embedding model won't download on a restricted network**
  — it fetches `all-MiniLM-L6-v2` from HuggingFace on first run. If `huggingface.co` is blocked or
  throttled where you're hosting, point it at a mirror via the `HF_ENDPOINT` environment variable
  (e.g. `HF_ENDPOINT=https://hf-mirror.com`) before starting the server.
- **hindsight: connection refused right after `docker run`** — give it a minute or two; it's still
  downloading/loading the embedding and reranker models. `docker logs hindsight` shows progress.
  Once it prints its startup banner, subsequent restarts are much faster (models are cached in the
  `hindsight-hf-cache` volume).
- **Using Colima instead of Docker Desktop and a bind-mount volume shows up empty in the
  container** — Colima only shares specific host paths into its VM (by default your home directory
  and `/tmp/colima`). Clone the repo somewhere under your home directory, not under a path outside
  Colima's configured mounts (e.g. not a random `/tmp/...` or `/private/tmp/...` location), or the
  `.:/app`-style bind mounts these projects use will silently mount as empty.
- **octo never calls `memory_recall`, or the backend never receives anything** — confirm
  `memory_backend.type` is actually set (an empty/missing block disables the feature with no error),
  and that `base_url` matches the port you actually exposed.
