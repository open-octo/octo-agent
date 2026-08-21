# ask_user_question — Claude Code Parity

## Goal

Make `ask_user_question` behave like Claude Code's `AskUserQuestion`: up to
four questions per call navigated as tabs, options that keep `label` /
`description` / `preview` as separate fields, two mutually exclusive picker
layouts, per-question notes, three distinct outcomes (submit / clarify /
reject), and Claude Code's tool-result wording. Every surface that can prompt
a human — plain stdin, the TUI modal, the web modal/banner, the mobile card,
and IM channels — renders the same payload at the fidelity its medium allows.

The reference behavior below is read out of the shipped Claude Code 2.1.238
binary (picker components, `validationErrorSteer`, and
`mapToolResultToToolResultBlockParam`), not paraphrased from the tool's public
schema.

## Where we stand

The tool already *declares* the nested Claude Code schema, but the executor
collapses it before anything else sees it (`internal/tools/ask_user_question.go`):

| Aspect | Claude Code | octo today |
| --- | --- | --- |
| Questions per call | 1–4, navigated as tabs, with a review/submit tab | `maxItems: 1`; extra entries dropped by `normalizeAskInput` |
| Option shape | `{label, description, preview}` | `optionLabels` folds them into one string `"label — description"` |
| Layout | two mutually exclusive ones: a flat list, or a two-column preview layout | one flat list |
| Notes | `n` attaches a note, in the preview layout only | absent |
| Outcomes | submit / clarify (`Chat about this`) / reject (Escape) | answer / cancel |
| `header` | required; the tab's label, not a chip | optional; used as the modal title |
| Result | four phrasings for submit, plus separate clarify and reject texts | `User chose: <label>` / `(user cancelled)` |

`AskRequest` has two producers — `AskUserQuestionTool.Execute` and
`wsAsker.AskSecret` (`internal/server/server.go:2131`) — and three
implementations: `replAsker` (`cmd/octo/asker.go`), `wsAsker`
(`internal/server/server.go`), `chatAsker` (`internal/server/channel_ask.go`).
`replAsker.AskSecret` bypasses `AskRequest` entirely and builds a
`UserPrompt{Kind: KindSecret}` directly.

## Contract

`internal/tools/ask_user_question.go`:

```go
type AskRequest struct {
    Questions []AskQuestion // 1..4
}

type AskQuestion struct {
    Question    string
    Header      string      // required from the model; the tab's label
    MultiSelect bool
    Options     []AskOption // 2..4 from the model; empty only on the secret path
}

type AskOption struct {
    Label       string
    Description string
    Preview     string // multi-line; only meaningful when !MultiSelect
}

type AskOutcome int

const (
    // AskSubmitted: the user submitted the answer set (possibly partial).
    AskSubmitted AskOutcome = iota
    // AskClarify: the user chose "Chat about this" — they want to talk
    // instead of picking. Answers given so far ride along.
    AskClarify
    // AskRejected: the user dismissed the picker (Escape / Cancel).
    // Answers are discarded.
    AskRejected
)

type AskResponse struct {
    Outcome AskOutcome
    // Answers is parallel to Questions. A zero-valued entry means the user
    // submitted without answering that question.
    Answers []AskAnswer
}

type AskAnswer struct {
    Choices []string // selected labels
    Custom  string   // "Other" free text (flat layout)
    Notes   string   // note attached to the question (preview layout)
    Preview string   // the chosen option's preview, echoed back to the model
}
```

Three outcomes, not two, is the substantive shape change. `AskRejected`
discards answers — a user who answers one question and then dismisses the
picker has *withdrawn*, and reporting the one answer as a success is how a
model ends up acting on a decision the user backed out of.

`Preview` is filled in `Execute`, never by an asker: `Execute` holds the
`AskRequest`, so it can match each answered label back to its option and copy
the preview across. Askers return only what the user did. Consequently
`formatAskResponse` takes both the request and the response — the result text
needs each question's text and its declared label set, neither of which lives
in `AskResponse`.

`SecretAsker` keeps its signature (`AskSecret(ctx, question) (string, bool,
error)`), but `wsAsker.AskSecret` now reads `res.Answers[0].Custom` instead of
`res.Custom`. `replAsker.AskSecret` is unaffected — it never built an
`AskRequest`.

### The three outcomes

**Submit** resolves the call with the answer set.

**Clarify** (`Chat about this`) resolves it *immediately* with a
clarification instruction that carries the answers given so far as prose. The
turn is not blocked and no state survives the call: the model reads the
instruction, asks the user what they want to clarify, and the user's next
message is an ordinary turn. This is Claude Code's mechanism, and it is worth
being explicit that it is *not* "capture the user's next message as the tool
result" — nothing anywhere waits for a follow-up.

**Reject** (Escape / Cancel) resolves it with Claude Code's tool-rejection
text, telling the model to stop and wait. In Claude Code this also aborts the
turn; here it does not — the asker has no handle on the turn, and octo's
interrupt is a separate user action (Ctrl-C, `/stop`). The model is told to
stop; killing the turn stays the user's move.

### Result text

`formatAskResponse(req AskRequest, res AskResponse) string`.

**Clarify** — Claude Code's wording, with every question listed:

```
The user wants to clarify these questions.
This means they may have additional information, context or questions for you.
Take their response into account and then reformulate the questions if appropriate.
Start by asking them what they would like to clarify.

Questions asked:
- "<question>"
  Answer: <answer>
  User notes: <notes>
- "<question>"
  (No answer provided)
```

`Answer:` and `(No answer provided)` are mutually exclusive; `User notes:` is
appended only when present.

**Reject** — `The user doesn't want to proceed with this tool use. The tool
use was rejected. STOP what you are doing and wait for the user to tell you
how to proceed.`

**Submit** — per-question segments, joined with `, `. A segment is:

```
"<question>"="<answer>"                     // answered
"<question>"=(no option selected)           // unanswered but annotated
```

with ` selected preview:\n<preview>` and ` notes: <notes>` appended when
present — space-joined onto the segment, not placed on their own lines. A
question with neither an answer nor a note is omitted entirely. A question
answered with notes but no selection carries the sentinel answer
`(notes only)`, which counts as "no answer" for the segment shape.

Then one of three phrasings:

| Condition | Result text |
| --- | --- |
| every answer *passes the label check* (below) and no question has notes | `Your questions have been answered: <segments>. You can now continue with these answers in mind.` |
| otherwise | `The user answered: <segments>. Read the answers carefully — they may request clarification, changes, or that you not proceed — and follow what they actually say.` |
| no segments at all | `The user did not answer the questions.` |

The label check, per question — this is the part most likely to be
implemented differently by two people, so it is spelled out:

- no answer, or the `(notes only)` sentinel → **passes** (an unanswered
  question does not push the whole result into the "user answered" phrasing)
- a multi-select answer (a list) → passes only if the question is
  multi-select, the list is non-empty, and every entry is a declared label
- a single-select answer that is a declared label → passes
- a single-select answer that is not a declared label → fails
- a multi-select question whose answer arrived as one string → passes only if
  splitting it on `", "` round-trips exactly and every part is a declared
  label
- any question carrying notes → fails (notes mean the user wrote prose)

The distinction is load-bearing: a user who typed rather than picked often
typed a *correction*, and the caution sentence is what stops the model from
pattern-matching it back onto the option it expected.

The current `(user cancelled)` / `User chose: …` strings go away; only
`internal/tools/ask_user_question_test.go` asserts on them.

Two Claude Code result paths are deliberately not adopted. Its AFK-timeout
branch (`Before going idle the user had selected: …`, with a visible countdown
and `CLAUDE_AFK_TIMEOUT_MS`) has no octo counterpart: octo's askers wait
indefinitely by design. And its `The user responded: <text>` branch is
unreachable from the CLI — `response` is not in the declared input schema and
only a non-CLI host (the SDK's `canUseTool`, an IDE) can inject it — so octo
has no such field.

### Schema and validation

`questions` becomes `minItems: 1, maxItems: 4`. Each option object gains
`preview`. `header` becomes **required** (Claude Code declares it
non-optional; its "max 12 chars" lives in the description text, not as a
`maxLength`, and the renderer simply truncates) and is the tab's label,
falling back to `Q1` / `Q2` when a model sends it empty anyway.

The description picks up Claude Code's authoring guidance, because it
materially changes what the model emits:

- ask only when blocked on a decision that is genuinely the user's — not for
  facts discoverable in the repo, and not for choices with an obvious default
- when recommending an option, put it first and suffix the label with
  `(Recommended)`
- `preview` is for concrete artifacts worth comparing side by side (ASCII
  mockups, code snippets, config or diagram variants) — not for simple
  preference questions; it is single-select only

Both tolerances octo grew are removed, and `Execute` performs the validation
Claude Code's host does, since octo has no schema-validation layer between the
model and the executor:

- **`normalizeAskInput` and its flat-shape fallback are deleted.**
  `questions` is the only accepted shape. This carries a real risk worth
  naming: the fallback was added because models *reverted* to the nested shape
  when the declared schema was flat, and octo is multi-provider — DeepSeek,
  Kimi, and DashScope models have no `AskUserQuestion` prior and may well
  reach for a flat `{question, options}`. The failure mode is a validation
  error the model must recover from, on providers that never saw Claude Code's
  shape. Accepted: one wire shape is the point of this change.
- **A question with fewer than two options is rejected, and the model is told
  not to retry.** This is the wording Claude Code uses, and it matters that it
  is *not* a retryable field error: a model that genuinely has one path cannot
  invent a second option, so "options must have 2-4 entries" would push it
  into inventing filler or looping. The message says: this call included a
  question with fewer than 2 options, so it was rejected and the person never
  saw it; a question with a single option has no decision in it; do not retry
  this call and do not invent a filler second option; state the one path you
  were going to offer as the approach you are taking and continue; if the call
  also contained questions with 2–4 options, those may be re-asked alone.
- **More than four options, or more than four questions, is rejected** with
  the same shape of message — re-ask with 2–4 options rather than retry
  verbatim.
- **Uniqueness is enforced**: question texts must be unique across the call,
  and option labels must be unique within a question. Claude Code needs this
  because its `answers` map is keyed by question text; octo's parallel arrays
  make key collision impossible, but duplicate labels still break the label
  check above.
- **Control characters in labels are replaced** (U+FFFD) before rendering, so
  a label can't corrupt a TUI frame.

`preview` gets Claude Code's 2000-character ceiling: a longer preview is
replaced, for display *and* for the echo-back, with `(preview cannot be shown
in full — compare the option labels and descriptions instead)`. Without this
a 20 KB preview would ride the WS frame, wreck the TUI frame, and land
verbatim in the tool result.

Claude Code's `answers` / `annotations` / `metadata` input properties stay
out: they exist because its host writes the user's selections, chosen preview,
and notes back into the recorded call, and octo's askers return that
information directly.

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
    Header      string        `json:"header"`
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

The flat `question`/`options`/`multi_select`/`header` fields are removed, with
no compatibility shim. For the CLI, `serve`, and the desktop build that costs
nothing — `webdist` is `go:embed`-ed, so the frontend ships with the server
that talks to it. The packaged mobile app is the exception: `npm run
bundle-web` copies `webdist` into the Capacitor bundle
(`mobile/capacitor.config.ts`), so its frontend is frozen at app-build time
while the `octo serve` it tunnels to is upgraded independently. An app built
before this change reads `undefined` where it expects `question`, and its
question card breaks until the app is rebuilt. That is accepted: the wire
format stays single-shaped, and the mobile bundle is refreshed with the
server.

A secret prompt is a one-question set with `secret: true`.

The answer frame carries the outcome and the parallel answer array:

```json
{"type":"user_question_answer","question_id":"q_…","outcome":"submitted",
 "answers":[{"choices":["OAuth with PKCE"],"custom":"","notes":""},
            {"choices":[],"custom":"only the schema part","notes":""}]}
```

`outcome` is `submitted` | `clarify` | `rejected`. The client never sends
`preview` — `Execute` fills it from the request, so a large preview never
travels back over the socket.

`handleWSUserQuestionAnswer` takes `(qid string, outcome string, answers
[]tools.AskAnswer)`. `wsMsgUserQuestionAnswer`
(`internal/server/ws_types.go`) and its dispatch in
`internal/server/ws_hub.go` change with it, as does `ws.answerQuestion`
(`web/src/lib/ws.ts`).

One frame closes the whole set, so the single ask slot (`acquireAskSlot`) and
the single `questionChans[qid]` entry are unchanged, as are global-broadcast
delivery and `pendingQuestions` replay. The cost of accumulating drafts
client-side is that a refresh mid-set loses the answers given so far and the
replayed question set starts empty; with one question that was invisible, with
four it is real, and it is accepted rather than solved with per-question
round-trips.

`WsEventRequestUserQuestion` and `QuestionModalEntry` (`web/src/lib/types.ts`,
`web/src/lib/stores.ts`) mirror the new shape:
`QuestionModalEntry.questions: AskQuestion[]` replaces
`question`/`options`/`multiSelect`/`header`.

## Surfaces

Shared model, matching Claude Code:

- questions are **tabs**, not a forced sequence. The tab row always renders
  (each tab labelled by its `header`, with an answered marker); left/right
  arrows appear only when there is more than one question. A **review/submit
  tab** is appended unless there is exactly one single-select question — a
  single *multi-select* question still needs one, because multi-select does
  not auto-advance.
- picking an option on a single-select question advances to the **next index**
  (not the next unanswered one). A lone single-select question submits the
  whole call on pick.
- the review tab lists the questions that *were* answered (notes are not
  shown), warns `You have not answered all questions` when some are blank, and
  offers `Submit answers` / `Cancel`; `Cancel` is the reject path.
- **two mutually exclusive layouts.** A question is rendered in the preview
  layout when it is single-select and at least one option has a preview;
  otherwise in the flat layout. They are not additive:

| | flat layout | preview layout |
| --- | --- | --- |
| option rows | label + description | label only, in a fixed-width left column |
| `Other` | present, an inline text input at N+1 | **absent** |
| notes | absent | present — the text slot holds notes, `n` opens it |
| `Chat about this` | numbered, at N+2 | unnumbered, below a divider, reached by arrowing past the last option |
| digit keys | select that row | move focus only |
| second column | — | the focused option's preview, or `No preview available` |

  Following Claude Code here means a question with previews shows no
  descriptions and offers no `Other`. That is a real fidelity call, not an
  oversight: `Other` is what the notes field replaces, and the descriptions
  are what the preview replaces.

- Escape rejects the whole set; the review tab's `Cancel` does the same.

### Plain stdin — `plainView.askQuestion` / `printQuestion` (`cmd/octo/turncore.go`), `parseSelection` (`cmd/octo/prompt.go`)

A line reader can't do tabs, so this surface degrades to sequential
questions — one card and one `ReadLine` each, `n/N` in the header:

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
indented block under the option, capped at 600 runes with the same rune-safe
truncation `askInputSummary` uses (that helper is unexported in
`internal/server`, so this is the same budget re-implemented, not a shared
call). Notes are not offered: there is no second column to annotate and no
spare key in a line reader, so a preview question here keeps its `Other` row.
`parseSelection` gains the two tail indices. An empty line skips the question;
EOF rejects the set; `Chat about this` returns `AskClarify` immediately.

### TUI modal — `modalState`, `newModalState`, `confirmQuestion`, `modalView` (`cmd/octo/tuirepl.go`, `cmd/octo/tuirepl_view.go`)

Full parity; this is the surface Claude Code's picker maps onto directly.
`UserPrompt` carries `Questions []UserQuestion`; `UserResponse` carries
`Outcome` plus `Answers []UserAnswer`. `modalState` gains `qIdx`,
`answers []UserAnswer`, a per-question `notes textinput.Model`, and
`noteActive`; `options` is rebuilt per question and per layout.

- header row: the question tabs — `[scope] [auth] [surfaces] [submit]` — with
  the current one highlighted and answered ones marked; `Tab`/`Shift-Tab`
  move between them
- flat layout: digit keys select, `Enter` records and advances, `Other` opens
  the inline input, `Chat about this` at N+2 resolves as `AskClarify`
- preview layout: the highlighted option's preview renders in a bordered pane —
  right of the list when `m.width >= 100`, below it otherwise, height-capped
  to keep the modal inside the terminal (bubbletea's inline renderer truncates
  over-wide lines with no marker); digits move focus; `n` opens the note field
- multi-select keeps `Space` to toggle and `Enter` to confirm the question
- `Esc` resolves as `AskRejected`

Because every outcome resolves the modal the same way it does today, the
existing modal queue (`m.modalQueue`) and `answerModal`'s hand-off to the next
queued prompt need no change — there is no state that outlives the modal.

### Web — `web/src/components/overlays/QuestionModal.svelte`, plus the WS→store mapping in `web/src/views/ChatView.svelte`

Both forms (bottom banner and expanded modal) render the current question:

- the tab row sits above the question, with the review/submit tab last
- flat layout: option rows, not pills — bold `label`, muted `description`
  beneath, radio/check affordance left, digit right; then the `Other` row,
  whose input reveals inline (today's always-present text box goes away); then
  `Chat about this`
- preview layout: two columns — labels left, the focused option's preview
  right in a monospace, scrollable, `overflow-x: auto` pane; below ~640px the
  preview moves under the list. The note field sits under the preview column;
  no `Other` row.
- footer: `Cancel` · `Back` · `Next` / `Submit`; the submit tab shows the
  review list and the unanswered warning
- `Chat about this` submits the frame with `outcome: "clarify"` and closes the
  overlay — nothing is captured afterwards, so no composer changes are needed
  in `ChatView.svelte`'s `send()` or in the mobile `sendMobile()`
  (`web/src/mobile/chatWiring.ts`)

Draft state moves from the flat `selected`/`customText` pair to an array
indexed by question, reset when `questionId` changes. The `others`-sessions
notification rows and the no-interruption rule from
`dev-docs/cross-client-ask-question.md` are unchanged; a row's preview text
uses `questions[0].question` plus `+N more` when the set is larger.

### Mobile — `web/src/mobile/QuestionOverlay.svelte`

Same model, phone layout: the tab row becomes a segmented pager, option rows
are tappable cards, and the preview layout renders the preview in a collapsed
`<details>` under the option — with the note field inside that disclosure —
rather than a second column. The current overlay hides its free-text box
behind an expand toggle; that box becomes the `Other` row in the flat layout
and disappears from preview-layout questions. Per-session drafts become
per-session, per-question drafts. The toast + "View" path for non-active
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
free text already *is* the reply, which is exactly what `Other` means here.
`Chat about this` is listed and returns `AskClarify` immediately — no extra
round-trip, because clarify resolves the call rather than waiting for a
follow-up. Previews and notes are omitted: previews exist to be compared side
by side, which a chat timeline can't do, and notes annotate that comparison.

An empty reply rejects the whole set (`AskRejected`) rather than skipping one
question — over chat there is no other way to say "stop asking", and skipping
silently would leave the user answering a set they've already abandoned. No
timeout, per the existing rationale.

### Secret prompts

The secret path (`wsAsker.AskSecret`, and `replAsker.AskSecret` via
`UserPrompt{Kind: KindSecret}`) is a one-question set with zero options, so it
renders as a bare masked input: no tab row, no option rows, no `Other` row,
no notes, and **no `Chat about this`** — routing a password into a
"talk to the model" channel would put the value in the conversation, which
`internal/tools/replay_secrets.go` explicitly forbids (the question text and
the fact a secret was provided may enter history; the value must not). The
TUI's existing `KindSecret` branch in `newModalState` (masked `textinput`,
`EchoPassword`, straight to text entry) survives unchanged.

## Callers in this repo that must change

The strict validation invalidates the examples octo itself ships, so these
move with the tool or the first call fails:

- `internal/prompt/base.md` — the base system prompt teaches a **string
  array** (`Example options: ["Proceed with the fix", …]`), which the
  object-only `options` schema rejects. Rewrite as `{label, description}`
  objects.
- `internal/skills/defaults/onboard/SKILL.md` — one question lists **seven**
  options; today `options[:4]` silently trims it. Cut to four or split the
  question.
- `internal/skills/defaults/channel-manager/SKILL.md` — one question lists
  **six** options (same trim), and another collects Discord token / app_id as
  pure free text with no options at all. The first needs cutting; the second
  needs a different mechanism (the secret path, or plain prose).
- `web/src/lib/i18n.ts` — every new string (tab labels, `Other`,
  `Chat about this`, `No preview available`, the note affordance,
  `You have not answered all questions`, `Submit answers`, Back/Next) needs
  an `en` and a `zh` entry, or `web/src/lib/i18n.coverage.test.ts` fails: it
  scans the whole tree for `$t('…')` literals.

## Divergences from Claude Code

- **Reject does not abort the turn.** Claude Code's Escape aborts; octo's
  asker has no turn handle, so it returns the rejection text and leaves
  interrupting to the user.
- **IM and plain stdin degrade.** IM omits previews and notes and drops the
  redundant `Other` row; plain stdin omits notes (and therefore keeps `Other`
  on preview questions) and asks sequentially instead of as tabs.
- **`Custom` and `Notes` are separate fields.** Claude Code stores both in one
  `textInputValue` slot, so a question there can never carry both. Splitting
  them is harmless but widens the label check's input space, which is why that
  check is specified above rather than left to "whatever CC does".
- **Not adopted:** the AFK timeout (and its countdown, `CLAUDE_AFK_TIMEOUT_MS`
  / `CLAUDE_AFK_COUNTDOWN_MS`); the `The user responded:` result branch, which
  no CLI path can reach; the separate screen-reader layout; image paste into
  the answer inputs; `ctrl+g` external-editor editing of `Other`/notes; and
  the `html` preview format with its input validation (previews are Markdown
  in a monospace pane).

## Tests

- `internal/tools/ask_user_question_test.go` — 1..4 questions parsed;
  `preview` retained; `label`/`description` no longer folded; `Preview`
  back-filled from the request by label. All three outcomes' result text,
  including: the clarify block with an answered, an unanswered, and a noted
  question; the reject text; each label-check branch (unanswered passes,
  `(notes only)` passes, multi-select list, single-select given a list,
  comma-round-trip, notes force the caution phrasing); segment shapes with
  preview and notes space-joined; a `(no option selected)` segment. Rejections
  that never reach the asker: the flat shape, zero/five questions, a
  one-option question (asserting the do-not-retry wording, not a field error),
  a five-option question, duplicate question texts, duplicate labels within a
  question, and an over-long preview replaced by the withheld placeholder.
- `internal/server/server_test.go`, `pending_prompt_test.go`,
  `live_replay_test.go` — the new event shape broadcasts globally and replays
  on resubscribe; one frame per outcome resolves a multi-question ask;
  `wsMsgUserQuestionAnswer` dispatch in `ws_hub.go` carries `outcome`.
- `internal/server/channel_ask_test.go` — three questions produce three
  messages and three reply slots; `Chat about this` resolves as clarify with
  the answers so far; an empty reply rejects the set; previews and notes are
  absent from the sent text.
- `internal/tools/ask_user_question_ctx_test.go`,
  `internal/tools/browser_test.go`, `internal/tools/workflow_test.go`,
  `internal/tools/replay_secrets_test.go` — these construct `AskResponse` (and
  embed `stubAsker`), so they move with the contract.
- `cmd/octo/asker_test.go`, `cmd/octo/tuirepl_p5_test.go` — `AskRequest` ↔
  `UserPrompt` round-trips the question set, per-question answers, notes, and
  the outcome.
- `cmd/octo/tuirepl_test.go` — `Tab` moves between questions and lands on the
  submit tab; digits select in the flat layout and only move focus in the
  preview layout; `n` attaches a note; `Esc` rejects; a lone single-select
  question submits on pick; preview pane placement flips at width 100.
- `web/src/lib/askStepper.ts` + `askStepper.test.ts` — tab/draft logic (move
  between questions, per-question drafts keyed by `questionId`, which layout
  a question gets, whether the submit tab exists, all-answered detection, and
  the frame each outcome builds) lives outside the components so it is
  unit-testable the way the rest of `web/src/lib` is; the components stay
  presentational. Vitest runs from `web/`. Visual layers (two-column preview,
  narrow-window fallback, dark mode) are covered by the CDP walkthrough.

## Verification

1. `make test`, `make vet`, `make fmt-check`; `npm test` and
   `npx svelte-check` from `web/`.
2. TUI: a scripted 3-question ask (one with previews, one multi-select, one
   flat) — tab between questions, select by digit, attach a note in the
   preview question, take `Other` in the flat one, land on the submit tab;
   then repeat and `Esc`; then repeat and take `Chat about this`, confirming
   the clarify block lists the answers already given and the turn continues.
3. Web: isolated `serve` on a scratch `HOME`, driven over CDP against that
   port (never the user's live session — see
   `reference_worktree_isolated_web_walkthrough`). Check the tab row, both
   layouts, the review tab's warning, dark mode, and a narrow window.
4. Mobile: the same server with `?mobile=1` — pager, collapsed preview, note
   field.
5. IM: one channel (Telegram or Feishu), three-question ask, answer `2`, free
   text, `Chat about this`, then an empty reply.
6. Secret: a replay-secret prompt still renders masked, with no `Other`,
   notes, or `Chat about this` row.
7. Cross-client: ask from session B while viewing session A → notification row
   only; click through, answer on mobile, confirm the desktop banner clears.

`docs/src/content/docs/reference/tools.md` (and its `zh` mirror) list
`ask_user_question` as a one-line table entry, so neither needs a change.
