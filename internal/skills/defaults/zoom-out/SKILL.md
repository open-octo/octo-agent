---
name: zoom-out
description: Give a higher-level map of an unfamiliar area of code before diving into details — modules, their responsibilities, callers, and the project's own domain vocabulary. Use when the user says "zoom out", "give me the big picture", "I don't know this area of code well", "how does this fit together", "画个模块地图", "这块代码我不熟", "帮我理清这块的架构", "这个模块是干嘛的，跟哪些东西有关系", or asks an orientation question rather than a specific implementation question. Trailing text names the area, feature, or file the user wants oriented on; if omitted, use whatever code was just discussed.
---

# Zoom out on unfamiliar code

The user doesn't want an implementation answer here — they want a map. Go up one
layer of abstraction: name the modules involved, what each one is responsible
for, how they call each other, and where the user's actual question sits on
that map. Use the project's own words for things, not generic ones.

## 1. Scope "this area"

Figure out what "this area" means before exploring:

- If the user named a feature, file, or symbol, start there.
- If they didn't, infer it from what was just discussed in this conversation
  (the file you were just editing, the bug just diagnosed, the PR just reviewed).
- If genuinely ambiguous, ask one short question rather than guessing at scope —
  a map of the wrong area wastes the exploration.

## 2. Find the project's own vocabulary first

Before naming anything yourself, check for the project's own terms — using the
project's real names beats inventing generic ones:

- `.octorules` in the project root (octo's own project-conventions file) and
  `~/.octo/octorules.md` for cross-project conventions
- `CONTEXT.md`, `GLOSSARY.md`, or `README.md` at the repo root or in the area
- Design docs under `dev-docs/`, `docs/adr/`, or similar — often named after the
  subsystem itself
- Existing comments/doc-strings in the code — a term used consistently across
  files (e.g. "Spawner", "Sender", "prelude") is a domain term worth reusing;
  a name that appears once is probably just a local variable

Cite where a term came from if it's not obvious ("per .octorules…",
"per dev-docs/x-design.md…") so the user can tell you're using their
vocabulary, not a paraphrase.

## 3. Gather structure efficiently

Don't hand-loop `grep` + `read_file` across dozens of files yourself — that's
slow and burns context on file bodies you don't need for a map. Prefer, in order:

1. **A code-intelligence MCP tool**, if one is connected in this session (check
   with `tool_search` — e.g. a "codegraph"-style server). These return
   structural answers (callers, callees, definitions) directly, without reading
   whole files.
2. **`sub_agent` with the built-in `explore` preset** (`sub_agent(type: "explore",
   prompt: "...")`) — a read-only agent built exactly for "locate and understand,
   report back" work. Use one call for a broad sweep, or a few in parallel for
   independent sub-areas (e.g. "the CLI entry point" + "the storage layer" +
   "the callers").
3. **Direct `grep`/`glob`/`read_file`**, only for a quick, narrow, single-symbol
   lookup you're confident about — not as your main exploration method.

## 4. Produce the map

Structure the answer as a map, not a narrative walkthrough:

- **Entry points** — where does execution/control actually start for this area
  (a CLI command, an HTTP handler, a tool call)?
- **Modules and responsibilities** — a short table or list: module/file →
  one-line responsibility → key symbols (`file:line`).
- **How they connect** — the call/data flow between them, in the order a
  request or piece of data actually moves.
- **Domain vocabulary** — the project's own terms for the concepts involved,
  each used the way step 2 found it, not redefined.
- **Where the user's question sits** — close the loop: point at the specific
  piece of the map their original question or task touches.

## Don't

- Don't paste whole file contents — cite `file:line` and quote only the few
  lines that make the point.
- Don't produce an exhaustive deep-dive of every function in the area — this is
  an orientation map, not a tutorial; depth belongs in a follow-up once the user
  knows where to look.
- Don't invent terminology when the project already has a name for something.
- Don't skip step 2 — a technically-correct map that uses the wrong words for
  things is harder for the user to connect back to their own mental model of
  the codebase.
