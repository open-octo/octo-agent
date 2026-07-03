# Documentation site design

Design for `octo-agent.dev/docs`: a multi-page reference and guide site that replaces the old
"Read the Docs" link, which used to point straight at `README.md` on GitHub. The marketing landing
page (`landing/index.html`) stays a separate, hand-rolled page and links into this site.

## Why a separate site

octo's real surface area — CLI, config, tools, MCP, skills, sandboxing, sub-agents, workflows,
hooks, memory, six IM adapters, a REST/SSE API, browser automation, goals — is far larger than one
README can hold without becoming unreadable. A single flat page also can't be searched, deep-linked
per topic, or read task-first ("how do I connect an MCP server") instead of top-to-bottom.

## Competitive scan (distilled)

Studied three adjacent projects' docs to find patterns worth taking and mistakes worth avoiding:

- **OpenAI Codex CLI** (`developers.openai.com/codex`) — clean topic-first IA, per-page **Feature
  Maturity** badges (Experimental/Beta/Stable), searchable/filterable reference tables, a "Copy
  page" button for pasting into an LLM, OS/package-manager tabs on install commands. Weakness: the
  same topics (MCP, Subagents) are duplicated across three top-level sections, so the canonical page
  is unclear — avoid that by giving every topic exactly one home and cross-linking to it.
- **Hermes Agent** (`hermes-agent.nousresearch.com/docs`) — task-first nav (Getting Started → Using
  → Features → Platforms → Integrations → Guides → Reference), full EN/中文 switcher, tabbed
  "pick your interface" content. Weakness: several top-level sections collapse to a single child
  page — premature scaffolding. No search box despite doc-set size.
- **OpenClaw** (`docs.openclaw.ai`, a chat-gateway product, not a coding agent — closest true peer
  is OpenCode) — its channel-integration pages are relevant to octo's own IM bridge (icon card grid
  per platform, install tabs per OS/package manager). Weakness: mascot-heavy, 70-tweet testimonial
  wall — reads as consumer marketing bleeding into developer docs; keep octo's docs tone off that.

Takeaways applied below: one canonical page per topic (no Codex-style duplication), don't create a
nav section until it earns 3+ pages (avoid Hermes's stubs), keep a search box from day one, keep the
tone practical and outcome-focused rather than hype-driven, and reuse the per-platform card-grid
pattern for the channels/integrations page.

## Information architecture

```
Introduction                        octo vs. Claude Code / Codex CLI / Hermes, one honest table

Getting Started
  Install                           script / .pkg / .exe / go install / from source, per OS
  Quickstart                        one-shot task in under 5 minutes
  Choose a provider                 Anthropic, OpenAI, DeepSeek/Kimi/Bailian, custom endpoint

Guides                              task-first, "how do I…" — the bulk of the content
  Use skills                        SKILL.md format, reuse ~/.claude/skills
  Connect MCP servers               mcp.json, OAuth, Tool Search
  Sandbox the agent                 --sandbox, Seatbelt/Landlock, read/write allowlists
  Give it memory                    ~/.octo/memories, extraction/consolidation
  Run sub-agents & workflows        fan-out, pipelines, worktree isolation, background runs
  Automate with hooks               the 7 lifecycle events, per-transport wiring
  Bridge to chat apps               WeChat iLink / Feishu / DingTalk / WeCom / Discord / Telegram
  Automate with browser control     record/replay, self-heal, CDP attach
  Run long-horizon goals            /goal, idle continuation
  Self-host `octo serve`            auth, LAN exposure, systemd/launchd, reverse proxy

Concepts                            short mental-model pages, not padded
  The agent loop                    tools, permission gating, streaming, compaction
  Configuration layers              soul.md / user.md / octorules.md / .octorules / --system
  Sessions & history                persistence, resume, crash durability

Reference                           dense, table-driven, searchable
  CLI                               every command and flag
  Config file                       config.yaml schema
  Tools                             terminal, file tools, glob/grep, web, skill, sub_agent, workflow, mcp_*
  HTTP & SSE API                    octo serve's REST surface
  Compatibility & exit codes        from COMPATIBILITY.md
  Security model                    from SECURITY.md

Architecture                        for contributors and the curious
  System layers                     cmd → agent → provider → tools, one-directional deps
  Provider protocols                Anthropic Messages vs. OpenAI Chat Completions
  Extending octo                    writing a tool, writing a channel adapter

Community
  Contributing                      from CONTRIBUTING.md
  Changelog
  FAQ & troubleshooting
```

Every top-level section ships with 3+ real pages before it appears in the nav — no stubs. Each topic
(e.g. MCP, sub-agents) has exactly one canonical Reference or Guide page; other pages link to it
rather than re-describing it.

## Visual design system

octo's brand indigo (`#4f46e5` light / `#818cf8` dark) and the existing landing page's shadows,
radii, and bilingual toggle carry over unchanged — the docs site is a sibling of that page, not a
different brand. What's new is a set of tokens and components sized for a multi-page reference site
rather than a single scrolling pitch.

**Palette** — neutrals are cool-biased toward the indigo accent rather than plain gray, and the dark
theme is a deep ink-blue rather than pure black (a small nod to the octopus mark, not a mascot bit):

| Token | Light | Dark |
|---|---|---|
| `--bg` | `#f7f7fb` | `#0a0e1a` |
| `--surface` (cards, sidebar) | `#ffffff` | `#111527` |
| `--fg` | `#111827` | `#e8e9f3` |
| `--muted` | `#5b5f76` | `#9497b0` |
| `--border` | `#e4e4ef` | `#20243a` |
| `--accent` | `#4f46e5` | `#818cf8` |
| `--accent-deep` (headings, active nav) | `#3730a3` | `#a5b4fc` |
| `--code-bg` | `#11142033` on `#f1f1f8` | `#0d0f1c` |
| `--success` / `--warning` / `--danger` | `#16a34a` / `#b45309` / `#dc2626` | `#4ade80` / `#fbbf24` / `#f87171` |

Semantic colors (success/warning/danger, used for maturity badges and callouts) are kept separate
from the brand accent so a "Stable" badge never gets confused with the indigo interactive color.

**Type** — two roles, both from the system stack (no webfont loading, no CDN):

- **Display / headings / logotype / nav labels**: monospace (`ui-monospace, "SF Mono", "Roboto
  Mono", Menlo, Consolas, monospace`), bold, tight letter-spacing. octo is a terminal tool first;
  headings that read like a command are the one deliberate, subject-specific typographic choice
  that separates this from a generic docs template.
- **Body copy**: the same humanist sans already used on the landing page (`-apple-system,
  "Segoe UI", ...`), kept to ~65ch measure for long guide text.
- **Code / tables / data**: the same monospace stack as headings, so numbers and flags line up
  (`font-variant-numeric: tabular-nums` in reference tables).

**Layout** — three-zone docs shell: a collapsible left sidebar (section tree), a center content
column capped at 720px, and a right-hand "on this page" outline that appears above 1100px viewport
width. A slim top bar echoes the three-dot terminal titlebar already used in the landing page's demo
blocks — logo, search, GitHub star count, EN/中文 toggle, theme toggle.

**Components**

- **Search** — `Cmd/Ctrl+K` command palette, static full-text index (Pagefind — no server, matches
  octo's own self-hosted/zero-runtime positioning). Present from the first release, unlike Hermes.
- **Code blocks** — copy button, language label, tabs where a command differs by OS or provider
  (install script vs. `.pkg`/`.exe`, Anthropic vs. OpenAI-protocol config).
- **Callouts** — Note / Tip / Warning / Danger, left color bar + icon, mapped to the semantic tokens.
- **Maturity badge** — `Stable` / `Beta` / `Experimental`, shown next to any Guide or Concept whose
  feature isn't fully settled (workflows' Ruby DSL, goals, browser self-heal start at `Beta`).
- **Reference tables** — consistent `Flag/Key · Type · Default · Description` columns, filterable by
  a text box for long tables (CLI flags, config schema).
- **Copy page** — one button that copies the current page as Markdown, for readers who'll paste it
  into an agent — the natural audience for this project.

## Tooling recommendation

**Astro Starlight**, built to static HTML at CI time and deployed to GitHub Pages — the Node
toolchain is a build-time dependency for doc authors, never something an end user runs, so it
doesn't conflict with octo's zero-runtime, single-binary positioning. Starlight ships sidebar nav,
dark/light, i18n routing, and MDX (for tabs/callouts) out of the box; Pagefind search bolts on as a
static plugin.

The source lives in `website/` (an Astro project); the hand-rolled landing page is a separate plain
HTML file at `landing/index.html`. `.github/workflows/pages.yml` builds `website/` and assembles the
two into one deploy: `landing/*` at the site root, `website/dist/*` (built with `base: "/docs"`)
under `/docs/`.

## Content migration

| Source | Destination |
|---|---|
| README "Why octo" table | Introduction |
| README Install section | Getting Started → Install |
| README Quick start | Getting Started → Quickstart + relevant Guides |
| README Configuration | Concepts → Configuration layers + Reference → Config file |
| README Skills / Sandboxing / Platform notes | Guides |
| README "What's implemented" | Introduction (trimmed) + maturity badges throughout |
| README Architecture | Architecture → System layers |
| `COMPATIBILITY.md` | Reference → Compatibility & exit codes |
| `SECURITY.md` | Reference → Security model |
| `CONTRIBUTING.md` | Community → Contributing |
| `dev-docs/*.md` | not published verbatim — these are internal design notes; Guides/Architecture pages are written fresh for an external audience and link back to source code, not to dev-docs |
