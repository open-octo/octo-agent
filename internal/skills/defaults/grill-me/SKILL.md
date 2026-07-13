---
name: grill-me
license: MIT
description:
  Interview the user relentlessly about a plan or design until reaching shared
  understanding, resolving each branch of the decision tree one at a time. Use
  when the user wants to stress-test a plan, pressure-test a design before
  building, or says "grill me", "拷问我", "帮我把方案想清楚", "挑战一下这个设计",
  "review my plan before I build". Pairs with the tech-design skill — grill first,
  then hand the resolved decisions to tech-design to write them up.
metadata:
  origin: adapted for octo — generic stack, octo-native codebase exploration
---

# Skill: grill-me

Interview the user relentlessly about every aspect of a plan until you reach a
shared, resolved understanding. You are the skeptic who surfaces the decisions
they haven't made yet — not a note-taker.

## ⚠️ Hard rule: ONE question per message

This is the single most important rule of this skill. Even when you spot five
things worth grilling on, send the first question, wait for the answer, then ask
the next.

**Why this matters**: a wall of five questions defeats the purpose. The user
can't think hard about any single decision when faced with a multi-question pile
— they'll skim and pick the easy ones, or push back asking "which first?". Both
waste the session.

**Anti-pattern**:
> ❌ "Here are 5 things I want to grill on: 1. … 2. … 3. …"

**Correct pattern**:
> ✅ "[First question, with options + recommendation]"
> [wait for answer]
> "[Next question, informed by the previous answer]"

If a topic has sub-questions, ask the top-level one first and drill in based on
the answer. Don't pre-emptively enumerate every branch — the answer to question
1 often kills questions 2-3.

## Phase 0 — Context exploration

Before asking any questions, do your homework:

1. **Read the input** — the user may provide anything from a one-line idea to a
   full PRD (local file, doc URL, or prose). Whatever the form, extract what you
   can: problem statement, core user flow, scope boundaries. The less the user
   gives, the more Phase 1 needs to cover.
2. **Explore the codebase** — identify the existing services, data models, APIs,
   message-queue topics, cache keys, and prior art relevant to the plan. Use
   codegraph if the project has it indexed, otherwise search directly. Report key
   findings to the user concisely before starting questions.

This phase is silent work — don't ask the user things you can learn from the code.

## Phase 1 — Grill

Walk down each branch of the design tree, resolving dependencies between
decisions one by one. If a question can be answered by exploring the codebase,
explore instead of asking.

### Question format

For each non-trivial decision, structure the message as:

1. **Set up the decision** — 1-2 sentences of context (what's at stake, why this
   is a branch point)
2. **Present 2-3 options** as A/B/C with a one-paragraph trade-off each (table if
   complex)
3. **State your recommendation** and why, grounded in the codebase findings
4. **End with the question**

Then stop and wait. Do not chain a second question after this one, even if it
feels closely related — the answer to question 1 usually reshapes question 2.

### Prefer multiple choice over open-ended

A/B/C questions are easier to answer and give you concrete signal to continue
from. Reserve open-ended for "what are you optimizing for?" / "what's the goal?"
style questions where the option space is genuinely unknown to you.

Simple yes/no or factual questions don't need the full options-and-recommendation
treatment — use judgment.

## Phase 2 — Coverage check

Before wrapping up, verify no critical area was missed. Scan the decisions made
so far against these domains:

| Domain | Check |
|---|---|
| Architecture | Service boundaries, sync vs async, failure handling |
| Data model | Tables/collections, columns, indexes, data volume, partitioning |
| API design | Contracts, pagination, idempotency, breaking changes |
| Messaging | Topics, schemas, consumer groups, retry/DLQ |
| Caching | Key format, TTL, eviction, invalidation |
| Configuration | Config / feature-flag keys, per-environment default differences, dynamic vs restart-to-apply, multi-key ordering |
| Rollout & safety | Grayscale, feature flags, rollback, monitoring |

For each domain relevant to the plan: if it was never discussed, ask about it now
(**still one question at a time** — the temptation to batch returns here, resist
it). If it was covered, skip it. Domains not applicable to the plan (e.g. no MQ
involved) can be skipped entirely.

## Phase 3 — Wrapping up

When all major branches of the decision tree are resolved, stop and summarize:

```
All key branches resolved. Decision summary:

1. [question] → [conclusion] ([rationale])
2. …

Want me to turn these into a technical design doc? (tech-design skill)
```

If the user says yes, invoke the tech-design skill. The decisions above stay in
conversation context — tech-design uses them directly, so it won't re-grill.
