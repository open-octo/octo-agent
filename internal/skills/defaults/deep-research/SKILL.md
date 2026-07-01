---
name: deep-research
license: MIT
description:
  Deep, multi-source, fact-checked research on a topic — fan out searches, read
  primary sources, adversarially verify each claim, and synthesize a cited report.
  Use when the user wants a thorough research report rather than a quick answer,
  e.g. "深度调研", "research this properly", "写一份调研报告", "帮我系统调研",
  "多来源核实", "give me a researched writeup", "背景调查一下". BEFORE starting,
  if the question is underspecified (scope, region, time window, use-case unclear),
  ask 2-3 clarifying questions to narrow it. For a single quick lookup use
  web_search directly; for a whole codebase/feature use the relevant dev skills.
---

# Skill: deep-research

A harness for research you can trust: breadth first (many angles), then depth
(primary sources), then an adversarial pass that tries to *break* each claim
before it goes in the report. Built on octo's native tools — `web_search`,
`web_fetch`, `sub_agent`, and (for login-gated / JS-rendered / anti-bot sites)
the `browser` tool via the `web-access` skill. No external services.

The goal is a **cited** report where every non-obvious claim traces to a source
you actually read — not a plausible-sounding summary of search snippets.

## 0. Scope before you search

Research is only as good as the question. Before any tool call, confirm you can
state the **deliverable**: what question, what decision it informs, what time
window, what region/market, what depth. If any of these is missing and would
change the answer, ask 2-3 sharp clarifying questions first — don't guess a
scope and burn a fan-out on the wrong one.

Then write down, in one line, what "done" looks like. That line is the acceptance
criterion the final report is checked against.

## 1. Fan out — breadth

Decompose the question into 4-8 **independent** sub-questions, each attacking a
different angle (definition, current state, competing views, data/numbers,
history, criticisms, primary actors). Independence matters: overlapping
sub-questions waste the fan-out.

Dispatch them in parallel. `web_search` / `web_fetch` are stateless, so this is
exactly the case sub-agents are for:

- One `sub_agent` per sub-question. Prompt it **goal-first**, not step-first:
  describe what to *find out*, not "search for X" — an anti-bot source may need
  `browser` on the main site, and "search" would anchor the sub-agent to
  `web_search`.
- Tell each sub-agent to **load the `web-access` skill and follow it**, to return
  findings *with source URLs*, and to flag anything it couldn't verify.
- Do NOT parallelize `browser` work — the browser session is single-page and
  process-shared; concurrent sub-agents fight over one page. Keep browser
  interaction to a single sequence; parallelize only the stateless search/fetch.

If `sub_agent` is unavailable in this session, run the sub-questions sequentially
inline and say so.

## 2. Go to the source — depth

Search engines and aggregators are a **discovery entry point, not proof**. N
outlets quoting the same wrong number is circular, not corroboration. For every
claim that matters, reach the **primary source** and read it:

| Claim type | Primary source |
|------------|----------------|
| Policy / regulation | Issuing body's official site |
| Company announcement | The company's own newsroom / filing |
| Academic / scientific | The original paper or the institution |
| Product capability / API | Official docs or source, not blog posts |
| Statistics | The dataset publisher, not the article citing it |

Use `web_fetch` on the source URL to pull the page as clean Markdown (pass the
raw URL — don't hand-build a `r.jina.ai/` prefix). When the source is behind a
login, renders via JS, or blocks fetching, switch to `browser` per `web-access`.

When no official source exists, an original report from an authoritative outlet
(not a reprint) can serve as a secondary basis — but say so explicitly:
"No official source found; the following relies on [outlet]'s reporting and may
carry transcription error."

## 3. Adversarial verify — try to break each claim

This is what separates research from a summary. For each load-bearing claim,
run a skeptical pass **before** trusting it:

- Does the source actually say this, or is it the article's spin on it?
- Is the source primary, or is it echoing someone else? Trace one hop back.
- Is it current, or superseded? Note the date on every source.
- Do independent sources *disagree*? Surface the disagreement — don't average it away.

For high-stakes claims, dispatch a verifier `sub_agent` prompted to **refute**
the claim (default to "unverified" when uncertain), not to confirm it. A claim
that survives an honest attempt to break it is worth reporting; one that doesn't
gets dropped or flagged as contested.

Track claim → source as you go. Anything you can't attribute to a source you
read does not enter the report as fact — at most as a clearly-labelled open
question.

## 4. Synthesize — the cited report

Structure to the deliverable from step 0, not a fixed template. Typical shape:

- **Bottom line** — the answer to the question, up front, in a few sentences.
- **Findings** — organized by sub-question or theme, each key claim carrying an
  inline source (title + URL). Present genuine disagreement as disagreement.
- **Confidence & gaps** — what's well-established, what's thin or single-sourced,
  what you couldn't resolve. State this honestly; a known gap is more useful than
  false certainty.
- **Sources** — the primary sources you actually read, deduped.

Then check the report against the step-0 acceptance line. If it doesn't answer
the question, name what's still missing rather than padding.

## Completeness check

Before finishing, ask: what's missing? A sub-question not run, a claim asserted
but never traced to a source, a contradiction glossed over, a source cited but
not read. Whatever that surfaces is the next round — loop back to step 1 for it,
don't ship around it. Stop when the acceptance criterion is met, not before, and
don't over-research past it.
