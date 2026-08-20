---
name: genui
description: Render structured, glanceable, interactive UI in the chat instead of plain text — dashboards, stat cards, filterable tables, charts, mermaid diagrams, choice panels, forms, quizzes. Panels can carry an id so later turns update them in place, and most interaction resolves in the browser with no round-trip. Read this before calling render_ui or emitting a ```octo-ui fence, so the spec you produce matches the node whitelist and caps the renderer actually enforces.
---

# GenUI

GenUI lets you describe a small UI tree as JSON — cards, stats, tables, lists,
badges, progress bars, callouts, charts, diagrams, code blocks, and form
controls — and have it render as real components in the chat instead of you
writing the same information out as prose or a markdown table. There are two ways to emit a spec; which one to use depends
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
| `divider` | none | A rule between groups |
| `code` | `code: string`, `lang?: string` | Monospaced excerpt. Highlighted for javascript, typescript, go, python, bash, json, xml; any other `lang` renders as plain monospace rather than failing |
| `collapsible` | `title: string`, `children: GenuiNode[]`, `open?: boolean` | Foldable section. `open` seeds the first render only — after that the user's toggle wins, and it survives a reload |
| `plot` | `plot: "bar"\|"line"\|"area"\|"pie"`, `series: {name?: string, points: {label: string, value: number}[]}[]`, `stacked?: boolean`, `legend?: boolean`, `xLabel?: string`, `yLabel?: string`, `height?: number` | See the plot notes below |
| `mermaid` | `code: string` | A mermaid diagram, rendered inline |

Notes on `plot`: the x axis is the union of every series' labels in
first-appearance order, so series need not agree on their labels or their
length. A label a series has no point for is a **gap**: `line` breaks there
rather than diving to zero, while `bar`/`area` draw it as zero. `pie` uses
`series[0]` and ignores the rest. Under `stacked`, negative values are
clamped to zero. Colours are assigned automatically and follow the user's
theme — there is no colour field, and there is no `type` for colour or CSS
anywhere in this table.

There is still no `href`-bearing node and no 3D node. Don't invent fields
outside this table — anything not listed here is stripped before it reaches
the renderer (see "Caps and what happens past them").

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
| `slider` | `field: string`, `min: number`, `max: number`, `step?: number`, `label?: string`, `value?: number` | `max` must exceed `min` or the node is dropped. `step` defaults to a hundredth of the range |
| `number` | `field: string`, `min?: number`, `max?: number`, `step?: number`, `label?: string`, `value?: number` | A numeric input. Separate from `input` on purpose — `input` has no type switch at all, so no password-style field can exist |
| `textarea` | `field: string`, `label?: string`, `placeholder?: string`, `value?: string`, `rows?: number` | Long free text; `rows` clamps to 2–12 |
| `quiz` | `field: string`, `question: string`, `options: {label: string, value: string}[]`, `correct: string`, `explanation?: string` | Scored in the browser the moment the user picks. The answer is visible in the page source, so use it as a comprehension aid, not an assessment |

A field's current value is tracked live as the user changes it, independent
of any `button` — when a `button` fires, the feedback message below carries
the value of every field in the same fence at that moment, not just the
button's own data.

## Prefer local interaction over a round-trip

Most interaction should never reach you. A tab switch, a filter, a fold, a
quiz answer, a slider drag — all of these resolve in the browser with no
message and no turn, **if you ship the data they need up front**. Reach for a
`button` only when the panel genuinely cannot answer by itself: fetching data
it doesn't have, or taking a real-world action.

Three mechanisms make that possible, and they cost you nothing but a field:

**`visibleWhen`** — any node may carry a condition and render only when it
holds:

```json
{"type": "text", "text": "…", "visibleWhen": {"field": "mode", "equals": "advanced"}}
```

The condition is one of two families. Equality: `equals`, `in` (an array), or
`not` — use exactly one; if you send several, only the first of that order
survives. Range: any combination of `gt`, `gte`, `lt`, `lte`, all of which
must hold, so `{"gte": 10, "lt": 100}` is the interval you would expect. A
field the user has not touched compares as the empty string, and fails every
range predicate — so a range-gated node stays hidden until its slider moves,
which is usually what you want.

**`table.filterBy`** — `{"field": "q", "column": "name"}` filters the rows
already in the table by an `input`'s value, case-insensitively. `column` must
name one of the table's own columns.

**`table.sortable`** — `true` makes the headers clickable, cycling
unsorted → ascending → descending. A column sorts numerically when every cell
in it is a number, lexicographically otherwise.

An input with nothing reading it is a control that does nothing. Every
`slider`/`number` you add should be named by a `visibleWhen` or a `filterBy`,
and every `textarea` should sit next to a submit `button`.

## Addressable panels and silent updates

Give a spec an `id` when the user is expected to act on it more than once:

```json
{"id": "sales", "title": "Sales", "items": [ … ]}
```

An id is 1–64 characters of letters, digits, `_` and `-`, unique within the
conversation. A panel without one is a one-shot — correct for a summary
nobody will touch again.

An id changes what happens when the user acts on the panel. Their action
arrives with a `panel` key:

```
[octo-ui-action] {"panel": "sales", "action": "refresh", "fields": {"range": "30d"}}
```

Neither that message nor your reply to it is drawn in the conversation. Your
reply **replaces the panel in place**, wherever it already sits.

For that to work your reply must be **exactly one ` ```octo-ui ` fence
carrying the same `id`, and nothing else** — no sentence before it, no
sign-off after it. Any prose turns the reply into an ordinary visible
message, which is a correct fallback when you really do need to say
something, but means the panel does not update in place. Choose one:

- Updating the panel? Emit the fence alone.
- Need to explain, refuse, or ask something? Write normally — say it, and
  include a fresh fence if the panel should also change.

Re-sending a panel inside an ordinary reply is not an error: it re-presents
the panel at that point in the conversation, which is right when you are
bringing it back up after other discussion.

Interaction state (field values, selected tab, fold state) belongs to the
panel id and survives a page reload. When you send a new version of a panel,
values whose fields still exist are kept and the rest are dropped — so a
refreshed dashboard stays under the filter the user set.

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

Treat this exactly like any other user message: read the JSON and figure out
what changed. Don't echo the raw JSON back to the user; respond to what it
means.

How to answer depends on whether the envelope carries a `panel` key:

- **With `panel`** — the strict form above applies: reply with exactly one
  fence carrying that id and no other text, and the panel updates in place
  with nothing added to the conversation. Prose makes it an ordinary visible
  reply instead.
- **Without `panel`** — the panel was anonymous, so there is nothing to
  update in place. Reply however the change calls for: a fresh `octo-ui`
  fence, a `render_ui` call, or plain text.

## Caps and what happens past them

Both the tool path and the inline-fence path enforce the same structural
caps. Respect them yourself rather than relying on the renderer to catch an
oversized spec — going over a cap doesn't fail your call, it silently trims:

- Max tree depth: **8**
- Max total nodes per spec: **200**
- Max string field length: **500** characters (labels, text, badge text, etc.)
- Max table cell length: **2000** characters
- Max table rows: **500**
- Max table columns: **50**
- Max `list`/`keyvalue` items: **200**
- Max `select`/`radio`/`quiz` options: **50**
- Max `tabs` entries: **8**
- Max `code` / `mermaid` / `textarea` default text: **5000** characters
- Max `plot` series: **8**; max points per series: **100**
- `textarea` `rows`: clamped to **2–12**
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

The same line separates a `mermaid` or `plot` node from a charting artifact.
A diagram that explains what you just said, or a chart the user is about to
filter, belongs in the panel. A visualization that is itself the deliverable
— something needing a heatmap, a sankey, a map, brush-and-zoom, or a
charting library's full expressiveness — belongs in an artifact, where you
can write a real page with real code.

Never route a report-sized document or a large dataset through a GenUI
`table` or `list` node just because the user asked for "a table." The guard
caps above are enforced by silent truncation, not by a rejection you'd
notice: a 900-row table becomes a 500-row table with no error, no warning in
your tool result, and no indication to you that anything was cut. If the
content genuinely doesn't fit inside a normal reply, that's the signal it
belongs in a written artifact instead, not a signal to compress it into a
GenUI node and hope the caps are generous enough.
