---
title: Connect a memory backend
description: Optional semantic recall via hindsight, mem0, or MemTensor/MemOS — separate from MEMORY.md.
---

octo can optionally connect to a self-hosted external memory service that indexes your
conversations and lets the agent search them later. This is separate from
[`MEMORY.md`](/docs/guides/memory/): `MEMORY.md` is the agent's curated standing guidance
(preferences, rules, project decisions), frozen into the system prompt every session. A memory
backend is free-form semantic recall over raw conversation text — the backend does its own
extraction and indexing, and octo doesn't touch or duplicate the `MEMORY.md` layer to support it.

Three backends are supported; pick at most one:

- [hindsight](https://github.com/vectorize-io/hindsight) — self-hosted, no auth by default.
- [mem0](https://github.com/mem0ai/mem0) — self-hosted (`server/` in the mem0ai/mem0 repo), auth on
  by default.
- [MemTensor/MemOS](https://github.com/MemTensor/MemOS) — self-hosted, no auth by default. (Not
  `usememos/memos`, which is an unrelated note-taking app, and not `agiresearch/MemOS`.)

All three need an LLM (for fact extraction) and an embedding model (for search) — either your own
OpenAI-compatible endpoint (DashScope/Bailian, DeepSeek, etc.) or, for hindsight, a fully local
setup. The steps below are a tested, copy-pasteable quick start for each — a supplement to their own
docs, not a replacement.

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

### MemOS (MemTensor)

The heaviest of the three — bundles Neo4j (graph store) and Qdrant (vector store) alongside the API.
Their docs give a ready-made [Bailian/DashScope example](https://github.com/MemTensor/MemOS/blob/main/docs/en/open_source/getting_started/rest_api_server.md)
that already has the embedding dimension set correctly, so it's the least fiddly of the three if
you have a DashScope key:

```bash
git clone https://github.com/MemTensor/MemOS
cd MemOS
cat > .env <<'EOF'
OPENAI_API_KEY=<your-bailian-api-key>
OPENAI_API_BASE=https://dashscope.aliyuncs.com/compatible-mode/v1
MOS_CHAT_MODEL=qwen3-max

MEMRADER_MODEL=qwen3-max
MEMRADER_API_KEY=<your-bailian-api-key>
MEMRADER_API_BASE=https://dashscope.aliyuncs.com/compatible-mode/v1

MOS_EMBEDDER_MODEL=text-embedding-v4
MOS_EMBEDDER_BACKEND=universal_api
MOS_EMBEDDER_API_BASE=https://dashscope.aliyuncs.com/compatible-mode/v1
MOS_EMBEDDER_API_KEY=<your-bailian-api-key>
EMBEDDING_DIMENSION=1024
MOS_RERANKER_BACKEND=cosine_local

NEO4J_BACKEND=neo4j-community
NEO4J_URI=bolt://localhost:7687
NEO4J_USER=neo4j
NEO4J_PASSWORD=12345678
NEO4J_DB_NAME=neo4j
MOS_NEO4J_SHARED_DB=false

DEFAULT_USE_REDIS_QUEUE=false
ENABLE_CHAT_API=true
CHAT_MODEL_LIST=[{"backend": "qwen", "api_base": "https://dashscope.aliyuncs.com/compatible-mode/v1", "api_key": "<your-bailian-api-key>", "model_name_or_path": "qwen3-max", "support_models": ["qwen3-max"]}]
EOF
cd docker
docker compose up --build
```

(Using a different OpenAI-compatible provider: swap `OPENAI_API_BASE`/`MOS_EMBEDDER_API_BASE` and set
`EMBEDDING_DIMENSION` to match your embedding model's actual output size — same rule as mem0 above.)

Verify it's up: `http://localhost:8000/docs` should load. No auth is required by default — identity
comes from an `X-User-Name` header instead, which octo sends automatically when `api_key` is blank.

## How it works

- **Storing is automatic.** After every turn, octo sends that turn's content to the backend in the
  background — there's no `memory_store` tool and nothing for the agent to decide. This matches how
  these backends are designed to be used: you feed them raw text and they do their own
  extraction/dedup. It's fire-and-forget — a failed store doesn't surface anywhere and doesn't slow
  down your turn.
- **Recall is a tool.** The agent calls `memory_recall` when it suspects something relevant was
  discussed in a prior session or conversation. This one *does* block on the network round trip and
  its errors do surface, since it's an explicit, visible action rather than a background side effect.

## Configuring

Add a `memory_backend` block to `~/.octo/config.yml`:

```yaml
memory_backend:
  type: hindsight        # hindsight | mem0 | memos
  base_url: http://localhost:8888
  api_key: ""            # optional — see per-backend notes below
  namespace: my-project  # scopes stored/recalled memories; defaults to "default"
```

- **`type`** selects the backend. Leaving it unset (or omitting the whole block) disables the
  feature entirely — no tool is advertised, nothing is sent anywhere.
- **`base_url`** is the backend's REST endpoint — wherever you're running its server (`http://localhost:8888`
  for hindsight/mem0 as set up above, `http://localhost:8000` for MemOS).
- **`api_key`** is optional and backend-dependent:
  - hindsight has no auth by default; set an API key only if you've enabled
    `HINDSIGHT_API_TENANT_API_KEY` on the server.
  - mem0 requires auth by default — set the server's `X-API-Key`-compatible key here, or run the
    server with `AUTH_DISABLED=true` for local development and leave this blank.
  - memos (MemTensor/MemOS) has no auth by default; leaving this blank sends your `namespace` as an
    `X-User-Name` header instead.
- **`namespace`** scopes what gets stored/recalled — hindsight's `bank_id`, mem0's `user_id`, or
  memos's `user_id`. Use something stable per project (or leave it as the default single bucket).

Restart `octo` (or `octo serve`) after changing this — it's read once at session start, the same as
every other config-file setting.

Sanity-check the wiring: start `octo`, have a short exchange, then ask something that requires
recalling it (in a fresh session, or after `octo` restarts) — you should see it call `memory_recall`
and get the earlier fact back.

## Troubleshooting

- **mem0: `psycopg.errors.DataException: expected 1536 dimensions, not N`** — mem0's Postgres vector
  column size is fixed the first time anything is stored (1536, matching OpenAI's default embedding
  model). If your embedder returns a different size, you must set `embedding_dims` via `/configure`
  **before** the first `/memories` call. If you already stored something with the wrong size, there's
  no in-place fix — wipe and start over: `docker compose down -v && docker compose up -d`, then
  `/configure` again immediately, before storing anything.
- **mem0/MemOS: `provider_auth_failed` / 401 from `api.openai.com`** — your LLM/embedder config is
  still pointing at real OpenAI. For mem0, set the base URL via `/configure` (not just `.env`); for
  MemOS, double check `OPENAI_API_BASE`/`MOS_EMBEDDER_API_BASE` in `.env` and rebuild
  (`docker compose up -d --force-recreate`).
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
