---
title: "Octo Onboarding Series (10): Record & Replay in Practice — Record Your Actions Once, Let octo Replay Them"
description: "Record a browser workflow you'd otherwise do by hand, distill it into a replayable, self-healing script — the next time it comes up, octo just clicks through it."
pubDate: 2026-07-18
author: "octo-agent team"
tags: ["onboarding", "octo-agent", "browser"]
locale: en
originalSlug: onboarding-browser-record-and-replay
---

# Octo Onboarding Series (10): Record & Replay in Practice — Record Your Actions Once, Let octo Replay Them

> The last post wired up the browser — fine for a one-off task where octo can just watch and click live. But if you do the same sequence every week, having the model re-observe-and-decide every single time is wasted work. This post covers recording it once so it can replay on its own — and fix its own selectors when they drift.

---

## It records what you do, not what the model does

This is the single most important thing to understand about the mechanism: once you call `record_start`, octo **hands the browser back to you**. You perform the steps yourself, say "done," and only then does it call `record_stop <name>` to save what happened. octo doesn't drive the page itself during recording — it watches and takes notes, it doesn't act.

Each step captures: the action type (`click`/`type`/`select`/`upload`/`navigate`, and so on), a selector anchored to the nearest ancestor element with a stable `id` (rather than a positional selector chain that breaks the moment the page layout shifts), that element's visible text at the time, and the URL it happened on — plus a redundant **fingerprint** of the target: alternate selectors built with different strategies, its `role` attribute, and the nearest label-like text next to it. The fingerprint is what lets replay find the element again after the page's CSS classes have all been renamed (more on this below).

The recorder also notices what happens *between* your actions. A click that fires off network requests gets an automatic "wait for network" step; one that opens a modal or date picker gets a "wait for element" step — so replay won't race ahead of a page that's still loading. A click that starts a file download is upgraded to a `download` step bound to a `file[]` output. And obvious fumbles — retyping the same field, clicking away and clicking back — are compressed out deterministically before any model sees the recording. When you stop, `record_stop` replies with a numbered run-plan (each step plus its check) for you to confirm before the recording is considered final.

## One click in the web UI

You don't need to remember the `record_start`/`record_stop` action names — the "Browser" panel in the web UI has a "● Record" button. Clicking it opens a fresh chat whose first message spells the whole flow out for octo: confirm the browser is connected, ask what to record and what to name it, call `record_start`, hand control to you, and `record_stop` once you say you're done.

Next to every saved recording in that same panel:

- **"▶ Replay"** — calls `replay` by name, through the full agent path (the self-heal mechanism covered below only kicks in on this path — it's not a server-side replay that bypasses the model).
- **"✎ Edit"** — also conversational: it reads the recording's YAML, lists the steps for you, and you say what to change (fix a selector, add/remove/reorder a step, set a param's default), and it writes the file back without replaying it on your behalf.

## What a saved recording looks like

The raw recording goes through one model pass — dropping detours that led nowhere, swapping the concrete values you typed for `{{param}}` placeholders, and writing a description — but this pass can only **reorder or rename steps that were actually recorded**, never invent a new one: any step whose selector isn't in the original recording gets rejected outright, falling back to the raw step instead.

The distilled result is saved as plain YAML at `~/.octo/browser-recordings/<name>.yaml` — readable, hand-editable, diffable in git. Roughly:

```yaml
name: submit-expense-report
description: Log into the expense system and submit a standard reimbursement form
params:
  - name: amount
    description: Reimbursement amount
    default: "128.00"
  - name: memo
    description: Reason for the expense
outputs:
  - name: receipt_url
    type: string
steps:
  - action: navigate
    url: https://expense.example.com/new
  - action: click
    selector: "#category-travel"
    label: Pick the "Travel" category
  - action: type
    selector: "#amount"
    hint: Reimbursement amount
    value: "{{amount}}"
  - action: type
    selector: "#memo"
    hint: Reason field
    value: "{{memo}}"
  - action: click
    selector: "#submit-btn"
    verify:
      text: Submitted successfully
  - action: extract
    js: document.querySelector('.receipt-link').href
    bind: receipt_url
```

Three fields in that YAML are worth knowing. `params` are replay-time inputs you can override: `{{amount}}`/`{{memo}}` in the steps get substituted with whatever's passed in, falling back to `default` when nothing is. `hint` is that form field's accessible name (placeholder/name/aria-label/id, or its associated `<label>` text) — when the positionally-recorded `selector` drifts, replay tries relocating the field by hint before handing off to self-heal (covered below). `outputs` declares the values this recording exposes: a step like `extract`/`download` can bind its result to a named output via `bind` for a downstream step to consume.

## What makes replay trustworthy

```
browser(action: "replay", name: "<recording-name>", params: { ... })
```

In the common case replay is deterministic and involves no model call at all: each step waits for its target to appear before acting, and runs a `verify` check if one was declared. A few robustness details worth knowing:

- A step recorded with a fingerprint re-identifies its target by **scoring** candidate elements — found via the original selector, the alternates, and text/role scans — against the recorded text, role, tag, and neighbor text. A page whose CSS class hashes all rolled over still resolves; and if the old positional selector now matches the *wrong* element, the step refuses and fails into self-heal instead of silently clicking it. (Recordings made before fingerprints keep the older behavior: exact selector first, then the element carrying the recorded visible text.)
- Auto-inserted `wait` steps let the page settle — network idle, or a specific element appearing — before the next action fires.
- A `download` step clicks its trigger, waits for the download to finish, and binds the saved file's path to its output.
- If a step opens a new tab, subsequent steps automatically follow onto it.
- If a field comes back empty right after typing into it, replay clears and retries once before actually calling the step a failure.

Replay is all-or-nothing by design — octo is explicitly instructed to only use a recording when the request matches it **start to finish**; it won't execute a partial subset, or improvise beyond the declared params. Recordings are also replay-only: there's no keyword trigger for this path (an earlier version tried one and it was pulled — not reliable enough).

## When the selector is truly gone — self-heal

If a step's selector and its `hint` both come up empty — and only when a model is configured specifically for this purpose — octo takes a plain-text summary of the page's currently interactive elements (selectors + visible text, no screenshot), sends it along with the intended action, the expected label, the selector that just failed, and the step's recorded fingerprint (role, tag, neighbor text) when it has one, and asks the model to reply with nothing but a corrected CSS selector. The fix gets one retry, and on success it's **written straight back into the recording's YAML file** — so a self-heal is a permanent fix, not a one-time patch; every future replay skips re-healing the same thing.

## Wiring it into a workflow

A recording's `outputs` can be plugged straight into [the workflow scripts from the previous post in this series](/blog/posts/en/onboarding-workflow-parallel-review/) via `recording("<name>", params)` — record "log in and export the invoice," bind the exported file's path as an output, and hand it to another agent to parse and summarize in the next step, with no manual hand-off in between.

---

## End of the series

Ten posts in: install, Skills, MCP, Loop, Cron, the weekly-report capstone, Workflow, Goal, Browser, and Record & Replay — covering both the "ask once, keep pushing on its own" spectrum and a second, completely different capability: going from "calling a tool" to "actually operating a web page for you." Which one fits depends on the task in front of you: a one-off question just gets asked directly; something that needs repeated checking gets `/loop`; something on a schedule gets cron; something that splits into independent chunks gets workflow; something you can't fully scope up front gets a goal; and anything with no API, only a UI, gets a browser connection you can drive live or record once and replay forever.

**Previous in the series**: [Octo Onboarding Series (9): Browser in Practice — Hand octo Your Own Browser](/blog/posts/en/onboarding-browser-setup/)
**Back to the start**: [Octo Onboarding Series (1): Install It, Say Your First Word to It](/blog/posts/en/onboarding-install-and-first-run/)
