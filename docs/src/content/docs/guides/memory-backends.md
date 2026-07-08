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

Follow each project's own docs to get its server running locally (typically a `docker compose up` or
equivalent) — octo only talks to the REST API once it's up.

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
- **`base_url`** is the backend's REST endpoint — wherever you're running its server.
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
