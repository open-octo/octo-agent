---
name: product-help
description: Answer questions about how to use octo by reading the product documentation. Use when the user asks "how do I...", "what is...", "where is the docs for...", or any usage/help question about octo itself.
---

# Product Help — answer octo usage questions from documentation

You have access to octo's product documentation. Use it to answer user questions accurately. Do not guess or hallucinate features.

## Documentation sources

1. **Bundled docs** — check the skill directory for reference files:
   - `README.md` — quick start, installation, basic usage, feature overview
   - `CLI.md` — command reference (`octo`, `octo config`, `octo skills`, `octo workflows`, `octo browser`, etc.)
   - `CONFIG.md` — configuration file format and all supported fields
   - `SKILLS.md` — the default-skill mechanism (embedding, precedence, `octo skills`) and how to write your own
   - `WORKFLOW.md` — contributor development workflow, git conventions, PR process (NOT the `octo workflows` product feature — see CLI.md/TUI.md for that)
   - `MCP.md` — MCP server configuration (`mcp.json`, OAuth, Tool Search)
   - `MEMORY.md` — cross-session memory (file layout, injection, attention-rule tiers)
   - `PERMISSIONS.md` — the permission engine (modes, rule format, per-session overrides)
   - `HOOKS.md` — the hooks engine (`hooks.yml`, event types, blocking, trust-on-first-use)
   - `IM.md` — the IM/chat bridge (`channels.yml`, supported platforms, `send_message`/`send_file`)
   - `TUI.md` — terminal UI reference (slash commands, keyboard shortcuts, status bar)
   - `TROUBLESHOOTING.md` — common issues and fixes

   Read the relevant file(s) with `read_file` before answering.

2. **Online docs** (fallback) — if the bundled docs don't cover the question, fetch from the official documentation:
   - Base URL: `https://github.com/open-octo/octo-agent/blob/main/`
   - Append the relevant doc filename (e.g. `README.md`, `dev-docs/go-rewrite-roadmap.md`)
   - Use `web_fetch` to retrieve the raw markdown content

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
