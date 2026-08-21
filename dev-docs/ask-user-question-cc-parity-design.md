# ask_user_question — Claude Code Parity

## Goal

Make `ask_user_question` behave exactly like Claude Code's `AskUserQuestion`:
up to four questions per call, options that keep `label` / `description` /
`preview` as separate fields, a picker whose tail rows are **Other** and
**Chat about this**, per-question notes, and Claude Code's tool-result
wording. Every surface that can prompt a human — plain stdin, the TUI modal,
the web modal/banner, the mobile card, and IM channels — renders the same
payload at the fidelity its medium allows.

The reference behavior below is Claude Code 2.1.238's question picker and its
tool-result mapper, not a paraphrase of the tool's public schema.

## Where we stand

The tool already *declares* the nested Claude Code schema, but the executor
collapses it before anything else sees it (`internal/tools/ask_user_question.go`):

| Aspect | Claude Code | octo today |
| --- | --- | --- |
| Questions per call | 1–4, navigated as tabs, with a final review/submit tab | `maxItems: 1`; extra entries dropped by `normalizeAskInput` |
| Option shape | `{label, description, preview}` | `optionLabels` folds them into one string `"label — description"` |
| `preview` | Markdown in a monospace column beside the list; single-select only; the chosen preview is echoed back to the model | not in the schema |
| Notes | `n` attaches a free-text note to the current question; the note is echoed back to the model | absent |
| Option tail | `Other` (inline text input row) and `Chat about this` (single-select only) | TUI/stdin append an `Other (free text)` row; web/mobile always show a bare text box |
| Selection | Enter, arrows, or the option's number | Enter/arrows (TUI), click (web), number (stdin, IM) |
| `header` | ≤12-char chip beside the question | used as the modal's title |
| Result | four distinct phrasings (below), with previews and notes inlined | `User chose: <label>` |

`AskRequest` has exactly one producer (`AskUserQuestionTool.Execute`) and three
implementations (`replAsker` in `cmd/octo/asker.go`, `wsAsker` in
`internal/server/server.go`, `chatAsker` in `internal/server/channel_ask.go`),
plus the `SecretAsker` side door used by replay-secret collection
(`internal/tools/replay_secrets.go`). That keeps the contract change contained.

## Contract

`internal/tools/ask_user_question.go`:

```go
type AskRequest struct {
    Questions []AskQuestion // 1..4
}

type AskQuestion struct {
    Question    string
    Header      string // ≤12 chars, rendered as a chip
    MultiSelect bool
    Options     []AskOption // 2..4 from the model; empty only on the internal secret path
}

type AskOption struct {
    Label       string
    Description string
    Preview     string // multi-line; ignored when MultiSelect
}

type AskResponse struct {
    // Answers is parallel to Questions. A zero-valued entry means the user
    // moved past that question without answering it.
    Answers []AskAnswer

    // Response is the free-form message the user typed after choosing
    // "Chat about this" instead of picking. When set, Answers is ignored.
    Response string

    Cancelled bool // dismissed with nothing answered and nothing typed
}

type AskAnswer struct {
    Choices []string // selected labels (no description folded in)
    Custom  string   // "Other" free text
    Preview string   // the selected option's preview, echoed back to the model
    Notes   string   // note the user attached to this question
}
```

Cancellation is per call, not per question: dismissing the prompt after
answering question 1 of 3 returns `Cancelled: false` with answers 2 and 3
zero-valued, so partial work is not thrown away. `Cancelled: true` only when
nothing was answered.

`SecretAsker` is unchanged — `AskSecret(ctx, question)` still takes a bare
string, and internally builds a one-question `AskRequest`.

### "Chat about this"

The last row of a single-select picker is `Chat about this`. Choosing it
dismisses the picker and puts the user back in the composer; **the next
message they submit becomes the tool result**, not a new conversation turn.
That is what makes it distinct from `Other`: `Other` answers *this question*
with custom text, while `Chat about this` abandons the question set and
replies to the model in prose.

So each surface needs a short-lived *response-capture* mode: while an ask is
in this state, the surface's normal message-submit path resolves the pending
ask (`Response`) instead of starting a turn. Concretely — the TUI input box,
the web/mobile composer, and the IM reply slot each already have a submit
path; response-capture is a flag on the pending-ask state that reroutes one
submission. `Chat about this` is absent from multi-select questions, matching
Claude Code.

### Result text

`formatAskResponse` reproduces Claude Code's mapper. Per-question segments,
joined with `, `:

```
"<question>"="<answer>"                     // answered
"<question>"=(no option selected)           // unanswered but annotated
```

with, appended to a segment when present:

```
 selected preview:
<preview>
 notes: <notes>
```

A question with neither an answer nor a note is omitted from the list.
Multi-select answers join their labels with `, `. Then, in order:

| Condition | Result text |
| --- | --- |
| `Response` non-empty | `The user responded: <response>` |
| every answer is exactly one of that question's declared labels, and no notes | `Your questions have been answered: <segments>. You can now continue with these answers in mind.` |
| any answer is free text, or any note is present | `The user answered: <segments>. Read the answers carefully — they may request clarification, changes, or that you not proceed — and follow what they actually say.` |
| nothing answered | `The user did not answer the questions.` |

The distinction in row 3 is load-bearing: a user who typed rather than picked
often typed a *correction*, and the caution sentence is what stops the model
from pattern-matching it back onto the option it expected. The current
`(user cancelled)` / `User chose: …` strings go away; only
`internal/tools/ask_user_question_test.go` asserts on them.

Claude Code's fifth branch — an AFK timeout producing `Before going idle the
user had selected: …` — is deliberately not adopted: octo's askers have no
timeout and wait for a real answer, which is a settled decision, not a gap.

### Schema

`questions` goes to `maxItems: 4`. Each option object gains `preview`.
`header` gains `maxLength: 12`. The description picks up Claude Code's
authoring guidance, because it materially changes what the model emits:

- ask only when blocked on a decision that is genuinely the user's — not for
  facts discoverable in the repo, and not for choices with an obvious default
- when recommending an option, put it first and suffix the label with
  `(Recommended)`
- `preview` is for concrete artifacts worth comparing side by side (ASCII
  mockups, code snippets, config or diagram variants) — not for simple
  preference questions; it is single-select only

Both of the tolerances octo grew are removed, so the surface is exactly
Claude Code's:

- `options` becomes **required, 2–4 entries** (`minItems: 2`, `maxItems: 4`).
  There is no free-text-only question: `Other` is always the tail row, and
  that is how an open-ended answer is given. `Execute` rejects a question
  with fewer than two or more than four options instead of trimming.
- `normalizeAskInput` and its flat-shape fallback (top-level
  `question`/`options`/`multi_select`) are deleted. `questions` is the only
  accepted shape; anything else is an error.

octo has no schema-validation layer between the model and the executor, so
`Execute` performs the validation the Claude Code host does, returning a
descriptive error (`ask_user_question: options must have 2-4 entries, got 1`)
rather than a `ToolResult`. A tool error is retryable — the model sees it and
re-emits a well-formed call — which is the same recovery loop a host-side
schema rejection produces. This is safe now in a way it wasn't before: the
reason the flat fallback existed was that the *declared* schema was flat
snake_case and fought the model's prior; with the declared schema being
Claude Code's own, the shape the model reaches for is the shape we accept.

Claude Code's `answers` / `annotations` / `metadata` input properties stay
out. They exist because its host writes the user's selections, chosen
preview, and notes back into the recorded call; octo's askers return those
directly in `AskResponse`, so the same information reaches the model without
a round-trip through the tool input.

Executor and schema must read the same keys: `questions[].options[].preview`
is parsed by `optionLabels`' replacement (`parseAskOptions`), so a schema-only
addition is not enough.

## Wire protocol

`wsEventRequestUserQuestion` (`internal/server/ws_types.go`) carries the
question set instead of one flattened question:

```go
type wsAskOption struct {
    Label       string `json:"label"`
    Description string `json:"description,omitempty"`
    Preview     string `json:"preview,omitempty"`
}

type wsAskQuestion struct {
    Question    string        `json:"question"`
    Header      string        `json:"header,omitempty"`
    MultiSelect bool          `json:"multi_select"`
    Options     []wsAskOption `json:"options,omitempty"`
}

type wsEventRequestUserQuestion struct {
    Type       string          `json:"type"`
    SessionID  string          `json:"session_id"`
    QuestionID string          `json:"question_id"`
    Questions  []wsAskQuestion `json:"questions"`
    Secret     bool            `json:"secret,omitempty"`
}
```

The flat `question`/`options`/`multi_select`/`header` fields are removed rather
than kept as a shim: `webdist` is `go:embed`-ed into the binary and the desktop
build embeds the same tree, so client and server never skew. A secret prompt is
a one-question set with `secret: true`.

The answer frame carries the parallel array plus the chat-response field:

```json
{"type":"user_question_answer","question_id":"q_…",
 "answers":[{"choices":["OAuth with PKCE"],"custom":"","preview":"","notes":"needs the refresh path too"},
            {"choices":[],"custom":"only the schema part","preview":"","notes":""}],
 "response":"", "cancelled":false}
```

`handleWSUserQuestionAnswer` takes `(qid string, answers []tools.AskAnswer,
response string, cancelled bool)`. `ws.answerQuestion` in `web/src/lib/ws.ts`
matches. One `user_question_answer` closes the whole set — the frontend
accumulates per-question drafts locally and submits once, so the single ask
slot (`acquireAskSlot`) and the single `questionChans[qid]` entry stay as they
are, and so does global-broadcast delivery plus `pendingQuestions` replay.

`selected preview` is filled server-side, not sent by the client: the option's
preview text is already in the request, so the client sends only which option
was chosen and `wsAsker` copies the preview into the answer. That keeps a
large preview off the answer frame.

`WsEventRequestUserQuestion` and `QuestionModalEntry`
(`web/src/lib/types.ts`, `web/src/lib/stores.ts`) mirror the new shape:
`QuestionModalEntry.questions: AskQuestion[]` replaces
`question`/`options`/`multiSelect`/`header`.

## Surfaces

Shared model, matching Claude Code:

- questions are **tabs**, not a forced sequence — the user can move between
  them freely, with a final **review/submit** tab that lists each question and
  its answer and warns `You have not answered all questions` when some are
  blank
- one question renders at a time: its header chip, its text, then the option
  rows, then `Other` (an inline text input), then `Chat about this`
- an option row shows the bold `label` with its `description` beneath
- options are numbered and the number selects: `N+1` is `Other`, `N+2` is
  `Chat about this`
- when the current question is single-select and any of its options has a
  preview, the picker gains a second column showing the focused option's
  preview (`No preview available` when that option has none), and `n` opens a
  note field for the question
- Escape cancels the whole set; partial answers are still returned

### Plain stdin — `plainView.askQuestion` / `printQuestion` (`cmd/octo/turncore.go`)

A line reader can't do tabs, so this surface degrades to sequential questions —
one card and one `ReadLine` each, `n/N` in the header:

```
[ask_user_question · scope 2/3]
  Include the migration too?
    1) Schema only (Recommended)
       leaves the data backfill for a follow-up
    2) Schema + backfill
       one PR, longer review
    3) Other (free text)
    4) Chat about this
  Select [1-4]:
```

`description` is an indented dim line under its label. `preview` prints as an
indented block under the option, capped (reuse the rune-safe truncation budget
already used by `askInputSummary`, 600 runes) so a long mockup can't push the
prompt off-screen. Notes are not offered here — there is no second column to
annotate and no spare key in a line reader. `parseSelection` gains the extra
tail index. An empty line skips that question; EOF cancels the rest; picking
`Chat about this` reads one more line and returns it as `Response`.

### TUI modal — `modalState`, `newModalState`, `confirmQuestion`, `modalView` (`cmd/octo/tuirepl.go`, `cmd/octo/tuirepl_view.go`)

Full parity; this is the surface Claude Code's own picker maps onto directly.
`UserPrompt` carries `Questions []UserQuestion`; `UserResponse` carries
`Answers []UserAnswer` plus `Response string`. `modalState` gains `qIdx`,
`answers []UserAnswer`, `notes` (a `textinput.Model` per question), and
`noteActive`; `options` is rebuilt per question with the `Other` and
`Chat about this` tail rows.

- header row: the question tabs — `[scope] [auth] [surfaces] [submit]` with the
  current one highlighted and answered ones marked; `Tab`/`Shift-Tab` move
  between them. With a single question the tab row is omitted and the hint
  reads `↑/↓ navigate`.
- `Enter` on an option records the answer and moves to the next unanswered
  question, or to the submit tab when none are left
- number keys select directly; `n` opens the note field; `Esc` cancels the set
- multi-select keeps `Space` to toggle and `Enter` to confirm the question
- preview: when the current question is single-select and any option has a
  preview, the highlighted option's preview renders in a bordered pane —
  right of the list when `m.width >= 100`, below it otherwise, height-capped
  to keep the modal inside the terminal (bubbletea's inline renderer truncates
  over-wide lines with no marker)
- `Chat about this` closes the modal, leaves the turn blocked, and puts the
  input box into response-capture: the next submitted line resolves the ask as
  `Response`

The existing modal queue (`m.modalQueue`) is untouched: it serializes
*concurrent* asks from different sub-agents, which is orthogonal to multiple
questions inside one ask.

### Web — `web/src/components/overlays/QuestionModal.svelte`

Both forms (bottom banner and expanded modal) render the current question:

- `header` becomes a chip left of the question text; the question tabs sit
  above it, with the review/submit tab last
- options become rows, not pills: bold `label`, muted `description` beneath,
  check/radio affordance on the left, the number on the right
- the tail rows are `Other` (revealing its input inline, so today's
  always-present text box goes away) and `Chat about this`
- when the current question is single-select and any option has a preview, the
  modal switches to two columns — option list left, preview right in a
  monospace, scrollable, `overflow-x: auto` pane; below ~640px the preview
  moves under the list. An `add note` affordance sits under the preview column.
- footer: `Cancel` · `Back` · `Next` / `Submit`; the submit tab shows the
  review list and the unanswered warning
- `Chat about this` closes the overlay and marks the composer as capturing:
  the composer keeps the pending `questionId`, and its next submit calls
  `ws.answerQuestion(qid, [], "", text)` instead of `ws.send` of a user
  message. The chat shows the text as the user's message and the ask card
  resolves to `You responded: …`.

Draft state moves from the current flat `selected`/`customText` pair to an
array indexed by question, reset when `questionId` changes. The
`others`-sessions notification rows and the no-interruption rule from
`dev-docs/cross-client-ask-question.md` are unchanged; the row's preview text
uses `questions[0].question` plus `+N more` when the set is larger.

### Mobile — `web/src/mobile/QuestionOverlay.svelte`

Same model, phone layout: the tabs become a segmented pager, option rows are
tappable cards (label + description), and the preview renders in a collapsed
`<details>` under the option rather than a second column — with the note field
inside that same disclosure. Per-session drafts become per-session,
per-question drafts. `Chat about this` closes the card and captures the next
composer submit, as on the desktop. The toast + "View" path for non-active
sessions is unchanged.

### IM — `chatAsker.Ask` (`internal/server/channel_ask.go`)

One message per question, each consuming its own `BeginAsk` reply slot, so
`parseAskReply` keeps working against a single question:

```
❓ [scope] (2/3) Include the migration too?
1. Schema only (Recommended) — leaves the data backfill for a follow-up
2. Schema + backfill — one PR, longer review
3. Chat about this
Reply with a number — or free text for something else.
```

`description` rides on the same line after an em dash. `Other` is not listed:
free text already *is* the reply, which is exactly what `Other` means here, so
listing it would be a row that says "do what you were going to do anyway".
`Chat about this` is listed, and picking it sends `Go ahead — what would you
like to say?` and takes the following message as `Response`; that extra round
is what the medium costs, and it preserves the distinction between answering
the question and talking past it.

**Previews are omitted over IM**, and so are notes. Previews exist to be
compared side by side, which a chat timeline can't do, and four multi-line
blocks per question would flood the conversation; notes are an annotation on
that comparison. This is a rendering decision only — the model still sends
previews, and every other surface shows them.

Cancellation over chat stays as it is (empty reply → cancelled); a cancel on
question 2 stops the loop and returns what was answered so far. No timeout, per
the existing rationale.

## Divergences from Claude Code

All three are forced by a medium, not chosen:

- IM omits `preview` and notes, and drops the `Other` row as redundant
  (above); plain stdin omits notes and renders questions sequentially instead
  of as tabs.
- No AFK-timeout result branch — octo's askers wait indefinitely by design.
- `answers` / `annotations` / `metadata` are not declared as tool inputs,
  because octo's askers return that information directly rather than through a
  host write-back.

## Tests

- `internal/tools/ask_user_question_test.go` — 1..4 questions parsed;
  `preview` retained; `label`/`description` no longer folded. Result text for
  each of the four branches, including the label-vs-free-text distinction that
  picks between `Your questions have been answered:` and `The user answered:`,
  a `(no option selected)` segment carrying only notes, and preview/notes
  inlining. Rejection cases, each returning an error and not reaching the
  asker: the flat shape, zero questions, five questions, and a question with
  one or five options.
- `internal/server/server_test.go`, `pending_prompt_test.go` — the new event
  shape broadcasts globally, replays on resubscribe, one `answers` frame
  resolves a multi-question ask, and a `response` frame resolves it as chat.
- `internal/server/channel_ask_test.go` — three questions produce three
  messages and three reply slots; `Chat about this` consumes one extra message
  as `Response`; mid-set cancel returns partial answers; previews and notes
  absent from the sent text.
- `cmd/octo/asker_test.go` — `AskRequest` ↔ `UserPrompt` round-trips the
  question set, per-question answers, notes, and `Response`.
- `cmd/octo/tuirepl_test.go` — `Tab` moves between questions and lands on the
  submit tab; number keys select; `n` attaches a note; `Esc` mid-set returns
  partial answers; preview pane placement flips at width 100;
  `Chat about this` leaves the input box in response-capture and the next line
  resolves the ask.
- `web/src/lib/askStepper.ts` + `askStepper.test.ts` — the draft/tab logic
  (move between questions, per-question drafts keyed by `questionId`, whether
  the preview column applies, all-answered detection for the submit tab, and
  the `answers`/`response` payload the submit builds) lives outside the
  components so it is unit-testable the way the rest of `web/src/lib` is; the
  components stay presentational. Vitest runs from `web/`. The visual layers
  (two-column preview, narrow-window fallback, dark mode) are covered by the
  CDP walkthrough below.

## Verification

1. `make test`, `make vet`, `make fmt-check`.
2. TUI: a scripted 3-question ask (one with previews, one multi-select, one
   plain) — tab between questions, select by number, attach a note, take
   `Other` on one, land on the submit tab; then repeat and `Esc` mid-set;
   then repeat and take `Chat about this`, confirming the typed line comes
   back as `The user responded: …`.
3. Web: isolated `serve` on a scratch `HOME`, driven over CDP against that
   port (never the user's live session — see
   `reference_worktree_isolated_web_walkthrough`). Check the tab row, the
   two-column preview in the expanded modal, the review tab's warning, the
   composer's response-capture, dark mode, and a narrow window.
4. Mobile: the same server with `?mobile=1` — pager, collapsed preview, note
   field, response-capture.
5. IM: one channel (Telegram or Feishu), three-question ask, answer `2`, free
   text, `Chat about this`, then an empty reply.
6. Cross-client: ask from session B while viewing session A → notification row
   only; click through, answer on mobile, confirm the desktop banner clears.

`docs/src/content/docs/reference/tools.md` (and its `zh` mirror) list
`ask_user_question` as a one-line table entry, so neither needs a change.
