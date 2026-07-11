# Cross-session memory

octo remembers preferences, project conventions, and past corrections across sessions — stored locally as plain markdown in `~/.octo/memories/<repo-slug>/`, never in the cloud. There's no dedicated remember/forget tool and no background consolidation: the agent manages memory with its own file tools (`read_file`/`write_file`/`edit_file`), keeping to one convention:

- `MEMORY.md` — the index, loaded into the system prompt every session (capped at 200 lines / 25KB).
- `<topic>.md` — detail files the agent creates and reads on demand, linked from the index.

Memory is scoped per repo (shared across that repo's worktrees) plus a home-level index inherited into every project for cross-project preferences. `MEMORY.md` can also carry two optional rule tiers beyond a plain index — always-apply rules (restated every turn) and triggered rules (recalled once per session on a keyword hit).

```
octo memory list     # list the project's and inherited memory files
octo memory path     # print the memory directories
octo --no-memory     # disable memory injection for a single session
```

`/memory` in the TUI shows the same listing.

Full mechanics (rule-tier syntax, the save-nudge hook, why the CLI composes once per session while web/IM recompose every turn): **https://octo-agent.dev/docs/guides/memory/** (`web_fetch`).

## Optional: an external memory backend (separate feature)

Everything above is `MEMORY.md` — curated standing guidance, frozen into the system prompt. octo can *additionally* connect to a self-hosted external semantic-memory service that indexes raw conversation text for later search. Separate, optional layer — off by default, doesn't touch `MEMORY.md`.

Three backends, pick at most one: [hindsight](https://github.com/vectorize-io/hindsight), [mem0](https://github.com/mem0ai/mem0), or [agentmemory](https://github.com/rohitg00/agentmemory) — each self-hosted, octo just talks to its REST API.

- **Storing is automatic and silent** — after every turn, in the background, no tool involved.
- **Recall is a tool** — the agent calls `memory_recall` when it suspects something relevant was discussed before; this one blocks and its errors surface.
- **Configure** via `memory_backend` in `~/.octo/config.yml`: `type` (`hindsight`\|`mem0`\|`agentmemory`; unset disables the feature entirely), `base_url`, `api_key` (backend-dependent — hindsight/agentmemory default to no auth, mem0 requires it by default), `namespace` (scopes stored/recalled memories).

Full backend-specific auth/setup notes: **https://octo-agent.dev/docs/guides/memory-backends/** (`web_fetch`).
