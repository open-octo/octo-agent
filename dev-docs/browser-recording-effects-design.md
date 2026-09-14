# Recording effects: side-effect evidence for the cleanup pass

The third part of #2406. It extends the record → distill → replay pipeline
described in `browser-computer-use-design.md`: every recorded gesture carries
what it observably did, a conservative marker names the trailing gestures that
did nothing, and both reach the distiller and the confirmation plan.

## Problem

The cleanup pass after `record_stop` is asked to "drop redundant back-and-forth
and retries; keep the intended linear path" (rule 2 of the distill prompt in
`GenerateRecording`, `internal/browser/recording.go`). Two things it needs for
that are missing, and #2406 supplies one of them (the user's stated `goal`,
carried on `record_start`). This document is about the other: **evidence**.

The case: a demonstration named "查最新评论" ends with three stray clicks — open
the comment box, expand one "N回复", press 取消. A person sees at once that they
did nothing: the URL did not move, no tab opened, nothing was downloaded, no
value was typed, and nothing that could have changed server state happened. The
model cannot see any of that, because the recorder does not capture it:

- `RecordedEvent` carries type / selector / tag / text / value / the URL *at
  capture time*. `renderTrace` sends the distiller even less — no URL per event.
- The in-page network monitor (`window.__octoNet` in `captureScript`,
  `internal/browser/recorder.go`) only counts in-flight fetch/XHR (`inc()` /
  `dec()` and a generation counter). It answers "did the click start any
  request?" — the signal that inserts a `wait network` step — but records
  neither method nor destination. A read-only GET and a state-changing POST look
  the same.
- Navigations are recorded as `navigate` events (`watchNavigations`, from
  `Page.frameNavigated` / `Page.navigatedWithinDocument`, with `SameDoc` and
  `NewTab` flags). Downloads upgrade the triggering click to a `download` event
  (`watchDownloads` → `upgradeLastClickToDownload`, attributed to the last click
  within `downloadAttributionWindow`). So *some* effects are already in the
  event stream — just not in a form the distiller is shown, and not per gesture.

`compressEvents` (deterministic, pre-distill) handles two provable patterns —
same-field overwrite and A→B→A backtrack. Three clicks on three different
elements that collectively did nothing match neither rule, so the only cleanup
they could get is the model's — which has no basis to make it.

## Design

Every user gesture carries an **effects summary** — what observably happened
because of it — a conservative **`likely_noop`** marker is derived from it, and
both feed the distiller and the confirmation plan. Nothing is deleted on the
marker alone: it is a question for the user, not a decision.

Code: `internal/browser/effects.go` (`Effects`, `attachEffects`,
`markLikelyNoop`), `Recorder.watchRequests` and `Recorder.Events` in
`recorder.go`, `renderTrace` / `backfillNoopHints` / `SummarizeRecording` in
`recording.go`.

### 1. Data: `Effects` on `RecordedEvent`

```go
// Effects is what observably happened because of a gesture (click / enter /
// change / upload). Captured into events.json; never serialized into
// recording.yaml, which stays the editable steps.
type Effects struct {
	URLAfter string         `json:"url_after,omitempty"` // top-level URL once the gesture settled, if it changed (RecordedEvent.URL is the URL before)
	NewTab   bool           `json:"new_tab,omitempty"`   // the gesture opened a tab
	Download bool           `json:"download,omitempty"`  // the gesture started a download
	Requests map[string]int `json:"requests,omitempty"`  // requests it triggered, counted by HTTP method: {"GET": 2, "POST": 1}
}
```

`RecordedEvent` carries `Effects *Effects json:"effects,omitempty"`,
`LikelyNoop bool json:"likely_noop,omitempty"` and `At int64 json:"at_ms,omitempty"`
(the arrival time that attributes requests). All optional: old `events.json`
files parse unchanged, and an event without `Effects` is treated as
unjudgeable — never marked. Gestures are click / enter / change / upload /
download; navigate and wait events carry no `Effects`.

Only **method counts** are recorded for requests — no URLs, no bodies. Request
URLs routinely carry tokens and identifiers; `events.json` is `0600` but is also
the file users paste into issues. The method is the only fact the noop rule
needs.

### 2. Capture

**URL / new tab / download — derived, no new capture.** `url_after` is the last
`navigate` event between the gesture and the next gesture (the final stop of a
redirect chain), set only when it differs from the URL the gesture was made on;
`NewTab` when any of those navigations opened a tab; `Download` is the event's
own type after `upgradeLastClickToDownload`. Computed by `attachEffects` on
every `Recorder.Events()` call from events the recorder already emits, so it is
exact and free, and the raw log stays exactly what was captured.

**Requests — one subscription.** `watchRequests` subscribes to
`Network.requestWillBeSent` on each instrumented session (the same sessions the
capture binding is installed on: top document, cross-origin iframes, tabs
opened during the demonstration), after `Network.enable`. Every event is
decoded, then only `type ∈ {XHR, Fetch, Document}` is kept — images, fonts,
scripts, stylesheets and pings are dropped — with method and arrival time.
The subscription uses a 1024-deep channel (`requestSubscriptionDepth`): the
CDP reader drops on overflow, and a page load's burst of asset requests must
not push out the one POST that separates a write from a no-op.

`attachEffects` assigns each request to the most recent gesture at or before
it, if within **`effectsAttributionWindow` = 1.5 s** — the same shape as the
download attribution, with a shorter window because a request the gesture
caused starts promptly while a download can take seconds to begin. Gesture and
request timestamps are stamped by different consumer goroutines, so a request
can carry a time a few ms before the click that caused it; a request no
preceding gesture claims goes to the next gesture if it follows within
`requestLeadTolerance` (300 ms). A request outside every window (polling, an
analytics heartbeat) attaches to nothing.

Why CDP and not the in-page hook: the hook lives in the document and is lost on
every cross-document navigation, misses `navigator.sendBeacon` and form
submissions, and is per-frame. `Network.requestWillBeSent` sees all of them,
carries `request.method` and `type`, and needs no page-side state. The in-page
monitor stays as it is — it still drives `wait network` insertion and replay's
`WaitForNetworkIdle`.

Cost: `Network.enable` makes Chrome stream request/response events for the
session for the duration of the recording only — `Recorder.Stop` unsubscribes
and calls `Network.disable` on every session the recorder enabled it on
(`netSessions`). The handler's work per request is one decode plus, for the
kept types, a map increment.

### 3. Deterministic rule: `likely_noop`

`markLikelyNoop`, applied with `attachEffects`, over the **trailing run** of
gestures only:

> Walk backwards from the last event, skipping recorder-synthesized waits. A
> click, or an Enter that carries no typed value, is `likely_noop` when its
> effects show **no request other than GET, no URL change, no new tab, no
> download**. Stop at the first event that fails the test — a typed value (a
> change, or an Enter carrying what was typed: that is a submission), a write,
> a navigation, a gesture with no effects (older recorder); everything before
> it is left unmarked.

Why tail only: a stray click that changed nothing and was followed by nothing
cannot have contributed to the recording's end state, so removing it is safe
*if the user agrees*. The same click in the middle may be a precondition for a
later step (expand a panel so a control appears) — it gets its effects rendered
in the trace so the model can reason about it, but no marker.

Why the rule is conservative in the right direction: "nothing visible changed"
is **not** "no side effect" — a like is a POST that leaves the URL alone. The
rule therefore keys on the request method, not on the page. A site that reads
through POST (GraphQL, some mini-program back-ends) simply never gets the
marker and falls back to intent (`goal`) and the user's confirmation. A pure
client-side toggle the user *meant* (a switch saved by a later click) is a
false positive at the tail — which is exactly why the marker is a question and
not a deletion.

### 4. Distiller input

`renderTrace` renders effects and the marker per event:

```
7. click selector="span.comment-btn" tag=SPAN text="说点什么..." url=https://…/notes  effects: none  [likely_noop]
8. click selector="div.reply-count" tag=DIV text="2回复"  effects: GET×1  [likely_noop]
9. click selector="button.cancel" tag=BUTTON text="取消"  effects: none  [likely_noop]
```

and a navigating or downloading gesture renders `effects: url→https://…`,
`new_tab`, `download`. Each event also carries its `url=`. Rule 2 of the system
prompt says: *events marked [likely_noop] changed nothing observable and
contributed nothing to the end state — they are candidates to drop: drop one
when the Goal clearly does not need it, keep it when the Goal names it or when
the Goal is missing or too vague to tell.* The wording errs toward keeping on
purpose: in the live-model runs a marked step the user wanted (a GET-only
点开评论 at the tail) was deleted once when no goal was given, and a stray the
user must remove by hand is the cheaper mistake. Together with the `goal` line
(#2406 part 2) the model has intent **and** evidence for the judgement rule 2
asks of it — and needs both: the marker alone flags that wanted tab too, and
only the goal says to keep it.

### 5. Confirmation plan

`record_stop`'s reply (`SummarizeRecording`) adds a separate block after the
numbered steps when any marked step survived the distiller:

```
以下步骤没有改变页面状态（没有跳转、没有写入请求、没有下载），可能是误点。要保留吗？
  7. 点击「说点什么...」
  8. 点击「2回复」
  9. 点击「取消」
```

The marker reaches the *refined* steps through `backfillNoopHints`: the
distiller re-parses its own YAML, so the flag is re-attached by frame+selector
from the baseline exactly as anchors are, and then the tail-only invariant is
re-imposed on the refined list — the same element clicked legitimately
mid-flow and again by mistake at the end shares a selector, and only the tail
occurrence may stay flagged. On `Step` it is the unexported `likelyNoop` hint
(like `expectNetwork`), never written to `recording.yaml`. The block words a
`key` step as a key press, not a click. After the user answers, the agent
edits the YAML; nothing about the marker persists.

### 6. Replay

Unchanged. Effects live in `events.json` only; they are diagnostic ground truth
for the compile stage and for a human reading the sidecar, not replay input.

## What this does not do

- It does not auto-delete anything. Deterministic deletion stays limited to the
  two provable `compressEvents` rules.
- It does not judge middle-of-recording clicks; they get evidence in the trace
  and nothing more.
- It does not record request URLs or bodies.
- It does not replace the `goal` (#2406 part 2). Intent and evidence are two
  halves: without the goal the model cannot tell a wanted no-op from a stray
  one; without the effects it cannot tell a no-op from a write.

## Decisions

1. **Request capture via CDP `Network.requestWillBeSent`**, not the in-page
   fetch/XHR hook: more complete (navigations, `sendBeacon`, form posts) and
   frame-agnostic, at the cost of the Network domain being enabled during
   recording.
2. **Attribution window 1.5 s** for requests; downloads keep their 5 s.
3. **Tail-only marking.** Safe by construction; marking middle clicks would
   need the model to reason about preconditions.
4. **Method counts only** for requests — no host, no path.
5. **Flagged steps stay in the plan and the user removes them.** The recording
   on disk always matches the plan shown.

## Tests

- `TestRecorderCapturesEffects` (Chrome): a POST button, a navigating button, a
  GET button and a button that touches nothing, clicked in that order — method
  counts per click, `url_after` on the navigation only, and exactly the two
  trailing clicks marked.
- `TestAttachEffectsAttribution` (incl. the lead tolerance and the window
  edge), `TestMarkLikelyNoopTailOnly` (incl. the Enter-with-value stopper),
  `TestEffectsString`: the pure functions over synthetic events.
- `TestRenderTraceEffects`, `TestLikelyNoopReachesPlan`,
  `TestBackfillNoopHintsStaysTailOnly`: the trace and the plan carry the
  evidence; the marker survives distillation onto kept steps, stays tail-only
  across a shared selector, and never reaches the YAML.
- `TestDistillE2E_RealModels` (`internal/app`, opt-in via `OCTO_E2E_DISTILL=1`,
  live keys): records the #2406 demonstration in headless Chrome and distils it
  with every model in the user's config, four ways — goal + effects, goal only,
  effects only, neither — asserting the design column cleans the recording for
  every model and logging the rest as the comparison matrix.
