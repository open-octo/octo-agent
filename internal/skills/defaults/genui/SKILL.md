---
name: genui
description: Render structured, glanceable UI in the chat instead of plain text — dashboards, stat cards, tables, lists, choice panels, forms. Read this before calling render_ui or emitting a ```octo-ui fence, so the spec you produce matches the node whitelist and caps the renderer actually enforces.
---

# GenUI

GenUI lets you describe a small UI tree as JSON — cards, stats, tables, lists,
badges, progress bars, callouts — and have it render as real components in
the chat instead of you writing the same information out as prose or a
markdown table. There are two ways to emit a spec; which one to use depends
on what you're building and where the reply is going. Read "The two output
surfaces" before picking.

## The two output surfaces

**1. The `render_ui` tool.** Call it with `{"spec": {"title"?: string, "items": GenuiNode[]}}`.
Read-only components only — no buttons, no inputs. The tool validates and
clamps your spec server-side and returns it on the same channel other
tools use for rich result cards, so it renders as a tool-result card in the
Web UI. This is the only surface available in **every** transport, but "every
transport" doesn't mean "the card is visible everywhere": IM and the TUI
never show tool-result cards at all — they show only your plain-text reply
for that turn. If you call `render_ui` in an IM or TUI conversation, follow
it with a plain-text reply that stands on its own; don't assume the user saw
the card.

**2. An inline ` ```octo-ui ` fence in your reply text.** Write a fenced code
block with the language tag `octo-ui` whose body is a GenUI spec (same shape
as `render_ui`'s `spec` argument). This is the only surface that supports the
interactive node types (see below), and it renders as a live component tree
inline with the rest of your markdown — but **only in a Web UI chat session**.
IM and the TUI cannot render a component tree, so they replace the fence with
a plain placeholder line before the user ever sees it. There is no reliable
signal available to you, in the turn itself, telling you which transport
you're replying into — so decide whether to use an inline fence from ordinary
conversational context (has the user been interacting with a visual UI this
session? did they mention a phone/chat app?), and when genuinely unsure,
prefer the `render_ui` tool plus a self-contained plain-text reply over an
inline fence, since the tool path degrades safely everywhere and the fence
does not.

## Node types

A spec is `{title?: string, items: GenuiNode[]}`. Every node is a JSON object
discriminated by its `type` field.

### Read-only nodes (both surfaces)

These render from both the `render_ui` tool card and an inline `octo-ui`
fence.

| `type` | Fields | Notes |
|---|---|---|
| `text` | `text: string`, `tone?: "default"\|"muted"\|"danger"` | Plain paragraph |
| `row` / `col` | `gap?: number`, `children: GenuiNode[]` | Flex layout container. `gap` is clamped to 0–64 |
| `card` | `title?: string`, `children: GenuiNode[]` | Bordered group |
| `list` | `items: (string \| {label: string, value?: string})[]` | Bulleted list |
| `table` | `columns: string[]`, `rows: (string\|number)[][]` | Renders with the same styling as a markdown table |
| `keyvalue` | `items: {label: string, value: string}[]` | Two-column definition list |
| `stat` | `label: string`, `value: string`, `delta?: string`, `tone?: "up"\|"down"\|"neutral"` | Metric card |
| `badge` | `text: string`, `tone?: "default"\|"success"\|"warning"\|"danger"\|"info"` | Small pill |
| `progress` | `value: number` (0–100), `label?: string` | Progress bar. `value` is clamped into range |
| `callout` | `tone?: "info"\|"success"\|"warning"\|"danger"`, `title?: string`, `text?: string` | Alert box |

There is no `type` for color/CSS, no `href`-bearing node, and no
math/plot/mermaid/3D node. Don't invent fields outside this table — anything
not listed here is stripped before it reaches the renderer (see "Caps and
what happens past them").

### Interactive nodes (inline fence only)

These only work inside an inline ` ```octo-ui ` fence — the `render_ui` tool
drops any of these types like any other unrecognized `type`, since its
tool-card output has no path back to you for a click or a field change.

| `type` | Fields | Notes |
|---|---|---|
| `button` | `label: string`, `action: string`, `payload?: object`, `variant?: "primary"\|"default"\|"danger"` | Fires the `[octo-ui-action]` feedback below |
| `input` | `field: string`, `label?: string`, `placeholder?: string`, `value?: string` | Always a plain text input — there is no `inputType` field, and one you send anyway is silently dropped; never render a password-style field here |
| `select` | `field: string`, `label?: string`, `options: {label: string, value: string}[]`, `value?: string` | |
| `checkbox` / `switch` | `field: string`, `label?: string`, `checked?: boolean` | |
| `radio` | `field: string`, `label?: string`, `options: {label: string, value: string}[]`, `value?: string` | |
| `tabs` | `tabs: {label: string, children: GenuiNode[]}[]` | Each tab's `children` can be any node type, including nested interactive ones |

A field's current value (from `input`/`select`/`checkbox`/`switch`/`radio`)
is tracked live as the user changes it, independent of any `button` — when a
`button` fires, the feedback message below carries the value of every field
in the same fence at that moment, not just the button's own data.

## The `[octo-ui-action]` feedback convention

When a user acts on a GenUI component you rendered earlier (clicks a button,
submits a field, picks an option), their action reaches you as a normal new
user turn whose text begins with the literal prefix `[octo-ui-action] `,
followed by a JSON object:

```
[octo-ui-action] {"action": "refresh", "fields": {"range": "7d"}, "payload": {}}
```

- `action` — the action name the interactive node declared.
- `fields` — the current value of every interactive field the user has set
  (text inputs, selections, checkboxes) at the time they triggered the action.
- `payload` — any fixed extra data you attached to that action when you
  rendered it.

Treat this exactly like any other user message: read the JSON, figure out
what changed, and respond by regenerating the relevant UI (a fresh `octo-ui`
fence or `render_ui` call reflecting the new state) or by replying in plain
text — whichever the change calls for. Don't echo the raw JSON back to the
user; respond to what it means.

## Caps and what happens past them

Both the tool path and the inline-fence path enforce the same structural
caps. Respect them yourself rather than relying on the renderer to catch an
oversized spec — going over a cap doesn't fail your call, it silently trims:

- Max tree depth: **8**
- Max total nodes per spec: **200**
- Max string field length: **500** characters (labels, text, badge text, etc.)
- Max table cell length: **2000** characters
- Max table rows: **100**
- Max table columns: **50**
- Max `list`/`keyvalue` items: **200**
- Max `select`/`radio` options: **50**
- Max `tabs` entries: **8**
- A `button`'s `payload` object: also depth- and width-capped (same depth
  limit as the node tree; up to 50 keys/entries per level), but that budget
  is separate from — not subtracted from — the 200-node total above.

An unrecognized `type` (including an interactive type sent through the
`render_ui` tool, which only accepts the read-only table) causes just that
node — and its subtree — to be dropped; its siblings still render. A spec
with no valid `items` array is the one case that fails the `render_ui` tool
call outright with an error you can see and retry.

## Boundary with artifacts (`write_file` / `edit_file` / `show_artifact`)

GenUI and the Artifacts panel look similar from the outside — both let you
produce something other than plain text — but they solve different problems,
and routing the wrong content through the wrong one produces a bad result
that won't error, it'll just look wrong:

- **Use `write_file`/`edit_file`/`show_artifact`** when the output is a
  deliverable that should survive this reply: a document, a standalone
  interactive page, an image, a code file — something the user might reopen,
  export, or refer back to later, independent of this conversation turn.
- **Use `render_ui`/`octo-ui`** for disposable structure inside this one
  reply that the user might act on right now — a quick comparison, a status
  summary, a small form — that has no reason to exist as a file.

Never route a report-sized document or a large dataset through a GenUI
`table` or `list` node just because the user asked for "a table." The guard
caps above are enforced by silent truncation, not by a rejection you'd
notice: a 500-row table becomes a 100-row table with no error, no warning in
your tool result, and no indication to you that anything was cut. If the
content genuinely doesn't fit inside a normal reply, that's the signal it
belongs in a written artifact instead, not a signal to compress it into a
GenUI node and hope the caps are generous enough.
