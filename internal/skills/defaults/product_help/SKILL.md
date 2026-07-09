---
name: product-help
description: Answer questions about how to use octo, or diagnose something that isn't working, by reading the product documentation. Use when the user asks "how do I...", "what is...", "where is the docs for...", any usage/help question about octo itself, or reports a problem — "not working", "won't start", "error", "connection refused", "daemon won't start" — by reading TROUBLESHOOTING.md.
---

# Product Help — answer octo usage questions from documentation

You have access to octo's product documentation. Use it to answer user questions accurately. Do not guess or hallucinate features.

## Documentation sources

1. **Bundled docs** — check the skill directory for reference files. Each is a concise, offline-readable summary; most end with a link to the fuller online guide for depth (exact schemas, edge cases, worked examples) — follow that link with `web_fetch` when the summary doesn't fully answer the question, rather than guessing at the missing detail.
   - `README.md` — quick start, installation, basic usage, feature overview
   - `CLI.md` — command reference (`octo`, `octo config`, `octo skills`, `octo workflows`, `octo browser`, etc.)
   - `CONFIG.md` — configuration file format and top-level fields
   - `SKILLS.md` — the default-skill mechanism (embedding, precedence, `octo skills`) and how to write your own
   - `WORKFLOW.md` — contributor development workflow for octo-agent's own codebase, git conventions, PR process (NOT the `octo workflows` product feature — see CLI.md for that)
   - `MCP.md` — MCP server configuration (`mcp.json`, OAuth, Tool Search)
   - `MEMORY.md` — cross-session memory (file layout, injection) and the optional external memory-backend feature
   - `PERMISSIONS.md` — the permission engine (modes, rule format, per-session overrides)
   - `HOOKS.md` — the hooks engine (`hooks.yml`, event types, blocking)
   - `IM.md` — the IM/chat bridge (`channels.yml`, supported platforms, `send_message`/`send_file`)
   - `TUI.md` — terminal UI reference (slash commands, keyboard shortcuts, status bar)
   - `TROUBLESHOOTING.md` — common issues and fixes

   Read the relevant file(s) with `read_file` before answering.

2. **Online docs** (fallback, and the source of truth for anything the bundled summaries don't cover) — the public docs site at `https://octo-agent.dev/docs/`, fetched with `web_fetch`:
   - `guides/<topic>/` for narrative how-tos — e.g. `guides/goals/`, `guides/workflows/`, `guides/loop/`, `guides/cron-tasks/`, `guides/browser-automation/`, `guides/sub-agents/`, `guides/sandbox-the-agent/`, `guides/self-host/`, `guides/channels/`, `guides/hooks/`, `guides/use-skills/`, `guides/connect-mcp-servers/`, `guides/memory/`, `guides/memory-backends/`
   - `reference/<topic>/` for exhaustive lookups — `reference/cli/`, `reference/config-file/`, `reference/permissions/`, `reference/slash-commands/`, `reference/tools/`, `reference/http-api/`, `reference/security/`, `reference/compatibility/`
   - Some topics (`sub-agents`, `sandbox-the-agent`, `self-host`) have a full guide online but no bundled summary file at all — go straight to `web_fetch` for those.
   - For anything neither the bundled docs nor the docs site cover (e.g. an internal design doc under `dev-docs/`), fall back to `https://github.com/open-octo/octo-agent/blob/main/<path>` via `web_fetch`.

## How to answer

1. **Identify the topic.** Map the user's question to the most relevant doc file(s).
2. **Read the source.** Use `read_file` for bundled docs or `web_fetch` for online docs.
3. **Quote selectively.** Cite specific sections or code blocks from the docs to back up your answer.
4. **Be concise.** Give the direct answer first, then optionally link to the full doc for deeper reading.
5. **Say when you don't know.** If neither bundled nor online docs cover the question, say so honestly — don't make up an answer.

## Example interactions

User: "how do I add a custom skill?"
→ Read `SKILLS.md` from the skill directory
→ Answer with the directory layout, frontmatter format, and how to trigger it

User: "what does permission_mode do?"
→ Read `CONFIG.md` from the skill directory
→ Explain the three modes with their behavior

User: "how do I switch to OpenAI?"
→ Read `CONFIG.md` and `CLI.md`
→ Explain `octo config` wizard vs. editing `~/.octo/config.yml` directly

User: "my IM bot stopped responding" / "daemon won't start"
→ Read `TROUBLESHOOTING.md` from the skill directory
→ Point at `~/.octo/serve.log` / `~/.octo/serve.pid` and `octo serve --status` before guessing at a cause

User: "how does the loop feature work?" / "what's the difference between /loop and cron?"
→ No bundled summary covers this — `web_fetch` `guides/loop/` (and `guides/cron-tasks/` for the comparison)
→ Explain the syntax and in-session-vs-persistent distinction; don't confuse this with actually
  *invoking* the `loop` skill — that happens only when the user wants a task run repeatedly right now
